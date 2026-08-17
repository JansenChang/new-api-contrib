package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	sqlitedriver "github.com/glebarez/go-sqlite"
	"gorm.io/gorm"
)

var (
	ErrEnterpriseNotFound            = errors.New("enterprise not found")
	ErrEnterpriseMembershipNotFound  = errors.New("enterprise membership not found")
	ErrEnterpriseOwnerRequired       = errors.New("enterprise owner required")
	ErrEnterpriseInsufficientQuota   = errors.New("enterprise insufficient quota")
	ErrEnterpriseIdempotencyConflict = errors.New("enterprise idempotency conflict")
	ErrInvalidQuotaAmount            = errors.New("invalid quota amount")
	ErrEnterpriseLedgerImmutable     = errors.New("enterprise ledger is immutable")
	ErrBillingSubjectImmutable       = errors.New("billing subject snapshot is immutable")
)

// enterpriseMoneyBeforeIdempotencyReadHook is a test-only synchronization
// seam. It remains nil in production and lets the SQLite concurrency test
// make both first reads happen before either transaction writes.
var enterpriseMoneyBeforeIdempotencyReadHook func()

const enterpriseMoneySQLiteRetryLimit = 3

// EnterpriseMoneyCommand is an internal, transaction-bound money command.
// It is deliberately not an HTTP DTO; authorization belongs to the caller
// that opens a future enterprise management boundary.
type EnterpriseMoneyCommand struct {
	EnterpriseID        int
	MembershipID        int
	ActorUserID         int
	Action              string
	AdjustmentDirection string
	ReversesLedgerID    int64
	Amount              int
	IdempotencyKey      string
	ReferenceType       string
	ReferenceID         string
	RequestID           string
	Reason              string
}

type EnterpriseMoneyResult struct {
	EnterpriseAvailableQuota int
	EnterpriseReservedQuota  int
	MemberAvailableQuota     int
	MemberReservedQuota      int
	LedgerID                 int64
	Replayed                 bool
}

const (
	BillingSubjectTypePersonal   = "personal"
	BillingSubjectTypeEnterprise = "enterprise"
)

// BillingSubjectSnapshot is copied to an order at creation time. Payment
// settlement must use this snapshot, never a relationship read at callback
// time.
type BillingSubjectSnapshot struct {
	Type         string
	SubjectID    int
	EnterpriseID int
}

func (s BillingSubjectSnapshot) Valid() bool {
	if s.Type == BillingSubjectTypePersonal {
		return s.SubjectID > 0 && s.EnterpriseID == 0
	}
	return s.Type == BillingSubjectTypeEnterprise && s.SubjectID > 0 && s.EnterpriseID > 0
}

func normalizeBillingSubject(subjectType *string, subjectID, enterpriseID *int, userID int) error {
	if *subjectType == "" {
		*subjectType = BillingSubjectTypePersonal
	}
	if *subjectType == BillingSubjectTypePersonal {
		*subjectID = userID
		*enterpriseID = 0
		if userID <= 0 {
			return errors.New("invalid personal billing subject")
		}
		return nil
	}
	if *subjectType != BillingSubjectTypeEnterprise || *subjectID <= 0 || *enterpriseID <= 0 {
		return errors.New("invalid billing subject snapshot")
	}
	return nil
}

func (topUp *TopUp) SetBillingSubject(snapshot BillingSubjectSnapshot) error {
	if !snapshot.Valid() {
		return errors.New("invalid billing subject snapshot")
	}
	if topUp.BillingSubjectType != "" && (topUp.BillingSubjectType != snapshot.Type || topUp.BillingSubjectId != snapshot.SubjectID || topUp.BillingEnterpriseId != snapshot.EnterpriseID) {
		return ErrBillingSubjectImmutable
	}
	topUp.BillingSubjectType = snapshot.Type
	topUp.BillingSubjectId = snapshot.SubjectID
	topUp.BillingEnterpriseId = snapshot.EnterpriseID
	return nil
}

func (o *SubscriptionOrder) SetBillingSubject(snapshot BillingSubjectSnapshot) error {
	if !snapshot.Valid() {
		return errors.New("invalid billing subject snapshot")
	}
	if o.BillingSubjectType != "" && (o.BillingSubjectType != snapshot.Type || o.BillingSubjectId != snapshot.SubjectID || o.BillingEnterpriseId != snapshot.EnterpriseID) {
		return ErrBillingSubjectImmutable
	}
	o.BillingSubjectType = snapshot.Type
	o.BillingSubjectId = snapshot.SubjectID
	o.BillingEnterpriseId = snapshot.EnterpriseID
	return nil
}

