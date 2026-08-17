package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	EnterpriseUsageStatePending           = "PENDING"
	EnterpriseUsageStateUpstreamSubmitted = "UPSTREAM_SUBMITTED"
	EnterpriseUsageStateSettled           = "SETTLED"
	EnterpriseUsageStateRefunded          = "REFUNDED"
	EnterpriseUsageStateAnomaly           = "ANOMALY"
	EnterpriseUsageStateManualReview      = "MANUAL_REVIEW"

	enterpriseUsageReferenceType = "enterprise_usage"
)

var (
	ErrEnterpriseUsageConflict       = errors.New("enterprise usage conflict")
	ErrEnterpriseBillingDisabled     = errors.New("enterprise billing is disabled")
	ErrEnterpriseUsageInvalidState   = errors.New("enterprise usage invalid state")
	ErrEnterpriseUsageInvalidRequest = errors.New("enterprise usage invalid request")
)

const (
	EnterpriseManualResolutionSuccess = "success"
	EnterpriseManualResolutionRefund  = "refund"
)

// EnterpriseUsageCommand is an internal pre-upstream command. Its fingerprint
// is already normalized and hashed by the relay boundary; raw request data is
// deliberately not accepted here.
type EnterpriseUsageCommand struct {
	ActorUserID        int
	TokenID            int
	IdempotencyKey     string
	RequestFingerprint string
	RequestID          string
	ModelName          string
	ChannelID          int
	ReservedQuota      int
}

type EnterpriseUsageOutcome struct {
	UsageID       int64
	State         string
	ReservedQuota int
	SettledQuota  int
	RefundedQuota int
	AnomalyQuota  int
	Replayed      bool
	Execute       bool
}

// ReserveEnterpriseUsage resolves the current active membership exactly once,
// then reserves only enterprise money in the same transaction as the usage and
// append-only RESERVE ledger row. A replay never changes funds or sends upstream.
func ReserveEnterpriseUsage(cmd EnterpriseUsageCommand) (EnterpriseUsageOutcome, error) {
	return reserveEnterpriseUsageWithTask(cmd, nil)
}

// ReserveEnterpriseUsageWithTask creates the enterprise reserve and local
// SUBMITTING draft in one main-database transaction before upstream submission.
func ReserveEnterpriseUsageWithTask(cmd EnterpriseUsageCommand, task *Task) (EnterpriseUsageOutcome, error) {
	if task == nil || task.TaskID == "" || task.Status != TaskStatusSubmitting {
		return EnterpriseUsageOutcome{}, ErrEnterpriseUsageInvalidRequest
	}
	return reserveEnterpriseUsageWithTask(cmd, task)
}

func reserveEnterpriseUsageWithTask(cmd EnterpriseUsageCommand, task *Task) (EnterpriseUsageOutcome, error) {
	if !common.EnterpriseBillingEnabled {
		return EnterpriseUsageOutcome{}, ErrEnterpriseBillingDisabled
	}
	if err := validateEnterpriseUsageCommand(cmd); err != nil {
		return EnterpriseUsageOutcome{}, err
	}
	outcome, err := withEnterpriseUsageRetry(func() (EnterpriseUsageOutcome, error) {
		return reserveEnterpriseUsage(cmd, task)
	})
	if err == nil || !looksLikeUniqueConstraint(err) {
		return outcome, err
	}
	var existing EnterpriseUsageRecord
	if lookupErr := DB.Where("token_id = ? AND idempotency_key = ?", cmd.TokenID, enterpriseUsageIdempotencyHash(cmd.IdempotencyKey)).First(&existing).Error; lookupErr != nil {
		return outcome, err
	}
	if existing.RequestFingerprint != cmd.RequestFingerprint {
		return EnterpriseUsageOutcome{}, ErrEnterpriseUsageConflict
	}
	return enterpriseUsageOutcome(existing, true, false), nil
}

