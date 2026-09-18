package open

import (
	"slices"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/service"
)

func TestPublicChatModelPublishesOnlyCatalogOperations(t *testing.T) {
	item := publicChatModel(service.AvailableModelCapability{
		ID:          "gpt-4.1",
		ModelCode:   "gpt_4_1",
		Name:        "GPT-4.1",
		Description: "General-purpose model",
		Type:        "chat",
		Types:       []string{"chat"},
		Visibility:  "public",
		Availability: &service.PricingAvailability{
			SuccessRate: "99.5", Source: "upstream", WindowMinutes: 120,
			ObservedAt: time.Unix(1789380174, 0).UTC(),
		},
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
	if item["description"] != "General-purpose model" || item["type"] != "chat" {
		t.Fatalf("metadata = %#v", item)
	}
	if item["model_code"] != "gpt-4.1" {
		t.Fatalf("public model_code exposed internal identity: %#v", item["model_code"])
	}
	if item["availability"] == nil {
		t.Fatal("availability was not published")
	}
}

func TestPublicChatModelOmitsUnknownAvailability(t *testing.T) {
	item := publicChatModel(service.AvailableModelCapability{ID: "image-model", Type: "image"})
	if _, exists := item["availability"]; exists {
		t.Fatal("unknown availability must be omitted rather than reported as zero")
	}
}
