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
	images := map[string]bool{
		"POST /v1/images/generations":       false,
		"POST /v1/images/edits":             false,
		"POST /v1/images/generations/async": false,
		"POST /v1/images/edits/async":       false,
		"GET /v1/images/tasks/:id":          false,
	}
	for _, route := range router.Routes() {
		if legacy[route.Path] {
			t.Fatalf("legacy route is still registered: %s %s", route.Method, route.Path)
		}
		key := route.Method + " " + route.Path
		if _, ok := images[key]; ok {
			images[key] = true
		}
	}
	for route, registered := range images {
		if !registered {
			t.Fatalf("unified image route is not registered: %s", route)
		}
	}
}
