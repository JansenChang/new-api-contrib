package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newEnterpriseFoundationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousMainDatabaseType := common.MainDatabaseType()
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		common.SetMainDatabaseType(previousMainDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestEnterpriseFoundationSchemaContracts(t *testing.T) {
	db := newEnterpriseFoundationTestDB(t)
	require.NoError(t, db.AutoMigrate(&Enterprise{}, &EnterpriseMembership{}, &EnterpriseInvitation{}, &EnterpriseUsageRecord{}))

	assert.True(t, db.Migrator().HasIndex(&EnterpriseUsageRecord{}, "idx_enterprise_usage_token_idempotency"))
	assert.True(t, db.Migrator().HasIndex(&EnterpriseUsageRecord{}, "idx_enterprise_usage_records_request_id"))

	first := EnterpriseUsageRecord{
		EnterpriseId:           1,
		TokenId:                10,
		IdempotencyKey:         "retry-1",
		RequestFingerprint:     "fingerprint-a",
		RequestId:              "same-gateway-request",
		FundingSource:          "ENTERPRISE",
		MembershipRoleSnapshot: EnterpriseMembershipRoleMember,
		State:                  "PENDING",
	}
	require.NoError(t, db.Create(&first).Error)

	// request_id is only an association index; it may repeat when the gateway
	// retries through a new HTTP request. The stable token/idempotency pair is
	// the database contract.
	second := first
	second.Id = 0
	second.TokenId = 11
	second.RequestFingerprint = "fingerprint-b"
	require.NoError(t, db.Create(&second).Error)

	duplicate := first
	duplicate.Id = 0
	duplicate.RequestId = "another-gateway-request"
	assert.Error(t, db.Create(&duplicate).Error)

	membership := EnterpriseMembership{
		EnterpriseId: 1,
		UserId:       1,
		Role:         EnterpriseMembershipRoleMember,
		Status:       EnterpriseMembershipStatusManualReview,
		JoinedAt:     common.GetTimestamp(),
	}
	require.NoError(t, db.Create(&membership).Error)
	assert.Equal(t, EnterpriseMembershipStatusManualReview, membership.Status)
	assert.Error(t, db.Create(&EnterpriseMembership{
		EnterpriseId: 1,
		UserId:       1,
		Role:         EnterpriseMembershipRoleMember,
		Status:       EnterpriseMembershipStatusActive,
		JoinedAt:     common.GetTimestamp(),
	}).Error)

	invitation := EnterpriseInvitation{
		EnterpriseId:  1,
		InviterUserId: 2,
		TargetEmail:   "member@example.com",
		ExpectedRole:  EnterpriseMembershipRoleMember,
		Status:        EnterpriseInvitationStatusRejected,
		RejectedAt:    common.GetTimestamp(),
		ExpiresAt:     common.GetTimestamp() + 3600,
		CreatedAt:     common.GetTimestamp(),
	}
	require.NoError(t, db.Create(&invitation).Error)
	var savedInvitation EnterpriseInvitation
	require.NoError(t, db.First(&savedInvitation, invitation.Id).Error)
	assert.Equal(t, EnterpriseMembershipRoleMember, savedInvitation.ExpectedRole)
	assert.Equal(t, EnterpriseInvitationStatusRejected, savedInvitation.Status)
	assert.NotZero(t, savedInvitation.RejectedAt)
}

func TestEnsurePlatformAdminEnterpriseCASConflictRollsBack(t *testing.T) {
	db := newEnterpriseFoundationTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &Enterprise{}, &EnterpriseMembership{}))
	user := User{Username: "cas-root", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	anchor := Enterprise{OwnerUserId: 999, Name: "other", Status: EnterpriseStatusActive}
	require.NoError(t, db.Create(&anchor).Error)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("active_enterprise_id", anchor.Id).Error)

	stale := user
	stale.ActiveEnterpriseId = 0
	err := db.Transaction(func(tx *gorm.DB) error {
		return ensurePlatformAdminEnterprise(tx, &stale)
	})
	require.ErrorIs(t, err, errEnterpriseOwnerConflict)

	var owned []Enterprise
	require.NoError(t, db.Where("owner_user_id = ?", user.Id).Find(&owned).Error)
	assert.Empty(t, owned, "a failed CAS must roll back the enterprise created before it")
	var reloaded User
	require.NoError(t, db.First(&reloaded, user.Id).Error)
	assert.Equal(t, anchor.Id, reloaded.ActiveEnterpriseId)
}

