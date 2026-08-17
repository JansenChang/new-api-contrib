package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrEnterpriseOwnerTargetInvalid = errors.New("enterprise owner target is invalid")
	ErrEnterpriseManagementRequired = errors.New("enterprise membership required")
	ErrEnterpriseQuotaInvalid       = errors.New("enterprise quota is invalid")
	ErrEnterpriseManagementKey      = errors.New("enterprise management idempotency key is invalid")
)

type EnterpriseOwnerAssignmentResult struct {
	UserID       int `json:"user_id"`
	EnterpriseID int `json:"enterprise_id"`
	MembershipID int `json:"membership_id"`
	PlatformRole int `json:"platform_role"`
}

type EnterpriseMemberProjection struct {
	MembershipID   int    `json:"membership_id"`
	UserID         int    `json:"user_id"`
	Username       string `json:"username"`
	DisplayName    string `json:"display_name"`
	Role           string `json:"role"`
	Status         string `json:"status"`
	JoinedAt       int64  `json:"joined_at"`
	PausedAt       int64  `json:"paused_at"`
	AvailableQuota int    `json:"available_quota"`
	ReservedQuota  int    `json:"reserved_quota"`
}

type enterpriseMemberProjectionRow struct {
	MembershipID   int
	UserID         int
	Username       string
	DisplayName    string
	Role           int
	Status         int
	JoinedAt       int64
	PausedAt       int64
	AvailableQuota int
	ReservedQuota  int
}

type EnterpriseSelfSummary struct {
	Enterprise EnterpriseSummaryProjection    `json:"enterprise"`
	Membership EnterpriseMembershipProjection `json:"membership"`
	Wallet     *EnterpriseWalletProjection    `json:"wallet,omitempty"`
}

type EnterpriseSummaryProjection struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type EnterpriseMembershipProjection struct {
	MembershipID   int    `json:"membership_id"`
	Role           string `json:"role"`
	Status         string `json:"status"`
	AvailableQuota int    `json:"available_quota"`
	ReservedQuota  int    `json:"reserved_quota"`
}

type EnterpriseWalletProjection struct {
	AvailableQuota int   `json:"available_quota"`
	ReservedQuota  int   `json:"reserved_quota"`
	AnomalyQuota   int   `json:"anomaly_quota"`
	MemberCount    int64 `json:"member_count"`
}

// SetEnterpriseOwner creates the independent enterprise relationship for an
// ordinary platform user. It never changes User.Role and performs the anchor
// write in the same transaction as the enterprise and membership rows.
func SetEnterpriseOwner(userID int) (EnterpriseOwnerAssignmentResult, error) {
	var result EnterpriseOwnerAssignmentResult
	if DB == nil || userID <= 0 {
		return result, ErrEnterpriseOwnerTargetInvalid
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Where("id = ?", userID).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnterpriseOwnerTargetInvalid
			}
			return err
		}
		if user.Role != common.RoleCommonUser {
			return ErrEnterpriseOwnerTargetInvalid
		}
		var activeMemberships int64
		if err := tx.Model(&EnterpriseMembership{}).Where("user_id = ? AND status <> ?", user.Id, EnterpriseMembershipStatusRemoved).Count(&activeMemberships).Error; err != nil {
			return err
		}
		if user.ActiveEnterpriseId != 0 || activeMemberships != 0 {
			return ErrEnterpriseMembershipConflict
		}
		enterprise := Enterprise{
			Name:        enterpriseNameForUser(&user),
			OwnerUserId: user.Id,
			Status:      EnterpriseStatusActive,
			CreatedAt:   common.GetTimestamp(),
			UpdatedAt:   common.GetTimestamp(),
		}
		if err := tx.Create(&enterprise).Error; err != nil {
			return err
		}
		updated := tx.Model(&User{}).Where("id = ? AND role = ? AND active_enterprise_id = 0", user.Id, common.RoleCommonUser).Update("active_enterprise_id", enterprise.Id)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrEnterpriseMembershipConflict
		}
		membership := EnterpriseMembership{
			EnterpriseId: enterprise.Id,
			UserId:       user.Id,
			Role:         EnterpriseMembershipRoleOwner,
			Status:       EnterpriseMembershipStatusActive,
			JoinedAt:     common.GetTimestamp(),
		}
		if err := tx.Create(&membership).Error; err != nil {
			return err
		}
		result = EnterpriseOwnerAssignmentResult{UserID: user.Id, EnterpriseID: enterprise.Id, MembershipID: membership.Id, PlatformRole: user.Role}
		return nil
	})
	return result, err
}

