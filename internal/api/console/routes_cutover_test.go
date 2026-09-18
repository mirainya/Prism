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

func TestRegisterRoutesIncludesTokenFileStorageSettings(t *testing.T) {
	router := gin.New()
	RegisterRoutes(router.Group("/api"))

	want := map[string]bool{
		"PUT /api/tokens/:id/file-storage":    false,
		"DELETE /api/tokens/:id/file-storage": false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Fatalf("route is not registered: %s", route)
		}
	}
}
