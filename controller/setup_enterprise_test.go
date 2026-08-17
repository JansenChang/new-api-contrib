package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPostSetupCreatesRootEnterpriseInSameTransaction(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	previousSetup := constant.Setup
	previousSelfUseMode := operation_setting.SelfUseModeEnabled
	previousDemoMode := operation_setting.DemoSiteEnabled
	previousOptionMap := common.OptionMap
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	db, err := gorm.Open(sqlite.Open("file:setup-enterprise-test?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.Enterprise{}, &model.EnterpriseMembership{}, &model.Option{}, &model.Setup{},
	))
	constant.Setup = false
	common.OptionMap = make(map[string]string)
	t.Cleanup(func() {
		constant.Setup = previousSetup
		operation_setting.SelfUseModeEnabled = previousSelfUseMode
		operation_setting.DemoSiteEnabled = previousDemoMode
		common.OptionMap = previousOptionMap
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(`{"username":"setup-root","password":"password123","confirmPassword":"password123"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	PostSetup(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var root model.User
	require.NoError(t, db.Where("role = ?", common.RoleRootUser).First(&root).Error)
	assert.NotZero(t, root.ActiveEnterpriseId)
	var enterprise model.Enterprise
	require.NoError(t, db.First(&enterprise, root.ActiveEnterpriseId).Error)
	assert.Equal(t, root.Id, enterprise.OwnerUserId)
	var membership model.EnterpriseMembership
	require.NoError(t, db.Where("enterprise_id = ? AND user_id = ?", enterprise.Id, root.Id).First(&membership).Error)
	assert.Equal(t, model.EnterpriseMembershipRoleOwner, membership.Role)
}
