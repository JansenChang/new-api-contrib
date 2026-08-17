package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"gorm.io/gorm"
)

const AuthFlowPurposeEnterpriseInvite = "enterprise_invite"

const EnterpriseMemberRemovalTimeout = 24 * time.Hour

const (
	// D owns the exported EnterpriseUsage state contract. B only needs these
	// local terminal values until D is integrated into this branch.
	enterpriseUsageStateSettled  = "SETTLED"
	enterpriseUsageStateRefunded = "REFUNDED"
	enterpriseUsageStateAnomaly  = "ANOMALY"
)

var (
	ErrEnterpriseInvitationNotFound       = errors.New("enterprise invitation not found")
	ErrEnterpriseInvitationEmailMismatch  = errors.New("enterprise invitation email does not match user")
	ErrEnterpriseMembershipConflict       = errors.New("user already has an enterprise relationship")
	ErrEnterpriseInvalidMembershipState   = errors.New("invalid enterprise membership state")
	ErrEnterpriseMembershipRemovalPending = errors.New("enterprise membership removal is pending settlement")
)

// EnterpriseInvitationCommand is a server-side command. FlowToken is only
// returned to the future delivery caller and must never be stored or logged.
type EnterpriseInvitationCommand struct {
	ActorUserID int
	TargetEmail string
	ExpiresAt   time.Time
}

type EnterpriseInvitationResult struct {
	Invitation EnterpriseInvitation
	FlowToken  string
}

// EnterpriseInviteeRegistrationCommand only accepts the username supplied by
// the invited person. Email, role and enterprise are always taken from the
// consumed enterprise invitation.
type EnterpriseInviteeRegistrationCommand struct {
	FlowToken string
	Username  string
}

type EnterpriseInviteeRegistrationResult struct {
	User       User
	Membership EnterpriseMembership
	Token      *Token
}

type enterpriseInvitationFlowPayload struct {
	InvitationID int `json:"invitation_id"`
}

// CreateEnterpriseInvitation creates a member-only invitation for the
// caller's active enterprise. It does not send email or open an HTTP route.
func CreateEnterpriseInvitation(command EnterpriseInvitationCommand) (EnterpriseInvitationResult, error) {
	result := EnterpriseInvitationResult{}
	command.TargetEmail = NormalizeEmail(command.TargetEmail)
	if command.ActorUserID <= 0 || command.TargetEmail == "" || len(command.TargetEmail) > 255 || !command.ExpiresAt.After(time.Now()) {
		return result, ErrAuthFlowInvalid
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		enterprise, err := loadActiveOwnedEnterprise(tx, command.ActorUserID)
		if err != nil {
			return err
		}
		invitation := EnterpriseInvitation{
			EnterpriseId:  enterprise.Id,
			InviterUserId: command.ActorUserID,
			TargetEmail:   command.TargetEmail,
			Status:        EnterpriseInvitationStatusPending,
			ExpectedRole:  EnterpriseMembershipRoleMember,
			ExpiresAt:     command.ExpiresAt.Unix(),
			CreatedAt:     common.GetTimestamp(),
		}
		if err := tx.Create(&invitation).Error; err != nil {
			return err
		}
		payload, err := common.Marshal(enterpriseInvitationFlowPayload{InvitationID: invitation.Id})
		if err != nil {
			return err
		}
		flowToken, flow, err := createAuthFlow(tx, AuthFlowCreate{
			Purpose:   AuthFlowPurposeEnterpriseInvite,
			Payload:   string(payload),
			ExpiresAt: command.ExpiresAt,
		})
		if err != nil {
			return err
		}
		if err := tx.Model(&EnterpriseInvitation{}).Where("id = ? AND auth_flow_id = 0", invitation.Id).Update("auth_flow_id", flow.Id).Error; err != nil {
			return err
		}
		invitation.AuthFlowId = int(flow.Id)
		result = EnterpriseInvitationResult{Invitation: invitation, FlowToken: flowToken}
		return nil
	})
	return result, err
}