// AllocateEnterpriseQuotaForManagement and ReclaimEnterpriseQuotaForManagement
// are the only management-facing wrappers around the C1 money commands.
func AllocateEnterpriseQuotaForManagement(actorUserID, membershipID, amount int, operation, requestKey string) (EnterpriseMoneyResult, error) {
	return executeManagementQuotaCommand(actorUserID, membershipID, amount, operation, requestKey, EnterpriseLedgerKindAllocate)
}

func ReclaimEnterpriseQuotaForManagement(actorUserID, membershipID, amount int, operation, requestKey string) (EnterpriseMoneyResult, error) {
	return executeManagementQuotaCommand(actorUserID, membershipID, amount, operation, requestKey, EnterpriseLedgerKindReclaim)
}

func executeManagementQuotaCommand(actorUserID, membershipID, amount int, operation, requestKey, kind string) (EnterpriseMoneyResult, error) {
	var result EnterpriseMoneyResult
	if actorUserID <= 0 || membershipID <= 0 || amount <= 0 || amount > common.MaxQuota || len(requestKey) < 1 || len(requestKey) > 128 || strings.TrimSpace(requestKey) != requestKey || operation == "" {
		return result, ErrEnterpriseQuotaInvalid
	}
	enterpriseID, member, err := getManagementOwnerAndMember(actorUserID, membershipID, kind)
	if err != nil {
		return result, err
	}
	if kind == EnterpriseLedgerKindReclaim && amount > member.AvailableQuota {
		return result, ErrEnterpriseInsufficientQuota
	}
	managementDigest := enterpriseLedgerHash(requestKey, 0)
	commandKey := fmt.Sprintf("enterprise-management:%s", managementDigest)
	referenceID := fmt.Sprintf("enterprise-management:%s", managementDigest)
	command := EnterpriseMoneyCommand{
		EnterpriseID:   enterpriseID,
		MembershipID:   membershipID,
		ActorUserID:    actorUserID,
		Amount:         amount,
		IdempotencyKey: commandKey,
		ReferenceType:  enterpriseLedgerReferenceTypes[kind],
		ReferenceID:    referenceID,
		RequestID:      managementDigest,
		Reason:         "enterprise management " + operation,
	}
	if kind == EnterpriseLedgerKindAllocate {
		return AllocateEnterpriseQuota(command)
	}
	return ReclaimEnterpriseQuota(command)
}

func getManagementOwnerAndMember(actorUserID, membershipID int, kind string) (int, *EnterpriseMembership, error) {
	if actorUserID <= 0 || membershipID <= 0 {
		return 0, nil, ErrEnterpriseOwnerRequired
	}
	var actor User
	if err := DB.Select("id", "active_enterprise_id").Where("id = ?", actorUserID).First(&actor).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, ErrEnterpriseOwnerRequired
		}
		return 0, nil, err
	}
	if actor.ActiveEnterpriseId <= 0 {
		return 0, nil, ErrEnterpriseOwnerRequired
	}
	var enterprise Enterprise
	if err := DB.Where("id = ? AND owner_user_id = ? AND status = ?", actor.ActiveEnterpriseId, actor.Id, EnterpriseStatusActive).First(&enterprise).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, ErrEnterpriseOwnerRequired
		}
		return 0, nil, err
	}
	var owner EnterpriseMembership
	if err := DB.Where("enterprise_id = ? AND user_id = ? AND role = ? AND status = ?", enterprise.Id, actor.Id, EnterpriseMembershipRoleOwner, EnterpriseMembershipStatusActive).First(&owner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, ErrEnterpriseOwnerRequired
		}
		return 0, nil, err
	}
	member := &EnterpriseMembership{}
	if err := DB.Where("id = ? AND enterprise_id = ? AND role = ?", membershipID, enterprise.Id, EnterpriseMembershipRoleMember).First(member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, ErrEnterpriseMembershipNotFound
		}
		return 0, nil, err
	}
	if kind == EnterpriseLedgerKindAllocate && member.Status != EnterpriseMembershipStatusActive {
		return 0, nil, ErrEnterpriseMembershipNotFound
	}
	if kind == EnterpriseLedgerKindReclaim && member.Status != EnterpriseMembershipStatusActive && member.Status != EnterpriseMembershipStatusPaused {
		return 0, nil, ErrEnterpriseMembershipNotFound
	}
	return enterprise.Id, member, nil
}