func (topUp *TopUp) BeforeCreate(*gorm.DB) error {
	return normalizeBillingSubject(&topUp.BillingSubjectType, &topUp.BillingSubjectId, &topUp.BillingEnterpriseId, topUp.UserId)
}

func (topUp *TopUp) BeforeUpdate(tx *gorm.DB) error {
	if billingSubjectChanged(tx) {
		return ErrBillingSubjectImmutable
	}
	return ensureBillingSubjectUnchanged(tx, "id = ?", topUp.Id, topUp.BillingSubjectType, topUp.BillingSubjectId, topUp.BillingEnterpriseId, func(current *TopUp) (string, int, int) {
		return current.BillingSubjectType, current.BillingSubjectId, current.BillingEnterpriseId
	})
}

func (o *SubscriptionOrder) BeforeCreate(*gorm.DB) error {
	return normalizeBillingSubject(&o.BillingSubjectType, &o.BillingSubjectId, &o.BillingEnterpriseId, o.UserId)
}

func (o *SubscriptionOrder) BeforeUpdate(tx *gorm.DB) error {
	if billingSubjectChanged(tx) {
		return ErrBillingSubjectImmutable
	}
	return ensureBillingSubjectUnchanged(tx, "id = ?", o.Id, o.BillingSubjectType, o.BillingSubjectId, o.BillingEnterpriseId, func(current *SubscriptionOrder) (string, int, int) {
		return current.BillingSubjectType, current.BillingSubjectId, current.BillingEnterpriseId
	})
}

func billingSubjectChanged(tx *gorm.DB) bool {
	return tx != nil && (tx.Statement.Changed("BillingSubjectType") || tx.Statement.Changed("BillingSubjectId") || tx.Statement.Changed("BillingEnterpriseId"))
}

func ensureBillingSubjectUnchanged[T any](tx *gorm.DB, query string, id int, subjectType string, subjectID, enterpriseID int, load func(*T) (string, int, int)) error {
	if tx == nil || id <= 0 || subjectType == "" {
		return nil
	}
	var current T
	if err := tx.Session(&gorm.Session{NewDB: true, SkipHooks: true}).Where(query, id).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	currentType, currentID, currentEnterpriseID := load(&current)
	if currentType != subjectType || currentID != subjectID || currentEnterpriseID != enterpriseID {
		return ErrBillingSubjectImmutable
	}
	return nil
}

// BeforeUpdate and BeforeDelete make the append-only ledger contract hold for
// ordinary GORM writes too. Corrections must be represented by REVERSAL or
// ADJUSTMENT rows, never by mutating history.
func (l *EnterpriseLedger) BeforeUpdate(*gorm.DB) error { return ErrEnterpriseLedgerImmutable }
func (l *EnterpriseLedger) BeforeDelete(*gorm.DB) error { return ErrEnterpriseLedgerImmutable }

func CreditEnterpriseWallet(cmd EnterpriseMoneyCommand) (EnterpriseMoneyResult, error) {
	cmd.Action = EnterpriseLedgerKindTopUp
	return executeEnterpriseMoney(cmd, EnterpriseLedgerKindTopUp)
}

func AllocateEnterpriseQuota(cmd EnterpriseMoneyCommand) (EnterpriseMoneyResult, error) {
	cmd.Action = EnterpriseLedgerKindAllocate
	return executeEnterpriseMoney(cmd, EnterpriseLedgerKindAllocate)
}

func ReclaimEnterpriseQuota(cmd EnterpriseMoneyCommand) (EnterpriseMoneyResult, error) {
	cmd.Action = EnterpriseLedgerKindReclaim
	return executeEnterpriseMoney(cmd, EnterpriseLedgerKindReclaim)
}

func AdjustEnterpriseQuota(cmd EnterpriseMoneyCommand) (EnterpriseMoneyResult, error) {
	cmd.Action = EnterpriseLedgerKindAdjustment
	return executeEnterpriseMoney(cmd, EnterpriseLedgerKindAdjustment)
}

