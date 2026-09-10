package open

import (
	"slices"
	"testing"

	"github.com/mirainya/Prism/internal/service"
)

func TestPublicChatModelPublishesOnlyCatalogOperations(t *testing.T) {
	item := publicChatModel(service.AvailableModelCapability{
		ID:         "gpt-4.1",
		ModelCode:  "gpt_4_1",
		Name:       "GPT-4.1",
		Visibility: "public",
		Operations: []service.AvailableModelOperation{
			{ID: "chat.completions", Path: "/v1/chat/completions"},
			{ID: "responses.create", Path: "/v1/responses"},
			{ID: "responses.create", Path: "/v1/responses"},
		},
	})

	endpoints, ok := item["supported_endpoints"].([]string)
	if !ok || !slices.Equal(endpoints, []string{"/v1/chat/completions", "/v1/responses"}) {
		t.Fatalf("supported_endpoints = %#v", item["supported_endpoints"])
	}
	operations, ok := item["supported_operations"].([]string)
	if !ok || !slices.Equal(operations, []string{"chat.completions", "responses.create"}) {
		t.Fatalf("supported_operations = %#v", item["supported_operations"])
	}
	if slices.Contains(endpoints, "/v1/messages") {
		t.Fatal("an undeclared endpoint was published")
	}
}