func reserveEnterpriseUsage(cmd EnterpriseUsageCommand, task *Task) (outcome EnterpriseUsageOutcome, err error) {
	err = DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "active_enterprise_id").Where("id = ?", cmd.ActorUserID).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnterpriseMembershipNotFound
			}
			return err
		}
		if user.ActiveEnterpriseId <= 0 {
			var existing EnterpriseUsageRecord
			idempotencyKeyHash := enterpriseUsageIdempotencyHash(cmd.IdempotencyKey)
			if err := tx.Where("token_id = ? AND idempotency_key = ?", cmd.TokenID, idempotencyKeyHash).First(&existing).Error; err == nil {
				if existing.ActorUserId != cmd.ActorUserID || existing.RequestFingerprint != cmd.RequestFingerprint {
					return ErrEnterpriseUsageConflict
				}
				outcome = enterpriseUsageOutcome(existing, true, false)
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return ErrEnterpriseMembershipNotFound
		}
		idempotencyKeyHash := enterpriseUsageIdempotencyHash(cmd.IdempotencyKey)
		var existing EnterpriseUsageRecord
		if err := tx.Where("token_id = ? AND idempotency_key = ?", cmd.TokenID, idempotencyKeyHash).First(&existing).Error; err == nil {
			if existing.ActorUserId != cmd.ActorUserID || existing.RequestFingerprint != cmd.RequestFingerprint {
				return ErrEnterpriseUsageConflict
			}
			outcome = enterpriseUsageOutcome(existing, true, false)
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var enterprise Enterprise
		if err := lockForUpdate(tx).Where("id = ? AND status = ?", user.ActiveEnterpriseId, EnterpriseStatusActive).First(&enterprise).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnterpriseNotFound
			}
			return err
		}

		var membership EnterpriseMembership
		if err := lockForUpdate(tx).Where("enterprise_id = ? AND user_id = ? AND status = ?", enterprise.Id, cmd.ActorUserID, EnterpriseMembershipStatusActive).First(&membership).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnterpriseMembershipNotFound
			}
			return err
		}

		if membership.Role != EnterpriseMembershipRoleOwner && membership.Role != EnterpriseMembershipRoleMember {
			return ErrEnterpriseMembershipNotFound
		}
		fundingSource := "enterprise_wallet"
		membershipID, allocationID := 0, 0
		if membership.Role == EnterpriseMembershipRoleOwner {
			if err := reserveEnterpriseWallet(tx, &enterprise, cmd.ReservedQuota); err != nil {
				return err
			}
		} else {
			fundingSource = "enterprise_allocation"
			membershipID, allocationID = membership.Id, membership.Id
			if err := reserveEnterpriseMember(tx, &membership, cmd.ReservedQuota); err != nil {
				return err
			}
		}

		now := common.GetTimestamp()
		usage := EnterpriseUsageRecord{
			EnterpriseId:           enterprise.Id,
			MembershipId:           membershipID,
			ActorUserId:            cmd.ActorUserID,
			TokenId:                cmd.TokenID,
			IdempotencyKey:         idempotencyKeyHash,
			RequestFingerprint:     cmd.RequestFingerprint,
			RequestId:              cmd.RequestID,
			BillingAccountId:       enterprise.Id,
			AllocationId:           allocationID,
			FundingSource:          fundingSource,
			MembershipRoleSnapshot: membership.Role,
			State:                  EnterpriseUsageStatePending,
			ReservedQuota:          cmd.ReservedQuota,
			ModelName:              cmd.ModelName,
			ChannelId:              cmd.ChannelID,
			CreatedAt:              now,
		}
		if err := tx.Create(&usage).Error; err != nil {
			return err
		}
		if membership.Role == EnterpriseMembershipRoleOwner {
			if err := createEnterpriseUsageLedger(tx, usage, EnterpriseLedgerKindReserve, cmd.ReservedQuota, -cmd.ReservedQuota, cmd.ReservedQuota, 0, 0); err != nil {
				return err
			}
		} else if err := createEnterpriseUsageLedger(tx, usage, EnterpriseLedgerKindReserve, cmd.ReservedQuota, 0, 0, -cmd.ReservedQuota, cmd.ReservedQuota); err != nil {
			return err
		}
		if task != nil {
			task.PrivateData.EnterpriseUsageRecordId = usage.Id
			if err := tx.Create(task).Error; err != nil {
				return err
			}
		}
		outcome = enterpriseUsageOutcome(usage, false, true)
		return nil
	})
	return outcome, err
}

// MarkEnterpriseUsageUpstreamSubmitted makes the submitted fact durable before
// later settlement. It never reopens a terminal or manual-review usage.
func MarkEnterpriseUsageUpstreamSubmitted(usageID int64) (EnterpriseUsageOutcome, error) {
	return transitionEnterpriseUsage(usageID, []string{EnterpriseUsageStatePending}, EnterpriseUsageStateUpstreamSubmitted)
}

