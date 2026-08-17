package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSinglePrimaryKeyNeverRevealsViaReadEndpoints(t *testing.T) {
	db := setupAPIKeyLoginTestDB(t)
	previous := common.SinglePrimaryAPIKeyEnabled
	common.SinglePrimaryAPIKeyEnabled = true
	t.Cleanup(func() { common.SinglePrimaryAPIKeyEnabled = previous })
	user := createAPIKeyLoginUser(t, db, "read-once-key")
	var token model.Token
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&token).Error)

	for _, tc := range []struct {
		name string
		call func(*gin.Context)
	}{
		{name: "single", call: GetTokenKey},
		{name: "batch", call: GetTokenKeysBatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("id", user.Id)
			c.Set("role", common.RoleCommonUser)
			if tc.name == "single" {
				c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
				c.Request = httptest.NewRequest(http.MethodPost, "/api/token/"+strconv.Itoa(token.Id)+"/key", nil)
			} else {
				c.Request = httptest.NewRequest(http.MethodPost, "/api/token/batch/keys", strings.NewReader(`{"ids":[`+strconv.Itoa(token.Id)+`]}`))
			}
			tc.call(c)
			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.NotContains(t, recorder.Body.String(), "read-once-key")
		})
	}
}

func TestRotatePrimaryAPIKeyRequiresSecurityProof(t *testing.T) {
	previous := common.SinglePrimaryAPIKeyEnabled
	common.SinglePrimaryAPIKeyEnabled = true
	t.Cleanup(func() { common.SinglePrimaryAPIKeyEnabled = previous })
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", 1)
	c.Set("role", common.RoleCommonUser)
	RotatePrimaryAPIKey(c)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "SECURITY_PROOF_INVALID")
}

func TestAPIKeyDeliveryRetrySendsTheSameRotatedKey(t *testing.T) {
	db := setupAPIKeyLoginTestDB(t)
	user := createAPIKeyLoginUser(t, db, "delivery-old-key")
	user.Email = "delivery@example.com"
	require.NoError(t, db.Model(user).Update("email", user.Email).Error)
	var original model.Token
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&original).Error)
	rotated, delivery, err := model.RotateAPIKeyForDelivery(user.Id, original.Id)
	require.NoError(t, err)

	oldSender := sendAPIKeyDeliveryEmail
	t.Cleanup(func() { sendAPIKeyDeliveryEmail = oldSender })
	var sentBodies []string
	sendAPIKeyDeliveryEmail = func(_ string, _ string, content string) error {
		sentBodies = append(sentBodies, content)
		return errors.New("SMTP unavailable")
	}
	assert.Equal(t, "pending", sendAPIKeyDelivery(delivery, rotated))

	retryDelivery, retryToken, err := model.GetPendingAPIKeyDeliveryForUser(user.Id, delivery.Id)
	require.NoError(t, err)
	assert.Equal(t, rotated.Key, retryToken.Key)
	// A valid five-minute security proof must not bypass the delivery cooldown.
	assert.Equal(t, "pending", sendAPIKeyDelivery(retryDelivery, retryToken))
	assert.Len(t, sentBodies, 1)
	require.NoError(t, db.Model(&model.APIKeyDelivery{}).Where("id = ?", delivery.Id).Update("next_attempt_at", common.GetTimestamp()-1).Error)
	retryDelivery, retryToken, err = model.GetPendingAPIKeyDeliveryForUser(user.Id, delivery.Id)
	require.NoError(t, err)
	sendAPIKeyDeliveryEmail = func(_ string, _ string, content string) error {
		sentBodies = append(sentBodies, content)
		return nil
	}
	assert.Equal(t, "sent", sendAPIKeyDelivery(retryDelivery, retryToken))
	require.Len(t, sentBodies, 2)
	assert.Contains(t, sentBodies[0], primaryAPIKeyForUser(rotated.Key))
	assert.Contains(t, sentBodies[1], primaryAPIKeyForUser(rotated.Key))
	assert.NotContains(t, sentBodies[0], "delivery-old-key")

	var saved model.APIKeyDelivery
	require.NoError(t, db.First(&saved, delivery.Id).Error)
	assert.Equal(t, model.APIKeyDeliveryStatusDelivered, saved.Status)
}
