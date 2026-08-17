package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEnterpriseUsageTestDB(t *testing.T) {
	db := newEnterpriseLedgerTestDB(t)
	require.NoError(t, db.AutoMigrate(&EnterpriseUsageRecord{}, &Task{}))
	previousGate := common.EnterpriseBillingEnabled
	common.EnterpriseBillingEnabled = true
	t.Cleanup(func() { common.EnterpriseBillingEnabled = previousGate })
}

func enterpriseUsageCommand(actorID, tokenID int, key string, amount int) EnterpriseUsageCommand {
	return EnterpriseUsageCommand{
		ActorUserID:        actorID,
		TokenID:            tokenID,
		IdempotencyKey:     key,
		RequestFingerprint: strings.Repeat("a", 64),
		RequestID:          "usage-test-request",
		ModelName:          "test-model",
		ReservedQuota:      amount,
	}
}

func TestEnterpriseUsageOwnerReserveSettleAndReplay(t *testing.T) {
	newEnterpriseUsageTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", enterprise.OwnerUserId).Update("active_enterprise_id", enterprise.Id).Error)
	_, err := CreditEnterpriseWallet(enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "usage-owner-topup", "usage-owner-topup", 1000))
	require.NoError(t, err)

	cmd := enterpriseUsageCommand(enterprise.OwnerUserId, 501, "client-idempotency-key", 100)
	reserved, err := ReserveEnterpriseUsage(cmd)
	require.NoError(t, err)
	assert.True(t, reserved.Execute)
	assert.False(t, reserved.Replayed)

	var usage EnterpriseUsageRecord
	var saved Enterprise
	require.NoError(t, DB.First(&usage, reserved.UsageID).Error)
	require.NoError(t, DB.First(&saved, enterprise.Id).Error)
	assert.Equal(t, EnterpriseUsageStatePending, usage.State)
	assert.Equal(t, enterpriseUsageIdempotencyHash(cmd.IdempotencyKey), usage.IdempotencyKey)
	assert.NotEqual(t, cmd.IdempotencyKey, usage.IdempotencyKey)
	assert.Equal(t, 900, saved.AvailableQuota)
	assert.Equal(t, 100, saved.ReservedQuota)
	assert.Zero(t, getUserQuotaForEnterpriseUsageTest(t, enterprise.OwnerUserId))

	replayed, err := ReserveEnterpriseUsage(cmd)
	require.NoError(t, err)
	assert.True(t, replayed.Replayed)
	assert.False(t, replayed.Execute)
	assert.Equal(t, reserved.UsageID, replayed.UsageID)

	conflict := cmd
	conflict.RequestFingerprint = strings.Repeat("b", 64)
	_, err = ReserveEnterpriseUsage(conflict)
	assert.ErrorIs(t, err, ErrEnterpriseUsageConflict)

	settled, err := SettleEnterpriseUsage(reserved.UsageID, 60)
	require.NoError(t, err)
	assert.Equal(t, EnterpriseUsageStateSettled, settled.State)
	assert.Equal(t, 60, settled.SettledQuota)
	require.NoError(t, DB.First(&saved, enterprise.Id).Error)
	assert.Equal(t, 940, saved.AvailableQuota)
	assert.Zero(t, saved.ReservedQuota)
	assert.Zero(t, getUserQuotaForEnterpriseUsageTest(t, enterprise.OwnerUserId))

	settledAgain, err := SettleEnterpriseUsage(reserved.UsageID, 60)
	require.NoError(t, err)
	assert.True(t, settledAgain.Replayed)
	var ledgers []EnterpriseLedger
	require.NoError(t, DB.Where("enterprise_id = ?", enterprise.Id).Order("id").Find(&ledgers).Error)
	require.Len(t, ledgers, 3) // TOPUP, RESERVE, SETTLE
	assert.Equal(t, EnterpriseLedgerKindReserve, ledgers[1].Kind)
	assert.Equal(t, EnterpriseLedgerKindSettle, ledgers[2].Kind)
}

func TestEnterpriseUsageReserveRejectsWhenJointReleaseGateIsClosed(t *testing.T) {
	newEnterpriseUsageTestDB(t)
	common.EnterpriseBillingEnabled = false
	_, err := ReserveEnterpriseUsage(enterpriseUsageCommand(1, 501, "closed-gate-key", 1))
	assert.ErrorIs(t, err, ErrEnterpriseBillingDisabled)
}