// SnapshotEnterpriseUsageChannel records the first actual selected channel on
// an existing PENDING usage. It never creates a second reservation and cannot
// overwrite a channel snapshot once the request is ready to send upstream.
func SnapshotEnterpriseUsageChannel(usageID int64, channelID int) error {
	if usageID <= 0 || channelID <= 0 {
		return ErrEnterpriseUsageInvalidRequest
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var usage EnterpriseUsageRecord
		if err := lockForUpdate(tx).Where("id = ?", usageID).First(&usage).Error; err != nil {
			return err
		}
		if usage.State != EnterpriseUsageStatePending {
			return ErrEnterpriseUsageInvalidState
		}
		if usage.ChannelId == channelID {
			return nil
		}
		if usage.ChannelId != 0 {
			return ErrEnterpriseUsageConflict
		}
		result := tx.Model(&EnterpriseUsageRecord{}).
			Where("id = ? AND state = ? AND channel_id = ?", usageID, EnterpriseUsageStatePending, 0).
			Update("channel_id", channelID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrEnterpriseUsageInvalidState
		}
		return nil
	})
}

// MarkEnterpriseUsageManualReview is used only when upstream execution cannot
// be disproven. It retains the reservation and does not refund or retry.
func MarkEnterpriseUsageManualReview(usageID int64) (EnterpriseUsageOutcome, error) {
	return transitionEnterpriseUsage(usageID, []string{EnterpriseUsageStatePending, EnterpriseUsageStateUpstreamSubmitted}, EnterpriseUsageStateManualReview)
}

func transitionEnterpriseUsage(usageID int64, from []string, to string) (outcome EnterpriseUsageOutcome, err error) {
	if usageID <= 0 {
		return outcome, ErrEnterpriseUsageInvalidRequest
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		var usage EnterpriseUsageRecord
		if err := lockForUpdate(tx).Where("id = ?", usageID).First(&usage).Error; err != nil {
			return err
		}
		if usage.State == to || isEnterpriseUsageTerminal(usage.State) {
			outcome = enterpriseUsageOutcome(usage, true, false)
			return nil
		}
		allowed := false
		for _, state := range from {
			allowed = allowed || usage.State == state
		}
		if !allowed {
			return ErrEnterpriseUsageInvalidState
		}
		result := tx.Model(&EnterpriseUsageRecord{}).Where("id = ? AND state = ?", usage.Id, usage.State).Update("state", to)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrEnterpriseUsageInvalidState
		}
		usage.State = to
		outcome = enterpriseUsageOutcome(usage, false, false)
		return nil
	})
	return outcome, err
}

// SettleEnterpriseUsage settles only a frozen enterprise reservation. An
// overage records ANOMALY and pauses that membership without touching personal
// wallet, subscription, or token quota.
func SettleEnterpriseUsage(usageID int64, actualQuota int) (EnterpriseUsageOutcome, error) {
	if usageID <= 0 || actualQuota < 0 || actualQuota > common.MaxQuota {
		return EnterpriseUsageOutcome{}, ErrEnterpriseUsageInvalidRequest
	}
	return withEnterpriseUsageRetry(func() (EnterpriseUsageOutcome, error) {
		var outcome EnterpriseUsageOutcome
		err := DB.Transaction(func(tx *gorm.DB) error {
			var err error
			outcome, err = settleEnterpriseUsageOn(tx, usageID, actualQuota, []string{EnterpriseUsageStatePending, EnterpriseUsageStateUpstreamSubmitted}, 0, "")
			return err
		})
		return outcome, err
	})
}

