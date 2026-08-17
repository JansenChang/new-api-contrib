package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enterpriseReadUser(t *testing.T, username string, role, enterpriseID int) *User {
	t.Helper()
	user := &User{Username: username, AffCode: username, Role: role, Status: common.UserStatusEnabled, ActiveEnterpriseId: enterpriseID}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func enterpriseReadEnterprise(t *testing.T, name string, owner *User) *Enterprise {
	t.Helper()
	enterprise := &Enterprise{Name: name, OwnerUserId: owner.Id, Status: EnterpriseStatusActive}
	require.NoError(t, DB.Create(enterprise).Error)
	require.NoError(t, DB.Model(owner).Update("active_enterprise_id", enterprise.Id).Error)
	require.NoError(t, DB.Create(&EnterpriseMembership{EnterpriseId: enterprise.Id, UserId: owner.Id, Role: EnterpriseMembershipRoleOwner, Status: EnterpriseMembershipStatusActive}).Error)
	return enterprise
}

func TestEnterpriseReadProjectionsAndScope(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	require.NoError(t, DB.AutoMigrate(&EnterpriseUsageRecord{}))

	ownerA := enterpriseReadUser(t, "read-owner-a", common.RoleCommonUser, 0)
	enterpriseA := enterpriseReadEnterprise(t, "read-a", ownerA)
	ownerB := enterpriseReadUser(t, "read-owner-b", common.RoleCommonUser, 0)
	enterpriseB := enterpriseReadEnterprise(t, "read-b", ownerB)
	memberA := enterpriseReadUser(t, "read-member-a", common.RoleCommonUser, enterpriseA.Id)
	memberB := enterpriseReadUser(t, "read-member-b", common.RoleCommonUser, enterpriseA.Id)
	membershipA := &EnterpriseMembership{EnterpriseId: enterpriseA.Id, UserId: memberA.Id, Role: EnterpriseMembershipRoleMember, Status: EnterpriseMembershipStatusActive}
	membershipB := &EnterpriseMembership{EnterpriseId: enterpriseA.Id, UserId: memberB.Id, Role: EnterpriseMembershipRoleMember, Status: EnterpriseMembershipStatusActive}
	require.NoError(t, DB.Create(membershipA).Error)
	require.NoError(t, DB.Create(membershipB).Error)
	removed := &EnterpriseMembership{EnterpriseId: enterpriseB.Id, UserId: 9999, Role: EnterpriseMembershipRoleMember, Status: EnterpriseMembershipStatusRemoved}
	require.NoError(t, DB.Create(removed).Error)

	require.NoError(t, DB.Create(&EnterpriseLedger{EnterpriseId: enterpriseA.Id, Kind: EnterpriseLedgerKindAllocate, Amount: 7, MembershipId: &membershipA.Id, ActorUserId: ownerA.Id, ReferenceType: "test", ReferenceId: "secret-reference", ReferenceHash: enterpriseReferenceHash("test", "secret-reference", int64(membershipA.Id)), IdempotencyKey: "secret-key", IdempotencyKeyHash: enterpriseLedgerHash("secret-key", 0), RequestId: "secret-request", Reason: "secret-reason", CommandSummary: "secret-command", CreatedAt: 100}).Error)
	require.NoError(t, DB.Create(&EnterpriseLedger{EnterpriseId: enterpriseA.Id, Kind: EnterpriseLedgerKindTopUp, Amount: 9, ActorUserId: ownerA.Id, ReferenceType: "test", ReferenceId: "secret-reference-2", ReferenceHash: enterpriseReferenceHash("test", "secret-reference-2", 0), IdempotencyKey: "secret-key-2", IdempotencyKeyHash: enterpriseLedgerHash("secret-key-2", 0), CreatedAt: 100}).Error)
	require.NoError(t, DB.Create(&EnterpriseLedger{EnterpriseId: enterpriseB.Id, Kind: EnterpriseLedgerKindTopUp, Amount: 99, ActorUserId: ownerB.Id, ReferenceType: "test", ReferenceId: "secret-reference-b", ReferenceHash: enterpriseReferenceHash("test", "secret-reference-b", 0), IdempotencyKeyHash: enterpriseLedgerHash("secret-key-b", 0), CreatedAt: 100}).Error)
	require.NoError(t, DB.Create(&EnterpriseUsageRecord{EnterpriseId: enterpriseA.Id, MembershipId: membershipA.Id, ActorUserId: memberA.Id, TokenId: 11, ModelName: "model-a", FundingSource: "enterprise_allocation", MembershipRoleSnapshot: EnterpriseMembershipRoleMember, State: EnterpriseUsageStateSettled, ReservedQuota: 10, SettledQuota: 8, CreatedAt: 100, RequestId: "secret-request", IdempotencyKey: "secret-idempotency", RequestFingerprint: "secret-fingerprint"}).Error)
	require.NoError(t, DB.Create(&EnterpriseUsageRecord{EnterpriseId: enterpriseA.Id, MembershipId: membershipB.Id, ActorUserId: memberB.Id, TokenId: 12, ModelName: "model-b", FundingSource: "enterprise_allocation", MembershipRoleSnapshot: EnterpriseMembershipRoleMember, State: EnterpriseUsageStateRefunded, CreatedAt: 101}).Error)
	require.NoError(t, DB.Create(&EnterpriseUsageRecord{EnterpriseId: enterpriseB.Id, MembershipId: 0, ActorUserId: ownerB.Id, TokenId: 99, ModelName: "other", FundingSource: "enterprise_wallet", MembershipRoleSnapshot: EnterpriseMembershipRoleOwner, State: EnterpriseUsageStateSettled, CreatedAt: 100}).Error)

	ledgers, total, err := ListEnterpriseLedger(ownerA.Id, -100, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, ledgers, 2)
	assert.Equal(t, EnterpriseLedgerKindTopUp, ledgers[0].Kind, "same timestamp uses id descending")
	assert.Equal(t, 7, ledgers[1].Amount)
	encoded, err := common.Marshal(ledgers)
	require.NoError(t, err)
	for _, forbidden := range []string{"secret-key", "secret-reference", "secret-request", "secret-reason", "secret-command", "request_id", "reference_id", "command_summary", "membership_id", "actor_user_id"} {
		assert.NotContains(t, string(encoded), forbidden)
	}

	usage, total, err := ListEnterpriseUsage(ownerA.Id, 0, 1000)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, usage, 2)
	usageJSON, err := common.Marshal(usage)
	require.NoError(t, err)
	for _, forbidden := range []string{"secret-idempotency", "secret-fingerprint", "secret-request", "idempotency_key", "request_fingerprint", "request_id", "membership_id", "actor_user_id", "token_id"} {
		assert.NotContains(t, string(usageJSON), forbidden)
	}

	memberUsage, total, err := ListEnterpriseUsage(memberA.Id, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, memberUsage, 1)
	_, _, err = ListEnterpriseLedger(memberA.Id, 0, 20)
	assert.ErrorIs(t, err, ErrEnterpriseOwnerRequired)
	_, _, err = ListEnterpriseUsage(memberB.Id, 0, 20)
	require.NoError(t, err)

	removedUser := enterpriseReadUser(t, "read-removed", common.RoleCommonUser, enterpriseA.Id)
	require.NoError(t, DB.Create(&EnterpriseMembership{EnterpriseId: enterpriseA.Id, UserId: removedUser.Id, Role: EnterpriseMembershipRoleMember, Status: EnterpriseMembershipStatusRemoved}).Error)
	_, _, err = ListEnterpriseUsage(removedUser.Id, 0, 20)
	assert.ErrorIs(t, err, ErrEnterpriseManagementRequired)
	_, _, err = ListEnterpriseLedger(removedUser.Id, 0, 20)
	assert.ErrorIs(t, err, ErrEnterpriseManagementRequired)
	_, _, err = ListEnterpriseUsage(999999, 0, 20)
	assert.ErrorIs(t, err, ErrEnterpriseManagementRequired)
	_, _, err = ListEnterpriseLedger(999999, 0, 20)
	assert.ErrorIs(t, err, ErrEnterpriseManagementRequired)
	_, _, err = ListEnterpriseLedger(ownerB.Id, 0, 20)
	require.NoError(t, err)

	// Keep the second enterprise fixture live in the test so accidental query
	// broadening cannot pass solely because it has no owner relationship.
	assert.NotZero(t, enterpriseB.Id)
}

