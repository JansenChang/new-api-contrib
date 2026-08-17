package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResetAPIKeyConsumesFlowAndCannotReplay(t *testing.T) {
	db := setupAPIKeyLoginTestDB(t)
	previousEmailDefaultTokenEnabled := common.EmailDefaultTokenEnabled
	common.EmailDefaultTokenEnabled = true
	t.Cleanup(func() { common.EmailDefaultTokenEnabled = previousEmailDefaultTokenEnabled })
	user := createAPIKeyLoginUser(t, db, "recover-old-key")
	user.Email = "recover@example.com"
	require.NoError(t, db.Model(user).Update("email", user.Email).Error)
	flowToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   model.AuthFlowPurposeAPIKeyReset,
		UserId:    user.Id,
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	call := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/reset-api-key", strings.NewReader(`{"email":"recover@example.com","token":"`+flowToken+`"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		ResetAPIKey(c)
		return recorder
	}
	first := call()
	assert.Equal(t, http.StatusOK, first.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			FullKey string `json:"full_key"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(first.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.NotEmpty(t, response.Data.FullKey)
	assert.NotEqual(t, "recover-old-key", response.Data.FullKey)
	var delivery model.APIKeyDelivery
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&delivery).Error)
	assert.Equal(t, model.APIKeyDeliveryStatusPending, delivery.Status)

	second := call()
	assert.Contains(t, second.Body.String(), `"success":false`)
	_, err = model.ValidateUserTokenForLogin("recover-old-key")
	assert.ErrorIs(t, err, model.ErrTokenInvalid)
}

func TestSendAPIKeyResetEmailDoesNotEnumerate(t *testing.T) {
	setupAPIKeyLoginTestDB(t)
	previousEmailDefaultTokenEnabled := common.EmailDefaultTokenEnabled
	common.EmailDefaultTokenEnabled = true
	t.Cleanup(func() { common.EmailDefaultTokenEnabled = previousEmailDefaultTokenEnabled })
	for _, email := range []string{"missing@example.com", "not-an-email"} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/reset_api_key", strings.NewReader(`{"email":"`+email+`"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		SendAPIKeyResetEmail(c)
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Contains(t, recorder.Body.String(), `"success":true`)
		assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
	}
}

func TestSendAPIKeyResetEmailUsesFrontendRoute(t *testing.T) {
	db := setupAPIKeyLoginTestDB(t)
	previousSinglePrimaryAPIKeyEnabled := common.SinglePrimaryAPIKeyEnabled
	previousEmailDefaultTokenEnabled := common.EmailDefaultTokenEnabled
	common.SinglePrimaryAPIKeyEnabled = true
	common.EmailDefaultTokenEnabled = true
	previousSMTPServer, previousSMTPAccount := common.SMTPServer, common.SMTPAccount
	common.SMTPServer, common.SMTPAccount = "", ""
	t.Cleanup(func() {
		common.SinglePrimaryAPIKeyEnabled = previousSinglePrimaryAPIKeyEnabled
		common.EmailDefaultTokenEnabled = previousEmailDefaultTokenEnabled
		common.SMTPServer, common.SMTPAccount = previousSMTPServer, previousSMTPAccount
	})
	user := createAPIKeyLoginUser(t, db, "route-test-key")
	user.Email = "route@example.com"
	require.NoError(t, db.Model(user).Update("email", user.Email).Error)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/reset_api_key", strings.NewReader(`{"email":"route@example.com"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	SendAPIKeyResetEmail(c)
	assert.Equal(t, http.StatusOK, recorder.Code)
	var flow model.AuthFlow
	require.NoError(t, db.Where("user_id = ? AND purpose = ?", user.Id, model.AuthFlowPurposeAPIKeyReset).First(&flow).Error)
	assert.NotNil(t, flow.ConsumedAt, "delivery failure must invalidate the flow")
}

func TestResetAPIKeyForPrivilegedFlowRotatesOnlyServerBoundToken(t *testing.T) {
	db := setupAPIKeyLoginTestDB(t)
	user := createAPIKeyLoginUser(t, db, "root-recovery-first-old")
	user.Email = "root-recovery@example.com"
	user.Role = common.RoleRootUser
	require.NoError(t, db.Model(user).Updates(map[string]any{"email": user.Email, "role": user.Role}).Error)
	var first model.Token
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&first).Error)
	second := &model.Token{UserId: user.Id, Name: "second", Key: "root-recovery-second-old", Status: common.TokenStatusEnabled, CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(), ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(second).Error)

	links, allowed, err := model.CreatePrivilegedAPIKeyResetFlows(user.Id, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.True(t, allowed)
	var target string
	for _, link := range links {
		if link.TokenId == second.Id {
			target = link.Token
		}
	}
	require.NotEmpty(t, target)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/reset-api-key", strings.NewReader(`{"email":"root-recovery@example.com","token":"`+target+`","token_id":`+strconv.Itoa(first.Id)+`}`))
	c.Request.Header.Set("Content-Type", "application/json")
	ResetAPIKey(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)

	var savedFirst, savedSecond model.Token
	require.NoError(t, db.First(&savedFirst, first.Id).Error)
	require.NoError(t, db.First(&savedSecond, second.Id).Error)
	assert.Equal(t, "root-recovery-first-old", savedFirst.Key)
	assert.NotEqual(t, "root-recovery-second-old", savedSecond.Key)
	var delivery model.APIKeyDelivery
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&delivery).Error)
	assert.Equal(t, second.Id, delivery.TokenId)
}
