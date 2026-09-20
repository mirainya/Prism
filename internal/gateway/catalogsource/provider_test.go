package catalogsource

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/adapter"
)

type testCatalogProvider struct {
	contracts []ProviderContract
}

func mustCatalogURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func (p testCatalogProvider) Contracts() []ProviderContract { return p.contracts }

func (testCatalogProvider) Discover(context.Context, DiscoveryProviderRequest) (DiscoveryResult, error) {
	return DiscoveryResult{Response: []byte(`{"ok":true}`)}, nil
}

type testAvailabilityProvider struct{ testCatalogProvider }

func (testAvailabilityProvider) RefreshAvailability(context.Context, AvailabilityProviderRequest) ([]AvailabilityObservation, error) {
	return nil, nil
}

func TestProviderRegistryMapsContractsAndAvailability(t *testing.T) {
	provider := testAvailabilityProvider{testCatalogProvider{contracts: []ProviderContract{{
		Code: "example_models_v1", Name: "Example models", MaxRequests: 2, Availability: true, AvailabilityMaxRequests: 3,
	}}}}
	registry, err := NewProviderRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	if got, maxRequests, ok := registry.Provider("EXAMPLE_MODELS_V1"); !ok || got == nil || maxRequests != 2 {
		t.Fatalf("provider lookup failed: %#v %d %v", got, maxRequests, ok)
	}
	bindings := registry.AvailabilityBindings()
	if len(bindings) != 1 || bindings[0].Contract != "example_models_v1" || bindings[0].MaxRequests != 3 || bindings[0].Provider == nil {
		t.Fatalf("availability bindings=%#v", bindings)
	}
	contracts := registry.Contracts()
	if len(contracts) != 1 || contracts[0].Code != "example_models_v1" || contracts[0].Name != "Example models" {
		t.Fatalf("contracts=%#v", contracts)
	}
}

func TestProviderRegistryRejectsDuplicateContractsAndUnknownAvailability(t *testing.T) {
	duplicate := testCatalogProvider{contracts: []ProviderContract{{Code: "same_v1", Name: "Same", MaxRequests: 1}}}
	if _, err := NewProviderRegistry(duplicate, duplicate); err != ErrProviderNotRegistered {
		t.Fatalf("duplicate error=%v", err)
	}
	invalidBudget := testCatalogProvider{contracts: []ProviderContract{{Code: "known_v1", Name: "Known", MaxRequests: 0}}}
	if _, err := NewProviderRegistry(invalidBudget); err != ErrProviderNotRegistered {
		t.Fatalf("invalid request budget error=%v", err)
	}
	invalidContract := testCatalogProvider{contracts: []ProviderContract{{Code: "invalid contract", Name: "Invalid", MaxRequests: 1}}}
	if _, err := NewProviderRegistry(invalidContract); err != ErrProviderNotRegistered {
		t.Fatalf("invalid contract error=%v", err)
	}
	emptyName := testCatalogProvider{contracts: []ProviderContract{{Code: "empty_name_v1", Name: " ", MaxRequests: 1}}}
	if _, err := NewProviderRegistry(emptyName); err != ErrProviderNotRegistered {
		t.Fatalf("empty name error=%v", err)
	}
	invalidAvailabilityBudget := testAvailabilityProvider{testCatalogProvider{contracts: []ProviderContract{{
		Code: "availability_v1", Name: "Availability", MaxRequests: 1, Availability: true,
	}}}}
	if _, err := NewProviderRegistry(invalidAvailabilityBudget); err != ErrProviderNotRegistered {
		t.Fatalf("invalid availability budget error=%v", err)
	}
	unexpectedAvailabilityBudget := testCatalogProvider{contracts: []ProviderContract{{
		Code: "models_v1", Name: "Models", MaxRequests: 1, AvailabilityMaxRequests: -1,
	}}}
	if _, err := NewProviderRegistry(unexpectedAvailabilityBudget); err != ErrProviderNotRegistered {
		t.Fatalf("unexpected availability budget error=%v", err)
	}
	missingCapability := testCatalogProvider{contracts: []ProviderContract{{
		Code: "availability_v1", Name: "Availability", MaxRequests: 1, Availability: true, AvailabilityMaxRequests: 1,
	}}}
	if _, err := NewProviderRegistry(missingCapability); err != ErrProviderNotRegistered {
		t.Fatalf("missing availability capability error=%v", err)
	}
}

