package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetEnterpriseOwnerCreatesOnlyEnterpriseRelationship(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	target := enterpriseMembershipUser(t, "management-target", "target@example.com")
	originalRole := target.Role

	result, err := SetEnterpriseOwner(target.Id)
	require.NoError(t, err)
	assert.Equal(t, target.Id, result.UserID)
	assert.Equal(t, common.RoleCommonUser, result.PlatformRole)
	assert.NotZero(t, result.EnterpriseID)
	assert.NotZero(t, result.MembershipID)

	var saved User
	require.NoError(t, DB.First(&saved, target.Id).Error)
	assert.Equal(t, originalRole, saved.Role)
	assert.Equal(t, result.EnterpriseID, saved.ActiveEnterpriseId)
	var membership EnterpriseMembership
	require.NoError(t, DB.First(&membership, result.MembershipID).Error)
	assert.Equal(t, EnterpriseMembershipRoleOwner, membership.Role)
	assert.Equal(t, EnterpriseMembershipStatusActive, membership.Status)

	_, err = SetEnterpriseOwner(target.Id)
	assert.ErrorIs(t, err, ErrEnterpriseMembershipConflict)
}

func TestEnterpriseManagementQuotaUsesC1AndReplaysByKey(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	owner := &User{}
	require.NoError(t, DB.First(owner, enterprise.OwnerUserId).Error)
	member := &EnterpriseMembership{EnterpriseId: enterprise.Id, UserId: 2002, Role: EnterpriseMembershipRoleMember, Status: EnterpriseMembershipStatusActive}
	require.NoError(t, DB.Create(member).Error)
	require.NoError(t, DB.Create(&User{Id: member.UserId, Username: "management-member", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}).Error)
	_, err := CreditEnterpriseWallet(EnterpriseMoneyCommand{EnterpriseID: enterprise.Id, ActorUserID: owner.Id, Amount: 100, IdempotencyKey: "seed", ReferenceType: enterpriseLedgerReferenceTypes[EnterpriseLedgerKindTopUp], ReferenceID: "seed", RequestID: "seed"})
	require.NoError(t, err)

	first, err := AllocateEnterpriseQuotaForManagement(owner.Id, member.Id, 20, "allocate", "management-request")
	require.NoError(t, err)
	second, err := AllocateEnterpriseQuotaForManagement(owner.Id, member.Id, 20, "allocate", "management-request")
	require.NoError(t, err)
	assert.False(t, first.Replayed)
	assert.True(t, second.Replayed)
	assert.Equal(t, first.LedgerID, second.LedgerID)

	_, err = AllocateEnterpriseQuotaForManagement(owner.Id, member.Id, 30, "allocate", "management-request")
	assert.ErrorIs(t, err, ErrEnterpriseIdempotencyConflict)
	var count int64
	require.NoError(t, DB.Model(&EnterpriseLedger{}).Where("enterprise_id = ? AND kind = ?", enterprise.Id, EnterpriseLedgerKindAllocate).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestEnterpriseManagementQuotaRejectsCrossOperationKeyReuse(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	owner := &User{}
	require.NoError(t, DB.First(owner, enterprise.OwnerUserId).Error)
	member := &EnterpriseMembership{EnterpriseId: enterprise.Id, UserId: 2003, Role: EnterpriseMembershipRoleMember, Status: EnterpriseMembershipStatusActive}
	require.NoError(t, DB.Create(member).Error)
	require.NoError(t, DB.Create(&User{Id: member.UserId, Username: "management-cross-operation-member", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}).Error)
	_, err := CreditEnterpriseWallet(EnterpriseMoneyCommand{EnterpriseID: enterprise.Id, ActorUserID: owner.Id, Amount: 100, IdempotencyKey: "cross-operation-seed", ReferenceType: enterpriseLedgerReferenceTypes[EnterpriseLedgerKindTopUp], ReferenceID: "cross-operation-seed", RequestID: "cross-operation-seed"})
	require.NoError(t, err)

	_, err = AllocateEnterpriseQuotaForManagement(owner.Id, member.Id, 20, "allocate", "cross-operation-key")
	require.NoError(t, err)
	_, err = ReclaimEnterpriseQuotaForManagement(owner.Id, member.Id, 20, "reclaim", "cross-operation-key")
	assert.ErrorIs(t, err, ErrEnterpriseIdempotencyConflict)

	var enterpriseAfter Enterprise
	require.NoError(t, DB.First(&enterpriseAfter, enterprise.Id).Error)
	assert.Equal(t, 80, enterpriseAfter.AvailableQuota)
	var memberAfter EnterpriseMembership
	require.NoError(t, DB.First(&memberAfter, member.Id).Error)
	assert.Equal(t, 20, memberAfter.AvailableQuota)
	var count int64
	require.NoError(t, DB.Model(&EnterpriseLedger{}).Where("enterprise_id = ? AND kind IN ?", enterprise.Id, []string{EnterpriseLedgerKindAllocate, EnterpriseLedgerKindReclaim}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestEnterpriseManagementReadsRequireConsistentOwnerEnterpriseAnchor(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	owner, enterprise := enterpriseMembershipOwner(t, "management-consistency-owner", "consistency-owner@example.com")

	_, _, err := ListEnterpriseMembers(owner.Id, 0, 20)
	require.NoError(t, err)

	require.NoError(t, DB.Model(&Enterprise{}).Where("id = ?", enterprise.Id).Update("owner_user_id", owner.Id+1).Error)
	_, _, err = ListEnterpriseMembers(owner.Id, 0, 20)
	assert.ErrorIs(t, err, ErrEnterpriseOwnerRequired)

	require.NoError(t, DB.Model(&Enterprise{}).Where("id = ?", enterprise.Id).Update("owner_user_id", owner.Id).Error)
	require.NoError(t, DB.Model(&Enterprise{}).Where("id = ?", enterprise.Id).Update("status", EnterpriseStatusClosed).Error)
	_, err = GetEnterpriseSelf(owner.Id)
	assert.ErrorIs(t, err, ErrEnterpriseManagementRequired)
}

func TestEnterpriseLedgerRejectsInconsistentOwnerAnchor(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, member := enterpriseLedgerFixture(t)
	_, err := CreditEnterpriseWallet(enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "management-anchor-topup", "management-anchor-topup", 100))
	require.NoError(t, err)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", enterprise.OwnerUserId).Update("active_enterprise_id", 0).Error)
	_, err = AllocateEnterpriseQuota(enterpriseMoneyCommand(enterprise.Id, member.Id, EnterpriseLedgerKindAllocate, "management-anchor-allocate", "management-anchor-allocate", 10))
	assert.ErrorIs(t, err, ErrEnterpriseOwnerRequired)

	var count int64
	require.NoError(t, DB.Model(&EnterpriseLedger{}).Where("enterprise_id = ? AND kind = ?", enterprise.Id, EnterpriseLedgerKindAllocate).Count(&count).Error)
	assert.Zero(t, count)
}

func TestEnterpriseMemberProjectionDoesNotExposeUserCredentials(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	owner, enterprise := enterpriseMembershipOwner(t, "management-owner", "owner@example.com")
	member := enterpriseMembershipUser(t, "management-projection", "member@example.com")
	require.NoError(t, DB.Create(&EnterpriseMembership{EnterpriseId: enterprise.Id, UserId: member.Id, Role: EnterpriseMembershipRoleMember, Status: EnterpriseMembershipStatusActive, AvailableQuota: 12}).Error)

	items, total, err := ListEnterpriseMembers(owner.Id, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, items, 2)
	assert.Equal(t, member.Username, items[1].Username)
	assert.Equal(t, 12, items[1].AvailableQuota)
}
