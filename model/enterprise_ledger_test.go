package model

import (
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newEnterpriseLedgerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousType := common.MainDatabaseType()
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	DB, LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&User{}, &Enterprise{}, &EnterpriseMembership{}, &EnterpriseLedger{}, &TopUp{}, &SubscriptionOrder{}))
	require.NoError(t, migrateEnterpriseLedgerIndexes(db))
	require.True(t, db.Migrator().HasIndex(&enterpriseLedgerIndexSchema{}, "idx_enterprise_ledger_enterprise_idempotency"))
	require.True(t, db.Migrator().HasIndex(&enterpriseLedgerIndexSchema{}, "idx_enterprise_ledger_enterprise_reference"))
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

func enterpriseLedgerFixture(t *testing.T) (*Enterprise, *EnterpriseMembership) {
	return enterpriseLedgerFixtureForOwner(t, 101)
}

func enterpriseLedgerFixtureForOwner(t *testing.T, ownerUserID int) (*Enterprise, *EnterpriseMembership) {
	t.Helper()
	enterprise := &Enterprise{Name: "ledger-test", OwnerUserId: ownerUserID, Status: EnterpriseStatusActive}
	require.NoError(t, DB.Create(enterprise).Error)
	require.NoError(t, DB.Create(&User{Id: ownerUserID, Username: fmt.Sprintf("owner-%d", ownerUserID), AffCode: fmt.Sprintf("owner-aff-%d", ownerUserID), Role: common.RoleAdminUser, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, DB.Create(&EnterpriseMembership{
		EnterpriseId: enterprise.Id,
		UserId:       enterprise.OwnerUserId,
		Role:         EnterpriseMembershipRoleOwner,
		Status:       EnterpriseMembershipStatusActive,
	}).Error)
	member := &EnterpriseMembership{
		EnterpriseId: enterprise.Id,
		UserId:       202,
		Role:         EnterpriseMembershipRoleMember,
		Status:       EnterpriseMembershipStatusActive,
	}
	require.NoError(t, DB.Create(member).Error)
	return enterprise, member
}

func enterpriseMoneyCommand(enterpriseID, membershipID int, action, idem, reference string, amount int) EnterpriseMoneyCommand {
	return EnterpriseMoneyCommand{
		EnterpriseID:   enterpriseID,
		MembershipID:   membershipID,
		ActorUserID:    101,
		Action:         action,
		Amount:         amount,
		IdempotencyKey: idem,
		ReferenceType:  enterpriseLedgerReferenceTypes[action],
		ReferenceID:    reference,
		RequestID:      idem + "-request",
	}
}

func TestEnterpriseLedgerTopUpAllocateReclaimAndIdempotency(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, member := enterpriseLedgerFixture(t)

	topup := enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "topup-1", "payment-1", 1000)
	credited, err := CreditEnterpriseWallet(topup)
	require.NoError(t, err)
	assert.Equal(t, 1000, credited.EnterpriseAvailableQuota)
	assert.False(t, credited.Replayed)

	replayed, err := CreditEnterpriseWallet(topup)
	require.NoError(t, err)
	assert.True(t, replayed.Replayed)
	assert.Equal(t, credited.LedgerID, replayed.LedgerID)
	assert.Equal(t, 1000, replayed.EnterpriseAvailableQuota)

	conflict := topup
	conflict.Amount = 900
	_, err = CreditEnterpriseWallet(conflict)
	assert.ErrorIs(t, err, ErrEnterpriseIdempotencyConflict)

	allocated, err := AllocateEnterpriseQuota(enterpriseMoneyCommand(enterprise.Id, member.Id, EnterpriseLedgerKindAllocate, "allocate-1", "allocation-1", 300))
	require.NoError(t, err)
	assert.Equal(t, 700, allocated.EnterpriseAvailableQuota)
	assert.Equal(t, 300, allocated.MemberAvailableQuota)

	replayed, err = AllocateEnterpriseQuota(enterpriseMoneyCommand(enterprise.Id, member.Id, EnterpriseLedgerKindAllocate, "allocate-1", "allocation-1", 300))
	require.NoError(t, err)
	assert.True(t, replayed.Replayed)
	assert.Equal(t, allocated.LedgerID, replayed.LedgerID)

	reclaimed, err := ReclaimEnterpriseQuota(enterpriseMoneyCommand(enterprise.Id, member.Id, EnterpriseLedgerKindReclaim, "reclaim-1", "reclaim-1", 100))
	require.NoError(t, err)
	assert.Equal(t, 800, reclaimed.EnterpriseAvailableQuota)
	assert.Equal(t, 200, reclaimed.MemberAvailableQuota)

	other, _ := enterpriseLedgerFixtureForOwner(t, 303)
	_, err = CreditEnterpriseWallet(enterpriseMoneyCommand(other.Id, 0, EnterpriseLedgerKindTopUp, "topup-1", "payment-1", 1000))
	require.NoError(t, err, "same idempotency and reference keys are scoped to the enterprise")
}

func TestEnterpriseLedgerInsufficientQuotaRollsBack(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, member := enterpriseLedgerFixture(t)
	_, err := CreditEnterpriseWallet(enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "topup-rollback", "payment-rollback", 100))
	require.NoError(t, err)

	_, err = AllocateEnterpriseQuota(enterpriseMoneyCommand(enterprise.Id, member.Id, EnterpriseLedgerKindAllocate, "allocate-too-much", "allocation-too-much", 101))
	require.ErrorIs(t, err, ErrEnterpriseInsufficientQuota)
	var savedEnterprise Enterprise
	var savedMember EnterpriseMembership
	require.NoError(t, DB.First(&savedEnterprise, enterprise.Id).Error)
	require.NoError(t, DB.First(&savedMember, member.Id).Error)
	assert.Equal(t, 100, savedEnterprise.AvailableQuota)
	assert.Zero(t, savedMember.AvailableQuota)
	var count int64
	require.NoError(t, DB.Model(&EnterpriseLedger{}).Where("enterprise_id = ? AND idempotency_key = ?", enterprise.Id, "allocate-too-much").Count(&count).Error)
	assert.Zero(t, count)
}

