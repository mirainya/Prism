package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
)

type QueryService struct {
	db *sql.DB
}

const (
	RouteOperationImagesGenerate = "images.generate"
	RouteOperationImagesEdit     = "images.edit"
	RouteOperationVideosGenerate = "videos.generate"
)

type AvailableModelSKU struct {
	ID              uint64   `json:"id"`
	Code            string   `json:"code"`
	DeliveryMode    string   `json:"delivery_mode"`
	MaxResults      uint32   `json:"max_results"`
	IdempotencyMode string   `json:"idempotency_mode"`
	ServiceTiers    []string `json:"service_tiers"`
}

type AvailableModelOperation struct {
	ID             string              `json:"id"`
	Method         string              `json:"method"`
	Path           string              `json:"path"`
	SupportsStream bool                `json:"supports_stream"`
	SKUs           []AvailableModelSKU `json:"skus"`
}

type AvailableModelChannel struct {
	ChannelID       uint64 `json:"channel_id"`
	ChannelType     string `json:"channel_type"`
	ChannelName     string `json:"channel_name"`
	Model           string `json:"model"`
	Protocol        string `json:"protocol"`
	InteractionMode string `json:"interaction_mode"`
	RouteOperation  string `json:"route_operation"`
}

type ThinkingInfo struct {
	Default string              `json:"default"`
	Locked  bool                `json:"locked"`
	Options []ThinkingLevelInfo `json:"options"`
}

type ThinkingLevelInfo struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type AvailableModelCapability struct {
	ID                     string                    `json:"id"`
	Code                   string                    `json:"code"`
	ModelCode              string                    `json:"model_code"`
	Object                 string                    `json:"object"`
	Name                   string                    `json:"name"`
	Type                   string                    `json:"type"`
	Types                  []string                  `json:"types"`
	Description            string                    `json:"description"`
	Visibility             string                    `json:"visibility"`
	Features               []string                  `json:"features"`
	Operations             []AvailableModelOperation `json:"operations"`
	Channels               []AvailableModelChannel   `json:"channels"`
	Transports             []string                  `json:"transports"`
	SupportsStream         bool                      `json:"supports_stream,omitempty"`
	DefaultStream          bool                      `json:"default_stream,omitempty"`
	SupportsTools          bool                      `json:"supports_tools,omitempty"`
	SupportsResponseFormat bool                      `json:"supports_response_format,omitempty"`
	SupportsMultimodal     bool                      `json:"supports_multimodal,omitempty"`
	MaxTokens              int                       `json:"max_tokens,omitempty"`
	Group                  string                    `json:"group,omitempty"`
	Thinking               *ThinkingInfo             `json:"thinking,omitempty"`
	Sort                   int                       `json:"-"`
	operationKeys          map[string]int
	channelKeys            map[string]struct{}
	typeSet                map[string]struct{}
	transportSet           map[string]struct{}
}

func NewQueryService() *QueryService {
	return &QueryService{}
}

func newQueryServiceWithDB(db *sql.DB) *QueryService {
	return &QueryService{db: db}
}

func (s *QueryService) store() (*repository.Store, error) {
	db := s.db
	if db == nil {
		var err error
		db, err = model.DB().DB()
		if err != nil {
			return nil, err
		}
	}
	return repository.New(db)
}

func (s *QueryService) activeRoutes(ctx context.Context) ([]repository.PublicCatalogRoute, *repository.Store, error) {
	store, err := s.store()
	if err != nil {
		return nil, nil, err
	}
	rows, err := store.ListActivePublicCatalog(ctx)
	return rows, store, err
}

func (s *QueryService) ListAvailableChannels(ctx context.Context) ([]string, error) {
	rows, _, err := s.activeRoutes(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(rows))
	result := make([]string, 0)
	for _, row := range rows {
		if _, exists := seen[row.ChannelCode]; exists {
			continue
		}
		seen[row.ChannelCode] = struct{}{}
		result = append(result, row.ChannelCode)
	}
	sort.Strings(result)
	return result, nil
}

