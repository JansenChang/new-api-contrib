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

func newAPIKeyDeliveryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousType := common.MainDatabaseType()
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	DB, LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &AuthFlow{}, &APIKeyDelivery{}, &EnterpriseMembership{}))
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func newDeliveryTestToken(t *testing.T, userID int, key string) *Token {
	t.Helper()
	token := &Token{UserId: userID, Name: key, Key: key, Status: common.TokenStatusEnabled, CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(), ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, DB.Create(token).Error)
	return token
}

func TestRotateAPIKeyForDeliveryChangesOnlySelectedPrivilegedToken(t *testing.T) {
	newAPIKeyDeliveryTestDB(t)
	user := &User{Username: "delivery-root", Email: "root@example.com", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, DB.Create(user).Error)
	target := newDeliveryTestToken(t, user.Id, "root-target-old")
	other := newDeliveryTestToken(t, user.Id, "root-other-stays")

	rotated, delivery, err := RotateAPIKeyForDelivery(user.Id, target.Id)
	require.NoError(t, err)
	require.NotNil(t, delivery)
	assert.Equal(t, target.Id, rotated.Id)
	assert.NotEqual(t, "root-target-old", rotated.Key)
	assert.Equal(t, target.Id, delivery.TokenId)
	assert.Equal(t, APIKeyDeliveryStatusPending, delivery.Status)

	var savedTarget, savedOther Token
	require.NoError(t, DB.First(&savedTarget, target.Id).Error)
	require.NoError(t, DB.First(&savedOther, other.Id).Error)
	assert.Equal(t, rotated.Key, savedTarget.Key)
	assert.Equal(t, "root-other-stays", savedOther.Key)
	_, err = ValidateUserTokenForLogin("root-target-old")
	assert.ErrorIs(t, err, ErrTokenInvalid)
	var savedUser User
	require.NoError(t, DB.First(&savedUser, user.Id).Error)
	assert.EqualValues(t, 2, savedUser.AuthVersion)
}

func TestPendingAPIKeyDeliveryResendsSameRotatedKeyWithoutAnotherRotation(t *testing.T) {
	newAPIKeyDeliveryTestDB(t)
	user := &User{Username: "delivery-user", Email: "user@example.com", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, DB.Create(user).Error)
	token := newDeliveryTestToken(t, user.Id, "user-key-old")

	rotated, delivery, err := RotateAPIKeyForDelivery(user.Id, token.Id)
	require.NoError(t, err)
	require.NoError(t, RecordAPIKeyDeliveryFailure(delivery.Id, "SMTP_SEND_FAILED"))
	retryDelivery, retryToken, err := GetPendingAPIKeyDeliveryForUser(user.Id, delivery.Id)
	require.NoError(t, err)
	assert.Equal(t, delivery.Id, retryDelivery.Id)
	assert.Equal(t, rotated.Key, retryToken.Key)
	_, _, err = RotateAPIKeyForDelivery(user.Id, token.Id)
	assert.ErrorIs(t, err, ErrAPIKeyDeliveryPending)

	var saved Token
	require.NoError(t, DB.First(&saved, token.Id).Error)
	assert.Equal(t, rotated.Key, saved.Key)
	var deliveryCount int64
	require.NoError(t, DB.Model(&APIKeyDelivery{}).Where("token_id = ?", token.Id).Count(&deliveryCount).Error)
	assert.EqualValues(t, 1, deliveryCount)
}

func TestCreatePrivilegedAPIKeyResetFlowsBindsEachTargetOnServer(t *testing.T) {
	newAPIKeyDeliveryTestDB(t)
	user := &User{Username: "recovery-root", Email: "root-recovery@example.com", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, DB.Create(user).Error)
	first := newDeliveryTestToken(t, user.Id, "root-recovery-first")
	second := newDeliveryTestToken(t, user.Id, "root-recovery-second")

	links, allowed, err := CreatePrivilegedAPIKeyResetFlows(user.Id, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.True(t, allowed)
	require.Len(t, links, 2)
	assert.ElementsMatch(t, []int{first.Id, second.Id}, []int{links[0].TokenId, links[1].TokenId})

	for _, link := range links {
		flow, err := GetAuthFlow(link.Token, AuthFlowMatch{Purpose: AuthFlowPurposeAPIKeyReset, UserId: user.Id})
		require.NoError(t, err)
		targetID, err := APIKeyResetFlowTargetTokenID(flow)
		require.NoError(t, err)
		assert.Equal(t, link.TokenId, targetID)
	}

	_, allowed, err = CreatePrivilegedAPIKeyResetFlows(user.Id, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.False(t, allowed, "an active selection batch must not be duplicated")
}

func TestPersonalAssetsFrozenUsesReleaseGateAndMembershipState(t *testing.T) {
	newAPIKeyDeliveryTestDB(t)
	previous := common.EnterpriseBillingEnabled
	common.EnterpriseBillingEnabled = false
	t.Cleanup(func() { common.EnterpriseBillingEnabled = previous })
	userID := 501
	for _, status := range []int{
		EnterpriseMembershipStatusActive,
		EnterpriseMembershipStatusPaused,
		EnterpriseMembershipStatusDraining,
		EnterpriseMembershipStatusReclaimed,
		EnterpriseMembershipStatusManualReview,
		EnterpriseMembershipStatusRemoved,
	} {
		require.NoError(t, DB.Create(&EnterpriseMembership{EnterpriseId: status, UserId: userID + status, Role: EnterpriseMembershipRoleMember, Status: status}).Error)
	}

	frozen, err := PersonalAssetsFrozen(userID + EnterpriseMembershipStatusActive)
	require.NoError(t, err)
	assert.False(t, frozen, "the default-off release gate preserves existing personal payments")
	common.EnterpriseBillingEnabled = true
	for _, status := range []int{
		EnterpriseMembershipStatusActive,
		EnterpriseMembershipStatusPaused,
		EnterpriseMembershipStatusDraining,
		EnterpriseMembershipStatusReclaimed,
		EnterpriseMembershipStatusManualReview,
		EnterpriseMembershipStatusRemoved,
	} {
		frozen, err := PersonalAssetsFrozen(userID + status)
		require.NoError(t, err)
		assert.Equal(t, status != EnterpriseMembershipStatusRemoved, frozen, "status=%d", status)
	}
}
