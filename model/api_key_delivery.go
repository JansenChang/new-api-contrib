package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrAPIKeyDeliveryPending      = errors.New("API key delivery is pending")
	ErrAPIKeyDeliveryNotFound     = errors.New("API key delivery not found")
	ErrAPIKeyRotationUnsupported  = errors.New("API key rotation is not supported for this role")
	ErrAPIKeyRotationTargetNeeded = errors.New("API key rotation target is required")
	ErrAPIKeyDeliveryEmailMissing = errors.New("API key delivery email is missing")
)

const apiKeyDeliveryRetryCooldownSeconds int64 = 60

// RotateAPIKeyForDelivery rotates one existing key and creates its delivery
// record in the same transaction as the auth-version increment. It never
// creates another Token.
func RotateAPIKeyForDelivery(userID, tokenID int) (*Token, *APIKeyDelivery, error) {
	var token *Token
	var delivery *APIKeyDelivery
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		token, delivery, err = RotateAPIKeyForDeliveryTx(tx, userID, tokenID)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return token, delivery, nil
}

// RotateAPIKeyForDeliveryTx supports the recovery-flow transaction too.
// Ordinary users have exactly one key; Root/Admin must select one of theirs.
func RotateAPIKeyForDeliveryTx(tx *gorm.DB, userID, tokenID int) (*Token, *APIKeyDelivery, error) {
	if tx == nil || userID <= 0 {
		return nil, nil, errors.New("invalid API key rotation")
	}
	var user User
	if err := lockForUpdate(tx).Where("id = ?", userID).First(&user).Error; err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(user.Email) == "" {
		return nil, nil, ErrAPIKeyDeliveryEmailMissing
	}

	var target Token
	switch user.Role {
	case common.RoleCommonUser:
		var tokens []Token
		if err := lockForUpdate(tx).Where("user_id = ?", userID).Order("id ASC").Find(&tokens).Error; err != nil {
			return nil, nil, err
		}
		if len(tokens) != 1 {
			return nil, nil, errors.New("ordinary user must own exactly one API key")
		}
		target = tokens[0]
		if tokenID != 0 && tokenID != target.Id {
			return nil, nil, ErrAPIKeyRotationTargetNeeded
		}
	case common.RoleAdminUser, common.RoleRootUser:
		if tokenID <= 0 {
			return nil, nil, ErrAPIKeyRotationTargetNeeded
		}
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", tokenID, userID).First(&target).Error; err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, ErrAPIKeyRotationUnsupported
	}

	var pending int64
	if err := tx.Model(&APIKeyDelivery{}).Where("token_id = ? AND status = ?", target.Id, APIKeyDeliveryStatusPending).Count(&pending).Error; err != nil {
		return nil, nil, err
	}
	if pending > 0 {
		return nil, nil, ErrAPIKeyDeliveryPending
	}

	key, err := common.GenerateKey()
	if err != nil {
		return nil, nil, err
	}
	if err := invalidateTokenCacheForMutation(target.Key); err != nil {
		return nil, nil, err
	}
	now := common.GetTimestamp()
	if err := tx.Model(&Token{}).Where("id = ? AND user_id = ?", target.Id, userID).Updates(map[string]any{
		"key":           key,
		"status":        common.TokenStatusEnabled,
		"expired_time":  int64(-1),
		"accessed_time": now,
	}).Error; err != nil {
		return nil, nil, err
	}
	if _, err := IncrementUserAuthVersionWithTx(tx, userID); err != nil {
		return nil, nil, err
	}
	delivery := &APIKeyDelivery{
		UserId:         userID,
		TokenId:        target.Id,
		Email:          NormalizeEmail(user.Email),
		Status:         APIKeyDeliveryStatusPending,
		IdempotencyKey: uuid.NewString(),
		CreatedAt:      now,
	}
	if err := tx.Create(delivery).Error; err != nil {
		return nil, nil, err
	}
	target.Key = key
	target.Status = common.TokenStatusEnabled
	target.ExpiredTime = -1
	target.AccessedTime = now
	return &target, delivery, nil
}

