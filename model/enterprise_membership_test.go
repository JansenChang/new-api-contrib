package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newEnterpriseMembershipTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousType := common.MainDatabaseType()
	previousSecret := common.SessionSecret
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SessionSecret = "enterprise-membership-test-secret"
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	DB, LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &Enterprise{}, &EnterpriseMembership{}, &EnterpriseInvitation{}, &EnterpriseLedger{}, &EnterpriseUsageRecord{}, &AuthFlow{}))
	require.NoError(t, migrateEnterpriseLedgerIndexes(db))
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousType)
		common.SessionSecret = previousSecret
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func enterpriseMembershipOwner(t *testing.T, username, email string) (*User, *Enterprise) {
	t.Helper()
	owner := &User{Username: username, Email: NormalizeEmail(email), Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "enterprise-owner-" + username}
	require.NoError(t, DB.Create(owner).Error)
	enterprise := &Enterprise{Name: username + " Enterprise", OwnerUserId: owner.Id, Status: EnterpriseStatusActive}
	require.NoError(t, DB.Create(enterprise).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", owner.Id).Update("active_enterprise_id", enterprise.Id).Error)
	owner.ActiveEnterpriseId = enterprise.Id
	require.NoError(t, DB.Create(&EnterpriseMembership{
		EnterpriseId: enterprise.Id,
		UserId:       owner.Id,
		Role:         EnterpriseMembershipRoleOwner,
		Status:       EnterpriseMembershipStatusActive,
		JoinedAt:     common.GetTimestamp(),
	}).Error)
	return owner, enterprise
}

func enterpriseMembershipUser(t *testing.T, username, email string) *User {
	t.Helper()
	user := &User{Username: username, Email: email, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "enterprise-member-" + username}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func TestEnterpriseMembershipInviteAcceptsMatchingExistingUser(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	owner, enterprise := enterpriseMembershipOwner(t, "owner", "owner@example.com")
	memberUser := enterpriseMembershipUser(t, "member", "Member@Example.com")

	created, err := CreateEnterpriseInvitation(EnterpriseInvitationCommand{
		ActorUserID: owner.Id,
		TargetEmail: " MEMBER@example.COM ",
		ExpiresAt:   time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, created.FlowToken)
	assert.Equal(t, EnterpriseMembershipRoleMember, created.Invitation.ExpectedRole)
	assert.Equal(t, "member@example.com", created.Invitation.TargetEmail)

	var flow AuthFlow
	require.NoError(t, DB.First(&flow, int64(created.Invitation.AuthFlowId)).Error)
	assert.Equal(t, AuthFlowPurposeEnterpriseInvite, flow.Purpose)
	assert.NotEqual(t, AuthFlowPurposeUserInvite, flow.Purpose)

	membership, err := AcceptEnterpriseInvitation(created.FlowToken, memberUser.Id)
	require.NoError(t, err)
	assert.Equal(t, enterprise.Id, membership.EnterpriseId)
	assert.Equal(t, EnterpriseMembershipRoleMember, membership.Role)
	assert.Equal(t, EnterpriseMembershipStatusActive, membership.Status)

	var savedUser User
	var invitation EnterpriseInvitation
	require.NoError(t, DB.First(&savedUser, memberUser.Id).Error)
	require.NoError(t, DB.First(&invitation, created.Invitation.Id).Error)
	assert.Equal(t, enterprise.Id, savedUser.ActiveEnterpriseId)
	assert.Equal(t, EnterpriseInvitationStatusAccepted, invitation.Status)
	assert.Equal(t, memberUser.Id, invitation.AcceptedUserId)
	assert.NotZero(t, invitation.AcceptedAt)
}

func TestEnterpriseMembershipInviteEmailMismatchDoesNotConsume(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	owner, _ := enterpriseMembershipOwner(t, "owner", "owner@example.com")
	wrongUser := enterpriseMembershipUser(t, "wrong", "wrong@example.com")
	created, err := CreateEnterpriseInvitation(EnterpriseInvitationCommand{ActorUserID: owner.Id, TargetEmail: "member@example.com", ExpiresAt: time.Now().Add(time.Hour)})
	require.NoError(t, err)

	_, err = AcceptEnterpriseInvitation(created.FlowToken, wrongUser.Id)
	require.ErrorIs(t, err, ErrEnterpriseInvitationEmailMismatch)
	var flow AuthFlow
	var invitation EnterpriseInvitation
	require.NoError(t, DB.First(&flow, int64(created.Invitation.AuthFlowId)).Error)
	require.NoError(t, DB.First(&invitation, created.Invitation.Id).Error)
	assert.Nil(t, flow.ConsumedAt)
	assert.Equal(t, EnterpriseInvitationStatusPending, invitation.Status)
}

func TestEnterpriseMembershipInviteRegistersNewUserWithSelfChosenUsername(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	owner, enterprise := enterpriseMembershipOwner(t, "owner", "owner@example.com")
	created, err := CreateEnterpriseInvitation(EnterpriseInvitationCommand{ActorUserID: owner.Id, TargetEmail: "new-member@example.com", ExpiresAt: time.Now().Add(time.Hour)})
	require.NoError(t, err)

	registered, err := RegisterEnterpriseInviteeWithPrimaryToken(EnterpriseInviteeRegistrationCommand{
		FlowToken: created.FlowToken,
		Username:  "new-member",
	})
	require.NoError(t, err)
	require.NotNil(t, registered.Token)
	assert.Equal(t, "new-member", registered.User.Username)
	assert.Equal(t, "new-member@example.com", registered.User.Email)
	assert.Equal(t, common.RoleCommonUser, registered.User.Role)
	assert.Equal(t, enterprise.Id, registered.User.ActiveEnterpriseId)
	assert.Equal(t, EnterpriseMembershipRoleMember, registered.Membership.Role)

	var invitation EnterpriseInvitation
	var tokenCount int64
	require.NoError(t, DB.First(&invitation, created.Invitation.Id).Error)
	require.NoError(t, DB.Model(&Token{}).Where("user_id = ?", registered.User.Id).Count(&tokenCount).Error)
	assert.Equal(t, EnterpriseInvitationStatusAccepted, invitation.Status)
	assert.EqualValues(t, 1, tokenCount)
}

func TestEnterpriseMembershipInviteConflictsWithExistingRelationship(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	firstOwner, firstEnterprise := enterpriseMembershipOwner(t, "first-owner", "first-owner@example.com")
	memberUser := enterpriseMembershipUser(t, "member", "member@example.com")
	firstMembership := &EnterpriseMembership{EnterpriseId: firstEnterprise.Id, UserId: memberUser.Id, Role: EnterpriseMembershipRoleMember, Status: EnterpriseMembershipStatusActive, JoinedAt: common.GetTimestamp()}
	require.NoError(t, DB.Create(firstMembership).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", memberUser.Id).Update("active_enterprise_id", firstEnterprise.Id).Error)
	secondOwner, _ := enterpriseMembershipOwner(t, "second-owner", "second-owner@example.com")
	created, err := CreateEnterpriseInvitation(EnterpriseInvitationCommand{ActorUserID: secondOwner.Id, TargetEmail: memberUser.Email, ExpiresAt: time.Now().Add(time.Hour)})
	require.NoError(t, err)

	_, err = AcceptEnterpriseInvitation(created.FlowToken, memberUser.Id)
	require.ErrorIs(t, err, ErrEnterpriseMembershipConflict)
	var invitation EnterpriseInvitation
	var flow AuthFlow
	var membershipCount int64
	require.NoError(t, DB.First(&invitation, created.Invitation.Id).Error)
	require.NoError(t, DB.First(&flow, int64(created.Invitation.AuthFlowId)).Error)
	require.NoError(t, DB.Model(&EnterpriseMembership{}).Where("user_id = ?", memberUser.Id).Count(&membershipCount).Error)
	assert.Equal(t, EnterpriseInvitationStatusConflicted, invitation.Status)
	assert.Equal(t, memberUser.Id, invitation.AcceptedUserId)
	assert.NotNil(t, flow.ConsumedAt)
	assert.EqualValues(t, 1, membershipCount)
	assert.Equal(t, firstOwner.ActiveEnterpriseId, firstEnterprise.Id)
}

func TestEnterpriseMembershipPauseAndResumeOnlyChangesRelationship(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	owner, enterprise := enterpriseMembershipOwner(t, "owner", "owner@example.com")
	memberUser := enterpriseMembershipUser(t, "member", "member@example.com")
	membership := &EnterpriseMembership{EnterpriseId: enterprise.Id, UserId: memberUser.Id, Role: EnterpriseMembershipRoleMember, Status: EnterpriseMembershipStatusActive, JoinedAt: common.GetTimestamp()}
	require.NoError(t, DB.Create(membership).Error)

	paused, err := PauseEnterpriseMember(owner.Id, membership.Id)
	require.NoError(t, err)
	assert.Equal(t, EnterpriseMembershipStatusPaused, paused.Status)
	assert.NotZero(t, paused.PausedAt)
	var savedUser User
	require.NoError(t, DB.First(&savedUser, memberUser.Id).Error)
	assert.Equal(t, common.UserStatusEnabled, savedUser.Status)
	_, err = PauseEnterpriseMember(owner.Id, membership.Id)
	require.ErrorIs(t, err, ErrEnterpriseInvalidMembershipState)

	resumed, err := ResumeEnterpriseMember(owner.Id, membership.Id)
	require.NoError(t, err)
	assert.Equal(t, EnterpriseMembershipStatusActive, resumed.Status)
	assert.Zero(t, resumed.PausedAt)
	_, err = ResumeEnterpriseMember(owner.Id, membership.Id)
	require.ErrorIs(t, err, ErrEnterpriseInvalidMembershipState)
}

func TestEnterpriseMembershipRejectsCrossEnterpriseMemberControl(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	_, enterprise := enterpriseMembershipOwner(t, "owner", "owner@example.com")
	otherOwner, otherEnterprise := enterpriseMembershipOwner(t, "other-owner", "other-owner@example.com")
	memberUser := enterpriseMembershipUser(t, "member", "member@example.com")
	membership := &EnterpriseMembership{EnterpriseId: enterprise.Id, UserId: memberUser.Id, Role: EnterpriseMembershipRoleMember, Status: EnterpriseMembershipStatusActive, JoinedAt: common.GetTimestamp()}
	require.NoError(t, DB.Create(membership).Error)

	_, err := PauseEnterpriseMember(otherOwner.Id, membership.Id)
	require.ErrorIs(t, err, ErrEnterpriseMembershipNotFound)
	var saved EnterpriseMembership
	require.NoError(t, DB.First(&saved, membership.Id).Error)
	assert.Equal(t, EnterpriseMembershipStatusActive, saved.Status)
	assert.NotEqual(t, otherEnterprise.Id, saved.EnterpriseId)
}

func TestEnterpriseMembershipRemovalReclaimsBeforeImmediateRemoval(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	owner, enterprise := enterpriseMembershipOwner(t, "owner", "owner@example.com")
	memberUser := enterpriseMembershipUser(t, "member", "member@example.com")
	membership := &EnterpriseMembership{EnterpriseId: enterprise.Id, UserId: memberUser.Id, Role: EnterpriseMembershipRoleMember, Status: EnterpriseMembershipStatusActive, AvailableQuota: 42, JoinedAt: common.GetTimestamp()}
	require.NoError(t, DB.Create(membership).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", memberUser.Id).Update("active_enterprise_id", enterprise.Id).Error)

	removed, err := BeginEnterpriseMemberRemoval(owner.Id, membership.Id)
	require.NoError(t, err)
	assert.Equal(t, EnterpriseMembershipStatusRemoved, removed.Status)
	var savedEnterprise Enterprise
	var savedMembership EnterpriseMembership
	var savedUser User
	var reclaimCount int64
	require.NoError(t, DB.First(&savedEnterprise, enterprise.Id).Error)
	require.NoError(t, DB.First(&savedMembership, membership.Id).Error)
	require.NoError(t, DB.First(&savedUser, memberUser.Id).Error)
	require.NoError(t, DB.Model(&EnterpriseLedger{}).Where("membership_id = ? AND kind = ?", membership.Id, EnterpriseLedgerKindReclaim).Count(&reclaimCount).Error)
	assert.Equal(t, 42, savedEnterprise.AvailableQuota)
	assert.Zero(t, savedMembership.AvailableQuota)
	assert.Equal(t, EnterpriseMembershipStatusRemoved, savedMembership.Status)
	assert.Zero(t, savedUser.ActiveEnterpriseId)
	assert.EqualValues(t, 1, reclaimCount)
}

func TestEnterpriseMembershipRemovalDrainsThenTimesOutForManualReview(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	owner, enterprise := enterpriseMembershipOwner(t, "owner", "owner@example.com")
	memberUser := enterpriseMembershipUser(t, "member", "member@example.com")
	membership := &EnterpriseMembership{EnterpriseId: enterprise.Id, UserId: memberUser.Id, Role: EnterpriseMembershipRoleMember, Status: EnterpriseMembershipStatusActive, AvailableQuota: 10, JoinedAt: common.GetTimestamp()}
	require.NoError(t, DB.Create(membership).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", memberUser.Id).Update("active_enterprise_id", enterprise.Id).Error)
	require.NoError(t, DB.Create(&EnterpriseUsageRecord{EnterpriseId: enterprise.Id, MembershipId: membership.Id, ActorUserId: memberUser.Id, TokenId: 99, IdempotencyKey: "draining-usage", FundingSource: "enterprise_allocation", State: "UPSTREAM_SUBMITTED"}).Error)

	draining, err := BeginEnterpriseMemberRemoval(owner.Id, membership.Id)
	require.NoError(t, err)
	assert.Equal(t, EnterpriseMembershipStatusDraining, draining.Status)
	assert.NotZero(t, draining.DrainingAt)
	_, err = FinalizeEnterpriseMemberRemoval(owner.Id, membership.Id)
	require.ErrorIs(t, err, ErrEnterpriseMembershipRemovalPending)

	tooEarly, err := markEnterpriseMemberRemovalTimedOutAt(owner.Id, membership.Id, time.Unix(draining.DrainingAt, 0).Add(EnterpriseMemberRemovalTimeout-time.Second))
	assert.Nil(t, tooEarly)
	require.ErrorIs(t, err, ErrEnterpriseInvalidMembershipState)

	manual, err := markEnterpriseMemberRemovalTimedOutAt(owner.Id, membership.Id, time.Unix(draining.DrainingAt, 0).Add(EnterpriseMemberRemovalTimeout))
	require.NoError(t, err)
	assert.Equal(t, EnterpriseMembershipStatusManualReview, manual.Status)
	require.NoError(t, DB.Model(&EnterpriseUsageRecord{}).Where("membership_id = ?", membership.Id).Update("state", enterpriseUsageStateRefunded).Error)
	removed, err := FinalizeEnterpriseMemberRemoval(owner.Id, membership.Id)
	require.NoError(t, err)
	assert.Equal(t, EnterpriseMembershipStatusRemoved, removed.Status)
	var savedEnterprise Enterprise
	require.NoError(t, DB.First(&savedEnterprise, enterprise.Id).Error)
	assert.Equal(t, 10, savedEnterprise.AvailableQuota)
}
