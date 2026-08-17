package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRechargeEpaySettlesEnterpriseSnapshotExactlyOnce(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 10
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	order := &TopUp{
		UserId:              enterprise.OwnerUserId,
		BillingSubjectType:  BillingSubjectTypeEnterprise,
		BillingSubjectId:    enterprise.OwnerUserId,
		BillingEnterpriseId: enterprise.Id,
		Amount:              2,
		TradeNo:             "enterprise-epay-once",
		PaymentMethod:       "alipay",
		PaymentProvider:     PaymentProviderEpay,
		Status:              common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())

	alreadyDone, err := RechargeEpay(order.TradeNo, "alipay", "127.0.0.1")
	require.NoError(t, err)
	assert.False(t, alreadyDone)
	assert.Zero(t, getUserQuotaForPaymentGuardTest(t, enterprise.OwnerUserId))

	var savedEnterprise Enterprise
	require.NoError(t, DB.First(&savedEnterprise, enterprise.Id).Error)
	assert.Equal(t, 20, savedEnterprise.AvailableQuota)
	var count int64
	require.NoError(t, DB.Model(&EnterpriseLedger{}).Where("enterprise_id = ? AND kind = ?", enterprise.Id, EnterpriseLedgerKindTopUp).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	alreadyDone, err = RechargeEpay(order.TradeNo, "alipay", "127.0.0.1")
	require.NoError(t, err)
	assert.True(t, alreadyDone)
	require.NoError(t, DB.Model(&EnterpriseLedger{}).Where("enterprise_id = ? AND kind = ?", enterprise.Id, EnterpriseLedgerKindTopUp).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestRechargeEpayKeepsPersonalSnapshotPersonalEvenForEnterpriseOwner(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 10
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	order := &TopUp{
		UserId:          enterprise.OwnerUserId,
		Amount:          2,
		TradeNo:         "personal-epay-owner",
		PaymentMethod:   "alipay",
		PaymentProvider: PaymentProviderEpay,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())

	_, err := RechargeEpay(order.TradeNo, "alipay", "127.0.0.1")
	require.NoError(t, err)
	assert.Equal(t, 20, getUserQuotaForPaymentGuardTest(t, enterprise.OwnerUserId))
	var savedEnterprise Enterprise
	require.NoError(t, DB.First(&savedEnterprise, enterprise.Id).Error)
	assert.Zero(t, savedEnterprise.AvailableQuota)
}

func TestPersonalTopUpRejectsQuotaOverflowWithoutCompletingOrder(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	user := &User{Id: 707, Username: "personal-quota-cap", Status: common.UserStatusEnabled, Quota: common.MaxQuota - 1}
	require.NoError(t, DB.Create(user).Error)
	order := &TopUp{
		UserId:          user.Id,
		Amount:          2,
		TradeNo:         "personal-quota-cap",
		PaymentMethod:   "alipay",
		PaymentProvider: PaymentProviderEpay,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())

	_, err := RechargeEpay(order.TradeNo, "alipay", "127.0.0.1")
	require.ErrorIs(t, err, ErrUserQuotaExceeded)
	assert.Equal(t, common.MaxQuota-1, getUserQuotaForPaymentGuardTest(t, user.Id))
	assert.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(order.TradeNo).Status)
}

func TestAllTopUpSettlersUseEnterpriseSnapshot(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 10
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	testCases := []struct {
		name     string
		provider string
		amount   int64
		money    float64
		expected int
		settle   func(string) error
	}{
		{"epay", PaymentProviderEpay, 2, 0, 20, func(tradeNo string) error { _, err := RechargeEpay(tradeNo, "alipay", "127.0.0.1"); return err }},
		{"stripe", PaymentProviderStripe, 0, 2, 20, func(tradeNo string) error { return Recharge(tradeNo, "", "127.0.0.1") }},
		{"creem", PaymentProviderCreem, 2, 0, 2, func(tradeNo string) error { return RechargeCreem(tradeNo, "", "", "127.0.0.1") }},
		{"waffo", PaymentProviderWaffo, 2, 0, 20, func(tradeNo string) error { return RechargeWaffo(tradeNo, "127.0.0.1") }},
		{"waffo-pancake", PaymentProviderWaffoPancake, 2, 0, 20, RechargeWaffoPancake},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			expectedQuota := enterprise.AvailableQuota + tc.expected
			order := &TopUp{
				UserId:              enterprise.OwnerUserId,
				BillingSubjectType:  BillingSubjectTypeEnterprise,
				BillingSubjectId:    enterprise.OwnerUserId,
				BillingEnterpriseId: enterprise.Id,
				Amount:              tc.amount,
				Money:               tc.money,
				TradeNo:             "enterprise-" + tc.name,
				PaymentMethod:       tc.provider,
				PaymentProvider:     tc.provider,
				Status:              common.TopUpStatusPending,
			}
			require.NoError(t, order.Insert())
			require.NoError(t, tc.settle(order.TradeNo))
			assert.Zero(t, getUserQuotaForPaymentGuardTest(t, enterprise.OwnerUserId))
			var saved Enterprise
			require.NoError(t, DB.First(&saved, enterprise.Id).Error)
			assert.Equal(t, expectedQuota, saved.AvailableQuota)
			enterprise.AvailableQuota = saved.AvailableQuota
		})
	}
}