// GetPendingAPIKeyDeliveryForUser reads the current key for the exact token
// referenced by an owned pending record. Callers may only put that key into
// the accepted email delivery body; it must not be persisted or logged.
func GetPendingAPIKeyDeliveryForUser(userID, deliveryID int) (*APIKeyDelivery, *Token, error) {
	if userID <= 0 || deliveryID <= 0 {
		return nil, nil, ErrAPIKeyDeliveryNotFound
	}
	var delivery APIKeyDelivery
	var token Token
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ? AND status = ?", deliveryID, userID, APIKeyDeliveryStatusPending).First(&delivery).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAPIKeyDeliveryNotFound
			}
			return err
		}
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", delivery.TokenId, userID).First(&token).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAPIKeyDeliveryNotFound
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return &delivery, &token, nil
}

func GetLatestPendingAPIKeyDeliveryForUser(userID int) (*APIKeyDelivery, *Token, error) {
	if userID <= 0 {
		return nil, nil, ErrAPIKeyDeliveryNotFound
	}
	var delivery APIKeyDelivery
	if err := DB.Where("user_id = ? AND status = ?", userID, APIKeyDeliveryStatusPending).Order("id DESC").First(&delivery).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrAPIKeyDeliveryNotFound
		}
		return nil, nil, err
	}
	return GetPendingAPIKeyDeliveryForUser(userID, delivery.Id)
}

func MarkAPIKeyDeliveryDelivered(deliveryID int) error {
	if deliveryID <= 0 {
		return ErrAPIKeyDeliveryNotFound
	}
	result := DB.Model(&APIKeyDelivery{}).Where("id = ? AND status = ?", deliveryID, APIKeyDeliveryStatusPending).Updates(map[string]any{
		"status":       APIKeyDeliveryStatusDelivered,
		"attempts":     gorm.Expr("attempts + ?", 1),
		"delivered_at": common.GetTimestamp(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAPIKeyDeliveryNotFound
	}
	return nil
}

// TryStartAPIKeyDeliveryAttempt atomically applies the retry cooldown before
// an SMTP call. This keeps a valid five-minute security proof from becoming a
// repeated full-Key send primitive, including when two resend requests race.
func TryStartAPIKeyDeliveryAttempt(deliveryID int) (bool, error) {
	if deliveryID <= 0 {
		return false, ErrAPIKeyDeliveryNotFound
	}
	now := common.GetTimestamp()
	result := DB.Model(&APIKeyDelivery{}).
		Where("id = ? AND status = ? AND (next_attempt_at = 0 OR next_attempt_at <= ?)", deliveryID, APIKeyDeliveryStatusPending, now).
		Updates(map[string]any{"next_attempt_at": now + apiKeyDeliveryRetryCooldownSeconds})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected != 1 {
		return false, nil
	}
	return true, nil
}

// RecordAPIKeyDeliveryFailure keeps the record pending. Raw SMTP errors are
// excluded because they can contain PII or provider details.
func RecordAPIKeyDeliveryFailure(deliveryID int, errorCode string) error {
	if deliveryID <= 0 {
		return ErrAPIKeyDeliveryNotFound
	}
	if errorCode == "" {
		errorCode = "SMTP_SEND_FAILED"
	}
	result := DB.Model(&APIKeyDelivery{}).Where("id = ? AND status = ?", deliveryID, APIKeyDeliveryStatusPending).Updates(map[string]any{
		"attempts":        gorm.Expr("attempts + ?", 1),
		"next_attempt_at": common.GetTimestamp() + apiKeyDeliveryRetryCooldownSeconds,
		"last_error_code": errorCode,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAPIKeyDeliveryNotFound
	}
	return nil
}