func ReverseEnterpriseLedger(cmd EnterpriseMoneyCommand) (EnterpriseMoneyResult, error) {
	cmd.Action = EnterpriseLedgerKindReversal
	return executeEnterpriseMoney(cmd, EnterpriseLedgerKindReversal)
}

// settleEnterpriseTopUp is the only C1 path from an enterprise-subject TopUp
// into an enterprise wallet. quotaToCredit is already normalized to internal
// quota units by its caller; this function must not infer it from TopUp.Amount,
// whose meaning differs between payment providers. It is intentionally
// package-private: C2 may call it only after verified payment dispatch and
// provider-specific quota normalization.
func settleEnterpriseTopUp(topUpID int, quotaToCredit int) (EnterpriseMoneyResult, error) {
	var result EnterpriseMoneyResult
	if DB == nil || topUpID <= 0 || quotaToCredit <= 0 || quotaToCredit > common.MaxQuota {
		return result, ErrInvalidQuotaAmount
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		var topUp TopUp
		if err := lockForUpdate(tx).Where("id = ?", topUpID).First(&topUp).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTopUpNotFound
			}
			return err
		}
		if topUp.Status != common.TopUpStatusSuccess || topUp.UserId <= 0 {
			return ErrInvalidQuotaAmount
		}
		snapshot := BillingSubjectSnapshot{
			Type:         topUp.BillingSubjectType,
			SubjectID:    topUp.BillingSubjectId,
			EnterpriseID: topUp.BillingEnterpriseId,
		}
		if snapshot.Type != BillingSubjectTypeEnterprise || !snapshot.Valid() {
			return ErrInvalidQuotaAmount
		}
		command := EnterpriseMoneyCommand{
			EnterpriseID:   snapshot.EnterpriseID,
			ActorUserID:    topUp.UserId,
			Amount:         quotaToCredit,
			IdempotencyKey: "enterprise-topup-settlement-" + strconv.Itoa(topUp.Id),
			ReferenceType:  enterpriseLedgerReferenceTypes[EnterpriseLedgerKindTopUp],
			ReferenceID:    strconv.Itoa(topUp.Id),
			RequestID:      "enterprise-topup-" + strconv.Itoa(topUp.Id),
			Reason:         "enterprise topup settlement",
		}
		var settleErr error
		result, settleErr = executeEnterpriseMoneyOn(tx, command, EnterpriseLedgerKindTopUp)
		return settleErr
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func executeEnterpriseMoney(cmd EnterpriseMoneyCommand, kind string) (result EnterpriseMoneyResult, err error) {
	return executeEnterpriseMoneyOn(DB, cmd, kind)
}

