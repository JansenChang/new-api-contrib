package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// EnterpriseLedgerItem is the deliberately small public projection of an
// append-only ledger row. Internal references and command material never
// cross this boundary.
type EnterpriseLedgerItem struct {
	ID                       int64  `json:"id"`
	Kind                     string `json:"kind"`
	Amount                   int    `json:"amount"`
	EnterpriseAvailableDelta int    `json:"enterprise_available_delta"`
	EnterpriseReservedDelta  int    `json:"enterprise_reserved_delta"`
	MemberAvailableDelta     int    `json:"member_available_delta"`
	MemberReservedDelta      int    `json:"member_reserved_delta"`
	CreatedAt                int64  `json:"created_at"`
}

// EnterpriseUsageItem is the public projection of an enterprise-funded
// request. It intentionally excludes request, idempotency and channel data.
type EnterpriseUsageItem struct {
	ID                     int64  `json:"id"`
	ModelName              string `json:"model_name"`
	FundingSource          string `json:"funding_source"`
	MembershipRoleSnapshot int    `json:"membership_role_snapshot"`
	State                  string `json:"state"`
	ReservedQuota          int    `json:"reserved_quota"`
	SettledQuota           int    `json:"settled_quota"`
	RefundedQuota          int    `json:"refunded_quota"`
	AnomalyQuota           int    `json:"anomaly_quota"`
	CreatedAt              int64  `json:"created_at"`
	SettledAt              int64  `json:"settled_at"`
	RefundedAt             int64  `json:"refunded_at"`
}

func normalizeEnterpriseReadPagination(offset, limit int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 100 {
		limit = common.ItemsPerPage
	}
	return offset, limit
}

// ListEnterpriseLedger returns only the active owner's current enterprise
// ledger. The enterprise lookup is independent from member status, so an
// owner's historical rows remain visible after a member is removed.
func ListEnterpriseLedger(actorUserID, offset, limit int) ([]EnterpriseLedgerItem, int64, error) {
	enterpriseID, _, owner, err := enterpriseUsageScope(actorUserID)
	if err != nil {
		return nil, 0, err
	}
	if !owner {
		return nil, 0, ErrEnterpriseOwnerRequired
	}
	offset, limit = normalizeEnterpriseReadPagination(offset, limit)
	query := DB.Model(&EnterpriseLedger{}).Where("enterprise_id = ?", enterpriseID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]EnterpriseLedgerItem, 0)
	if err := query.Select("id, kind, amount, enterprise_available_delta, enterprise_reserved_delta, member_available_delta, member_reserved_delta, created_at").Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Scan(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListEnterpriseUsage returns all enterprise usage for an owner, or only the
// current member's records for a member. The scope is always derived from the
// authenticated user's active enterprise anchor.
func ListEnterpriseUsage(actorUserID, offset, limit int) ([]EnterpriseUsageItem, int64, error) {
	enterpriseID, membershipID, owner, err := enterpriseUsageScope(actorUserID)
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizeEnterpriseReadPagination(offset, limit)
	query := DB.Model(&EnterpriseUsageRecord{}).Where("enterprise_id = ?", enterpriseID)
	if !owner {
		query = query.Where("membership_id = ? AND actor_user_id = ?", membershipID, actorUserID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]EnterpriseUsageItem, 0)
	if err := query.Select("id, model_name, funding_source, membership_role_snapshot, state, reserved_quota, settled_quota, refunded_quota, anomaly_quota, created_at, settled_at, refunded_at").Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Scan(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func enterpriseUsageScope(actorUserID int) (enterpriseID, membershipID int, owner bool, err error) {
	if actorUserID <= 0 {
		return 0, 0, false, ErrEnterpriseManagementRequired
	}
	var user User
	if err := DB.Select("id, active_enterprise_id").Where("id = ?", actorUserID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, 0, false, ErrEnterpriseManagementRequired
		}
		return 0, 0, false, err
	}
	if user.ActiveEnterpriseId <= 0 {
		return 0, 0, false, ErrEnterpriseManagementRequired
	}
	var enterprise Enterprise
	if err := DB.Where("id = ? AND status = ?", user.ActiveEnterpriseId, EnterpriseStatusActive).First(&enterprise).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, 0, false, ErrEnterpriseManagementRequired
		}
		return 0, 0, false, err
	}
	var membership EnterpriseMembership
	if err := DB.Where("enterprise_id = ? AND user_id = ? AND status <> ?", enterprise.Id, actorUserID, EnterpriseMembershipStatusRemoved).First(&membership).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, 0, false, ErrEnterpriseManagementRequired
		}
		return 0, 0, false, err
	}
	if membership.Role == EnterpriseMembershipRoleOwner {
		if membership.Status != EnterpriseMembershipStatusActive || enterprise.OwnerUserId != actorUserID {
			return 0, 0, false, ErrEnterpriseManagementRequired
		}
		return enterprise.Id, membership.Id, true, nil
	}
	if membership.Role != EnterpriseMembershipRoleMember || !enterpriseMemberSelfVisible(membership.Status) {
		return 0, 0, false, ErrEnterpriseManagementRequired
	}
	return enterprise.Id, membership.Id, false, nil
}