func TestEnterpriseLedgerConcurrentSameKeyReplaysExistingEntry(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	command := enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "concurrent-replay", "payment-concurrent", 100)
	var firstReads atomic.Int32
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	enterpriseMoneyBeforeIdempotencyReadHook = func() {
		if firstReads.Add(1) > 2 {
			return
		}
		arrived <- struct{}{}
		if firstReads.Load() == 2 {
			releaseOnce.Do(func() { close(release) })
		}
		<-release
	}
	t.Cleanup(func() { enterpriseMoneyBeforeIdempotencyReadHook = nil })

	results := make([]EnterpriseMoneyResult, 2)
	errorsFound := make([]error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			results[index], errorsFound[index] = CreditEnterpriseWallet(command)
		}(i)
	}
	close(start)
	for i := 0; i < 2; i++ {
		<-arrived
	}
	wg.Wait()
	createdCount := 0
	replayedCount := 0
	for i := range results {
		require.NoError(t, errorsFound[i])
		if results[i].Replayed {
			replayedCount++
		} else {
			createdCount++
		}
	}
	assert.Equal(t, 1, createdCount)
	assert.Equal(t, 1, replayedCount)
	var ledgerCount int64
	require.NoError(t, DB.Model(&EnterpriseLedger{}).Where("enterprise_id = ?", enterprise.Id).Count(&ledgerCount).Error)
	assert.Equal(t, int64(1), ledgerCount)
	var saved Enterprise
	require.NoError(t, DB.First(&saved, enterprise.Id).Error)
	assert.Equal(t, 100, saved.AvailableQuota)
}