func TestRechargeEpayRollsBackWhenEnterpriseSnapshotCannotSettle(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	require.NoError(t, DB.Create(&User{Id: 701, Username: "enterprise-payment-missing", Status: common.UserStatusEnabled}).Error)
	order := &TopUp{
		UserId:              701,
		BillingSubjectType:  BillingSubjectTypeEnterprise,
		BillingSubjectId:    701,
		BillingEnterpriseId: 99999,
		Amount:              2,
		TradeNo:             "enterprise-epay-missing",
		PaymentMethod:       "alipay",
		PaymentProvider:     PaymentProviderEpay,
		Status:              common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())

	_, err := RechargeEpay(order.TradeNo, "alipay", "127.0.0.1")
	require.ErrorIs(t, err, ErrEnterpriseNotFound)
	assert.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(order.TradeNo).Status)
	assert.Zero(t, getUserQuotaForPaymentGuardTest(t, order.UserId))
}

func TestManualCompleteTopUpSettlesEnterpriseSnapshotWithoutPersonalCredit(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 10
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	order := &TopUp{
		UserId:              enterprise.OwnerUserId,
		BillingSubjectType:  BillingSubjectTypeEnterprise,
		BillingSubjectId:    enterprise.OwnerUserId,
		BillingEnterpriseId: enterprise.Id,
		Amount:              2,
		TradeNo:             "enterprise-manual-topup",
		PaymentMethod:       "alipay",
		PaymentProvider:     PaymentProviderEpay,
		Status:              common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())
	require.NoError(t, ManualCompleteTopUp(order.TradeNo, "127.0.0.1"))

	assert.Zero(t, getUserQuotaForPaymentGuardTest(t, enterprise.OwnerUserId))
	assert.Equal(t, common.TopUpStatusSuccess, GetTopUpByTradeNo(order.TradeNo).Status)
	var savedEnterprise Enterprise
	require.NoError(t, DB.First(&savedEnterprise, enterprise.Id).Error)
	assert.Equal(t, 20, savedEnterprise.AvailableQuota)
	var ledgerCount int64
	require.NoError(t, DB.Model(&EnterpriseLedger{}).Where("enterprise_id = ? AND kind = ?", enterprise.Id, EnterpriseLedgerKindTopUp).Count(&ledgerCount).Error)
	assert.Equal(t, int64(1), ledgerCount)
}

func TestCompleteSubscriptionOrderRejectsEnterpriseSnapshotWithoutSideEffects(t *testing.T) {
	newEnterpriseLedgerTestDB(t)
	require.NoError(t, DB.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}))
	enterprise, _ := enterpriseLedgerFixture(t)
	plan := &SubscriptionPlan{
		Id:            801,
		Title:         "enterprise subscription boundary",
		PriceAmount:   9.99,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
	}
	require.NoError(t, DB.Create(plan).Error)
	order := &SubscriptionOrder{
		UserId:              enterprise.OwnerUserId,
		BillingSubjectType:  BillingSubjectTypeEnterprise,
		BillingSubjectId:    enterprise.OwnerUserId,
		BillingEnterpriseId: enterprise.Id,
		PlanId:              plan.Id,
		Money:               plan.PriceAmount,
		TradeNo:             "enterprise-subscription-rejected",
		PaymentMethod:       PaymentMethodStripe,
		PaymentProvider:     PaymentProviderStripe,
		Status:              common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())

	err := CompleteSubscriptionOrder(order.TradeNo, `{"event":"paid"}`, PaymentProviderStripe, "")
	require.ErrorIs(t, err, ErrEnterpriseSubscriptionUnsupported)
	assert.Equal(t, common.TopUpStatusPending, GetSubscriptionOrderByTradeNo(order.TradeNo).Status)
	assert.Zero(t, countUserSubscriptionsForPaymentGuardTest(t, order.UserId))
	assert.Nil(t, GetTopUpByTradeNo(order.TradeNo))
}
