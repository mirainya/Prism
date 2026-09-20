package catalogsource

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalidSnapshot        = errors.New("catalog source: invalid provider snapshot")
	ErrProviderInvalidRequest = errors.New("catalog source: invalid provider request")
	ErrProviderNotRegistered  = errors.New("catalog source: provider is not registered")
	ErrProviderRequest        = errors.New("catalog source: provider request failed")
	ErrProviderRequestLimit   = errors.New("catalog source: provider request limit exceeded")
	ErrProviderResponse       = errors.New("catalog source: provider response invalid")
)

const maxProviderRequests = 16

var providerContractPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

// ProviderExchange is the transport boundary exposed to a catalog provider.
// Discovery requests go through the worker's logged exchange; availability
// requests use the cache refresh exchange. Provider implementations therefore
// describe only their protocol and do not own leases, persistence, or logging.
type ProviderExchange func(ctx context.Context, method, target string, body []byte, header http.Header) ([]byte, error)

type DiscoveryProviderRequest struct {
	Contract      string
	ExternalGroup string
	Secret        []byte
	BaseURL       *url.URL
	Exchange      ProviderExchange
}

type AvailabilityProviderRequest struct {
	Contract string
	Secret   []byte
	BaseURL  *url.URL
	Exchange ProviderExchange
}

type AvailabilityObservation struct {
	ModelCode                string
	Category                 string
	SuccessRate              string
	AverageCompletionSeconds string
	WindowMinutes            uint32
	ObservedAt               time.Time
}

type DiscoveredModel struct {
	Code                 string
	Description          string
	Tags                 string
	VendorID             *uint64
	ProviderQuotaType    *uint8
	ModelPrice           *string
	ModelRatio           *string
	CompletionRatio      *string
	OwnerBy              string
	PricingVersion       string
	Groups               []string
	EndpointTypes        []string
	SelectedGroupEnabled bool
	PriceCandidate       bool
}

type DiscoveryResult struct {
	Models   []DiscoveredModel
	Response []byte
}

// ProviderContract declares one protocol contract and its bounded discovery
// request budget. Availability marks contracts that also supply observations.
type ProviderContract struct {
	Code                    string
	Name                    string
	MaxRequests             int
	Availability            bool
	AvailabilityMaxRequests int
}

// CatalogSourceProvider contains the required catalog discovery capability.
type CatalogSourceProvider interface {
	Contracts() []ProviderContract
	Discover(context.Context, DiscoveryProviderRequest) (DiscoveryResult, error)
}

// CatalogAvailabilityProvider is optional. Providers implement it only when
// at least one declared contract supplies volatile upstream observations.
type CatalogAvailabilityProvider interface {
	CatalogSourceProvider
	RefreshAvailability(context.Context, AvailabilityProviderRequest) ([]AvailabilityObservation, error)
}

// ProviderAvailabilityBinding connects a volatile observation contract to the
// provider that knows how to fetch it.
type ProviderAvailabilityBinding struct {
	Contract    string
	MaxRequests int
	Provider    CatalogAvailabilityProvider
}

// ProviderRegistry resolves source contract codes without putting provider
// names into the worker's execution path.
type ProviderRegistry struct {
	byContract   map[string]registeredProvider
	availability []ProviderAvailabilityBinding
}

type registeredProvider struct {
	provider CatalogSourceProvider
	contract ProviderContract
}

func NewProviderRegistry(providers ...CatalogSourceProvider) (ProviderRegistry, error) {
	registry := ProviderRegistry{byContract: make(map[string]registeredProvider)}
	for _, provider := range providers {
		if provider == nil {
			return ProviderRegistry{}, ErrProviderNotRegistered
		}
		contracts := provider.Contracts()
		if len(contracts) == 0 {
			return ProviderRegistry{}, ErrProviderNotRegistered
		}
		for _, descriptor := range contracts {
			contract := strings.ToLower(strings.TrimSpace(descriptor.Code))
			name := strings.TrimSpace(descriptor.Name)
			if !providerContractPattern.MatchString(contract) || name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 128 ||
				strings.ContainsAny(name, "\x00\r\n\t") || descriptor.MaxRequests < 1 || descriptor.MaxRequests > maxProviderRequests {
				return ProviderRegistry{}, ErrProviderNotRegistered
			}
			if descriptor.Availability {
				if descriptor.AvailabilityMaxRequests < 1 || descriptor.AvailabilityMaxRequests > maxProviderRequests {
					return ProviderRegistry{}, ErrProviderNotRegistered
				}
			} else if descriptor.AvailabilityMaxRequests != 0 {
				return ProviderRegistry{}, ErrProviderNotRegistered
			}
			if _, exists := registry.byContract[contract]; exists {
				return ProviderRegistry{}, ErrProviderNotRegistered
			}
			descriptor.Code = contract
			descriptor.Name = name
			registry.byContract[contract] = registeredProvider{provider: provider, contract: descriptor}
			if descriptor.Availability {
				availabilityProvider, ok := provider.(CatalogAvailabilityProvider)
				if !ok {
					return ProviderRegistry{}, ErrProviderNotRegistered
				}
				registry.availability = append(registry.availability, ProviderAvailabilityBinding{
					Contract: contract, MaxRequests: descriptor.AvailabilityMaxRequests, Provider: availabilityProvider,
				})
			}
		}
	}
	return registry, nil
}

func DefaultProviderRegistry() (ProviderRegistry, error) {
	return NewProviderRegistry(NewAICostProvider())
}

func (r ProviderRegistry) Provider(contract string) (CatalogSourceProvider, int, bool) {
	entry, ok := r.byContract[strings.ToLower(strings.TrimSpace(contract))]
	return entry.provider, entry.contract.MaxRequests, ok
}

func (r ProviderRegistry) AvailabilityBindings() []ProviderAvailabilityBinding {
	return append([]ProviderAvailabilityBinding(nil), r.availability...)
}

func (r ProviderRegistry) Contracts() []ProviderContract {
	contracts := make([]ProviderContract, 0, len(r.byContract))
	for _, entry := range r.byContract {
		contracts = append(contracts, entry.contract)
	}
	sort.Slice(contracts, func(i, j int) bool { return contracts[i].Code < contracts[j].Code })
	return contracts
}
