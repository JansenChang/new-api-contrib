package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This is deliberately a route-level regression test. Every listed endpoint
// reaches UserAuth first, then must be rejected by the enterprise asset guard
// before its controller can create a payment/order, redeem a code, or credit a
// personal wallet.
func TestEnterpriseAssetFreezeCoversEveryPersonalAssetWriteRoute(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousGate := common.EnterpriseBillingEnabled
	previousRedis := common.RedisEnabled
	previousTurnstile := common.TurnstileCheckEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.EnterpriseBillingEnabled = true
	common.RedisEnabled = false
	common.TurnstileCheckEnabled = false
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EnterpriseMembership{}))
	accessToken := "enterprise-freeze-route-test"
	user := &model.User{Username: "enterprise-freeze-user", Password: "unused-password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AccessToken: &accessToken}
	require.NoError(t, db.Create(user).Error)
	require.NoError(t, db.Create(&model.EnterpriseMembership{EnterpriseId: 1, UserId: user.Id, Role: model.EnterpriseMembershipRoleMember, Status: model.EnterpriseMembershipStatusPaused}).Error)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.EnterpriseBillingEnabled = previousGate
		common.RedisEnabled = previousRedis
		common.TurnstileCheckEnabled = previousTurnstile
		common.SetMainDatabaseType(previousMainDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	for _, path := range []string{
		"/api/user/topup",
		"/api/user/pay",
		"/api/user/amount",
		"/api/user/stripe/pay",
		"/api/user/stripe/amount",
		"/api/user/creem/pay",
		"/api/user/waffo/amount",
		"/api/user/waffo/pay",
		"/api/user/waffo-pancake/amount",
		"/api/user/waffo-pancake/pay",
		"/api/user/aff_transfer",
		"/api/user/checkin",
		"/api/subscription/balance/pay",
		"/api/subscription/epay/pay",
		"/api/subscription/stripe/pay",
		"/api/subscription/creem/pay",
		"/api/subscription/waffo-pancake/pay",
	} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, nil)
			request.Header.Set("Authorization", "Bearer "+accessToken)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.Contains(t, recorder.Body.String(), "ENTERPRISE_PERSONAL_ASSETS_FROZEN")
		})
	}
}