func settleEnterpriseUsageOn(tx *gorm.DB, usageID int64, actualQuota int, fromStates []string, actorUserID int, reason string) (outcome EnterpriseUsageOutcome, err error) {
	usage, enterprise, membership, err := lockEnterpriseUsageFunding(tx, usageID)
	if err != nil {
		return outcome, err
	}
	if usage.State == EnterpriseUsageStateSettled || usage.State == EnterpriseUsageStateAnomaly {
		if usage.SettledQuota != minEnterpriseQuota(actualQuota, usage.ReservedQuota) || usage.AnomalyQuota != maxEnterpriseQuota(actualQuota-usage.ReservedQuota, 0) {
			return outcome, ErrEnterpriseUsageConflict
		}
		return enterpriseUsageOutcome(*usage, true, false), nil
	}
	if !enterpriseUsageStateAllowed(usage.State, fromStates) {
		return outcome, ErrEnterpriseUsageInvalidState
	}

	settledQuota := minEnterpriseQuota(actualQuota, usage.ReservedQuota)
	anomalyQuota := maxEnterpriseQuota(actualQuota-usage.ReservedQuota, 0)
	if usage.MembershipRoleSnapshot == EnterpriseMembershipRoleOwner {
		if err := settleEnterpriseWallet(tx, enterprise, usage.ReservedQuota, settledQuota, anomalyQuota); err != nil {
			return outcome, err
		}
	} else if usage.MembershipRoleSnapshot == EnterpriseMembershipRoleMember && membership != nil {
		if err := settleEnterpriseMember(tx, membership, usage.ReservedQuota, settledQuota); err != nil {
			return outcome, err
		}
	} else {
		return outcome, ErrEnterpriseUsageInvalidState
	}
	if anomalyQuota > 0 {
		result := tx.Model(&EnterpriseMembership{}).Where("id = ? AND status <> ?", membership.Id, EnterpriseMembershipStatusRemoved).Update("status", EnterpriseMembershipStatusManualReview)
		if result.Error != nil {
			return outcome, result.Error
		}
	}
	if anomalyQuota > 0 {
		if int64(enterprise.AnomalyQuota)+int64(anomalyQuota) > int64(common.MaxQuota) {
			return outcome, ErrEnterpriseUsageInvalidRequest
		}
		result := tx.Model(&Enterprise{}).Where("id = ? AND anomaly_quota >= 0", enterprise.Id).Update("anomaly_quota", gorm.Expr("anomaly_quota + ?", anomalyQuota))
		if result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return outcome, result.Error
			}
			return outcome, ErrEnterpriseUsageInvalidState
		}
	}

	now := common.GetTimestamp()
	state := EnterpriseUsageStateSettled
	if anomalyQuota > 0 {
		state = EnterpriseUsageStateAnomaly
	}
	if err := tx.Model(&EnterpriseUsageRecord{}).Where("id = ? AND state IN ?", usage.Id, fromStates).Updates(map[string]any{
		"state":         state,
		"settled_quota": settledQuota,
		"anomaly_quota": anomalyQuota,
		"settled_at":    now,
	}).Error; err != nil {
		return outcome, err
	}
	if usage.MembershipRoleSnapshot == EnterpriseMembershipRoleOwner {
		if err := createEnterpriseUsageLedgerForActor(tx, *usage, EnterpriseLedgerKindSettle, settledQuota, usage.ReservedQuota-settledQuota, -usage.ReservedQuota, 0, 0, actorUserID, reason); err != nil {
			return outcome, err
		}
	} else if err := createEnterpriseUsageLedgerForActor(tx, *usage, EnterpriseLedgerKindSettle, settledQuota, 0, 0, usage.ReservedQuota-settledQuota, -usage.ReservedQuota, actorUserID, reason); err != nil {
		return outcome, err
	}
	if anomalyQuota > 0 {
		if err := createEnterpriseUsageLedgerForActor(tx, *usage, EnterpriseLedgerKindAnomaly, anomalyQuota, 0, 0, 0, 0, actorUserID, reason); err != nil {
			return outcome, err
		}
	}
	usage.State, usage.SettledQuota, usage.AnomalyQuota, usage.SettledAt = state, settledQuota, anomalyQuota, now
	return enterpriseUsageOutcome(*usage, false, false), nil
}

// RefundEnterpriseUsage releases a reservation only when the caller has proved
// no upstream execution happened. Unknown execution must use MANUAL_REVIEW.
func RefundEnterpriseUsage(usageID int64) (EnterpriseUsageOutcome, error) {
	if usageID <= 0 {
		return EnterpriseUsageOutcome{}, ErrEnterpriseUsageInvalidRequest
	}
	return withEnterpriseUsageRetry(func() (EnterpriseUsageOutcome, error) {
		var outcome EnterpriseUsageOutcome
		err := DB.Transaction(func(tx *gorm.DB) error {
			var err error
			outcome, err = refundEnterpriseUsageOn(tx, usageID, []string{EnterpriseUsageStatePending, EnterpriseUsageStateUpstreamSubmitted}, 0, "")
			return err
		})
		return outcome, err
	})
}

