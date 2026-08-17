package router

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestEnterpriseManagementRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	routes := map[string]bool{}
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"POST /api/user/:id/enterprise-admin",
		"GET /api/enterprise/self",
		"GET /api/enterprise/members",
		"POST /api/enterprise/members/:id/allocations",
		"POST /api/enterprise/members/:id/reclaims",
		"POST /api/enterprise/members/:id/pause",
		"POST /api/enterprise/members/:id/resume",
		"POST /api/enterprise/members/:id/remove",
	} {
		assert.True(t, routes[route], "missing route %s", route)
	}
}