func TestMigrateBillingSubjectSnapshotsBackfillsBothTables(t *testing.T) {
	db := newEnterpriseLedgerTestDB(t)
	// Bypass the creation hooks to represent rows written by an older schema.
	legacyTopUp := &TopUp{UserId: 601, BillingSubjectType: BillingSubjectTypeEnterprise, BillingSubjectId: 9001, BillingEnterpriseId: 9001, TradeNo: "legacy-subject-topup"}
	legacyOrder := &SubscriptionOrder{UserId: 602, BillingSubjectType: BillingSubjectTypeEnterprise, BillingSubjectId: 9002, BillingEnterpriseId: 9002, TradeNo: "legacy-subject-order"}
	validTopUp := &TopUp{UserId: 603, BillingSubjectType: BillingSubjectTypePersonal, BillingSubjectId: 603, BillingEnterpriseId: 0, TradeNo: "valid-subject-topup"}
	validOrder := &SubscriptionOrder{UserId: 604, BillingSubjectType: BillingSubjectTypePersonal, BillingSubjectId: 604, BillingEnterpriseId: 0, TradeNo: "valid-subject-order"}
	for _, row := range []any{legacyTopUp, validTopUp} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(row).Error)
	}
	for _, row := range []any{legacyOrder, validOrder} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(row).Error)
	}
	require.NoError(t, migrateBillingSubjectSnapshots(db))

	var gotTopUp TopUp
	var gotOrder SubscriptionOrder
	require.NoError(t, db.First(&gotTopUp, legacyTopUp.Id).Error)
	require.NoError(t, db.First(&gotOrder, legacyOrder.Id).Error)
	assert.Equal(t, BillingSubjectTypePersonal, gotTopUp.BillingSubjectType)
	assert.Equal(t, 601, gotTopUp.BillingSubjectId)
	assert.Zero(t, gotTopUp.BillingEnterpriseId)
	assert.Equal(t, BillingSubjectTypePersonal, gotOrder.BillingSubjectType)
	assert.Equal(t, 602, gotOrder.BillingSubjectId)
	assert.Zero(t, gotOrder.BillingEnterpriseId)

	var unchangedTopUp TopUp
	var unchangedOrder SubscriptionOrder
	require.NoError(t, db.First(&unchangedTopUp, validTopUp.Id).Error)
	require.NoError(t, db.First(&unchangedOrder, validOrder.Id).Error)
	assert.Equal(t, validTopUp.BillingSubjectType, unchangedTopUp.BillingSubjectType)
	assert.Equal(t, validOrder.BillingSubjectType, unchangedOrder.BillingSubjectType)
}