func refundEnterpriseUsageOn(tx *gorm.DB, usageID int64, fromStates []string, actorUserID int, reason string) (outcome EnterpriseUsageOutcome, err error) {
	usage, enterprise, membership, err := lockEnterpriseUsageFunding(tx, usageID)
	if err != nil {
		return outcome, err
	}
	if usage.State == EnterpriseUsageStateRefunded {
		return enterpriseUsageOutcome(*usage, true, false), nil
	}
	if !enterpriseUsageStateAllowed(usage.State, fromStates) {
		return outcome, ErrEnterpriseUsageInvalidState
	}
	if usage.MembershipRoleSnapshot == EnterpriseMembershipRoleOwner {
		if err := releaseEnterpriseWallet(tx, enterprise, usage.ReservedQuota); err != nil {
			return outcome, err
		}
	} else if usage.MembershipRoleSnapshot == EnterpriseMembershipRoleMember && membership != nil {
		if err := releaseEnterpriseMember(tx, membership, usage.ReservedQuota); err != nil {
			return outcome, err
		}
	} else {
		return outcome, ErrEnterpriseUsageInvalidState
	}
	now := common.GetTimestamp()
	if err := tx.Model(&EnterpriseUsageRecord{}).Where("id = ? AND state IN ?", usage.Id, fromStates).Updates(map[string]any{
		"state":          EnterpriseUsageStateRefunded,
		"refunded_quota": usage.ReservedQuota,
		"refunded_at":    now,
	}).Error; err != nil {
		return outcome, err
	}
	if usage.MembershipRoleSnapshot == EnterpriseMembershipRoleOwner {
		if err := createEnterpriseUsageLedgerForActor(tx, *usage, EnterpriseLedgerKindRefund, usage.ReservedQuota, usage.ReservedQuota, -usage.ReservedQuota, 0, 0, actorUserID, reason); err != nil {
			return outcome, err
		}
	} else if err := createEnterpriseUsageLedgerForActor(tx, *usage, EnterpriseLedgerKindRefund, usage.ReservedQuota, 0, 0, usage.ReservedQuota, -usage.ReservedQuota, actorUserID, reason); err != nil {
		return outcome, err
	}
	usage.State, usage.RefundedQuota, usage.RefundedAt = EnterpriseUsageStateRefunded, usage.ReservedQuota, now
	return enterpriseUsageOutcome(*usage, false, false), nil
}

// ResolveEnterpriseTaskManualReview closes a manual-review enterprise Task in
// one transaction. The durable ledger row records the platform administrator
// and reason; no current membership or personal balance is consulted.
func ResolveEnterpriseTaskManualReview(taskID int64, actorUserID int, outcomeKind string, actualQuota int, reason string) (EnterpriseUsageOutcome, error) {
	reason = strings.TrimSpace(reason)
	if taskID <= 0 || actorUserID <= 0 || len(reason) == 0 || len(reason) > 255 || (outcomeKind != EnterpriseManualResolutionSuccess && outcomeKind != EnterpriseManualResolutionRefund) || actualQuota < 0 || actualQuota > common.MaxQuota || (outcomeKind == EnterpriseManualResolutionRefund && actualQuota != 0) {
		return EnterpriseUsageOutcome{}, ErrEnterpriseUsageInvalidRequest
	}
	return withEnterpriseUsageRetry(func() (EnterpriseUsageOutcome, error) {
		var outcome EnterpriseUsageOutcome
		err := DB.Transaction(func(tx *gorm.DB) error {
			if err := requirePlatformAdmin(tx, actorUserID); err != nil {
				return err
			}
			var task Task
			if err := lockForUpdate(tx).Where("id = ?", taskID).First(&task).Error; err != nil {
				return err
			}
			if task.PrivateData.EnterpriseUsageRecordId <= 0 {
				return ErrEnterpriseUsageInvalidState
			}
			if task.Status != TaskStatusManualReview {
				var usage EnterpriseUsageRecord
				if err := tx.Where("id = ?", task.PrivateData.EnterpriseUsageRecordId).First(&usage).Error; err != nil {
					return err
				}
				if outcomeKind == EnterpriseManualResolutionSuccess && (usage.State == EnterpriseUsageStateSettled || usage.State == EnterpriseUsageStateAnomaly) && usage.SettledQuota == minEnterpriseQuota(actualQuota, usage.ReservedQuota) && usage.AnomalyQuota == maxEnterpriseQuota(actualQuota-usage.ReservedQuota, 0) {
					outcome = enterpriseUsageOutcome(usage, true, false)
					return nil
				}
				if outcomeKind == EnterpriseManualResolutionRefund && usage.State == EnterpriseUsageStateRefunded {
					outcome = enterpriseUsageOutcome(usage, true, false)
					return nil
				}
				return ErrEnterpriseUsageInvalidState
			}

			if outcomeKind == EnterpriseManualResolutionSuccess {
				var err error
				outcome, err = settleEnterpriseUsageOn(tx, task.PrivateData.EnterpriseUsageRecordId, actualQuota, []string{EnterpriseUsageStateManualReview}, actorUserID, reason)
				if err != nil {
					return err
				}
				result := tx.Model(&Task{}).Where("id = ? AND status = ?", task.ID, TaskStatusManualReview).Updates(map[string]any{"status": TaskStatusSuccess, "progress": "100%", "finish_time": common.GetTimestamp(), "updated_at": common.GetTimestamp()})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrEnterpriseUsageInvalidState
				}
				return nil
			}

			var err error
			outcome, err = refundEnterpriseUsageOn(tx, task.PrivateData.EnterpriseUsageRecordId, []string{EnterpriseUsageStateManualReview}, actorUserID, reason)
			if err != nil {
				return err
			}
			result := tx.Model(&Task{}).Where("id = ? AND status = ?", task.ID, TaskStatusManualReview).Updates(map[string]any{"status": TaskStatusFailure, "progress": "100%", "finish_time": common.GetTimestamp(), "fail_reason": "企业管理员人工确认未执行：" + reason, "updated_at": common.GetTimestamp()})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrEnterpriseUsageInvalidState
			}
			return nil
		})
		return outcome, err
	})
}