func RevokeEnterpriseInvitation(actorUserID, invitationID int) error {
	if actorUserID <= 0 || invitationID <= 0 {
		return ErrEnterpriseInvitationNotFound
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		enterprise, err := loadActiveOwnedEnterprise(tx, actorUserID)
		if err != nil {
			return err
		}
		now := common.GetTimestamp()
		updated := tx.Model(&EnterpriseInvitation{}).
			Where("id = ? AND enterprise_id = ? AND status = ?", invitationID, enterprise.Id, EnterpriseInvitationStatusPending).
			Updates(map[string]any{"status": EnterpriseInvitationStatusRevoked, "revoked_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrEnterpriseInvitationNotFound
		}
		return nil
	})
}

// AcceptEnterpriseInvitation accepts an existing-account invitation. The
// unregistered-account path has no approved username/account contract yet and
// is intentionally not implemented here.
func AcceptEnterpriseInvitation(flowToken string, userID int) (*EnterpriseMembership, error) {
	if strings.TrimSpace(flowToken) == "" || userID <= 0 {
		return nil, ErrAuthFlowInvalid
	}
	var membership EnterpriseMembership
	var conflict bool
	_, err := ConsumeAuthFlowWithAction(flowToken, AuthFlowMatch{Purpose: AuthFlowPurposeEnterpriseInvite}, func(tx *gorm.DB, flow *AuthFlow) error {
		var payload enterpriseInvitationFlowPayload
		if flow.Payload == "" || common.Unmarshal([]byte(flow.Payload), &payload) != nil || payload.InvitationID <= 0 {
			return ErrAuthFlowInvalid
		}
		var invitation EnterpriseInvitation
		if err := lockForUpdate(tx).Where("id = ? AND auth_flow_id = ?", payload.InvitationID, flow.Id).First(&invitation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnterpriseInvitationNotFound
			}
			return err
		}
		if invitation.Status != EnterpriseInvitationStatusPending || invitation.ExpectedRole != EnterpriseMembershipRoleMember || invitation.ExpiresAt <= common.GetTimestamp() {
			return ErrEnterpriseInvitationNotFound
		}
		var enterprise Enterprise
		if err := lockForUpdate(tx).Where("id = ? AND status = ?", invitation.EnterpriseId, EnterpriseStatusActive).First(&enterprise).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnterpriseInvitationNotFound
			}
			return err
		}
		var user User
		if err := lockForUpdate(tx).Where("id = ?", userID).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnterpriseInvitationNotFound
			}
			return err
		}
		if NormalizeEmail(user.Email) != invitation.TargetEmail {
			return ErrEnterpriseInvitationEmailMismatch
		}

		var nonRemovedCount int64
		if err := tx.Model(&EnterpriseMembership{}).Where("user_id = ? AND status <> ?", user.Id, EnterpriseMembershipStatusRemoved).Count(&nonRemovedCount).Error; err != nil {
			return err
		}
		var sameEnterpriseCount int64
		if err := tx.Model(&EnterpriseMembership{}).Where("enterprise_id = ? AND user_id = ?", invitation.EnterpriseId, user.Id).Count(&sameEnterpriseCount).Error; err != nil {
			return err
		}
		if user.Role != common.RoleCommonUser || user.ActiveEnterpriseId != 0 || nonRemovedCount > 0 || sameEnterpriseCount > 0 {
			now := common.GetTimestamp()
			updated := tx.Model(&EnterpriseInvitation{}).Where("id = ? AND status = ?", invitation.Id, EnterpriseInvitationStatusPending).Updates(map[string]any{
				"status":           EnterpriseInvitationStatusConflicted,
				"accepted_user_id": user.Id,
				"rejected_at":      now,
			})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return ErrEnterpriseInvitationNotFound
			}
			conflict = true
			return nil
		}

		membership = EnterpriseMembership{
			EnterpriseId: invitation.EnterpriseId,
			UserId:       user.Id,
			Role:         EnterpriseMembershipRoleMember,
			Status:       EnterpriseMembershipStatusActive,
			JoinedAt:     common.GetTimestamp(),
		}
		if err := tx.Create(&membership).Error; err != nil {
			return err
		}
		anchored := tx.Model(&User{}).Where("id = ? AND active_enterprise_id = 0", user.Id).Update("active_enterprise_id", invitation.EnterpriseId)
		if anchored.Error != nil {
			return anchored.Error
		}
		if anchored.RowsAffected != 1 {
			return ErrEnterpriseMembershipConflict
		}
		now := common.GetTimestamp()
		updated := tx.Model(&EnterpriseInvitation{}).Where("id = ? AND status = ?", invitation.Id, EnterpriseInvitationStatusPending).Updates(map[string]any{
			"status":           EnterpriseInvitationStatusAccepted,
			"accepted_user_id": user.Id,
			"accepted_at":      now,
		})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrEnterpriseInvitationNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if conflict {
		return nil, ErrEnterpriseMembershipConflict
	}
	return &membership, nil
}

