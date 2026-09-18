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

func TestRegisterRoutesIncludesV5ClassAPatchAliases(t *testing.T) {
	router := gin.New()
	RegisterRoutes(router.Group("/api/admin"))
	want := map[string]bool{
		http.MethodPatch + " /api/admin/unified-gateway/credentials/:id":      false,
		http.MethodPatch + " /api/admin/unified-gateway/credential-pools/:id": false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, exists := want[key]; exists {
			want[key] = true
		}
	}
	for route, registered := range want {
		if !registered {
			t.Errorf("v5 A-class alias not registered: %s", route)
		}
	}
}

func TestRegisterRoutesExcludesLegacyCatalogLifecycleAndDeploymentEndpoints(t *testing.T) {
	router := gin.New()
	RegisterRoutes(router.Group("/api/admin"))
	legacy := map[string]bool{
		http.MethodPost + " /api/admin/unified-gateway/catalog/:id/fork":                                     false,
		http.MethodPost + " /api/admin/unified-gateway/catalog/:id/publish":                                  false,
		http.MethodPost + " /api/admin/unified-gateway/catalog/:id/retire":                                   false,
		http.MethodPost + " /api/admin/unified-gateway/catalog/:id/activate":                                 false,
		http.MethodPost + " /api/admin/unified-gateway/releases/:id/rollback":                                false,
		http.MethodPost + " /api/admin/unified-gateway/deployments":                                          false,
		http.MethodGet + " /api/admin/unified-gateway/deployments":                                           false,
		http.MethodGet + " /api/admin/unified-gateway/deployments/runtime-identity":                          false,
		http.MethodPost + " /api/admin/unified-gateway/deployments/:id/members":                              false,
		http.MethodPost + " /api/admin/unified-gateway/deployments/:id/prove-current":                        false,
		http.MethodPost + " /api/admin/unified-gateway/deployments/:id/members/:member_id/catalog-readiness": false,
		http.MethodPost + " /api/admin/unified-gateway/deployments/:id/members/:member_id/crypto-readiness":  false,
		http.MethodPost + " /api/admin/unified-gateway/deployments/:id/activate":                             false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, exists := legacy[key]; exists {
			legacy[key] = true
		}
	}
	for route, registered := range legacy {
		if registered {
			t.Errorf("legacy management endpoint is still registered: %s", route)
		}
	}
}

func TestRegisterRoutesIncludesDirectConfigurationEndpoints(t *testing.T) {
	router := gin.New()
	RegisterRoutes(router.Group("/api/admin"))
	want := map[string]bool{
		http.MethodPatch + " /api/admin/unified-gateway/pools/:id":                              false,
		http.MethodPatch + " /api/admin/unified-gateway/credential-pools/:id":                   false,
		http.MethodPatch + " /api/admin/unified-gateway/credentials/:id":                        false,
		http.MethodPatch + " /api/admin/unified-gateway/offerings/:offering_id/runtime-state":   false,
		http.MethodPatch + " /api/admin/unified-gateway/model-meta/:model_name":                 false,
		http.MethodPost + " /api/admin/unified-gateway/catalog-changes/sell-rate":               false,
		http.MethodPost + " /api/admin/unified-gateway/catalog-changes/cost-rate":               false,
		http.MethodPost + " /api/admin/unified-gateway/catalog-changes/route-weight":            false,
		http.MethodPost + " /api/admin/unified-gateway/catalog-changes/sku-variant":             false,
		http.MethodPost + " /api/admin/unified-gateway/catalog-changes/sku-downstream-paths":    false,
		http.MethodPost + " /api/admin/unified-gateway/catalog-changes/product":                 false,
		http.MethodPost + " /api/admin/unified-gateway/catalog-changes/product-create":          false,
		http.MethodPost + " /api/admin/unified-gateway/catalog-changes/model-onboard":           false,
		http.MethodPost + " /api/admin/unified-gateway/catalog-changes/public-model-identities": false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, exists := want[key]; exists {
			want[key] = true
		}
	}
	for route, registered := range want {
		if !registered {
			t.Errorf("direct configuration endpoint not registered: %s", route)
		}
	}
}

func TestRegisterRoutesIncludesCatalogModelEntries(t *testing.T) {
	router := gin.New()
	RegisterRoutes(router.Group("/api/admin"))
	for _, route := range router.Routes() {
		if route.Method == http.MethodGet && route.Path == "/api/admin/unified-gateway/catalog/:id/model-entries" {
			return
		}
	}
	t.Fatal("catalog model entries route not registered")
}