func TestMigrateEnterpriseLedgerIndexesKeepsLegacyConstraintOnPrecheckFailure(t *testing.T) {
	db := newEnterpriseLedgerTestDB(t)
	for _, indexName := range []string{"idx_enterprise_ledger_enterprise_idempotency", "idx_enterprise_ledger_enterprise_reference"} {
		require.NoError(t, db.Migrator().DropIndex(&enterpriseLedgerIndexSchema{}, indexName))
	}
	require.NoError(t, db.Exec("CREATE UNIQUE INDEX idx_enterprise_ledgers_idempotency_key ON enterprise_ledgers (idempotency_key)").Error)
	rows := []EnterpriseLedger{
		{EnterpriseId: 1, Kind: EnterpriseLedgerKindTopUp, Amount: 1, IdempotencyKey: "legacy-a", IdempotencyKeyHash: "duplicate-hash", ReferenceType: "enterprise_topup", ReferenceId: "legacy-a", ReferenceHash: enterpriseReferenceHash("enterprise_topup", "legacy-a", 0)},
		{EnterpriseId: 1, Kind: EnterpriseLedgerKindTopUp, Amount: 1, IdempotencyKey: "legacy-b", IdempotencyKeyHash: "duplicate-hash", ReferenceType: "enterprise_topup", ReferenceId: "legacy-b", ReferenceHash: enterpriseReferenceHash("enterprise_topup", "legacy-b", 0)},
	}
	require.NoError(t, db.Create(&rows).Error)

	err := migrateEnterpriseLedgerIndexes(db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enterprise_id=1")
	assert.Contains(t, err.Error(), "hash=duplicate-hash")
	assert.Contains(t, err.Error(), "count=2")
	assert.True(t, db.Migrator().HasIndex(&EnterpriseLedger{}, "idx_enterprise_ledgers_idempotency_key"))
	assert.False(t, db.Migrator().HasIndex(&enterpriseLedgerIndexSchema{}, "idx_enterprise_ledger_enterprise_idempotency"))
}

func TestMigrateEnterpriseLedgerIndexesReportsReferenceHashAndCount(t *testing.T) {
	db := newEnterpriseLedgerTestDB(t)
	for _, indexName := range []string{"idx_enterprise_ledger_enterprise_idempotency", "idx_enterprise_ledger_enterprise_reference"} {
		require.NoError(t, db.Migrator().DropIndex(&enterpriseLedgerIndexSchema{}, indexName))
	}
	referenceHash := enterpriseReferenceHash("enterprise_topup", "duplicate-reference", 0)
	rows := []EnterpriseLedger{
		{EnterpriseId: 1, Kind: EnterpriseLedgerKindTopUp, Amount: 1, IdempotencyKey: "reference-a", IdempotencyKeyHash: enterpriseLedgerHash("reference-a", 0), ReferenceType: "enterprise_topup", ReferenceId: "duplicate-reference", ReferenceHash: referenceHash},
		{EnterpriseId: 1, Kind: EnterpriseLedgerKindTopUp, Amount: 1, IdempotencyKey: "reference-b", IdempotencyKeyHash: enterpriseLedgerHash("reference-b", 0), ReferenceType: "enterprise_topup", ReferenceId: "duplicate-reference", ReferenceHash: referenceHash},
	}
	require.NoError(t, db.Create(&rows).Error)

	err := migrateEnterpriseLedgerIndexes(db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enterprise_id=1")
	assert.Contains(t, err.Error(), "reference_type=enterprise_topup")
	assert.Contains(t, err.Error(), "hash="+referenceHash)
	assert.Contains(t, err.Error(), "count=2")
}

func TestMigrateEnterpriseLedgerIndexesReplacesLegacyConstraintAfterSuccess(t *testing.T) {
	db := newEnterpriseLedgerTestDB(t)
	for _, indexName := range []string{"idx_enterprise_ledger_enterprise_idempotency", "idx_enterprise_ledger_enterprise_reference"} {
		require.NoError(t, db.Migrator().DropIndex(&enterpriseLedgerIndexSchema{}, indexName))
	}
	require.NoError(t, db.Exec("CREATE UNIQUE INDEX idx_enterprise_ledgers_idempotency_key ON enterprise_ledgers (idempotency_key)").Error)

	require.NoError(t, migrateEnterpriseLedgerIndexes(db))
	assert.False(t, db.Migrator().HasIndex(&EnterpriseLedger{}, "idx_enterprise_ledgers_idempotency_key"))
	assert.True(t, db.Migrator().HasIndex(&enterpriseLedgerIndexSchema{}, "idx_enterprise_ledger_enterprise_idempotency"))
	assert.True(t, db.Migrator().HasIndex(&enterpriseLedgerIndexSchema{}, "idx_enterprise_ledger_enterprise_reference"))
}

func TestEnterpriseLedgerAdjustmentAndReversal(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	_, err := CreditEnterpriseWallet(enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "topup-adjust", "payment-adjust", 100))
	require.NoError(t, err)

	adjustment := enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindAdjustment, "adjust-1", "adjust-1", 30)
	adjustment.AdjustmentDirection = "debit"
	adjusted, err := AdjustEnterpriseQuota(adjustment)
	require.NoError(t, err)
	assert.Equal(t, 70, adjusted.EnterpriseAvailableQuota)

	wrongDirection := adjustment
	wrongDirection.AdjustmentDirection = "credit"
	_, err = AdjustEnterpriseQuota(wrongDirection)
	require.ErrorIs(t, err, ErrEnterpriseIdempotencyConflict)

	reversal := enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindReversal, "reverse-1", "reverse-1", 30)
	reversal.ReversesLedgerID = adjusted.LedgerID
	reversed, err := ReverseEnterpriseLedger(reversal)
	require.NoError(t, err)
	assert.Equal(t, 100, reversed.EnterpriseAvailableQuota)

	replayed, err := ReverseEnterpriseLedger(reversal)
	require.NoError(t, err)
	assert.True(t, replayed.Replayed)
	assert.Equal(t, reversed.LedgerID, replayed.LedgerID)

	assert.ErrorIs(t, DB.Model(&EnterpriseLedger{}).Where("id = ?", adjusted.LedgerID).Update("reason", "mutated").Error, ErrEnterpriseLedgerImmutable)
}

