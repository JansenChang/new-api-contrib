package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

// EnterpriseFeatureEnabled is the fail-closed release gate for enterprise
// management routes. Authentication and cache policy are deliberately kept
// as separate route middleware so every route has the same ordering.
func EnterpriseFeatureEnabled() gin.HandlerFunc {
	return func(c *gin.Context) {
		if common.EnterpriseBillingEnabled {
			c.Next()
			return
		}
		// AdminAuth installs the generic write-audit fallback before invoking the
		// next handler. A closed release gate is not an enterprise operation and
		// must not leave an audit row behind.
		common.SetContextKey(c, constant.ContextKeyAuditLogged, true)
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"success": false,
			"code":    "ENTERPRISE_FEATURE_DISABLED",
			"message": "enterprise feature is disabled",
		})
	}
}
