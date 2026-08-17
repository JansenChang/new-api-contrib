package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnterpriseOwnerLifecycleRejectsDeleteHardDeleteAndDisable(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&UserSession{}))
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedisEnabled })

	owner, _ := enterpriseMembershipOwner(t, "lifecycle-owner", "lifecycle-owner@example.com")

	assert.ErrorIs(t, owner.Delete(), ErrEnterpriseOwnerLifecycleBlocked)
	assert.ErrorIs(t, (&User{Id: owner.Id}).HardDelete(), ErrEnterpriseOwnerLifecycleBlocked)

	owner.Status = common.UserStatusDisabled
	assert.ErrorIs(t, owner.Update(false), ErrEnterpriseOwnerLifecycleBlocked)

	var saved User
	require.NoError(t, DB.Unscoped().First(&saved, owner.Id).Error)
	assert.Equal(t, common.UserStatusEnabled, saved.Status)
	var membership EnterpriseMembership
	require.NoError(t, DB.Where("user_id = ? AND role = ?", owner.Id, EnterpriseMembershipRoleOwner).First(&membership).Error)
	assert.Equal(t, EnterpriseMembershipStatusActive, membership.Status)
}

func TestEnterpriseOwnerLifecycleAllowsNonOwnerMemberAndUnrelatedUser(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&UserSession{}))
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedisEnabled })

	owner, enterprise := enterpriseMembershipOwner(t, "lifecycle-member-owner", "lifecycle-member-owner@example.com")
	member := enterpriseMembershipUser(t, "lifecycle-member", "lifecycle-member@example.com")
	removed := EnterpriseMembership{
		EnterpriseId: enterprise.Id,
		UserId:       member.Id,
		Role:         EnterpriseMembershipRoleMember,
		Status:       EnterpriseMembershipStatusRemoved,
	}
	require.NoError(t, DB.Create(&removed).Error)

	// A removed Member relationship does not make the user an enterprise
	// owner, so ordinary platform disablement remains available.
	member.Status = common.UserStatusDisabled
	require.NoError(t, member.Update(false))
	var savedMember User
	require.NoError(t, DB.First(&savedMember, member.Id).Error)
	assert.Equal(t, common.UserStatusDisabled, savedMember.Status)

	// The user without any enterprise relationship is also unaffected.
	personal := &User{
		Username: "lifecycle-personal",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(personal).Error)
	personal.Status = common.UserStatusDisabled
	require.NoError(t, personal.Update(false))
	var savedPersonal User
	require.NoError(t, DB.First(&savedPersonal, personal.Id).Error)
	assert.Equal(t, common.UserStatusDisabled, savedPersonal.Status)

	// The enterprise owner itself remains protected independently of the
	// member operations above.
	assert.ErrorIs(t, owner.Delete(), ErrEnterpriseOwnerLifecycleBlocked)
}

func TestEnterpriseOwnerLifecycleChecksOwnerAnchorAfterMembershipRemoval(t *testing.T) {
	newEnterpriseMembershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&UserSession{}))
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedisEnabled })

	owner, enterprise := enterpriseMembershipOwner(t, "lifecycle-anchor-owner", "lifecycle-anchor-owner@example.com")
	require.NoError(t, DB.Model(&EnterpriseMembership{}).
		Where("enterprise_id = ? AND user_id = ? AND role = ?", enterprise.Id, owner.Id, EnterpriseMembershipRoleOwner).
		Update("status", EnterpriseMembershipStatusRemoved).Error)

	// A stale Enterprise.owner_user_id is independently sufficient to block
	// lifecycle operations; no ownership transfer/closure is guessed here.
	assert.ErrorIs(t, (&User{Id: owner.Id}).HardDelete(), ErrEnterpriseOwnerLifecycleBlocked)
}