func executeEnterpriseMoneyOn(db *gorm.DB, cmd EnterpriseMoneyCommand, kind string) (result EnterpriseMoneyResult, err error) {
	if db == nil {
		return result, errors.New("database is not initialized")
	}
	if err := validateEnterpriseMoneyCommand(cmd, kind); err != nil {
		return result, err
	}

	for attempt := 0; ; attempt++ {
		err = db.Transaction(func(tx *gorm.DB) error {
			var enterprise Enterprise
			if err := lockForUpdate(tx).Where("id = ?", cmd.EnterpriseID).First(&enterprise).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrEnterpriseNotFound
				}
				return err
			}

			var member *EnterpriseMembership
			if cmd.MembershipID != 0 {
				member = &EnterpriseMembership{}
				if err := lockForUpdate(tx).Where("id = ? AND enterprise_id = ?", cmd.MembershipID, cmd.EnterpriseID).First(member).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return ErrEnterpriseMembershipNotFound
					}
					return err
				}
			}
			if kind == EnterpriseLedgerKindAdjustment || kind == EnterpriseLedgerKindReversal {
				if err := requirePlatformAdmin(tx, cmd.ActorUserID); err != nil {
					return err
				}
			}

			if enterpriseMoneyBeforeIdempotencyReadHook != nil {
				enterpriseMoneyBeforeIdempotencyReadHook()
			}
			var existing EnterpriseLedger
			keyHash := enterpriseLedgerHash(cmd.IdempotencyKey, 0)
			if err := lockForUpdate(tx).Where("enterprise_id = ? AND idempotency_key_hash = ?", cmd.EnterpriseID, keyHash).First(&existing).Error; err == nil {
				if !sameEnterpriseMoneyCommand(existing, cmd, kind) {
					return ErrEnterpriseIdempotencyConflict
				}
				result = enterpriseMoneyResult(enterprise, member, existing.Id, true)
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			var referenced EnterpriseLedger
			refHash := enterpriseReferenceHash(cmd.ReferenceType, cmd.ReferenceID, 0)
			if err := lockForUpdate(tx).Where("enterprise_id = ? AND reference_type = ? AND reference_hash = ?", cmd.EnterpriseID, cmd.ReferenceType, refHash).First(&referenced).Error; err == nil {
				return ErrEnterpriseIdempotencyConflict
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}

			if kind != EnterpriseLedgerKindTopUp && kind != EnterpriseLedgerKindAdjustment && kind != EnterpriseLedgerKindReversal {
				if member == nil {
					return ErrEnterpriseMembershipNotFound
				}
				if err := requireEnterpriseOwner(tx, cmd.EnterpriseID, cmd.ActorUserID); err != nil {
					return err
				}
				if member.Role != EnterpriseMembershipRoleMember {
					return ErrEnterpriseMembershipNotFound
				}
				if kind == EnterpriseLedgerKindAllocate && member.Status != EnterpriseMembershipStatusActive {
					return ErrEnterpriseMembershipNotFound
				}
				if kind == EnterpriseLedgerKindReclaim && member.Status == EnterpriseMembershipStatusRemoved {
					return ErrEnterpriseMembershipNotFound
				}
			}

			var ledger EnterpriseLedger
			switch kind {
			case EnterpriseLedgerKindTopUp:
				if err := updateEnterpriseAvailable(tx, &enterprise, cmd.Amount, false); err != nil {
					return err
				}
				ledger = newEnterpriseLedger(cmd, kind, nil, cmd.Amount, cmd.Amount, 0, 0, 0)
			case EnterpriseLedgerKindAllocate:
				if err := updateEnterpriseAvailable(tx, &enterprise, cmd.Amount, true); err != nil {
					return err
				}
				if err := updateMemberAvailable(tx, member, cmd.Amount, true); err != nil {
					return err
				}
				ledger = newEnterpriseLedger(cmd, kind, &member.Id, cmd.Amount, -cmd.Amount, 0, cmd.Amount, 0)
			case EnterpriseLedgerKindReclaim:
				if err := updateMemberAvailable(tx, member, cmd.Amount, false); err != nil {
					return err
				}
				if err := updateEnterpriseAvailable(tx, &enterprise, cmd.Amount, false); err != nil {
					return err
				}
				ledger = newEnterpriseLedger(cmd, kind, &member.Id, cmd.Amount, cmd.Amount, 0, -cmd.Amount, 0)
			case EnterpriseLedgerKindAdjustment:
				if cmd.MembershipID != 0 {
					return ErrEnterpriseMembershipNotFound
				}
				switch cmd.AdjustmentDirection {
				case "credit":
					if err := updateEnterpriseAvailable(tx, &enterprise, cmd.Amount, false); err != nil {
						return err
					}
					ledger = newEnterpriseLedger(cmd, kind, nil, cmd.Amount, cmd.Amount, 0, 0, 0)
				case "debit":
					if err := updateEnterpriseAvailable(tx, &enterprise, cmd.Amount, true); err != nil {
						return err
					}
					ledger = newEnterpriseLedger(cmd, kind, nil, cmd.Amount, -cmd.Amount, 0, 0, 0)
				default:
					return ErrInvalidQuotaAmount
				}
			case EnterpriseLedgerKindReversal:
				if cmd.MembershipID != 0 {
					return ErrInvalidQuotaAmount
				}
				var original EnterpriseLedger
				if err := lockForUpdate(tx).Where("id = ? AND enterprise_id = ?", cmd.ReversesLedgerID, cmd.EnterpriseID).First(&original).Error; err != nil {
					return ErrEnterpriseIdempotencyConflict
				}
				if !isReversibleEnterpriseLedgerKind(original.Kind) || original.Amount != cmd.Amount {
					return ErrEnterpriseIdempotencyConflict
				}
				var previousReversal EnterpriseLedger
				if err := lockForUpdate(tx).
					Where("enterprise_id = ? AND kind = ? AND reverses_ledger_id = ?", cmd.EnterpriseID, EnterpriseLedgerKindReversal, original.Id).
					First(&previousReversal).Error; err == nil {
					return ErrEnterpriseIdempotencyConflict
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if original.MembershipId != nil {
					member = &EnterpriseMembership{}
					if err := lockForUpdate(tx).Where("id = ? AND enterprise_id = ?", *original.MembershipId, cmd.EnterpriseID).First(member).Error; err != nil {
						return ErrEnterpriseMembershipNotFound
					}
				}
				if original.EnterpriseAvailableDelta < 0 {
					if err := updateEnterpriseAvailable(tx, &enterprise, -original.EnterpriseAvailableDelta, true); err != nil {
						return err
					}
				} else if original.EnterpriseAvailableDelta > 0 {
					if err := updateEnterpriseAvailable(tx, &enterprise, original.EnterpriseAvailableDelta, false); err != nil {
						return err
					}
				}
				if member != nil {
					if original.MemberAvailableDelta < 0 {
						if err := updateMemberAvailable(tx, member, -original.MemberAvailableDelta, true); err != nil {
							return err
						}
					} else if original.MemberAvailableDelta > 0 {
						if err := updateMemberAvailable(tx, member, original.MemberAvailableDelta, false); err != nil {
							return err
						}
					}
				}
				ledger = newEnterpriseLedger(cmd, kind, original.MembershipId, cmd.Amount, -original.EnterpriseAvailableDelta, -original.EnterpriseReservedDelta, -original.MemberAvailableDelta, -original.MemberReservedDelta)
				ledger.ReversesLedgerId = original.Id
			default:
				return fmt.Errorf("unsupported enterprise ledger kind %q", kind)
			}

			if err := tx.Create(&ledger).Error; err != nil {
				return err
			}
			result = enterpriseMoneyResult(enterprise, member, ledger.Id, false)
			return nil
		})
		if !isSQLiteBusyOrLocked(err) || attempt >= enterpriseMoneySQLiteRetryLimit {
			break
		}
		// The transaction is fully discarded before retrying. A short local
		// backoff gives the competing SQLite writer a chance to commit; the
		// next attempt re-reads the idempotency row and compares its summary.
		time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
	}
	if err != nil && looksLikeUniqueConstraint(err) {
		if replay, replayErr := replayEnterpriseMoneyOn(db, cmd, kind); replayErr == nil {
			return replay, nil
		}
	}
	return result, err
}

