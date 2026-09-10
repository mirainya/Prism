package open

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterRoutesUsesUnifiedImagePathsAndExcludesLegacyCapabilityTasks(t *testing.T) {
	router := gin.New()
	RegisterRoutes(router.Group("/v1"))

	legacy := map[string]bool{
		"/v1/channels":                 true,
		"/v1/capabilities":             true,
		"/v1/capabilities/:capability": true,
		"/v1/tasks/:task_no":           true,
	}
	images := map[string]bool{"/v1/images/generations": false, "/v1/images/edits": false}
	for _, route := range router.Routes() {
		if legacy[route.Path] {
			t.Fatalf("legacy route is still registered: %s %s", route.Method, route.Path)
		}
		if _, ok := images[route.Path]; ok && route.Method == "POST" {
			images[route.Path] = true
		}
	}
	for path, registered := range images {
		if !registered {
			t.Fatalf("unified image route is not registered: POST %s", path)
		}
	}
}
