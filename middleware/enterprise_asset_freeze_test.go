package middleware

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

func TestPersonalAssetWriteAllowedBlocksOnlyReleasedMemberships(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType := common.MainDatabaseType()
	previousGate := common.EnterpriseBillingEnabled
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.EnterpriseBillingEnabled = false
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.EnterpriseMembership{}))
	member := &model.EnterpriseMembership{EnterpriseId: 1, UserId: 88, Role: model.EnterpriseMembershipRoleMember, Status: model.EnterpriseMembershipStatusPaused}
	require.NoError(t, db.Create(member).Error)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousType)
		common.EnterpriseBillingEnabled = previousGate
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	newRouter := func() *gin.Engine {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.POST("/personal-write", func(c *gin.Context) { c.Set("id", 88) }, PersonalAssetWriteAllowed(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
		return router
	}
	request := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		newRouter().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/personal-write", nil))
		return recorder
	}

	assert.Equal(t, http.StatusNoContent, request().Code)
	common.EnterpriseBillingEnabled = true
	recorder := request()
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "ENTERPRISE_PERSONAL_ASSETS_FROZEN")
}