// RegisterEnterpriseInviteeWithPrimaryToken creates the unregistered
// invitee's ordinary account and member relationship atomically. Delivery of
// the returned full token is deliberately left to the later protected HTTP
// and email orchestration layer.
func RegisterEnterpriseInviteeWithPrimaryToken(command EnterpriseInviteeRegistrationCommand) (EnterpriseInviteeRegistrationResult, error) {
	result := EnterpriseInviteeRegistrationResult{}
	command.FlowToken = strings.TrimSpace(command.FlowToken)
	command.Username = strings.TrimSpace(command.Username)
	if command.FlowToken == "" || command.Username == "" || len(command.Username) > UserNameMaxLength {
		return result, ErrAuthFlowInvalid
	}

	_, err := ConsumeAuthFlowWithAction(command.FlowToken, AuthFlowMatch{Purpose: AuthFlowPurposeEnterpriseInvite}, func(tx *gorm.DB, flow *AuthFlow) error {
		invitation, err := loadPendingEnterpriseInvitation(tx, flow)
		if err != nil {
			return err
		}
		user := User{
			Username:    command.Username,
			DisplayName: command.Username,
			Email:       invitation.TargetEmail,
			Role:        common.RoleCommonUser,
		}
		group := ""
		if setting.DefaultUseAutoGroup {
			group = "auto"
		}
		token, err := RegisterUserWithPrimaryTokenTx(tx, &user, 0, group)
		if err != nil {
			return err
		}
		membership := EnterpriseMembership{
			EnterpriseId: invitation.EnterpriseId,
			UserId:       user.Id,
			Role:         EnterpriseMembershipRoleMember,
			Status:       EnterpriseMembershipStatusActive,
			JoinedAt:     common.GetTimestamp(),
		}
		if err := tx.Create(&membership).Error; err != nil {
			return err
		}
		anchored := tx.Model(&User{}).Where("id = ? AND active_enterprise_id = 0", user.Id).Update("active_enterprise_id", invitation.EnterpriseId)
		if anchored.Error != nil {
			return anchored.Error
		}
		if anchored.RowsAffected != 1 {
			return ErrEnterpriseMembershipConflict
		}
		now := common.GetTimestamp()
		updated := tx.Model(&EnterpriseInvitation{}).Where("id = ? AND status = ?", invitation.Id, EnterpriseInvitationStatusPending).Updates(map[string]any{
			"status":           EnterpriseInvitationStatusAccepted,
			"accepted_user_id": user.Id,
			"accepted_at":      now,
		})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrEnterpriseInvitationNotFound
		}
		user.ActiveEnterpriseId = invitation.EnterpriseId
		result = EnterpriseInviteeRegistrationResult{User: user, Membership: membership, Token: token}
		return nil
	})
	if err != nil {
		return EnterpriseInviteeRegistrationResult{}, err
	}
	result.User.FinalizeOAuthUserCreation(0)
	return result, nil
}

func PauseEnterpriseMember(actorUserID, membershipID int) (*EnterpriseMembership, error) {
	return updateEnterpriseMemberStatus(actorUserID, membershipID, EnterpriseMembershipStatusActive, EnterpriseMembershipStatusPaused)
}

func ResumeEnterpriseMember(actorUserID, membershipID int) (*EnterpriseMembership, error) {
	return updateEnterpriseMemberStatus(actorUserID, membershipID, EnterpriseMembershipStatusPaused, EnterpriseMembershipStatusActive)
}