func TestEnterpriseReadPaginationIsStableAndBounded(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	owner := enterpriseReadUser(t, "read-page-owner", common.RoleCommonUser, 0)
	enterprise := enterpriseReadEnterprise(t, "read-page", owner)
	for i := 0; i < 3; i++ {
		require.NoError(t, DB.Create(&EnterpriseLedger{EnterpriseId: enterprise.Id, Kind: fmt.Sprintf("K%d", i), Amount: i + 1, ActorUserId: owner.Id, ReferenceType: "test", ReferenceId: fmt.Sprintf("page-%d", i), ReferenceHash: enterpriseReferenceHash("test", fmt.Sprintf("page-%d", i), 0), IdempotencyKeyHash: enterpriseLedgerHash(fmt.Sprintf("page-key-%d", i), 0), CreatedAt: 500}).Error)
	}
	page1, total, err := ListEnterpriseLedger(owner.Id, -1, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	require.Len(t, page1, 1)
	page2, _, err := ListEnterpriseLedger(owner.Id, 1, 1)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.NotEqual(t, page1[0].ID, page2[0].ID)
	pageAll, _, err := ListEnterpriseLedger(owner.Id, 0, 101)
	require.NoError(t, err)
	assert.Len(t, pageAll, 3, "limit is bounded even when fewer rows exist")
}