func TestInsertAndPromotionCreatePlatformAdminEnterpriseInTransaction(t *testing.T) {
	db := newEnterpriseFoundationTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &Enterprise{}, &EnterpriseMembership{}))

	admin := User{
		Username: "new-admin",
		Password: "password",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return admin.InsertWithTx(tx, 0)
	}))
	assert.NotZero(t, admin.ActiveEnterpriseId)
	var adminMembership EnterpriseMembership
	require.NoError(t, db.Where("user_id = ?", admin.Id).First(&adminMembership).Error)
	assert.Equal(t, EnterpriseMembershipRoleOwner, adminMembership.Role)

	ordinary := User{
		Username: "promoted-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&ordinary).Error)
	ordinary.Role = common.RoleAdminUser
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ordinary.UpdateWithTx(tx, false)
	}))
	assert.NotZero(t, ordinary.ActiveEnterpriseId)
	var promoted Enterprise
	require.NoError(t, db.Where("owner_user_id = ?", ordinary.Id).First(&promoted).Error)
	assert.Equal(t, ordinary.ActiveEnterpriseId, promoted.Id)
	var promotedMembership EnterpriseMembership
	require.NoError(t, db.Where("enterprise_id = ? AND user_id = ?", promoted.Id, ordinary.Id).First(&promotedMembership).Error)
	assert.Equal(t, EnterpriseMembershipRoleOwner, promotedMembership.Role)
}

func TestMigrateEnterpriseFoundationIsIdempotent(t *testing.T) {
	db := newEnterpriseFoundationTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}))
	users := []User{
		{Username: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AffCode: "root-aff"},
		{Username: "admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AffCode: "admin-aff"},
		{Username: "user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "user-aff"},
	}
	require.NoError(t, db.Create(&users).Error)

	require.NoError(t, migrateEnterpriseFoundation(db))
	require.NoError(t, migrateEnterpriseFoundation(db))
	for _, table := range []string{
		"enterprises",
		"enterprise_memberships",
		"enterprise_invitations",
		"enterprise_ledgers",
		"enterprise_usage_records",
		"api_key_deliveries",
	} {
		assert.True(t, db.Migrator().HasTable(table), table)
	}

	var enterprises []Enterprise
	require.NoError(t, db.Order("id ASC").Find(&enterprises).Error)
	assert.Len(t, enterprises, 2)
	var memberships []EnterpriseMembership
	require.NoError(t, db.Find(&memberships).Error)
	assert.Len(t, memberships, 2)

	for _, user := range users[:2] {
		var reloaded User
		require.NoError(t, db.First(&reloaded, user.Id).Error)
		assert.NotZero(t, reloaded.ActiveEnterpriseId)
		var owned Enterprise
		require.NoError(t, db.Where("owner_user_id = ?", user.Id).First(&owned).Error)
		assert.Equal(t, reloaded.ActiveEnterpriseId, owned.Id)
	}
	var ordinary User
	require.NoError(t, db.First(&ordinary, users[2].Id).Error)
	assert.Zero(t, ordinary.ActiveEnterpriseId)
}

func TestMigrateEnterpriseFoundationAddsAnchorToLegacyUsers(t *testing.T) {
	db := newEnterpriseFoundationTestDB(t)
	// Build the complete current User schema first, then reproduce the only
	// legacy difference under test: the pre-enterprise column is absent.
	require.NoError(t, db.AutoMigrate(&User{}))
	legacyUsers := []User{
		{Username: "legacy-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled},
		{Username: "legacy-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled},
	}
	require.NoError(t, db.Create(&legacyUsers).Error)
	require.NoError(t, db.Migrator().DropColumn(&User{}, "active_enterprise_id"))

	columns, err := db.Migrator().ColumnTypes(&User{})
	require.NoError(t, err)
	for _, column := range columns {
		assert.NotEqual(t, "active_enterprise_id", column.Name())
	}

	require.NoError(t, migrateEnterpriseFoundation(db))
	columns, err = db.Migrator().ColumnTypes(&User{})
	require.NoError(t, err)
	var anchorColumnFound bool
	for _, column := range columns {
		if column.Name() == "active_enterprise_id" {
			anchorColumnFound = true
			break
		}
	}
	assert.True(t, anchorColumnFound)

	var enterprises []Enterprise
	require.NoError(t, db.Find(&enterprises).Error)
	assert.Len(t, enterprises, 2)
}

func TestEnsurePlatformAdminEnterprisesRejectsConflictingAnchor(t *testing.T) {
	db := newEnterpriseFoundationTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &Enterprise{}, &EnterpriseMembership{}))
	users := []User{
		{Username: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AffCode: "root-conflict-aff"},
		{Username: "admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AffCode: "admin-conflict-aff"},
	}
	require.NoError(t, db.Create(&users).Error)
	enterprise := Enterprise{OwnerUserId: users[0].Id, Name: "root Enterprise", Status: EnterpriseStatusActive}
	require.NoError(t, db.Create(&enterprise).Error)
	users[1].ActiveEnterpriseId = enterprise.Id
	require.NoError(t, db.Save(&users[1]).Error)

	err := ensurePlatformAdminEnterprises(db)
	require.Error(t, err)
	assert.ErrorIs(t, err, errEnterpriseOwnerConflict)

	var memberships []EnterpriseMembership
	require.NoError(t, db.Find(&memberships).Error)
	assert.Empty(t, memberships)
}