// BeginEnterpriseMemberRemoval stops new enterprise-funded work by moving the
// relationship to DRAINING, then atomically reclaims its current available
// allocation through C1. D owns later refunds and all usage settlement.
func BeginEnterpriseMemberRemoval(actorUserID, membershipID int) (*EnterpriseMembership, error) {
	return advanceEnterpriseMemberRemoval(actorUserID, membershipID, false, time.Time{})
}

// FinalizeEnterpriseMemberRemoval removes a drained or manually reviewed
// member only after every enterprise usage record has reached a terminal D
// state. It performs the final available-quota reclaim before clearing the
// user's enterprise anchor.
func FinalizeEnterpriseMemberRemoval(actorUserID, membershipID int) (*EnterpriseMembership, error) {
	return advanceEnterpriseMemberRemoval(actorUserID, membershipID, true, time.Time{})
}

// MarkEnterpriseMemberRemovalTimedOut moves a still-in-flight removal to
// MANUAL_REVIEW after the fixed 24-hour drain timeout. The production entry
// point always uses server time; callers cannot supply the clock.
func MarkEnterpriseMemberRemovalTimedOut(actorUserID, membershipID int) (*EnterpriseMembership, error) {
	return markEnterpriseMemberRemovalTimedOutAt(actorUserID, membershipID, time.Now())
}

// markEnterpriseMemberRemovalTimedOutAt is a package-local clock seam for
// deterministic model tests. Keep it unexported so a Controller cannot make a
// removal appear to have timed out early by supplying a future timestamp.
func markEnterpriseMemberRemovalTimedOutAt(actorUserID, membershipID int, now time.Time) (*EnterpriseMembership, error) {
	if now.IsZero() {
		now = time.Now()
	}
	var member EnterpriseMembership
	err := DB.Transaction(func(tx *gorm.DB) error {
		enterprise, err := loadActiveOwnedEnterprise(tx, actorUserID)
		if err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("id = ? AND enterprise_id = ? AND role = ?", membershipID, enterprise.Id, EnterpriseMembershipRoleMember).First(&member).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnterpriseMembershipNotFound
			}
			return err
		}
		if member.Status != EnterpriseMembershipStatusDraining || member.DrainingAt <= 0 || now.Unix() < member.DrainingAt+int64(EnterpriseMemberRemovalTimeout/time.Second) {
			return ErrEnterpriseInvalidMembershipState
		}
		pending, err := hasOutstandingEnterpriseUsage(tx, member.Id)
		if err != nil {
			return err
		}
		if !pending {
			return ErrEnterpriseInvalidMembershipState
		}
		updated := tx.Model(&EnterpriseMembership{}).Where("id = ? AND status = ?", member.Id, EnterpriseMembershipStatusDraining).Update("status", EnterpriseMembershipStatusManualReview)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrEnterpriseInvalidMembershipState
		}
		member.Status = EnterpriseMembershipStatusManualReview
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &member, nil
}

func advanceEnterpriseMemberRemoval(actorUserID, membershipID int, requireExistingDrain bool, _ time.Time) (*EnterpriseMembership, error) {
	if actorUserID <= 0 || membershipID <= 0 {
		return nil, ErrEnterpriseMembershipNotFound
	}
	var member EnterpriseMembership
	err := DB.Transaction(func(tx *gorm.DB) error {
		enterprise, err := loadActiveOwnedEnterprise(tx, actorUserID)
		if err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("id = ? AND enterprise_id = ? AND role = ?", membershipID, enterprise.Id, EnterpriseMembershipRoleMember).First(&member).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnterpriseMembershipNotFound
			}
			return err
		}
		if member.Status == EnterpriseMembershipStatusActive || member.Status == EnterpriseMembershipStatusPaused {
			if requireExistingDrain {
				return ErrEnterpriseInvalidMembershipState
			}
			drainingAt := common.GetTimestamp()
			updated := tx.Model(&EnterpriseMembership{}).Where("id = ? AND status = ?", member.Id, member.Status).Updates(map[string]any{
				"status":      EnterpriseMembershipStatusDraining,
				"draining_at": drainingAt,
			})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return ErrEnterpriseInvalidMembershipState
			}
			member.Status = EnterpriseMembershipStatusDraining
			member.DrainingAt = drainingAt
			if err := reclaimEnterpriseMemberAvailableQuota(tx, enterprise, &member, actorUserID, "initial"); err != nil {
				return err
			}
		} else if member.Status != EnterpriseMembershipStatusDraining && member.Status != EnterpriseMembershipStatusManualReview {
			return ErrEnterpriseInvalidMembershipState
		}

		completed, err := completeEnterpriseMemberRemoval(tx, enterprise, &member, actorUserID)
		if err != nil {
			return err
		}
		if requireExistingDrain && !completed {
			return ErrEnterpriseMembershipRemovalPending
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &member, nil
}

