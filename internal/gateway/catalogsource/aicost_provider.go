package catalogsource

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// AICostProvider contains only AICost's wire protocol. The catalog worker does
// not need to know how AICost authenticates, which endpoints it exposes, or how
// its availability categories are combined.
type AICostProvider struct{}

func NewAICostProvider() AICostProvider {
	return AICostProvider{}
}

func (AICostProvider) Contracts() []ProviderContract {
	return []ProviderContract{
		{Code: AICostModelsV1, Name: "AICost 模型清单", MaxRequests: 1},
		{Code: AICostPricingV1, Name: "AICost 价格清单", MaxRequests: 2, Availability: true, AvailabilityMaxRequests: 4},
	}
}

func (AICostProvider) Discover(ctx context.Context, request DiscoveryProviderRequest) (DiscoveryResult, error) {
	if request.Exchange == nil || request.BaseURL == nil || len(request.Secret) == 0 {
		return DiscoveryResult{}, ErrProviderInvalidRequest
	}
	var response []byte
	var err error
	contract := strings.ToLower(strings.TrimSpace(request.Contract))
	switch contract {
	case AICostModelsV1:
		header := make(http.Header)
		header.Set("Authorization", "Bearer "+string(request.Secret))
		response, err = request.Exchange(ctx, http.MethodGet, resolveCatalogPath(request.BaseURL, "/v1/models"), nil, header)
	case AICostPricingV1:
		account, parseErr := ParseAICostAccountSecret(request.Secret)
		if parseErr != nil {
			return DiscoveryResult{}, parseErr
		}
		body, bodyErr := account.LoginBody()
		if bodyErr != nil {
			return DiscoveryResult{}, bodyErr
		}
		loginHeader := make(http.Header)
		loginHeader.Set("Content-Type", "application/json")
		loginResponse, requestErr := request.Exchange(ctx, http.MethodPost, resolveCatalogPath(request.BaseURL, "/api/user/login"), body, loginHeader)
		clear(body)
		if requestErr != nil {
			return DiscoveryResult{}, requestErr
		}
		login, parseErr := ParseAICostLogin(loginResponse)
		clear(loginResponse)
		if parseErr != nil {
			return DiscoveryResult{}, parseErr
		}
		pricingHeader := make(http.Header)
		pricingHeader.Set("New-Api-User", strconv.FormatUint(login.UserID, 10))
		response, err = request.Exchange(ctx, http.MethodGet, resolveCatalogPath(request.BaseURL, "/api/pricing"), nil, pricingHeader)
	default:
		return DiscoveryResult{}, ErrProviderInvalidRequest
	}
	if err != nil {
		return DiscoveryResult{}, err
	}
	if len(response) == 0 {
		return DiscoveryResult{}, ErrProviderResponse
	}
	var models []DiscoveredModel
	switch contract {
	case AICostModelsV1:
		models, err = ParseAICostModels(response, request.ExternalGroup)
	case AICostPricingV1:
		models, err = ParseAICostPricing(response)
	}
	if err != nil {
		clear(response)
		return DiscoveryResult{}, err
	}
	for index := range models {
		models[index].SelectedGroupEnabled = contract == AICostModelsV1 || contains(models[index].Groups, request.ExternalGroup)
		models[index].PriceCandidate = contract == AICostPricingV1 && models[index].SelectedGroupEnabled && models[index].ModelPrice != nil
	}
	return DiscoveryResult{Models: models, Response: response}, nil
}

func (AICostProvider) RefreshAvailability(ctx context.Context, request AvailabilityProviderRequest) ([]AvailabilityObservation, error) {
	if !strings.EqualFold(strings.TrimSpace(request.Contract), AICostPricingV1) ||
		request.Exchange == nil || request.BaseURL == nil || len(request.Secret) == 0 {
		return nil, ErrProviderInvalidRequest
	}
	account, err := ParseAICostAccountSecret(request.Secret)
	if err != nil {
		return nil, err
	}
	body, err := account.LoginBody()
	if err != nil {
		return nil, err
	}
	loginHeader := make(http.Header)
	loginHeader.Set("Content-Type", "application/json")
	loginResponse, err := request.Exchange(ctx, http.MethodPost, resolveCatalogPath(request.BaseURL, "/api/user/login"), body, loginHeader)
	clear(body)
	if err != nil {
		return nil, err
	}
	login, err := ParseAICostLogin(loginResponse)
	clear(loginResponse)
	if err != nil {
		return nil, err
	}
	header := make(http.Header)
	header.Set("New-Api-User", strconv.FormatUint(login.UserID, 10))

	var failures []error
	itemsByModel := make(map[string]UpstreamAvailability)
	for _, category := range aicostAvailabilityCategories {
		response, requestErr := request.Exchange(ctx, http.MethodGet, aicostAvailabilityURL(request.BaseURL, category), nil, header)
		if requestErr != nil {
			failures = append(failures, fmt.Errorf("%s: %w", category, requestErr))
			continue
		}
		items, parseErr := ParseAICostAvailability(response, category)
		clear(response)
		if parseErr != nil {
			failures = append(failures, fmt.Errorf("%s: %w", category, parseErr))
			continue
		}
		for _, item := range items {
			key := strings.ToLower(strings.TrimSpace(item.ModelCode))
			current, exists := itemsByModel[key]
			if !exists || item.HasData && !current.HasData || item.HasData == current.HasData && item.ObservedAt.After(current.ObservedAt) {
				itemsByModel[key] = item
			}
		}
	}
	if len(failures) != 0 {
		return nil, errors.Join(failures...)
	}
	rows := make([]AvailabilityObservation, 0, len(itemsByModel))
	for _, item := range itemsByModel {
		if !item.HasData {
			continue
		}
		rows = append(rows, AvailabilityObservation{
			ModelCode: item.ModelCode, Category: item.Category, SuccessRate: item.SuccessRate,
			AverageCompletionSeconds: item.AverageCompletionSeconds,
			WindowMinutes:            item.WindowMinutes, ObservedAt: item.ObservedAt,
		})
	}
	return rows, nil
}

const (
	aicostAvailabilityRowLimit = "2000"
)

var aicostAvailabilityCategories = []string{"language", "image", "video"}

func aicostAvailabilityURL(base *url.URL, category string) string {
	target := *base
	target.Path, target.RawPath, target.Fragment = AICostAvailabilityPath, "", ""
	query := url.Values{}
	query.Set("category", category)
	query.Set("limit", aicostAvailabilityRowLimit)
	target.RawQuery = query.Encode()
	return target.String()
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
