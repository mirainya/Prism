package console

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterRoutesExcludesLegacyPlaygroundCapabilityAndTaskPaths(t *testing.T) {
	router := gin.New()
	RegisterRoutes(router.Group("/api"))

	for _, route := range router.Routes() {
		if strings.Contains(route.Path, "/playground/:token_id/capabilities") ||
			strings.Contains(route.Path, "/playground/:token_id/tasks") ||
			route.Path == "/api/capability-channels" {
			t.Fatalf("legacy route is still registered: %s %s", route.Method, route.Path)
		}
	}
}