func (s *QueryService) ListAvailableCapabilities(ctx context.Context, channelCode, modelType string) ([]AvailableModelCapability, error) {
	rows, _, err := s.activeRoutes(ctx)
	if err != nil {
		return nil, err
	}
	channelCode = strings.TrimSpace(channelCode)
	modelType = strings.ToLower(strings.TrimSpace(modelType))
	items := make(map[string]*AvailableModelCapability)
	for _, row := range rows {
		if channelCode != "" && row.ChannelCode != channelCode {
			continue
		}
		operationType := publicOperationModelType(row.OperationCode, row.RouteTemplate)
		if modelType != "" && operationType != modelType {
			continue
		}
		tiers, err := decodeCatalogStringArray(row.ServiceTiers)
		if err != nil || len(tiers) == 0 {
			return nil, fmt.Errorf("active catalog SKU %d has invalid service tiers", row.SKUID)
		}
		features := decodeCatalogFeatures(row.CapabilityTags)
		item := items[row.APIName]
		if item == nil {
			name := strings.TrimSpace(row.DisplayName)
			if name == "" {
				name = row.APIName
			}
			item = &AvailableModelCapability{
				ID: row.APIName, Code: row.APIName, ModelCode: row.ModelCode,
				Object: "model_capability", Name: name, Description: row.Description,
				Visibility: row.Visibility, Features: []string{}, Types: []string{},
				Operations: []AvailableModelOperation{}, Channels: []AvailableModelChannel{},
				Transports: []string{}, Sort: row.SortOrder,
				operationKeys: make(map[string]int), channelKeys: make(map[string]struct{}),
				typeSet: make(map[string]struct{}), transportSet: make(map[string]struct{}),
			}
			items[row.APIName] = item
		}
		item.addType(operationType)
		item.applyFeatures(features)
		item.addRoute(row, tiers)
	}

	result := make([]AvailableModelCapability, 0, len(items))
	for _, item := range items {
		sort.SliceStable(item.Operations, func(i, j int) bool {
			left, right := item.Operations[i], item.Operations[j]
			if operationRank(left.ID) != operationRank(right.ID) {
				return operationRank(left.ID) < operationRank(right.ID)
			}
			if left.Path != right.Path {
				return left.Path < right.Path
			}
			return left.Method < right.Method
		})
		for index := range item.Operations {
			sort.SliceStable(item.Operations[index].SKUs, func(i, j int) bool {
				return item.Operations[index].SKUs[i].Code < item.Operations[index].SKUs[j].Code
			})
		}
		sort.Strings(item.Features)
		sort.Strings(item.Transports)
		item.operationKeys = nil
		item.channelKeys = nil
		item.typeSet = nil
		item.transportSet = nil
		result = append(result, *item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Sort != result[j].Sort {
			return result[i].Sort < result[j].Sort
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (item *AvailableModelCapability) addType(value string) {
	if value == "" {
		return
	}
	if _, exists := item.typeSet[value]; exists {
		return
	}
	item.typeSet[value] = struct{}{}
	item.Types = append(item.Types, value)
	if item.Type == "" || modelTypeRank(value) < modelTypeRank(item.Type) {
		item.Type = value
	}
}

func (item *AvailableModelCapability) applyFeatures(features map[string]bool) {
	for name, enabled := range features {
		if !enabled || containsCatalogString(item.Features, name) {
			continue
		}
		item.Features = append(item.Features, name)
	}
	item.SupportsStream = item.SupportsStream || features["stream"]
	item.SupportsTools = item.SupportsTools || features["tools"]
	item.SupportsResponseFormat = item.SupportsResponseFormat || features["structured_output"] || features["response_format"]
	item.SupportsMultimodal = item.SupportsMultimodal || features["vision"] || features["multimodal"]
}

func (item *AvailableModelCapability) addRoute(row repository.PublicCatalogRoute, tiers []string) {
	operationKey := row.OperationCode + "\x00" + row.HTTPMethod + "\x00" + row.RouteTemplate
	operationIndex, exists := item.operationKeys[operationKey]
	if !exists {
		operationIndex = len(item.Operations)
		item.operationKeys[operationKey] = operationIndex
		item.Operations = append(item.Operations, AvailableModelOperation{
			ID: row.OperationCode, Method: row.HTTPMethod, Path: row.RouteTemplate,
			SupportsStream: item.SupportsStream, SKUs: []AvailableModelSKU{},
		})
	}
	operation := &item.Operations[operationIndex]
	operation.SupportsStream = operation.SupportsStream || item.SupportsStream
	if !operationHasSKU(*operation, row.SKUID) {
		operation.SKUs = append(operation.SKUs, AvailableModelSKU{
			ID: row.SKUID, Code: row.SKUCode, DeliveryMode: row.DeliveryMode,
			MaxResults: row.MaxResults, IdempotencyMode: row.IdempotencyMode,
			ServiceTiers: append([]string(nil), tiers...),
		})
	}

	interactionMode := "sync"
	if row.TaskScope == "task" {
		interactionMode = "poll"
	}
	channelKey := fmt.Sprintf("%d\x00%s\x00%s\x00%s", row.ChannelID, row.VendorModel, row.OperationCode, row.RouteTemplate)
	if _, exists := item.channelKeys[channelKey]; !exists {
		item.channelKeys[channelKey] = struct{}{}
		item.Channels = append(item.Channels, AvailableModelChannel{
			ChannelID: row.ChannelID, ChannelType: row.ChannelCode,
			ChannelName: row.ChannelName, Model: row.VendorModel, Protocol: row.Protocol,
			InteractionMode: interactionMode, RouteOperation: row.OperationCode,
		})
	}
	if _, exists := item.transportSet[row.Protocol]; !exists {
		item.transportSet[row.Protocol] = struct{}{}
		item.Transports = append(item.Transports, row.Protocol)
	}
}

func operationHasSKU(operation AvailableModelOperation, skuID uint64) bool {
	for _, sku := range operation.SKUs {
		if sku.ID == skuID {
			return true
		}
	}
	return false
}

func decodeCatalogStringArray(raw []byte) ([]string, error) {
	var values []string
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil {
		return nil, fmt.Errorf("invalid string array")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || containsCatalogString(result, value) {
			continue
		}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func decodeCatalogFeatures(raw []byte) map[string]bool {
	result := make(map[string]bool)
	var object map[string]bool
	if len(raw) > 0 && json.Unmarshal(raw, &object) == nil {
		for name, enabled := range object {
			if enabled {
				result[strings.ToLower(strings.TrimSpace(name))] = true
			}
		}
		return result
	}
	var values []string
	if len(raw) > 0 && json.Unmarshal(raw, &values) == nil {
		for _, value := range values {
			value = strings.ToLower(strings.TrimSpace(value))
			if value != "" {
				result[value] = true
			}
		}
	}
	return result
}

func containsCatalogString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func publicOperationModelType(operationCode, route string) string {
	operationCode = strings.ToLower(operationCode)
	route = strings.ToLower(route)
	switch {
	case strings.Contains(operationCode, "image") || strings.HasPrefix(route, "/v1/images/"):
		return string(model.ModelTypeImage)
	case strings.Contains(operationCode, "video") || strings.HasPrefix(route, "/v1/videos/"):
		return string(model.ModelTypeVideo)
	case strings.Contains(operationCode, "audio") || strings.HasPrefix(route, "/v1/audio/"):
		return string(model.ModelTypeAudio)
	case strings.Contains(operationCode, "embedding") || strings.HasPrefix(route, "/v1/embeddings"):
		return string(model.ModelTypeEmbedding)
	case operationCode == "chat.completions" || operationCode == "responses.create" || operationCode == "messages.create" ||
		strings.HasPrefix(route, "/v1/chat/") || route == "/v1/responses" || route == "/v1/messages":
		return string(model.ModelTypeChat)
	default:
		return "other"
	}
}

func modelTypeRank(value string) int {
	switch value {
	case string(model.ModelTypeChat):
		return 10
	case string(model.ModelTypeImage):
		return 20
	case string(model.ModelTypeVideo):
		return 30
	case string(model.ModelTypeAudio):
		return 40
	case string(model.ModelTypeEmbedding):
		return 50
	default:
		return 100
	}
}

func operationRank(operationID string) int {
	switch operationID {
	case "chat.completions":
		return 10
	case "responses.create":
		return 20
	case "messages.create":
		return 30
	case RouteOperationImagesGenerate:
		return 100
	case RouteOperationImagesEdit:
		return 110
	case RouteOperationVideosGenerate:
		return 200
	default:
		return 1000
	}
}

func (s *QueryService) ListCapabilityChannels(ctx context.Context) ([]AvailableModelCapability, error) {
	return s.ListAvailableCapabilities(ctx, "", "")
}

type PricingService struct {
	query *QueryService
}

func NewPricingService() *PricingService {
	return &PricingService{query: NewQueryService()}
}

type PricingCapability struct {
	Code        string       `json:"code"`
	ModelCode   string       `json:"model_code"`
	Name        string       `json:"name"`
	Type        string       `json:"type"`
	Description string       `json:"description"`
	Visibility  string       `json:"visibility"`
	SKUs        []PricingSKU `json:"skus"`
	Sort        int          `json:"-"`
	skuIndexes  map[uint64]int
}

type PricingSKU struct {
	ID              uint64                  `json:"id"`
	Code            string                  `json:"code"`
	Operation       string                  `json:"operation"`
	Routes          []PricingRoute          `json:"routes"`
	DeliveryMode    string                  `json:"delivery_mode"`
	MaxResults      uint32                  `json:"max_results"`
	IdempotencyMode string                  `json:"idempotency_mode"`
	ServiceTiers    []string                `json:"service_tiers"`
	Currency        PricingCurrency         `json:"currency"`
	Components      []billing.RateComponent `json:"components"`
}

type PricingRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type PricingCurrency struct {
	Code           string `json:"code"`
	Version        uint32 `json:"version"`
	FractionDigits int32  `json:"fraction_digits"`
}

func (s *PricingService) GetPricing(ctx context.Context) ([]PricingCapability, error) {
	rows, store, err := s.query.activeRoutes(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []PricingCapability{}, nil
	}
	releaseID := rows[0].ReleaseID
	schedules, err := repository.LoadSellSchedules(ctx, store.DB(), releaseID)
	if err != nil {
		return nil, err
	}
	items := make(map[string]*PricingCapability)
	for _, row := range rows {
		if row.ReleaseID != releaseID {
			return nil, fmt.Errorf("active catalog changed while pricing was read")
		}
		schedule, ok := schedules[row.SKUID]
		if !ok {
			return nil, fmt.Errorf("active catalog SKU %d has no valid sell schedule", row.SKUID)
		}
		tiers, err := decodeCatalogStringArray(row.ServiceTiers)
		if err != nil || len(tiers) == 0 {
			return nil, fmt.Errorf("active catalog SKU %d has invalid service tiers", row.SKUID)
		}
		item := items[row.APIName]
		if item == nil {
			name := strings.TrimSpace(row.DisplayName)
			if name == "" {
				name = row.APIName
			}
			item = &PricingCapability{
				Code: row.APIName, ModelCode: row.ModelCode, Name: name,
				Type:        publicOperationModelType(row.OperationCode, row.RouteTemplate),
				Description: row.Description, Visibility: row.Visibility, Sort: row.SortOrder,
				SKUs: []PricingSKU{}, skuIndexes: make(map[uint64]int),
			}
			items[row.APIName] = item
		}
		index, exists := item.skuIndexes[row.SKUID]
		if !exists {
			index = len(item.SKUs)
			item.skuIndexes[row.SKUID] = index
			item.SKUs = append(item.SKUs, PricingSKU{
				ID: row.SKUID, Code: row.SKUCode, Operation: row.OperationCode,
				Routes: []PricingRoute{}, DeliveryMode: row.DeliveryMode,
				MaxResults: row.MaxResults, IdempotencyMode: row.IdempotencyMode,
				ServiceTiers: tiers,
				Currency:     PricingCurrency{Code: schedule.Currency.Code, Version: schedule.Currency.Version, FractionDigits: schedule.Currency.FractionDigits},
				Components:   append([]billing.RateComponent(nil), schedule.Components...),
			})
		}
		sku := &item.SKUs[index]
		route := PricingRoute{Method: row.HTTPMethod, Path: row.RouteTemplate}
		if !containsPricingRoute(sku.Routes, route) {
			sku.Routes = append(sku.Routes, route)
		}
	}

	result := make([]PricingCapability, 0, len(items))
	for _, item := range items {
		sort.SliceStable(item.SKUs, func(i, j int) bool { return item.SKUs[i].Code < item.SKUs[j].Code })
		item.skuIndexes = nil
		result = append(result, *item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Sort != result[j].Sort {
			return result[i].Sort < result[j].Sort
		}
		return result[i].Code < result[j].Code
	})
	return result, nil
}

func containsPricingRoute(values []PricingRoute, target PricingRoute) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