func completeEnterpriseMemberRemoval(tx *gorm.DB, enterprise *Enterprise, member *EnterpriseMembership, actorUserID int) (bool, error) {
	pending, err := hasOutstandingEnterpriseUsage(tx, member.Id)
	if err != nil {
		return false, err
	}
	if pending {
		return false, nil
	}
	if err := lockForUpdate(tx).Where("id = ?", member.Id).First(member).Error; err != nil {
		return false, err
	}
	if err := reclaimEnterpriseMemberAvailableQuota(tx, enterprise, member, actorUserID, "final"); err != nil {
		return false, err
	}
	now := common.GetTimestamp()
	updated := tx.Model(&EnterpriseMembership{}).Where("id = ? AND status IN (?, ?)", member.Id, EnterpriseMembershipStatusDraining, EnterpriseMembershipStatusManualReview).Updates(map[string]any{
		"status":     EnterpriseMembershipStatusRemoved,
		"removed_at": now,
	})
	if updated.Error != nil {
		return false, updated.Error
	}
	if updated.RowsAffected != 1 {
		return false, ErrEnterpriseInvalidMembershipState
	}
	anchored := tx.Model(&User{}).Where("id = ? AND active_enterprise_id = ?", member.UserId, enterprise.Id).Update("active_enterprise_id", 0)
	if anchored.Error != nil {
		return false, anchored.Error
	}
	if anchored.RowsAffected != 1 {
		return false, ErrEnterpriseMembershipConflict
	}
	member.Status = EnterpriseMembershipStatusRemoved
	member.RemovedAt = now
	return true, nil
}

func reclaimEnterpriseMemberAvailableQuota(tx *gorm.DB, enterprise *Enterprise, member *EnterpriseMembership, actorUserID int, phase string) error {
	if member.AvailableQuota == 0 {
		return nil
	}
	if member.AvailableQuota < 0 || member.DrainingAt <= 0 {
		return ErrInvalidQuotaAmount
	}
	key := fmt.Sprintf("member-removal-%s-%d-%d", phase, member.Id, member.DrainingAt)
	result, err := executeEnterpriseMoneyOn(tx, EnterpriseMoneyCommand{
		EnterpriseID:   enterprise.Id,
		MembershipID:   member.Id,
		ActorUserID:    actorUserID,
		Amount:         member.AvailableQuota,
		IdempotencyKey: key,
		ReferenceType:  enterpriseLedgerReferenceTypes[EnterpriseLedgerKindReclaim],
		ReferenceID:    key,
		RequestID:      key,
		Reason:         "enterprise member removal reclaim",
	}, EnterpriseLedgerKindReclaim)
	if err != nil {
		return err
	}
	member.AvailableQuota = result.MemberAvailableQuota
	member.ReservedQuota = result.MemberReservedQuota
	if phase == "initial" {
		updated := tx.Model(&EnterpriseMembership{}).Where("id = ? AND reclaimed_at = 0", member.Id).Update("reclaimed_at", common.GetTimestamp())
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrEnterpriseInvalidMembershipState
		}
		member.ReclaimedAt = common.GetTimestamp()
	}
	return nil
}