func TestEnterpriseLedgerAdjustmentAndReversalGuards(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, member := enterpriseLedgerFixture(t)
	command := enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "guard-topup", "payment-guard", 100)
	_, err := CreditEnterpriseWallet(command)
	require.NoError(t, err)

	adjustment := enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindAdjustment, "guard-adjust", "adjust-guard", 10)
	adjustment.AdjustmentDirection = "debit"
	_, err = AdjustEnterpriseQuota(adjustment)
	require.NoError(t, err)

	ordinary := &User{Id: 404, Username: "ordinary-404", AffCode: "ordinary-aff-404", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(ordinary).Error)
	unauthorized := adjustment
	unauthorized.IdempotencyKey = "guard-adjust-unauthorized"
	unauthorized.ReferenceID = "adjust-unauthorized"
	unauthorized.ActorUserID = ordinary.Id
	_, err = AdjustEnterpriseQuota(unauthorized)
	assert.ErrorIs(t, err, ErrEnterpriseOwnerRequired)

	badReference := enterpriseMoneyCommand(enterprise.Id, member.Id, EnterpriseLedgerKindAllocate, "guard-allocate", "wrong-reference", 1)
	badReference.ReferenceType = "admin_adjustment"
	_, err = AllocateEnterpriseQuota(badReference)
	assert.ErrorIs(t, err, ErrInvalidQuotaAmount)

	badOriginal := EnterpriseLedger{EnterpriseId: enterprise.Id, Kind: EnterpriseLedgerKindReserve, Amount: 1, ReferenceType: "membership_allocation", ReferenceId: "reserve-1", IdempotencyKey: "reserve-1", IdempotencyKeyHash: enterpriseLedgerHash("reserve-1", 0), ReferenceHash: enterpriseReferenceHash("membership_allocation", "reserve-1", 0), CommandSummary: "legacy", CommandFingerprint: enterpriseLedgerHash("legacy", 0)}
	require.NoError(t, DB.Create(&badOriginal).Error)
	reversal := enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindReversal, "guard-reverse", "reverse-guard", 1)
	reversal.ReversesLedgerID = badOriginal.Id
	_, err = ReverseEnterpriseLedger(reversal)
	assert.ErrorIs(t, err, ErrEnterpriseIdempotencyConflict)
}

func TestBillingSubjectSnapshotsCannotChangeAfterCreation(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	topup := &TopUp{UserId: 501, Amount: 10, TradeNo: "immutable-topup"}
	require.NoError(t, topup.Insert())
	topup.BillingSubjectType = BillingSubjectTypeEnterprise
	topup.BillingSubjectId = 22
	topup.BillingEnterpriseId = 22
	assert.ErrorIs(t, topup.Update(), ErrBillingSubjectImmutable)

	order := &SubscriptionOrder{UserId: 502, PlanId: 1, TradeNo: "immutable-subscription"}
	require.NoError(t, order.Insert())
	order.BillingSubjectType = BillingSubjectTypeEnterprise
	order.BillingSubjectId = 22
	order.BillingEnterpriseId = 22
	assert.ErrorIs(t, order.Update(), ErrBillingSubjectImmutable)
}

func TestBillingSubjectDefaultsToPersonalSnapshot(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	topup := &TopUp{UserId: 501, Amount: 10, TradeNo: "personal-topup"}
	require.NoError(t, topup.Insert())
	assert.Equal(t, BillingSubjectTypePersonal, topup.BillingSubjectType)
	assert.Equal(t, 501, topup.BillingSubjectId)
	assert.Zero(t, topup.BillingEnterpriseId)

	order := &SubscriptionOrder{UserId: 502, PlanId: 1, TradeNo: "personal-subscription"}
	require.NoError(t, order.Insert())
	assert.Equal(t, BillingSubjectTypePersonal, order.BillingSubjectType)
	assert.Equal(t, 502, order.BillingSubjectId)
	assert.Zero(t, order.BillingEnterpriseId)
}

func TestSettleEnterpriseTopUpCreditsOnlyEnterpriseWallet(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", enterprise.OwnerUserId).Update("quota", 77).Error)
	topUp := &TopUp{
		UserId:              enterprise.OwnerUserId,
		BillingSubjectType:  BillingSubjectTypeEnterprise,
		BillingSubjectId:    enterprise.OwnerUserId,
		BillingEnterpriseId: enterprise.Id,
		// Amount is intentionally not the credited quota: payment providers
		// store incompatible Amount semantics. The caller supplies normalized
		// internal quota to settleEnterpriseTopUp.
		Amount:  1,
		TradeNo: "enterprise-topup-settlement",
		Status:  common.TopUpStatusSuccess,
	}
	require.NoError(t, topUp.Insert())

	settled, err := settleEnterpriseTopUp(topUp.Id, 100)
	require.NoError(t, err)
	assert.False(t, settled.Replayed)
	assert.Equal(t, 100, settled.EnterpriseAvailableQuota)

	replayed, err := settleEnterpriseTopUp(topUp.Id, 100)
	require.NoError(t, err)
	assert.True(t, replayed.Replayed)
	assert.Equal(t, settled.LedgerID, replayed.LedgerID)

	var savedEnterprise Enterprise
	var savedUser User
	var ledger EnterpriseLedger
	require.NoError(t, DB.First(&savedEnterprise, enterprise.Id).Error)
	require.NoError(t, DB.First(&savedUser, enterprise.OwnerUserId).Error)
	require.NoError(t, DB.First(&ledger, settled.LedgerID).Error)
	assert.Equal(t, 100, savedEnterprise.AvailableQuota)
	assert.Equal(t, 77, savedUser.Quota)
	assert.Equal(t, EnterpriseLedgerKindTopUp, ledger.Kind)
	assert.Equal(t, strconv.Itoa(topUp.Id), ledger.ReferenceId)

	_, err = settleEnterpriseTopUp(topUp.Id, 0)
	assert.ErrorIs(t, err, ErrInvalidQuotaAmount)
}

