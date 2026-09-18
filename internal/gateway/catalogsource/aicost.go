// Package catalogsource contains fixed, non-scriptable catalog discovery
// contracts. Parsed snapshots are safe staging facts; they never become an
// active catalog until an administrator reviews and publishes a draft.
package catalogsource

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

const (
	AICostModelsV1  = "aicost_models_v1"
	AICostPricingV1 = "aicost_pricing_v1"
)

var (
	ErrInvalidSnapshot = errors.New("catalog source: invalid provider snapshot")
	endpointPattern    = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
)

type AICostAccountSecret struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func ParseAICostAccountSecret(value []byte) (AICostAccountSecret, error) {
	var secret AICostAccountSecret
	if err := decodeExact(value, &secret); err != nil ||
		!validText(secret.Username, 128) || !validText(secret.Password, 512) {
		return AICostAccountSecret{}, ErrInvalidSnapshot
	}
	return secret, nil
}

func (secret AICostAccountSecret) LoginBody() ([]byte, error) {
	if !validText(secret.Username, 128) || !validText(secret.Password, 512) {
		return nil, ErrInvalidSnapshot
	}
	return json.Marshal(secret)
}

type AICostLogin struct {
	UserID uint64
}

func ParseAICostLogin(value []byte) (AICostLogin, error) {
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    *struct {
			ID uint64 `json:"id"`
		} `json:"data"`
	}
	if err := decode(value, &response); err != nil || !response.Success || response.Data == nil || response.Data.ID == 0 {
		return AICostLogin{}, ErrInvalidSnapshot
	}
	return AICostLogin{UserID: response.Data.ID}, nil
}

type DiscoveredModel struct {
	Code              string
	Description       string
	Tags              string
	VendorID          *uint64
	ProviderQuotaType *uint8
	ModelPrice        *string
	ModelRatio        *string
	CompletionRatio   *string
	OwnerBy           string
	PricingVersion    string
	Groups            []string
	EndpointTypes     []string
}

func ParseAICostModels(value []byte, group string) ([]DiscoveredModel, error) {
	if !validText(group, 128) {
		return nil, ErrInvalidSnapshot
	}
	var response struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := decode(value, &response); err != nil || len(response.Data) == 0 || len(response.Data) > 10000 {
		return nil, ErrInvalidSnapshot
	}
	models := make([]DiscoveredModel, 0, len(response.Data))
	seen := make(map[string]struct{}, len(response.Data))
	for _, item := range response.Data {
		code := strings.TrimSpace(item.ID)
		if !validModelCode(code) {
			return nil, ErrInvalidSnapshot
		}
		if _, exists := seen[code]; exists {
			return nil, fmt.Errorf("%w: duplicate model %s", ErrInvalidSnapshot, code)
		}
		seen[code] = struct{}{}
		models = append(models, DiscoveredModel{Code: code, Groups: []string{group}})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Code < models[j].Code })
	return models, nil
}

type aicostPrice struct {
	ModelName              string          `json:"model_name"`
	Description            string          `json:"description"`
	Tags                   string          `json:"tags"`
	VendorID               *uint64         `json:"vendor_id"`
	QuotaType              *uint8          `json:"quota_type"`
	ModelRatio             json.Number     `json:"model_ratio"`
	ModelPrice             json.Number     `json:"model_price"`
	OwnerBy                string          `json:"owner_by"`
	CompletionRatio        json.Number     `json:"completion_ratio"`
	EnableGroups           []string        `json:"enable_groups"`
	SupportedEndpointTypes []string        `json:"supported_endpoint_types"`
	PricingVersion         json.RawMessage `json:"pricing_version"`
}

func ParseAICostPricing(value []byte) ([]DiscoveredModel, error) {
	var response struct {
		Success bool          `json:"success"`
		Message string        `json:"message"`
		Data    []aicostPrice `json:"data"`
	}
	if err := decode(value, &response); err != nil || !response.Success || len(response.Data) == 0 || len(response.Data) > 10000 {
		return nil, ErrInvalidSnapshot
	}
	models := make([]DiscoveredModel, 0, len(response.Data))
	seen := make(map[string]struct{}, len(response.Data))
	for _, item := range response.Data {
		model, err := normalizeAICostPrice(item)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[model.Code]; exists {
			return nil, fmt.Errorf("%w: duplicate model %s", ErrInvalidSnapshot, model.Code)
		}
		seen[model.Code] = struct{}{}
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Code < models[j].Code })
	return models, nil
}