func hasOutstandingEnterpriseUsage(tx *gorm.DB, membershipID int) (bool, error) {
	var records []EnterpriseUsageRecord
	if err := lockForUpdate(tx).Where("membership_id = ?", membershipID).Find(&records).Error; err != nil {
		return false, err
	}
	for _, record := range records {
		switch record.State {
		case enterpriseUsageStateSettled, enterpriseUsageStateRefunded, enterpriseUsageStateAnomaly:
			continue
		default:
			return true, nil
		}
	}
	return false, nil
}

func loadPendingEnterpriseInvitation(tx *gorm.DB, flow *AuthFlow) (*EnterpriseInvitation, error) {
	if flow == nil {
		return nil, ErrAuthFlowInvalid
	}
	var payload enterpriseInvitationFlowPayload
	if flow.Payload == "" || common.Unmarshal([]byte(flow.Payload), &payload) != nil || payload.InvitationID <= 0 {
		return nil, ErrAuthFlowInvalid
	}
	invitation := &EnterpriseInvitation{}
	if err := lockForUpdate(tx).Where("id = ? AND auth_flow_id = ?", payload.InvitationID, flow.Id).First(invitation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEnterpriseInvitationNotFound
		}
		return nil, err
	}
	if invitation.Status != EnterpriseInvitationStatusPending || invitation.ExpectedRole != EnterpriseMembershipRoleMember || invitation.ExpiresAt <= common.GetTimestamp() {
		return nil, ErrEnterpriseInvitationNotFound
	}
	var enterprise Enterprise
	if err := lockForUpdate(tx).Where("id = ? AND status = ?", invitation.EnterpriseId, EnterpriseStatusActive).First(&enterprise).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEnterpriseInvitationNotFound
		}
		return nil, err
	}
	return invitation, nil
}

func updateEnterpriseMemberStatus(actorUserID, membershipID, fromStatus, toStatus int) (*EnterpriseMembership, error) {
	if actorUserID <= 0 || membershipID <= 0 {
		return nil, ErrEnterpriseMembershipNotFound
	}
	var member EnterpriseMembership
	err := DB.Transaction(func(tx *gorm.DB) error {
		enterprise, err := loadActiveOwnedEnterprise(tx, actorUserID)
		if err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("id = ? AND enterprise_id = ? AND role = ?", membershipID, enterprise.Id, EnterpriseMembershipRoleMember).First(&member).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnterpriseMembershipNotFound
			}
			return err
		}
		if member.Status != fromStatus {
			return ErrEnterpriseInvalidMembershipState
		}
		pausedAt := int64(0)
		updates := map[string]any{"status": toStatus}
		if toStatus == EnterpriseMembershipStatusPaused {
			pausedAt = common.GetTimestamp()
		}
		updates["paused_at"] = pausedAt
		updated := tx.Model(&EnterpriseMembership{}).Where("id = ? AND enterprise_id = ? AND role = ? AND status = ?", member.Id, enterprise.Id, EnterpriseMembershipRoleMember, fromStatus).Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrEnterpriseInvalidMembershipState
		}
		member.Status = toStatus
		member.PausedAt = pausedAt
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &member, nil
}

func loadActiveOwnedEnterprise(tx *gorm.DB, actorUserID int) (*Enterprise, error) {
	var actor User
	if err := lockForUpdate(tx).Where("id = ?", actorUserID).First(&actor).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEnterpriseOwnerRequired
		}
		return nil, err
	}
	if actor.ActiveEnterpriseId <= 0 {
		return nil, ErrEnterpriseOwnerRequired
	}
	enterprise := &Enterprise{}
	if err := lockForUpdate(tx).Where("id = ? AND owner_user_id = ? AND status = ?", actor.ActiveEnterpriseId, actor.Id, EnterpriseStatusActive).First(enterprise).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEnterpriseOwnerRequired
		}
		return nil, err
	}
	var owner EnterpriseMembership
	if err := lockForUpdate(tx).Where("enterprise_id = ? AND user_id = ? AND role = ? AND status = ?", enterprise.Id, actor.Id, EnterpriseMembershipRoleOwner, EnterpriseMembershipStatusActive).First(&owner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEnterpriseOwnerRequired
		}
		return nil, err
	}
	return enterprise, nil
}
