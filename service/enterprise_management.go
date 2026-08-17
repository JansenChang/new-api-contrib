package service

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/model"
)

var ErrEnterpriseIdempotencyKeyInvalid = errors.New("enterprise idempotency key is invalid")

// SetEnterpriseOwner is the HTTP-facing orchestration boundary. The atomic
// enterprise/membership/anchor write remains in model so no controller can
// accidentally split the relationship across transactions.
func SetEnterpriseOwner(userID int) (model.EnterpriseOwnerAssignmentResult, error) {
	return model.SetEnterpriseOwner(userID)
}

func GetEnterpriseSelf(userID int) (model.EnterpriseSelfSummary, error) {
	return model.GetEnterpriseSelf(userID)
}

func ListEnterpriseMembers(userID, offset, limit int) ([]model.EnterpriseMemberProjection, int64, error) {
	return model.ListEnterpriseMembers(userID, offset, limit)
}

func ListEnterpriseLedger(userID, offset, limit int) ([]model.EnterpriseLedgerItem, int64, error) {
	return model.ListEnterpriseLedger(userID, offset, limit)
}

func ListEnterpriseUsage(userID, offset, limit int) ([]model.EnterpriseUsageItem, int64, error) {
	return model.ListEnterpriseUsage(userID, offset, limit)
}

func GetEnterpriseMember(userID, membershipID int) (model.EnterpriseMemberProjection, error) {
	return model.GetEnterpriseMemberProjection(userID, membershipID)
}

func AllocateEnterpriseQuota(userID, membershipID, amount int, requestKey string) (model.EnterpriseMoneyResult, error) {
	if !validManagementIdempotencyKey(requestKey) {
		return model.EnterpriseMoneyResult{}, ErrEnterpriseIdempotencyKeyInvalid
	}
	return model.AllocateEnterpriseQuotaForManagement(userID, membershipID, amount, "allocate", requestKey)
}

func ReclaimEnterpriseQuota(userID, membershipID, amount int, requestKey string) (model.EnterpriseMoneyResult, error) {
	if !validManagementIdempotencyKey(requestKey) {
		return model.EnterpriseMoneyResult{}, ErrEnterpriseIdempotencyKeyInvalid
	}
	return model.ReclaimEnterpriseQuotaForManagement(userID, membershipID, amount, "reclaim", requestKey)
}

func PauseEnterpriseMember(userID, membershipID int) (model.EnterpriseMemberProjection, error) {
	if _, err := model.PauseEnterpriseMember(userID, membershipID); err != nil {
		return model.EnterpriseMemberProjection{}, err
	}
	return model.GetEnterpriseMemberProjection(userID, membershipID)
}

func ResumeEnterpriseMember(userID, membershipID int) (model.EnterpriseMemberProjection, error) {
	if _, err := model.ResumeEnterpriseMember(userID, membershipID); err != nil {
		return model.EnterpriseMemberProjection{}, err
	}
	return model.GetEnterpriseMemberProjection(userID, membershipID)
}

// RemoveEnterpriseMember starts or advances the existing drain workflow. A
// single call may finish immediately when there is no outstanding usage. The
// pre-read supplies a safe user projection even when the model has just moved
// the relationship to REMOVED and therefore excludes it from normal queries.
func RemoveEnterpriseMember(userID, membershipID int) (model.EnterpriseMemberProjection, bool, error) {
	projection, err := model.GetEnterpriseMemberProjection(userID, membershipID)
	if err != nil {
		return model.EnterpriseMemberProjection{}, false, err
	}
	member, err := model.BeginEnterpriseMemberRemoval(userID, membershipID)
	if err != nil {
		return model.EnterpriseMemberProjection{}, false, err
	}
	projection.Status = enterpriseMembershipStatusName(member.Status)
	projection.AvailableQuota = member.AvailableQuota
	projection.ReservedQuota = member.ReservedQuota
	return projection, projection.Status != "REMOVED", nil
}

func validManagementIdempotencyKey(key string) bool {
	return len(key) >= 1 && len(key) <= 128 && strings.TrimSpace(key) == key
}

func enterpriseMembershipStatusName(status int) string {
	switch status {
	case model.EnterpriseMembershipStatusActive:
		return "ACTIVE"
	case model.EnterpriseMembershipStatusPaused:
		return "PAUSED"
	case model.EnterpriseMembershipStatusDraining:
		return "DRAINING"
	case model.EnterpriseMembershipStatusReclaimed:
		return "RECLAIMED"
	case model.EnterpriseMembershipStatusRemoved:
		return "REMOVED"
	case model.EnterpriseMembershipStatusManualReview:
		return "MANUAL_REVIEW"
	default:
		return "UNKNOWN"
	}
}