func replayEnterpriseMoneyOn(db *gorm.DB, cmd EnterpriseMoneyCommand, kind string) (result EnterpriseMoneyResult, err error) {
	err = db.Transaction(func(tx *gorm.DB) error {
		var enterprise Enterprise
		if err := lockForUpdate(tx).Where("id = ?", cmd.EnterpriseID).First(&enterprise).Error; err != nil {
			return err
		}
		if kind == EnterpriseLedgerKindAdjustment || kind == EnterpriseLedgerKindReversal {
			if err := requirePlatformAdmin(tx, cmd.ActorUserID); err != nil {
				return err
			}
		}
		var ledger EnterpriseLedger
		if err := lockForUpdate(tx).Where("enterprise_id = ? AND idempotency_key_hash = ?", cmd.EnterpriseID, enterpriseLedgerHash(cmd.IdempotencyKey, 0)).First(&ledger).Error; err != nil {
			return err
		}
		if !sameEnterpriseMoneyCommand(ledger, cmd, kind) {
			return ErrEnterpriseIdempotencyConflict
		}
		var member *EnterpriseMembership
		if ledger.MembershipId != nil {
			member = &EnterpriseMembership{}
			if err := lockForUpdate(tx).Where("id = ? AND enterprise_id = ?", *ledger.MembershipId, cmd.EnterpriseID).First(member).Error; err != nil {
				return err
			}
		}
		result = enterpriseMoneyResult(enterprise, member, ledger.Id, true)
		return nil
	})
	return result, err
}

func looksLikeUniqueConstraint(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate") || strings.Contains(message, "constraint failed")
}

func isSQLiteBusyOrLocked(err error) bool {
	if err == nil || !common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		return false
	}
	var sqliteErr *sqlitedriver.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	// SQLite extended result codes keep BUSY/LOCKED in the low byte.
	code := sqliteErr.Code()
	return code&0xff == 5 || code&0xff == 6
}