func ListEnterpriseMembers(actorUserID, offset, limit int) ([]EnterpriseMemberProjection, int64, error) {
	enterpriseID, err := managementOwnerEnterpriseID(actorUserID)
	if err != nil {
		return nil, 0, err
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 100 {
		limit = common.ItemsPerPage
	}
	var total int64
	query := DB.Model(&EnterpriseMembership{}).Where("enterprise_memberships.enterprise_id = ? AND enterprise_memberships.status <> ?", enterpriseID, EnterpriseMembershipStatusRemoved)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []enterpriseMemberProjectionRow
	if err := query.Select("enterprise_memberships.id AS membership_id, enterprise_memberships.user_id, users.username, users.display_name, enterprise_memberships.role, enterprise_memberships.status, enterprise_memberships.joined_at, enterprise_memberships.paused_at, enterprise_memberships.available_quota, enterprise_memberships.reserved_quota").Joins("JOIN users ON users.id = enterprise_memberships.user_id AND users.deleted_at IS NULL").Order("enterprise_memberships.id ASC").Offset(offset).Limit(limit).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]EnterpriseMemberProjection, len(rows))
	for i, row := range rows {
		items[i] = EnterpriseMemberProjection{
			MembershipID: row.MembershipID, UserID: row.UserID, Username: row.Username, DisplayName: row.DisplayName,
			Role: enterpriseMembershipRoleName(row.Role), Status: enterpriseMembershipStatusName(row.Status),
			JoinedAt: row.JoinedAt, PausedAt: row.PausedAt, AvailableQuota: row.AvailableQuota, ReservedQuota: row.ReservedQuota,
		}
	}
	return items, total, nil
}

func GetEnterpriseMemberProjection(actorUserID, membershipID int) (EnterpriseMemberProjection, error) {
	var projection EnterpriseMemberProjection
	enterpriseID, err := managementOwnerEnterpriseID(actorUserID)
	if err != nil {
		return projection, err
	}
	var row enterpriseMemberProjectionRow
	query := DB.Model(&EnterpriseMembership{}).
		Select("enterprise_memberships.id AS membership_id, enterprise_memberships.user_id, users.username, users.display_name, enterprise_memberships.role, enterprise_memberships.status, enterprise_memberships.joined_at, enterprise_memberships.paused_at, enterprise_memberships.available_quota, enterprise_memberships.reserved_quota").
		Joins("JOIN users ON users.id = enterprise_memberships.user_id AND users.deleted_at IS NULL").
		Where("enterprise_memberships.enterprise_id = ? AND enterprise_memberships.id = ? AND enterprise_memberships.status <> ?", enterpriseID, membershipID, EnterpriseMembershipStatusRemoved)
	if err := query.First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return projection, ErrEnterpriseMembershipNotFound
		}
		return projection, err
	}
	return EnterpriseMemberProjection{
		MembershipID: row.MembershipID, UserID: row.UserID, Username: row.Username, DisplayName: row.DisplayName,
		Role: enterpriseMembershipRoleName(row.Role), Status: enterpriseMembershipStatusName(row.Status),
		JoinedAt: row.JoinedAt, PausedAt: row.PausedAt, AvailableQuota: row.AvailableQuota, ReservedQuota: row.ReservedQuota,
	}, nil
}

