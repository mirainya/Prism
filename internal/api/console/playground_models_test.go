package console

import (
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/service"
)

func TestPlaygroundChatModelListIncludesCatalogOperations(t *testing.T) {
	result := playgroundChatModelList([]service.AvailableModelCapability{{
		ID: "gpt-4.1", Operations: []service.AvailableModelOperation{
			{ID: "chat.completions", Path: "/v1/chat/completions"},
			{ID: "responses.create", Path: "/v1/responses"},
			{ID: "responses.create", Path: "/v1/responses"},
		},
	}})
	if result["object"] != "list" {
		t.Fatalf("object = %#v", result["object"])
	}
	items, ok := result["data"].([]gin.H)
	if !ok || len(items) != 1 {
		t.Fatalf("data = %#v", result["data"])
	}
	if !reflect.DeepEqual(items[0]["supported_operations"], []string{"chat.completions", "responses.create"}) {
		t.Fatalf("operations = %#v", items[0]["supported_operations"])
	}
	if !reflect.DeepEqual(items[0]["supported_endpoints"], []string{"/v1/chat/completions", "/v1/responses"}) {
		t.Fatalf("endpoints = %#v", items[0]["supported_endpoints"])
	}
}
