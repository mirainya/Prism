package admin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterRoutesExcludesLegacyGatewayManagement(t *testing.T) {
	router := gin.New()
	RegisterRoutes(router.Group("/api/admin"))

	legacyPrefixes := []string{
		"/api/admin/channels",
		"/api/admin/channel-accounts",
		"/api/admin/capabilities",
		"/api/admin/channel-capabilities",
		"/api/admin/endpoints",
		"/api/admin/request-logs",
		"/api/admin/gw",
		"/api/admin/video/channels",
		"/api/admin/video/keys",
	}
	for _, route := range router.Routes() {
		for _, prefix := range legacyPrefixes {
			if route.Path == prefix || strings.HasPrefix(route.Path, prefix+"/") {
				t.Fatalf("legacy management route is still registered: %s %s", route.Method, route.Path)
			}
		}
	}
}

func TestRegisterRoutesIncludesCatalogDiscoveryControlPlane(t *testing.T) {
	router := gin.New()
	RegisterRoutes(router.Group("/api/admin"))
	want := map[string]bool{
		http.MethodGet + " /api/admin/unified-gateway/catalog-sources":                                  false,
		http.MethodPost + " /api/admin/unified-gateway/catalog-sources":                                 false,
		http.MethodPost + " /api/admin/unified-gateway/catalog/:id/catalog-sources/:source_id/discover": false,
		http.MethodGet + " /api/admin/unified-gateway/catalog/:id/discoveries":                          false,
		http.MethodPost + " /api/admin/unified-gateway/catalog-discoveries/:snapshot_id/review":         false,
		http.MethodGet + " /api/admin/unified-gateway/catalog/:id/price-candidates":                     false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, exists := want[key]; exists {
			want[key] = true
		}
	}
	for route, registered := range want {
		if !registered {
			t.Errorf("catalog discovery route not registered: %s", route)
		}
	}
}
