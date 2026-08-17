package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// PersonalAssetWriteAllowed blocks only new personal-asset mutations. Payment
// callbacks deliberately do not use it: their immutable order snapshot is the
// authority for existing orders.
func PersonalAssetWriteAllowed() gin.HandlerFunc {
	return func(c *gin.Context) {
		frozen, err := model.PersonalAssetsFrozen(c.GetInt("id"))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "企业关系状态读取失败",
			})
			return
		}
		if frozen {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"code":    "ENTERPRISE_PERSONAL_ASSETS_FROZEN",
				"message": "当前企业关系期间不能使用个人资产",
			})
			return
		}
		c.Next()
	}
}