func normalizeAICostPrice(item aicostPrice) (DiscoveredModel, error) {
	item.ModelName = strings.TrimSpace(item.ModelName)
	item.Description = normalizeProviderText(item.Description)
	item.Tags = normalizeProviderText(item.Tags)
	item.OwnerBy = normalizeProviderText(item.OwnerBy)
	if !validModelCode(item.ModelName) || !validOptionalText(item.Description, 1000) ||
		!validOptionalText(item.Tags, 512) || !validOptionalText(item.OwnerBy, 128) ||
		item.QuotaType != nil && *item.QuotaType > 1 {
		return DiscoveredModel{}, ErrInvalidSnapshot
	}
	groups, err := normalizeStrings(item.EnableGroups, 128, nil)
	if err != nil || len(groups) == 0 {
		return DiscoveredModel{}, ErrInvalidSnapshot
	}
	endpoints, err := normalizeStrings(item.SupportedEndpointTypes, 64, endpointPattern)
	if err != nil || len(endpoints) == 0 {
		return DiscoveredModel{}, ErrInvalidSnapshot
	}
	price, err := optionalDecimal(item.ModelPrice)
	if err != nil {
		return DiscoveredModel{}, err
	}
	ratio, err := optionalDecimal(item.ModelRatio)
	if err != nil {
		return DiscoveredModel{}, err
	}
	completion, err := optionalDecimal(item.CompletionRatio)
	if err != nil {
		return DiscoveredModel{}, err
	}
	version, err := canonicalScalar(item.PricingVersion)
	if err != nil {
		return DiscoveredModel{}, err
	}
	return DiscoveredModel{
		Code: item.ModelName, Description: item.Description, Tags: item.Tags,
		VendorID: item.VendorID, ProviderQuotaType: item.QuotaType,
		ModelPrice: price, ModelRatio: ratio, CompletionRatio: completion,
		OwnerBy: item.OwnerBy, PricingVersion: version,
		Groups: groups, EndpointTypes: endpoints,
	}, nil
}

func normalizeProviderText(value string) string {
	if !utf8.ValidString(value) {
		return value
	}
	return strings.Join(strings.Fields(value), " ")
}

func optionalDecimal(number json.Number) (*string, error) {
	if number == "" {
		return nil, nil
	}
	amount, err := billing.ParseAmount(number.String(), 18, true)
	if err != nil {
		return nil, ErrInvalidSnapshot
	}
	value := amount.String()
	return &value, nil
}

func canonicalScalar(value json.RawMessage) (string, error) {
	if len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return "", nil
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var scalar any
	if err := decoder.Decode(&scalar); err != nil {
		return "", ErrInvalidSnapshot
	}
	switch scalar.(type) {
	case string, json.Number:
	default:
		return "", ErrInvalidSnapshot
	}
	encoded, err := json.Marshal(scalar)
	if err != nil || len(encoded) > 128 {
		return "", ErrInvalidSnapshot
	}
	return string(encoded), nil
}

func normalizeStrings(values []string, maxLength int, pattern *regexp.Regexp) ([]string, error) {
	if len(values) > 256 {
		return nil, ErrInvalidSnapshot
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if !validText(value, maxLength) || pattern != nil && !pattern.MatchString(value) {
			return nil, ErrInvalidSnapshot
		}
		if _, exists := seen[value]; exists {
			return nil, ErrInvalidSnapshot
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func validText(value string, maxRunes int) bool {
	return value != "" && validOptionalText(value, maxRunes)
}

func validModelCode(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	runes := []rune(value)
	if len(runes) == 0 || len(runes) > 128 || !unicode.IsLetter(runes[0]) && !unicode.IsDigit(runes[0]) {
		return false
	}
	for _, current := range runes[1:] {
		if unicode.IsLetter(current) || unicode.IsDigit(current) || strings.ContainsRune("._:/-%", current) {
			continue
		}
		return false
	}
	return true
}

func validOptionalText(value string, maxRunes int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes &&
		!strings.ContainsAny(value, "\x00\r\n\t")
}

func decode(value []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrInvalidSnapshot
	}
	return nil
}

func decodeExact(value []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrInvalidSnapshot
	}
	return nil
}
