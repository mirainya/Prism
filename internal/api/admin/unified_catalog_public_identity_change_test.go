package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func publicModelIdentityChangeRouter(actor uint) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if actor != 0 {
			c.Set("user_id", actor)
		}
	})
	router.POST("/catalog-changes/public-model-identities", ChangeUnifiedPublicModelIdentities)
	return router
}

const publicModelIdentityChangeBody = `{"expected_active_release_id":7,"expected_config_version":9,"reason_code":"remove_source_brand","renames":[{"from_api_name":"aicost-model-a","to_api_name":"model-a","display_name":"Model A"}]}`

func TestPublicModelIdentityChangeHandlerRejectsMalformedBatchesBeforeStore(t *testing.T) {
	for name, body := range map[string]string{
		"missing active release": `{"expected_config_version":9,"renames":[{"from_api_name":"old","to_api_name":"new","display_name":"New"}]}`,
		"missing config version": `{"expected_active_release_id":7,"renames":[{"from_api_name":"old","to_api_name":"new","display_name":"New"}]}`,
		"empty batch":            `{"expected_active_release_id":7,"expected_config_version":9,"renames":[]}`,
		"duplicate source":       `{"expected_active_release_id":7,"expected_config_version":9,"renames":[{"from_api_name":"old","to_api_name":"new-a","display_name":"A"},{"from_api_name":"old","to_api_name":"new-b","display_name":"B"}]}`,
		"duplicate target":       `{"expected_active_release_id":7,"expected_config_version":9,"renames":[{"from_api_name":"old-a","to_api_name":"new","display_name":"A"},{"from_api_name":"old-b","to_api_name":"new","display_name":"B"}]}`,
		"invalid display":        `{"expected_active_release_id":7,"expected_config_version":9,"renames":[{"from_api_name":"old","to_api_name":"new","display_name":"Bad\nName"}]}`,
		"unknown field":          `{"expected_active_release_id":7,"expected_config_version":9,"renames":[{"from_api_name":"old","to_api_name":"new","display_name":"New","delete_old_alias":true}]}`,
		"caller digest":          `{"expected_active_release_id":7,"expected_config_version":9,"semantic_digest":"deadbeef","renames":[{"from_api_name":"old","to_api_name":"new","display_name":"New"}]}`,
		"trailing json":          publicModelIdentityChangeBody + ` {}`,
		"null":                   `null`,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest("POST", "/catalog-changes/public-model-identities", strings.NewReader(body))
			publicModelIdentityChangeRouter(7).ServeHTTP(response, request)
			if response.Code != 400 {
				t.Fatalf("code=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

func TestPublicModelIdentityChangeHandlerRequiresAdminAfterValidation(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/catalog-changes/public-model-identities", strings.NewReader(publicModelIdentityChangeBody))
	publicModelIdentityChangeRouter(0).ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}