func GetEnterpriseSelf(actorUserID int) (EnterpriseSelfSummary, error) {
	var result EnterpriseSelfSummary
	if actorUserID <= 0 {
		return result, ErrEnterpriseManagementRequired
	}
	var user User
	if err := DB.Select("id", "active_enterprise_id").Where("id = ?", actorUserID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, ErrEnterpriseManagementRequired
		}
		return result, err
	}
	if user.ActiveEnterpriseId <= 0 {
		return result, ErrEnterpriseManagementRequired
	}
	var enterprise Enterprise
	if err := DB.Where("id = ?", user.ActiveEnterpriseId).First(&enterprise).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, ErrEnterpriseManagementRequired
		}
		return result, err
	}
	if enterprise.Status != EnterpriseStatusActive {
		return result, ErrEnterpriseManagementRequired
	}
	var membership EnterpriseMembership
	if err := DB.Where("enterprise_id = ? AND user_id = ? AND status <> ?", enterprise.Id, actorUserID, EnterpriseMembershipStatusRemoved).First(&membership).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, ErrEnterpriseManagementRequired
		}
		return result, err
	}
	if membership.Role == EnterpriseMembershipRoleOwner {
		if membership.Status != EnterpriseMembershipStatusActive || enterprise.OwnerUserId != actorUserID {
			return result, ErrEnterpriseManagementRequired
		}
	}
	if membership.Role != EnterpriseMembershipRoleOwner && !enterpriseMemberSelfVisible(membership.Status) {
		return result, ErrEnterpriseManagementRequired
	}
	result.Enterprise = EnterpriseSummaryProjection{ID: enterprise.Id, Name: enterprise.Name, Status: enterpriseStatusName(enterprise.Status)}
	result.Membership = EnterpriseMembershipProjection{MembershipID: membership.Id, Role: enterpriseMembershipRoleName(membership.Role), Status: enterpriseMembershipStatusName(membership.Status), AvailableQuota: membership.AvailableQuota, ReservedQuota: membership.ReservedQuota}
	if membership.Role == EnterpriseMembershipRoleOwner {
		var count int64
		if err := DB.Model(&EnterpriseMembership{}).Where("enterprise_id = ? AND status <> ? AND role = ?", enterprise.Id, EnterpriseMembershipStatusRemoved, EnterpriseMembershipRoleMember).Count(&count).Error; err != nil {
			return EnterpriseSelfSummary{}, err
		}
		result.Wallet = &EnterpriseWalletProjection{AvailableQuota: enterprise.AvailableQuota, ReservedQuota: enterprise.ReservedQuota, AnomalyQuota: enterprise.AnomalyQuota, MemberCount: count}
	}
	return result, nil
}

func managementOwnerEnterpriseID(actorUserID int) (int, error) {
	if actorUserID <= 0 {
		return 0, ErrEnterpriseOwnerRequired
	}
	var enterpriseID int
	err := DB.Transaction(func(tx *gorm.DB) error {
		enterprise, err := loadActiveOwnedEnterprise(tx, actorUserID)
		if err != nil {
			return err
		}
		enterpriseID = enterprise.Id
		return nil
	})
	return enterpriseID, err
}

func enterpriseMemberSelfVisible(status int) bool {
	switch status {
	case EnterpriseMembershipStatusActive, EnterpriseMembershipStatusPaused, EnterpriseMembershipStatusDraining, EnterpriseMembershipStatusManualReview, EnterpriseMembershipStatusReclaimed:
		return true
	default:
		return false
	}
}

func enterpriseStatusName(status int) string {
	switch status {
	case EnterpriseStatusActive:
		return "ACTIVE"
	case EnterpriseStatusClosing:
		return "CLOSING"
	case EnterpriseStatusClosed:
		return "CLOSED"
	default:
		return "UNKNOWN"
	}
}

func enterpriseMembershipRoleName(role int) string {
	if role == EnterpriseMembershipRoleOwner {
		return "OWNER"
	}
	return "MEMBER"
}

func enterpriseMembershipStatusName(status int) string {
	switch status {
	case EnterpriseMembershipStatusActive:
		return "ACTIVE"
	case EnterpriseMembershipStatusPaused:
		return "PAUSED"
	case EnterpriseMembershipStatusDraining:
		return "DRAINING"
	case EnterpriseMembershipStatusReclaimed:
		return "RECLAIMED"
	case EnterpriseMembershipStatusRemoved:
		return "REMOVED"
	case EnterpriseMembershipStatusManualReview:
		return "MANUAL_REVIEW"
	default:
		return "UNKNOWN"
	}
}