func TestBillingSubjectMapUpdatesAreRejectedAndOrdinaryUpdatesWork(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	topUp := &TopUp{UserId: 501, Amount: 10, TradeNo: "snapshot-map-topup", Status: common.TopUpStatusPending}
	require.NoError(t, topUp.Insert())
	order := &SubscriptionOrder{UserId: 502, PlanId: 1, Money: 1, TradeNo: "snapshot-map-order", Status: common.TopUpStatusPending}
	require.NoError(t, order.Insert())

	snapshotUpdate := map[string]any{
		"billing_subject_type":  BillingSubjectTypeEnterprise,
		"billing_subject_id":    999,
		"billing_enterprise_id": 999,
	}
	assert.ErrorIs(t, DB.Model(&TopUp{}).Where("id = ?", topUp.Id).Updates(snapshotUpdate).Error, ErrBillingSubjectImmutable)
	assert.ErrorIs(t, DB.Model(&SubscriptionOrder{}).Where("id = ?", order.Id).Updates(snapshotUpdate).Error, ErrBillingSubjectImmutable)

	topUp.Status = common.TopUpStatusSuccess
	topUp.Money = 2
	require.NoError(t, topUp.Update())
	order.Status = common.TopUpStatusSuccess
	order.Money = 3
	require.NoError(t, order.Update())

	var savedTopUp TopUp
	var savedOrder SubscriptionOrder
	require.NoError(t, DB.First(&savedTopUp, topUp.Id).Error)
	require.NoError(t, DB.First(&savedOrder, order.Id).Error)
	assert.Equal(t, BillingSubjectTypePersonal, savedTopUp.BillingSubjectType)
	assert.Equal(t, 501, savedTopUp.BillingSubjectId)
	assert.Zero(t, savedTopUp.BillingEnterpriseId)
	assert.Equal(t, common.TopUpStatusSuccess, savedTopUp.Status)
	assert.Equal(t, 2.0, savedTopUp.Money)
	assert.Equal(t, BillingSubjectTypePersonal, savedOrder.BillingSubjectType)
	assert.Equal(t, 502, savedOrder.BillingSubjectId)
	assert.Zero(t, savedOrder.BillingEnterpriseId)
	assert.Equal(t, common.TopUpStatusSuccess, savedOrder.Status)
	assert.Equal(t, 3.0, savedOrder.Money)
}

func TestSubscriptionTopUpMirrorCopiesAndValidatesPaymentProvider(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	order := &SubscriptionOrder{
		UserId:          601,
		PlanId:          1,
		Money:           12,
		TradeNo:         "subscription-mirror-provider",
		PaymentMethod:   PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe,
		CreateTime:      common.GetTimestamp(),
	}
	require.NoError(t, order.Insert())
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return upsertSubscriptionTopUpTx(tx, order)
	}))

	var mirror TopUp
	require.NoError(t, DB.Where("trade_no = ?", order.TradeNo).First(&mirror).Error)
	assert.Equal(t, order.PaymentProvider, mirror.PaymentProvider)
	assert.Equal(t, order.BillingSubjectType, mirror.BillingSubjectType)
	assert.Equal(t, order.BillingSubjectId, mirror.BillingSubjectId)
	assert.Equal(t, order.BillingEnterpriseId, mirror.BillingEnterpriseId)

	mirror.PaymentProvider = ""
	require.NoError(t, mirror.Update())
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return upsertSubscriptionTopUpTx(tx, order)
	}))
	require.NoError(t, DB.First(&mirror, mirror.Id).Error)
	assert.Equal(t, order.PaymentProvider, mirror.PaymentProvider)

	mirror.PaymentProvider = PaymentProviderCreem
	require.NoError(t, mirror.Update())
	err := DB.Transaction(func(tx *gorm.DB) error {
		return upsertSubscriptionTopUpTx(tx, order)
	})
	assert.ErrorIs(t, err, ErrPaymentProviderMismatch)
}
