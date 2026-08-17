package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type adminSiteOptionTestUsers struct {
	rootToken  string
	adminToken string
	userToken  string
}

func setupAdminSiteOptionTest(t *testing.T) adminSiteOptionTestUsers {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousOptions := common.OptionMap
	previousRedisEnabled := common.RedisEnabled
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Log{}))
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{
		"SystemName":           "Test site",
		"Logo":                 "/logo.svg",
		"ServerAddress":        "https://internal.example",
		"PasswordLoginEnabled": "true",
	}
	common.OptionMapRWMutex.Unlock()

	users := adminSiteOptionTestUsers{
		rootToken:  "site-option-root-token",
		adminToken: "site-option-admin-token",
		userToken:  "site-option-user-token",
	}
	for _, user := range []struct {
		username string
		role     int
		token    string
	}{
		{"site-option-root", common.RoleRootUser, users.rootToken},
		{"site-option-admin", common.RoleAdminUser, users.adminToken},
		{"site-option-user", common.RoleCommonUser, users.userToken},
	} {
		token := user.token
		require.NoError(t, model.DB.Create(&model.User{
			Username: user.username, Password: "password-placeholder", Role: user.role,
			Status: common.UserStatusEnabled, Group: "default", AccessToken: &token, AuthVersion: 1,
			AffCode: "site-option-aff-" + user.username,
		}).Error)
	}
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return users
}

func newAdminSiteOptionTestRouter() *gin.Engine {
	router := gin.New()
	siteOptions := router.Group("/api/option/site")
	siteOptions.Use(middleware.AdminAuth())
	siteOptions.GET("", GetAdminSiteOptions)
	siteOptions.PUT("", UpdateAdminSiteOption)
	rootOptions := router.Group("/api/option")
	rootOptions.Use(middleware.RootAuth())
	rootOptions.GET("/", GetOptions)
	return router
}

func performAdminSiteOptionRequest(router *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestGetSiteOptionsOnlyReturnsAllowlistedKeys(t *testing.T) {
	users := setupAdminSiteOptionTest(t)
	recorder := performAdminSiteOptionRequest(newAdminSiteOptionTestRouter(), http.MethodGet, "/api/option/site", users.adminToken, "")

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"key":"SystemName"`)
	assert.NotContains(t, recorder.Body.String(), "ServerAddress")
	assert.NotContains(t, recorder.Body.String(), "PasswordLoginEnabled")
}

func TestSiteOptionAuthorizationBoundaries(t *testing.T) {
	users := setupAdminSiteOptionTest(t)
	router := newAdminSiteOptionTestRouter()

	assert.Equal(t, http.StatusOK, performAdminSiteOptionRequest(router, http.MethodGet, "/api/option/site", users.adminToken, "").Code)
	assert.Equal(t, http.StatusForbidden, performAdminSiteOptionRequest(router, http.MethodGet, "/api/option/site", users.userToken, "").Code)
	assert.Equal(t, http.StatusForbidden, performAdminSiteOptionRequest(router, http.MethodGet, "/api/option/", users.adminToken, "").Code)
	assert.Equal(t, http.StatusOK, performAdminSiteOptionRequest(router, http.MethodGet, "/api/option/", users.rootToken, "").Code)
}

func TestUpdateSiteOptionAllowsAllowlistedKey(t *testing.T) {
	users := setupAdminSiteOptionTest(t)
	recorder := performAdminSiteOptionRequest(newAdminSiteOptionTestRouter(), http.MethodPut, "/api/option/site", users.adminToken, `{"key":"SystemName","value":"Updated site"}`)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var option model.Option
	require.NoError(t, model.DB.First(&option, "key = ?", "SystemName").Error)
	assert.Equal(t, "Updated site", option.Value)
}

func TestUpdateSiteOptionRejectsNonAllowlistedOrInvalidValues(t *testing.T) {
	users := setupAdminSiteOptionTest(t)
	router := newAdminSiteOptionTestRouter()
	for _, body := range []string{
		`{"key":"ServerAddress","value":"changed"}`,
		`{"key":"PasswordLoginEnabled","value":"changed"}`,
		`{"key":"SystemName","value":true}`,
		`{"key":"HeaderNavModules","value":"[]"}`,
		`{"key":"SidebarModulesAdmin","value":"not-json"}`,
	} {
		recorder := performAdminSiteOptionRequest(router, http.MethodPut, "/api/option/site", users.adminToken, body)
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Contains(t, recorder.Body.String(), `"success":false`)
	}
	for _, key := range []string{"ServerAddress", "PasswordLoginEnabled", "HeaderNavModules", "SidebarModulesAdmin"} {
		var option model.Option
		assert.Error(t, model.DB.First(&option, "key = ?", key).Error)
	}
}
