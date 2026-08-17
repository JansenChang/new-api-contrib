package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestEnterpriseFeatureEnabledClosedGateIsForbiddenAndSkipsAudit(t *testing.T) {
	previous := common.EnterpriseBillingEnabled
	common.EnterpriseBillingEnabled = false
	t.Cleanup(func() { common.EnterpriseBillingEnabled = previous })

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/api/enterprise/self", nil)
	c.Set(string(constant.ContextKeyAuditLogged), false)
	EnterpriseFeatureEnabled()(c)

	assert.True(t, c.IsAborted())
	assert.Equal(t, 403, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "ENTERPRISE_FEATURE_DISABLED")
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyAuditLogged))
}