func TestEnterpriseUsageMemberAnomalyNeverUsesPersonalAssets(t *testing.T) {
	newEnterpriseUsageTestDB(t)
	enterprise, member := enterpriseLedgerFixture(t)
	require.NoError(t, DB.Create(&User{Id: member.UserId, Username: "member-user", AffCode: "member-usage-aff", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 777, ActiveEnterpriseId: enterprise.Id}).Error)
	require.NoError(t, DB.Model(&EnterpriseMembership{}).Where("id = ?", member.Id).Updates(map[string]any{"available_quota": 200, "self_key_limit": 200}).Error)

	reserved, err := ReserveEnterpriseUsage(enterpriseUsageCommand(member.UserId, 502, "member-idempotency-key", 200))
	require.NoError(t, err)
	settled, err := SettleEnterpriseUsage(reserved.UsageID, 230)
	require.NoError(t, err)
	assert.Equal(t, EnterpriseUsageStateAnomaly, settled.State)
	assert.Equal(t, 200, settled.SettledQuota)
	assert.Equal(t, 30, settled.AnomalyQuota)

	var savedMember EnterpriseMembership
	var savedEnterprise Enterprise
	require.NoError(t, DB.First(&savedMember, member.Id).Error)
	require.NoError(t, DB.First(&savedEnterprise, enterprise.Id).Error)
	assert.Equal(t, EnterpriseMembershipStatusManualReview, savedMember.Status)
	assert.Zero(t, savedMember.AvailableQuota)
	assert.Zero(t, savedMember.ReservedQuota)
	assert.Equal(t, 200, savedMember.SelfKeyUsedQuota)
	assert.Equal(t, 30, savedEnterprise.AnomalyQuota)
	assert.Equal(t, 777, getUserQuotaForEnterpriseUsageTest(t, member.UserId))
}

func TestEnterpriseUsageUnknownSubmissionStaysManualReviewWithoutRefund(t *testing.T) {
	newEnterpriseUsageTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", enterprise.OwnerUserId).Update("active_enterprise_id", enterprise.Id).Error)
	_, err := CreditEnterpriseWallet(enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "usage-manual-topup", "usage-manual-topup", 100))
	require.NoError(t, err)

	reserved, err := ReserveEnterpriseUsage(enterpriseUsageCommand(enterprise.OwnerUserId, 503, "manual-idempotency-key", 50))
	require.NoError(t, err)
	manual, err := MarkEnterpriseUsageManualReview(reserved.UsageID)
	require.NoError(t, err)
	assert.Equal(t, EnterpriseUsageStateManualReview, manual.State)
	_, err = RefundEnterpriseUsage(reserved.UsageID)
	assert.ErrorIs(t, err, ErrEnterpriseUsageInvalidState)

	var saved Enterprise
	require.NoError(t, DB.First(&saved, enterprise.Id).Error)
	assert.Equal(t, 50, saved.AvailableQuota)
	assert.Equal(t, 50, saved.ReservedQuota)
}

func TestEnterpriseUsageManualReviewCannotBeSettledLater(t *testing.T) {
	newEnterpriseUsageTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", enterprise.OwnerUserId).Update("active_enterprise_id", enterprise.Id).Error)
	_, err := CreditEnterpriseWallet(enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "usage-manual-settle-topup", "usage-manual-settle-topup", 100))
	require.NoError(t, err)

	reserved, err := ReserveEnterpriseUsage(enterpriseUsageCommand(enterprise.OwnerUserId, 504, "manual-settle-idempotency-key", 50))
	require.NoError(t, err)
	_, err = MarkEnterpriseUsageManualReview(reserved.UsageID)
	require.NoError(t, err)

	_, err = SettleEnterpriseUsage(reserved.UsageID, 0)
	assert.ErrorIs(t, err, ErrEnterpriseUsageInvalidState)
}

func TestPlatformAdminCanResolveManualEnterpriseTaskAtomically(t *testing.T) {
	newEnterpriseUsageTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", enterprise.OwnerUserId).Update("active_enterprise_id", enterprise.Id).Error)
	_, err := CreditEnterpriseWallet(enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "manual-task-topup", "manual-task-topup", 100))
	require.NoError(t, err)
	task := &Task{TaskID: "manual-enterprise-task", UserId: enterprise.OwnerUserId, Status: TaskStatusSubmitting, Progress: "0%"}
	reserved, err := ReserveEnterpriseUsageWithTask(enterpriseUsageCommand(enterprise.OwnerUserId, 505, "manual-task-idempotency-key", 50), task)
	require.NoError(t, err)
	_, err = MarkEnterpriseUsageManualReview(reserved.UsageID)
	require.NoError(t, err)
	task.Status = TaskStatusManualReview
	won, err := task.UpdateWithStatus(TaskStatusSubmitting)
	require.NoError(t, err)
	require.True(t, won)

	resolved, err := ResolveEnterpriseTaskManualReview(task.ID, enterprise.OwnerUserId, EnterpriseManualResolutionSuccess, 30, "上游确认成功")
	require.NoError(t, err)
	assert.Equal(t, EnterpriseUsageStateSettled, resolved.State)
	assert.Equal(t, 30, resolved.SettledQuota)

	replayed, err := ResolveEnterpriseTaskManualReview(task.ID, enterprise.OwnerUserId, EnterpriseManualResolutionSuccess, 30, "重复提交")
	require.NoError(t, err)
	assert.True(t, replayed.Replayed)

	var usage EnterpriseUsageRecord
	var savedTask Task
	var ledger EnterpriseLedger
	require.NoError(t, DB.First(&usage, reserved.UsageID).Error)
	require.NoError(t, DB.First(&savedTask, task.ID).Error)
	require.NoError(t, DB.Where("reference_id = ?", fmt.Sprintf("usage:%d:%s", reserved.UsageID, EnterpriseLedgerKindSettle)).First(&ledger).Error)
	assert.Equal(t, EnterpriseUsageStateSettled, usage.State)
	assert.Equal(t, TaskStatus(TaskStatusSuccess), savedTask.Status)
	assert.Equal(t, enterprise.OwnerUserId, ledger.ActorUserId)
	assert.Equal(t, "上游确认成功", ledger.Reason)
}