func validateEnterpriseMoneyCommand(cmd EnterpriseMoneyCommand, kind string) error {
	if cmd.EnterpriseID <= 0 || cmd.Amount <= 0 || cmd.Amount > common.MaxQuota {
		return ErrInvalidQuotaAmount
	}
	if cmd.IdempotencyKey == "" || len(cmd.IdempotencyKey) > 191 || cmd.ReferenceType == "" || len(cmd.ReferenceType) > 40 || cmd.ReferenceID == "" || len(cmd.ReferenceID) > 255 {
		return ErrInvalidQuotaAmount
	}
	if len(cmd.RequestID) > 64 || len(cmd.Reason) > 255 {
		return ErrInvalidQuotaAmount
	}
	expectedReferenceType, ok := enterpriseLedgerReferenceTypes[kind]
	if !ok || cmd.ReferenceType != expectedReferenceType {
		return ErrInvalidQuotaAmount
	}
	if kind != EnterpriseLedgerKindTopUp && kind != EnterpriseLedgerKindAdjustment && kind != EnterpriseLedgerKindReversal && cmd.MembershipID <= 0 {
		return ErrEnterpriseMembershipNotFound
	}
	if kind == EnterpriseLedgerKindAdjustment && cmd.AdjustmentDirection != "credit" && cmd.AdjustmentDirection != "debit" {
		return ErrInvalidQuotaAmount
	}
	if kind == EnterpriseLedgerKindReversal && cmd.ReversesLedgerID <= 0 {
		return ErrInvalidQuotaAmount
	}
	return nil
}

var enterpriseLedgerReferenceTypes = map[string]string{
	EnterpriseLedgerKindTopUp:      "enterprise_topup",
	EnterpriseLedgerKindAllocate:   "membership_allocation",
	EnterpriseLedgerKindReclaim:    "membership_allocation",
	EnterpriseLedgerKindAdjustment: "admin_adjustment",
	EnterpriseLedgerKindReversal:   "ledger_reversal",
}

func isReversibleEnterpriseLedgerKind(kind string) bool {
	switch kind {
	case EnterpriseLedgerKindTopUp, EnterpriseLedgerKindAllocate, EnterpriseLedgerKindReclaim, EnterpriseLedgerKindAdjustment:
		return true
	default:
		return false
	}
}

func requirePlatformAdmin(tx *gorm.DB, userID int) error {
	if userID <= 0 {
		return ErrEnterpriseOwnerRequired
	}
	var user User
	if err := lockForUpdate(tx).Select("id", "role").Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrEnterpriseOwnerRequired
		}
		return err
	}
	if !isPlatformAdminRole(user.Role) {
		return ErrEnterpriseOwnerRequired
	}
	return nil
}

func requireEnterpriseOwner(tx *gorm.DB, enterpriseID, userID int) error {
	if userID <= 0 {
		return ErrEnterpriseOwnerRequired
	}
	var owner EnterpriseMembership
	if err := lockForUpdate(tx).Where("enterprise_id = ? AND user_id = ? AND role = ?", enterpriseID, userID, EnterpriseMembershipRoleOwner).First(&owner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrEnterpriseOwnerRequired
		}
		return err
	}
	if owner.Status != EnterpriseMembershipStatusActive {
		return ErrEnterpriseOwnerRequired
	}
	return nil
}

func updateEnterpriseAvailable(tx *gorm.DB, enterprise *Enterprise, amount int, subtract bool) error {
	if subtract && enterprise.AvailableQuota < amount {
		return ErrEnterpriseInsufficientQuota
	}
	if !subtract && int64(enterprise.AvailableQuota)+int64(amount) > int64(common.MaxQuota) {
		return ErrInvalidQuotaAmount
	}
	value := gorm.Expr("available_quota + ?", amount)
	if subtract {
		value = gorm.Expr("available_quota - ?", amount)
	}
	query := tx.Model(&Enterprise{}).Where("id = ? AND reserved_quota >= 0 AND anomaly_quota >= 0", enterprise.Id)
	if subtract {
		query = query.Where("available_quota >= ?", amount)
	} else {
		query = query.Where("available_quota >= 0")
	}
	result := query.Update("available_quota", value)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEnterpriseInsufficientQuota
	}
	enterprise.AvailableQuota += map[bool]int{true: -amount, false: amount}[subtract]
	return nil
}