func validateEnterpriseUsageCommand(cmd EnterpriseUsageCommand) error {
	if DB == nil || cmd.ActorUserID <= 0 || cmd.TokenID <= 0 || cmd.ReservedQuota <= 0 || cmd.ReservedQuota > common.MaxQuota || len(cmd.IdempotencyKey) == 0 || len(cmd.IdempotencyKey) > 191 || len(cmd.RequestFingerprint) != 64 || len(cmd.RequestID) > 64 || len(cmd.ModelName) > 191 {
		return ErrEnterpriseUsageInvalidRequest
	}
	for _, c := range cmd.RequestFingerprint {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return ErrEnterpriseUsageInvalidRequest
		}
	}
	return nil
}

func withEnterpriseUsageRetry(fn func() (EnterpriseUsageOutcome, error)) (EnterpriseUsageOutcome, error) {
	for attempt := 0; ; attempt++ {
		outcome, err := fn()
		if !isSQLiteBusyOrLocked(err) || attempt >= enterpriseMoneySQLiteRetryLimit {
			return outcome, err
		}
		time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
	}
}

func reserveEnterpriseWallet(tx *gorm.DB, enterprise *Enterprise, amount int) error {
	result := tx.Model(&Enterprise{}).Where("id = ? AND status = ? AND available_quota >= ? AND reserved_quota >= 0 AND anomaly_quota >= 0", enterprise.Id, EnterpriseStatusActive, amount).Updates(map[string]any{
		"available_quota": gorm.Expr("available_quota - ?", amount),
		"reserved_quota":  gorm.Expr("reserved_quota + ?", amount),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEnterpriseInsufficientQuota
	}
	return nil
}

func reserveEnterpriseMember(tx *gorm.DB, member *EnterpriseMembership, amount int) error {
	if member.SelfKeyLimit > 0 && int64(member.SelfKeyUsedQuota)+int64(member.SelfKeyReservedQuota)+int64(amount) > int64(member.SelfKeyLimit) {
		return ErrEnterpriseInsufficientQuota
	}
	query := tx.Model(&EnterpriseMembership{}).Where("id = ? AND status = ? AND available_quota >= ? AND reserved_quota >= 0 AND self_key_reserved_quota >= 0 AND self_key_used_quota >= 0", member.Id, EnterpriseMembershipStatusActive, amount)
	if member.SelfKeyLimit > 0 {
		query = query.Where("self_key_used_quota + self_key_reserved_quota + ? <= self_key_limit", amount)
	}
	result := query.Updates(map[string]any{
		"available_quota":         gorm.Expr("available_quota - ?", amount),
		"reserved_quota":          gorm.Expr("reserved_quota + ?", amount),
		"self_key_reserved_quota": gorm.Expr("self_key_reserved_quota + ?", amount),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEnterpriseInsufficientQuota
	}
	return nil
}

func lockEnterpriseUsageFunding(tx *gorm.DB, usageID int64) (*EnterpriseUsageRecord, *Enterprise, *EnterpriseMembership, error) {
	var initial EnterpriseUsageRecord
	if err := tx.Select("id", "actor_user_id", "enterprise_id", "membership_id", "membership_role_snapshot").Where("id = ?", usageID).First(&initial).Error; err != nil {
		return nil, nil, nil, err
	}
	var user User
	if err := lockForUpdate(tx).Select("id").Where("id = ?", initial.ActorUserId).First(&user).Error; err != nil {
		return nil, nil, nil, err
	}
	var enterprise Enterprise
	if err := lockForUpdate(tx).Where("id = ?", initial.EnterpriseId).First(&enterprise).Error; err != nil {
		return nil, nil, nil, err
	}
	var membership EnterpriseMembership
	membershipQuery := lockForUpdate(tx).Where("enterprise_id = ?", initial.EnterpriseId)
	if initial.MembershipRoleSnapshot == EnterpriseMembershipRoleOwner {
		membershipQuery = membershipQuery.Where("user_id = ? AND role = ?", initial.ActorUserId, EnterpriseMembershipRoleOwner)
	} else {
		membershipQuery = membershipQuery.Where("id = ?", initial.MembershipId)
	}
	if err := membershipQuery.First(&membership).Error; err != nil {
		return nil, nil, nil, err
	}
	var usage EnterpriseUsageRecord
	if err := lockForUpdate(tx).Where("id = ?", usageID).First(&usage).Error; err != nil {
		return nil, nil, nil, err
	}
	return &usage, &enterprise, &membership, nil
}

func settleEnterpriseWallet(tx *gorm.DB, enterprise *Enterprise, reservedQuota, settledQuota, anomalyQuota int) error {
	refund := reservedQuota - settledQuota
	if int64(enterprise.AvailableQuota)+int64(refund) > int64(common.MaxQuota) {
		return ErrEnterpriseUsageInvalidRequest
	}
	result := tx.Model(&Enterprise{}).Where("id = ? AND reserved_quota >= ? AND available_quota >= 0", enterprise.Id, reservedQuota).Updates(map[string]any{
		"available_quota": gorm.Expr("available_quota + ?", refund),
		"reserved_quota":  gorm.Expr("reserved_quota - ?", reservedQuota),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEnterpriseUsageInvalidState
	}
	return nil
}

func settleEnterpriseMember(tx *gorm.DB, member *EnterpriseMembership, reservedQuota, settledQuota int) error {
	refund := reservedQuota - settledQuota
	if int64(member.AvailableQuota)+int64(refund) > int64(common.MaxQuota) || int64(member.SelfKeyUsedQuota)+int64(settledQuota) > int64(common.MaxQuota) {
		return ErrEnterpriseUsageInvalidRequest
	}
	result := tx.Model(&EnterpriseMembership{}).Where("id = ? AND reserved_quota >= ? AND self_key_reserved_quota >= ? AND available_quota >= 0 AND self_key_used_quota >= 0", member.Id, reservedQuota, reservedQuota).Updates(map[string]any{
		"available_quota":         gorm.Expr("available_quota + ?", refund),
		"reserved_quota":          gorm.Expr("reserved_quota - ?", reservedQuota),
		"self_key_reserved_quota": gorm.Expr("self_key_reserved_quota - ?", reservedQuota),
		"self_key_used_quota":     gorm.Expr("self_key_used_quota + ?", settledQuota),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEnterpriseUsageInvalidState
	}
	return nil
}

func releaseEnterpriseWallet(tx *gorm.DB, enterprise *Enterprise, amount int) error {
	if int64(enterprise.AvailableQuota)+int64(amount) > int64(common.MaxQuota) {
		return ErrEnterpriseUsageInvalidRequest
	}
	result := tx.Model(&Enterprise{}).Where("id = ? AND reserved_quota >= ? AND available_quota >= 0", enterprise.Id, amount).Updates(map[string]any{
		"available_quota": gorm.Expr("available_quota + ?", amount),
		"reserved_quota":  gorm.Expr("reserved_quota - ?", amount),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEnterpriseUsageInvalidState
	}
	return nil
}

func releaseEnterpriseMember(tx *gorm.DB, member *EnterpriseMembership, amount int) error {
	if int64(member.AvailableQuota)+int64(amount) > int64(common.MaxQuota) {
		return ErrEnterpriseUsageInvalidRequest
	}
	result := tx.Model(&EnterpriseMembership{}).Where("id = ? AND reserved_quota >= ? AND self_key_reserved_quota >= ? AND available_quota >= 0", member.Id, amount, amount).Updates(map[string]any{
		"available_quota":         gorm.Expr("available_quota + ?", amount),
		"reserved_quota":          gorm.Expr("reserved_quota - ?", amount),
		"self_key_reserved_quota": gorm.Expr("self_key_reserved_quota - ?", amount),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEnterpriseUsageInvalidState
	}
	return nil
}

func createEnterpriseUsageLedger(tx *gorm.DB, usage EnterpriseUsageRecord, kind string, amount, enterpriseAvailableDelta, enterpriseReservedDelta, memberAvailableDelta, memberReservedDelta int) error {
	return createEnterpriseUsageLedgerForActor(tx, usage, kind, amount, enterpriseAvailableDelta, enterpriseReservedDelta, memberAvailableDelta, memberReservedDelta, usage.ActorUserId, "")
}

func createEnterpriseUsageLedgerForActor(tx *gorm.DB, usage EnterpriseUsageRecord, kind string, amount, enterpriseAvailableDelta, enterpriseReservedDelta, memberAvailableDelta, memberReservedDelta int, actorUserID int, reason string) error {
	if amount < 0 || amount > common.MaxQuota {
		return ErrEnterpriseUsageInvalidRequest
	}
	if actorUserID <= 0 {
		actorUserID = usage.ActorUserId
	}
	if reason == "" {
		reason = "enterprise usage " + kind
	}
	ledger := EnterpriseLedger{
		EnterpriseId:             usage.EnterpriseId,
		Kind:                     kind,
		EnterpriseAvailableDelta: enterpriseAvailableDelta,
		EnterpriseReservedDelta:  enterpriseReservedDelta,
		MemberAvailableDelta:     memberAvailableDelta,
		MemberReservedDelta:      memberReservedDelta,
		Amount:                   amount,
		ActorUserId:              actorUserID,
		ReferenceType:            enterpriseUsageReferenceType,
		ReferenceId:              fmt.Sprintf("usage:%d:%s", usage.Id, kind),
		RequestId:                usage.RequestId,
		Reason:                   reason,
		CreatedAt:                common.GetTimestamp(),
	}
	if usage.MembershipId > 0 {
		membershipID := usage.MembershipId
		ledger.MembershipId = &membershipID
	}
	ledger.IdempotencyKey = enterpriseLedgerHash("usage:"+strconv.FormatInt(usage.Id, 10)+":"+kind, 0)
	ledger.IdempotencyKeyHash = enterpriseLedgerHash(ledger.IdempotencyKey, 0)
	ledger.ReferenceHash = enterpriseReferenceHash(ledger.ReferenceType, ledger.ReferenceId, 0)
	ledger.CommandSummary = fmt.Sprintf("usage=%d\x00kind=%s\x00amount=%d", usage.Id, kind, amount)
	ledger.CommandFingerprint = enterpriseLedgerHash(ledger.CommandSummary, 0)
	return tx.Create(&ledger).Error
}

func enterpriseUsageOutcome(usage EnterpriseUsageRecord, replayed, execute bool) EnterpriseUsageOutcome {
	return EnterpriseUsageOutcome{
		UsageID:       usage.Id,
		State:         usage.State,
		ReservedQuota: usage.ReservedQuota,
		SettledQuota:  usage.SettledQuota,
		RefundedQuota: usage.RefundedQuota,
		AnomalyQuota:  usage.AnomalyQuota,
		Replayed:      replayed,
		Execute:       execute,
	}
}

func isEnterpriseUsageTerminal(state string) bool {
	return state == EnterpriseUsageStateSettled || state == EnterpriseUsageStateRefunded || state == EnterpriseUsageStateAnomaly || state == EnterpriseUsageStateManualReview
}

func enterpriseUsageStateAllowed(state string, states []string) bool {
	for _, allowed := range states {
		if state == allowed {
			return true
		}
	}
	return false
}

func minEnterpriseQuota(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxEnterpriseQuota(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func enterpriseUsageIdempotencyHash(value string) string {
	return enterpriseLedgerHash(value, 0)
}