func TestManualEnterpriseTaskRefundRequiresPlatformAdmin(t *testing.T) {
	newEnterpriseUsageTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", enterprise.OwnerUserId).Update("active_enterprise_id", enterprise.Id).Error)
	_, err := CreditEnterpriseWallet(enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "manual-refund-topup", "manual-refund-topup", 100))
	require.NoError(t, err)
	task := &Task{TaskID: "manual-refund-task", UserId: enterprise.OwnerUserId, Status: TaskStatusSubmitting, Progress: "0%"}
	reserved, err := ReserveEnterpriseUsageWithTask(enterpriseUsageCommand(enterprise.OwnerUserId, 506, "manual-refund-idempotency-key", 50), task)
	require.NoError(t, err)
	_, err = MarkEnterpriseUsageManualReview(reserved.UsageID)
	require.NoError(t, err)
	task.Status = TaskStatusManualReview
	won, err := task.UpdateWithStatus(TaskStatusSubmitting)
	require.NoError(t, err)
	require.True(t, won)
	require.NoError(t, DB.Create(&User{Id: 507, Username: "ordinary-user", AffCode: "ordinary-user-aff", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}).Error)

	_, err = ResolveEnterpriseTaskManualReview(task.ID, 507, EnterpriseManualResolutionRefund, 0, "无权退款")
	assert.ErrorIs(t, err, ErrEnterpriseOwnerRequired)

	resolved, err := ResolveEnterpriseTaskManualReview(task.ID, enterprise.OwnerUserId, EnterpriseManualResolutionRefund, 0, "确认上游未执行")
	require.NoError(t, err)
	assert.Equal(t, EnterpriseUsageStateRefunded, resolved.State)
	var savedTask Task
	require.NoError(t, DB.First(&savedTask, task.ID).Error)
	assert.Equal(t, TaskStatus(TaskStatusFailure), savedTask.Status)
}

func TestEnterpriseUsageSnapshotsSelectedChannelBeforeSubmission(t *testing.T) {
	newEnterpriseUsageTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", enterprise.OwnerUserId).Update("active_enterprise_id", enterprise.Id).Error)
	_, err := CreditEnterpriseWallet(enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "usage-channel-topup", "usage-channel-topup", 100))
	require.NoError(t, err)

	reserved, err := ReserveEnterpriseUsage(enterpriseUsageCommand(enterprise.OwnerUserId, 505, "channel-idempotency-key", 50))
	require.NoError(t, err)
	require.NoError(t, SnapshotEnterpriseUsageChannel(reserved.UsageID, 88))
	require.NoError(t, SnapshotEnterpriseUsageChannel(reserved.UsageID, 88))
	assert.ErrorIs(t, SnapshotEnterpriseUsageChannel(reserved.UsageID, 89), ErrEnterpriseUsageConflict)
	_, err = MarkEnterpriseUsageUpstreamSubmitted(reserved.UsageID)
	require.NoError(t, err)
	assert.ErrorIs(t, SnapshotEnterpriseUsageChannel(reserved.UsageID, 88), ErrEnterpriseUsageInvalidState)

	var usage EnterpriseUsageRecord
	require.NoError(t, DB.First(&usage, reserved.UsageID).Error)
	assert.Equal(t, 88, usage.ChannelId)
}

func TestEnterpriseTaskDraftAndReserveAreAtomic(t *testing.T) {
	newEnterpriseUsageTestDB(t)
	enterprise, _ := enterpriseLedgerFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", enterprise.OwnerUserId).Update("active_enterprise_id", enterprise.Id).Error)
	_, err := CreditEnterpriseWallet(enterpriseMoneyCommand(enterprise.Id, 0, EnterpriseLedgerKindTopUp, "task-draft-topup", "task-draft-topup", 100))
	require.NoError(t, err)
	task := &Task{TaskID: "task-enterprise-draft", UserId: enterprise.OwnerUserId, Status: TaskStatusSubmitting, Progress: "0%"}
	outcome, err := ReserveEnterpriseUsageWithTask(enterpriseUsageCommand(enterprise.OwnerUserId, 506, "task-draft-key", 50), task)
	require.NoError(t, err)
	assert.Equal(t, outcome.UsageID, task.PrivateData.EnterpriseUsageRecordId)
	var saved Task
	require.NoError(t, DB.First(&saved, task.ID).Error)
	assert.Equal(t, TaskStatus(TaskStatusSubmitting), saved.Status)
	assert.Equal(t, outcome.UsageID, saved.PrivateData.EnterpriseUsageRecordId)
}

func getUserQuotaForEnterpriseUsageTest(t *testing.T, userID int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("quota").Where("id = ?", userID).First(&user).Error)
	return user.Quota
}