func updateMemberAvailable(tx *gorm.DB, member *EnterpriseMembership, amount int, add bool) error {
	if !add && member.AvailableQuota < amount {
		return ErrEnterpriseInsufficientQuota
	}
	if add && int64(member.AvailableQuota)+int64(amount) > int64(common.MaxQuota) {
		return ErrInvalidQuotaAmount
	}
	value := gorm.Expr("available_quota + ?", amount)
	if !add {
		value = gorm.Expr("available_quota - ?", amount)
	}
	query := tx.Model(&EnterpriseMembership{}).Where("id = ? AND reserved_quota >= 0", member.Id)
	if !add {
		query = query.Where("available_quota >= ?", amount)
	} else {
		query = query.Where("available_quota >= 0")
	}
	result := query.Update("available_quota", value)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEnterpriseInsufficientQuota
	}
	member.AvailableQuota += map[bool]int{true: amount, false: -amount}[add]
	return nil
}

func newEnterpriseLedger(cmd EnterpriseMoneyCommand, kind string, membershipID *int, amount, enterpriseAvailableDelta, enterpriseReservedDelta, memberAvailableDelta, memberReservedDelta int) EnterpriseLedger {
	ledger := EnterpriseLedger{
		EnterpriseId:             cmd.EnterpriseID,
		MembershipId:             membershipID,
		Kind:                     kind,
		EnterpriseAvailableDelta: enterpriseAvailableDelta,
		EnterpriseReservedDelta:  enterpriseReservedDelta,
		MemberAvailableDelta:     memberAvailableDelta,
		MemberReservedDelta:      memberReservedDelta,
		Amount:                   amount,
		ActorUserId:              cmd.ActorUserID,
		ReferenceType:            cmd.ReferenceType,
		ReferenceId:              cmd.ReferenceID,
		IdempotencyKey:           cmd.IdempotencyKey,
		IdempotencyKeyHash:       enterpriseLedgerHash(cmd.IdempotencyKey, 0),
		ReferenceHash:            enterpriseReferenceHash(cmd.ReferenceType, cmd.ReferenceID, 0),
		RequestId:                cmd.RequestID,
		Reason:                   cmd.Reason,
		CreatedAt:                common.GetTimestamp(),
	}
	ledger.CommandSummary = enterpriseMoneyCommandSummary(cmd, kind)
	ledger.CommandFingerprint = enterpriseLedgerHash(ledger.CommandSummary, 0)
	return ledger
}

func sameEnterpriseMoneyCommand(ledger EnterpriseLedger, cmd EnterpriseMoneyCommand, kind string) bool {
	summary := enterpriseMoneyCommandSummary(cmd, kind)
	membershipMatches := (ledger.MembershipId == nil && cmd.MembershipID == 0) || (ledger.MembershipId != nil && *ledger.MembershipId == cmd.MembershipID)
	// A reversal stores the original membership on its ledger row so the
	// reverse delta remains auditable; the command identifies that membership
	// through ReversesLedgerID and therefore carries no MembershipID.
	if kind == EnterpriseLedgerKindReversal && cmd.MembershipID == 0 {
		membershipMatches = true
	}
	return ledger.EnterpriseId == cmd.EnterpriseID && ledger.Kind == kind && ledger.CommandSummary == summary && ledger.CommandFingerprint == enterpriseLedgerHash(summary, 0) && membershipMatches
}

func enterpriseMoneyCommandSummary(cmd EnterpriseMoneyCommand, kind string) string {
	direction := ""
	if kind == EnterpriseLedgerKindAdjustment {
		direction = cmd.AdjustmentDirection
	}
	return fmt.Sprintf("kind=%s\x00membership=%d\x00amount=%d\x00actor=%d\x00reference_type=%s\x00reference_id=%s\x00adjustment_direction=%s\x00reverses_ledger_id=%d", kind, cmd.MembershipID, cmd.Amount, cmd.ActorUserID, cmd.ReferenceType, cmd.ReferenceID, direction, cmd.ReversesLedgerID)
}

func membershipIDValue(id *int) int {
	if id == nil {
		return 0
	}
	return *id
}

func enterpriseMoneyResult(enterprise Enterprise, member *EnterpriseMembership, ledgerID int64, replayed bool) EnterpriseMoneyResult {
	result := EnterpriseMoneyResult{EnterpriseAvailableQuota: enterprise.AvailableQuota, EnterpriseReservedQuota: enterprise.ReservedQuota, LedgerID: ledgerID, Replayed: replayed}
	if member != nil {
		result.MemberAvailableQuota = member.AvailableQuota
		result.MemberReservedQuota = member.ReservedQuota
	}
	return result
}