func TestAICostPricingDiscoveryUsesLoginAndPricingRequests(t *testing.T) {
	provider := AICostProvider{}
	calls := 0
	result, err := provider.Discover(context.Background(), DiscoveryProviderRequest{
		Contract: AICostPricingV1, ExternalGroup: "group-a",
		Secret: []byte(`{"username":"key","password":"secret"}`), BaseURL: mustCatalogURL(t, "https://aicost.example"),
		Exchange: func(_ context.Context, method, target string, body []byte, header http.Header) ([]byte, error) {
			calls++
			switch calls {
			case 1:
				if method != http.MethodPost || !strings.HasSuffix(target, "/api/user/login") || !strings.Contains(string(body), `"username":"key"`) {
					t.Fatalf("login request=%s %s %s", method, target, body)
				}
				return []byte(`{"success":true,"data":{"id":42}}`), nil
			case 2:
				if method != http.MethodGet || !strings.HasSuffix(target, "/api/pricing") || header.Get("New-Api-User") != "42" {
					t.Fatalf("pricing request=%s %s header=%v", method, target, header)
				}
				return []byte(`{"success":true,"data":[{"model_name":"model-a","model_price":0.2,"enable_groups":["group-a"],"supported_endpoint_types":["openai"]}]}`), nil
			default:
				t.Fatal("unexpected provider request")
				return nil, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(result.Models) != 1 || !result.Models[0].SelectedGroupEnabled || !result.Models[0].PriceCandidate {
		t.Fatalf("calls=%d result=%#v", calls, result)
	}
}

func TestAICostAvailabilityRefreshesAllCategories(t *testing.T) {
	provider := AICostProvider{}
	calls := 0
	rows, err := provider.RefreshAvailability(context.Background(), AvailabilityProviderRequest{
		Contract: AICostPricingV1, Secret: []byte(`{"username":"key","password":"secret"}`),
		BaseURL: mustCatalogURL(t, "https://aicost.example"),
		Exchange: func(_ context.Context, method, target string, _ []byte, _ http.Header) ([]byte, error) {
			calls++
			if calls == 1 {
				if method != http.MethodPost {
					t.Fatalf("login method=%s", method)
				}
				return []byte(`{"success":true,"data":{"id":42}}`), nil
			}
			parsed, parseErr := url.Parse(target)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			category := parsed.Query().Get("category")
			return []byte(fmt.Sprintf(`{"success":true,"data":{"category":%q,"window_minutes":120,"updated_at":1789380174,"rows":[{"model":%q,"success_rate":99,"average_completion_seconds":1,"buckets":[{"has_data":true}]}]}}`, category, category+"-model")), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 || len(rows) != 3 {
		t.Fatalf("calls=%d rows=%#v", calls, rows)
	}
}

func TestProviderRegistrySupportsDiscoveryOnlyProviderAndKeepsSnapshot(t *testing.T) {
	provider := testCatalogProvider{contracts: []ProviderContract{{Code: " EXAMPLE_MODELS_V1 ", Name: " Example models ", MaxRequests: 1}}}
	registry, err := NewProviderRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	provider.contracts[0].Name = "changed"
	contracts := registry.Contracts()
	if len(contracts) != 1 || contracts[0].Code != "example_models_v1" || contracts[0].Name != "Example models" {
		t.Fatalf("contracts=%#v", contracts)
	}
	if len(registry.AvailabilityBindings()) != 0 {
		t.Fatal("discovery-only provider unexpectedly registered availability")
	}
}

func TestDefaultProviderContractsHaveCatalogAdapters(t *testing.T) {
	registry, err := DefaultProviderRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range registry.Contracts() {
		descriptor, ok := adapter.DescriptorFor(contract.Code, 1)
		if !ok || descriptor.Protocol != "catalog_discovery" {
			t.Fatalf("contract %q has no catalog adapter", contract.Code)
		}
	}
	for _, descriptor := range adapter.Descriptors() {
		if descriptor.Protocol != "catalog_discovery" {
			continue
		}
		if _, _, ok := registry.Provider(descriptor.Code); !ok {
			t.Fatalf("catalog adapter %q has no provider", descriptor.Code)
		}
	}
}

func TestAICostProviderDiscoveryKeepsProtocolOutOfWorker(t *testing.T) {
	provider := AICostProvider{}
	var calls []string
	result, err := provider.Discover(context.Background(), DiscoveryProviderRequest{
		Contract: AICostModelsV1, ExternalGroup: "group", Secret: []byte("token"),
		BaseURL: mustCatalogURL(t, "https://aicost.example"),
		Exchange: func(_ context.Context, method, target string, _ []byte, header http.Header) ([]byte, error) {
			calls = append(calls, method+" "+target)
			if header.Get("Authorization") != "Bearer token" {
				t.Fatalf("authorization=%q", header.Get("Authorization"))
			}
			return []byte(`{"object":"list","data":[{"id":"model-a"}]}`), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || result.Models[0].Code != "model-a" || !result.Models[0].SelectedGroupEnabled || len(result.Response) == 0 {
		t.Fatalf("calls=%v result=%#v", calls, result)
	}
}

func TestAICostAvailabilityRejectsAnotherContract(t *testing.T) {
	provider := AICostProvider{}
	_, err := provider.RefreshAvailability(context.Background(), AvailabilityProviderRequest{
		Contract: "other_pricing_v1", Secret: []byte(`{"username":"key","password":"secret"}`),
		BaseURL: mustCatalogURL(t, "https://aicost.example"),
		Exchange: func(context.Context, string, string, []byte, http.Header) ([]byte, error) {
			t.Fatal("unexpected provider request")
			return nil, nil
		},
	})
	if err != ErrProviderInvalidRequest {
		t.Fatalf("error=%v", err)
	}
}
