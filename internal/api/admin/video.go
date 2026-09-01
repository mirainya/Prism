package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/internal/video/generic"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

// ========== Video Channel ==========

var adminVideoEngine *video.Engine

func SetVideoEngine(engine *video.Engine) {
	adminVideoEngine = engine
}

func ListVideoChannels(c *gin.Context) {
	var channels []video.VideoChannel
	if err := model.DB().Order("priority DESC, id ASC").Find(&channels).Error; err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, channels)
}

func GetVideoChannel(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var ch video.VideoChannel
	if err := model.DB().First(&ch, id).Error; err != nil {
		resp.NotFound(c, pkgErrors.ErrTaskNotFound)
		return
	}
	resp.Success(c, ch)
}

type discoveredVideoModelOption struct {
	VendorModel  string   `json:"vendor_model"`
	PublicModels []string `json:"public_models"`
}

// DiscoverVideoChannelModels reads the model matrix exposed by the channel's
// active upstream key. The result is advisory until an administrator saves it.
func DiscoverVideoChannelModels(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	if adminVideoEngine == nil || adminVideoEngine.Registry() == nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}

	var channel video.VideoChannel
	if err := model.DB().First(&channel, id).Error; err != nil {
		resp.NotFound(c, pkgErrors.ErrTaskNotFound)
		return
	}
	var key video.VideoChannelKey
	if err := model.DB().Where("channel_id = ? AND status = ?", id, "active").
		Order("weight DESC, id ASC").First(&key).Error; err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "channel has no active video key"))
		return
	}

	adapter := adminVideoEngine.Registry().Get(channel.AdapterType, &channel, &key)
	discoverer, ok := adapter.(video.CapabilityDiscoverer)
	if !ok {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "channel adapter does not expose model discovery"))
		return
	}
	discoveryContext, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	discovered, err := discoverer.DiscoverCapabilities(discoveryContext)
	if err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, fmt.Sprintf("discover upstream video models: %v", err)))
		return
	}
	resp.Success(c, gin.H{"models": buildDiscoveredVideoModelOptions(channel.Models, discovered)})
}

func buildDiscoveredVideoModelOptions(rawModels []byte, discovered map[string]video.DiscoveredModelCapabilities) []discoveredVideoModelOption {
	publicByVendor := make(map[string][]string)
	if mappings, err := video.ParseVideoModelMappings(rawModels); err == nil {
		for _, mapping := range mappings {
			publicByVendor[mapping.VendorModel] = append(publicByVendor[mapping.VendorModel], mapping.ModelName)
		}
	}
	options := make([]discoveredVideoModelOption, 0, len(discovered))
	for vendorModel := range discovered {
		vendorModel = strings.TrimSpace(vendorModel)
		if vendorModel == "" {
			continue
		}
		publicModels := append([]string(nil), publicByVendor[vendorModel]...)
		sort.Strings(publicModels)
		options = append(options, discoveredVideoModelOption{VendorModel: vendorModel, PublicModels: publicModels})
	}
	sort.Slice(options, func(i, j int) bool { return options[i].VendorModel < options[j].VendorModel })
	return options
}

type createVideoChannelRequest struct {
	Name                  string         `json:"name" binding:"required"`
	AdapterType           string         `json:"adapter_type" binding:"required"`
	AdapterProfile        string         `json:"adapter_profile"`
	BaseURL               string         `json:"base_url" binding:"required"`
	Status                string         `json:"status"`
	Priority              int            `json:"priority"`
	RequestTimeoutSeconds int            `json:"request_timeout_seconds"`
	Models                datatypes.JSON `json:"models"`
	Capabilities          datatypes.JSON `json:"capabilities"`
	SupportsFirstFrame    *bool          `json:"supports_first_frame"`
	SupportsLastFrame     *bool          `json:"supports_last_frame"`
	SupportsAudio         *bool          `json:"supports_audio"`
	SupportsWebSearch     *bool          `json:"supports_web_search"`
	CancelMode            string         `json:"cancel_mode"`
	Pricing               datatypes.JSON `json:"pricing"`
	PricingMode           string         `json:"pricing_mode"`
	FixedPrice            *float64       `json:"fixed_price"`
	MarkupRatio           *float64       `json:"markup_ratio"`
	AssetResolver         string         `json:"asset_resolver"`
	ResultStorageEnabled  *bool          `json:"result_storage_enabled"`
	ExtraConfig           datatypes.JSON `json:"extra_config"`
}

func normalizeFormalVideoChannelSettings(req *createVideoChannelRequest) error {
	if req == nil {
		return nil
	}
	var pricing struct {
		Mode        string   `json:"mode"`
		FixedPrice  *float64 `json:"fixed_price"`
		MarkupRatio *float64 `json:"markup_ratio"`
	}
	if len(req.Pricing) > 0 && string(req.Pricing) != "null" {
		if err := json.Unmarshal(req.Pricing, &pricing); err != nil {
			return fmt.Errorf("invalid video pricing: %w", err)
		}
	}
	if strings.TrimSpace(req.PricingMode) == "" {
		req.PricingMode = strings.TrimSpace(pricing.Mode)
	}
	if req.PricingMode == "" {
		req.PricingMode = "fixed"
	}
	if req.FixedPrice == nil {
		value := 0.0
		if pricing.FixedPrice != nil {
			value = *pricing.FixedPrice
		}
		req.FixedPrice = &value
	}
	if req.MarkupRatio == nil {
		value := 1.0
		if pricing.MarkupRatio != nil {
			value = *pricing.MarkupRatio
		}
		req.MarkupRatio = &value
	}

	var capabilities map[string]bool
	if len(req.Capabilities) > 0 && string(req.Capabilities) != "null" {
		if err := json.Unmarshal(req.Capabilities, &capabilities); err != nil || capabilities == nil {
			return fmt.Errorf("capabilities must be a JSON boolean object")
		}
	}
	if req.SupportsFirstFrame == nil {
		value := capabilities["first_frame"]
		req.SupportsFirstFrame = &value
	}
	if req.SupportsLastFrame == nil {
		value := capabilities["last_frame"]
		req.SupportsLastFrame = &value
	}
	if req.SupportsAudio == nil {
		value := capabilities["audio"]
		req.SupportsAudio = &value
	}
	if req.SupportsWebSearch == nil {
		value := capabilities["web_search"]
		req.SupportsWebSearch = &value
	}

	var extra struct {
		Adapter struct {
			Profile        string `json:"profile"`
			TimeoutSeconds int    `json:"timeout_seconds"`
			Cancel         struct {
				Enabled bool `json:"enabled"`
			} `json:"cancel"`
			LocalCancel struct {
				Enabled *bool `json:"enabled"`
			} `json:"local_cancel"`
		} `json:"adapter"`
		ResultStorage struct {
			Enabled bool `json:"enabled"`
		} `json:"result_storage"`
	}
	if len(req.ExtraConfig) > 0 && string(req.ExtraConfig) != "null" {
		if err := json.Unmarshal(req.ExtraConfig, &extra); err != nil {
			return fmt.Errorf("extra_config must be a JSON object")
		}
	}
	if req.AdapterProfile == "" {
		req.AdapterProfile = extra.Adapter.Profile
	}
	if req.AdapterType == video.AdapterTypeGeneric && strings.TrimSpace(req.AdapterProfile) == "" {
		req.AdapterProfile = generic.ProfileJSONTaskV1
	}
	if req.RequestTimeoutSeconds <= 0 {
		req.RequestTimeoutSeconds = extra.Adapter.TimeoutSeconds
	}
	if req.RequestTimeoutSeconds <= 0 {
		req.RequestTimeoutSeconds = 30
	}
	if req.CancelMode == "" {
		switch {
		case extra.Adapter.Cancel.Enabled:
			req.CancelMode = video.CancelModeProvider
		case extra.Adapter.LocalCancel.Enabled != nil && *extra.Adapter.LocalCancel.Enabled:
			req.CancelMode = video.CancelModeLocalOnly
		default:
			req.CancelMode = video.CancelModeDisabled
		}
		if req.AdapterType == video.AdapterTypeSeedance && capabilities["cancel"] {
			req.CancelMode = video.CancelModeProvider
		}
	}
	if req.ResultStorageEnabled == nil {
		value := extra.ResultStorage.Enabled
		req.ResultStorageEnabled = &value
	}
	if req.CancelMode != video.CancelModeDisabled && req.CancelMode != video.CancelModeLocalOnly && req.CancelMode != video.CancelModeProvider {
		return fmt.Errorf("unsupported video cancel mode %q", req.CancelMode)
	}
	if req.PricingMode != "fixed" && req.PricingMode != "upstream_estimate" {
		return fmt.Errorf("unsupported video pricing mode %q", req.PricingMode)
	}
	if req.RequestTimeoutSeconds < 1 || req.RequestTimeoutSeconds > 300 {
		return fmt.Errorf("video request timeout must be between 1 and 300 seconds")
	}
	if *req.FixedPrice < 0 || *req.MarkupRatio < 0 {
		return fmt.Errorf("video pricing cannot contain negative values")
	}
	return nil
}

func stripPromotedVideoChannelJSON(req *createVideoChannelRequest) error {
	if req == nil {
		return nil
	}
	var err error
	if req.Capabilities, err = stripVideoChannelJSONKeys(req.Capabilities,
		"first_frame", "last_frame", "audio", "web_search", "cancel"); err != nil {
		return fmt.Errorf("strip promoted capabilities: %w", err)
	}
	if req.Pricing, err = stripVideoChannelJSONKeys(req.Pricing,
		"mode", "fixed_price", "markup_ratio"); err != nil {
		return fmt.Errorf("strip promoted pricing: %w", err)
	}
	if len(req.ExtraConfig) == 0 || string(req.ExtraConfig) == "null" {
		return nil
	}
	var extra map[string]any
	if err := json.Unmarshal(req.ExtraConfig, &extra); err != nil || extra == nil {
		return fmt.Errorf("extra_config must be a JSON object")
	}
	if adapter, ok := extra["adapter"].(map[string]any); ok {
		delete(adapter, "profile")
		delete(adapter, "timeout_seconds")
		stripNestedVideoChannelJSONKey(adapter, "cancel", "enabled")
		stripNestedVideoChannelJSONKey(adapter, "local_cancel", "enabled")
		if len(adapter) == 0 {
			delete(extra, "adapter")
		}
	}
	if resultStorage, ok := extra["result_storage"].(map[string]any); ok {
		delete(resultStorage, "enabled")
		if len(resultStorage) == 0 {
			delete(extra, "result_storage")
		}
	}
	encoded, err := json.Marshal(extra)
	if err != nil {
		return fmt.Errorf("encode extra_config: %w", err)
	}
	if len(extra) == 0 {
		req.ExtraConfig = nil
	} else {
		req.ExtraConfig = datatypes.JSON(encoded)
	}
	return nil
}

func stripVideoChannelJSONKeys(raw datatypes.JSON, keys ...string) (datatypes.JSON, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, fmt.Errorf("value must be a JSON object")
	}
	for _, key := range keys {
		delete(object, key)
	}
	if len(object) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(encoded), nil
}

func stripNestedVideoChannelJSONKey(parent map[string]any, objectKey, fieldKey string) {
	nested, ok := parent[objectKey].(map[string]any)
	if !ok {
		return
	}
	delete(nested, fieldKey)
	if len(nested) == 0 {
		delete(parent, objectKey)
	}
}

func validateVideoChannelRequest(req *createVideoChannelRequest) error {
	if req == nil || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.AdapterType) == "" {
		return fmt.Errorf("name and adapter_type are required")
	}
	parsed, err := url.Parse(strings.TrimSpace(req.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("base_url must be an HTTP or HTTPS URL")
	}
	req.BaseURL = parsed.String()
	req.Name = strings.TrimSpace(req.Name)
	req.AdapterType = strings.TrimSpace(req.AdapterType)
	if err := normalizeFormalVideoChannelSettings(req); err != nil {
		return err
	}
	if err := stripPromotedVideoChannelJSON(req); err != nil {
		return err
	}
	if req.AdapterType != video.AdapterTypeSeedance && req.AdapterType != video.AdapterTypeGeneric {
		return fmt.Errorf("unsupported video adapter type %q", req.AdapterType)
	}
	mappings, err := video.ParseVideoModelMappings(req.Models)
	if err != nil {
		return err
	}
	normalizedModels, err := json.Marshal(mappings)
	if err != nil {
		return fmt.Errorf("encode models: %w", err)
	}
	req.Models = datatypes.JSON(normalizedModels)
	if req.Status != "" && req.Status != "active" && req.Status != "inactive" && req.Status != "disabled" {
		return fmt.Errorf("unsupported channel status %q", req.Status)
	}
	resolver := strings.TrimSpace(req.AssetResolver)
	if resolver == "" {
		resolver = video.AssetResolverDirectURL
	}
	req.AssetResolver = resolver
	if len(req.Capabilities) > 0 && string(req.Capabilities) != "null" {
		var capabilities map[string]bool
		if err := json.Unmarshal(req.Capabilities, &capabilities); err != nil || capabilities == nil {
			return fmt.Errorf("capabilities must be a JSON boolean object")
		}
	}
	for name, raw := range map[string]datatypes.JSON{
		"capabilities": req.Capabilities, "pricing": req.Pricing,
		"extra_config": req.ExtraConfig,
	} {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return fmt.Errorf("%s must be a JSON object", name)
		}
	}
	if err := validateVideoPricing(req.PricingMode, *req.FixedPrice, *req.MarkupRatio, req.AdapterType); err != nil {
		return err
	}
	resolverChannel := &video.VideoChannel{
		AdapterType: req.AdapterType, AdapterProfile: req.AdapterProfile,
		BaseURL: req.BaseURL, Pricing: req.Pricing, PricingMode: req.PricingMode,
		FixedPrice: decimal.NewFromFloat(*req.FixedPrice), MarkupRatio: decimal.NewFromFloat(*req.MarkupRatio),
		RequestTimeoutSeconds: req.RequestTimeoutSeconds, CancelMode: req.CancelMode,
		AssetResolver: req.AssetResolver, ResultStorageEnabled: req.ResultStorageEnabled, ExtraConfig: req.ExtraConfig,
	}
	if req.AdapterType == video.AdapterTypeGeneric {
		if err := generic.ValidateChannelConfig(resolverChannel); err != nil {
			return err
		}
	} else if !emptyJSONObject(req.ExtraConfig) {
		return fmt.Errorf("seedance adapter does not accept extra_config")
	}
	if _, err := video.NewAssetResolver(resolver, video.AssetResolverOptions{Channel: resolverChannel}); err != nil {
		return err
	}
	return nil
}

func emptyJSONObject(raw datatypes.JSON) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var object map[string]any
	return json.Unmarshal(raw, &object) == nil && len(object) == 0
}

func validateVideoPricing(mode string, fixedPrice, markupRatio float64, adapterType string) error {
	if mode != video.PricingModeFixed && mode != video.PricingModeUpstreamEstimate {
		return fmt.Errorf("unsupported video pricing mode %q", mode)
	}
	if fixedPrice < 0 || markupRatio < 0 {
		return fmt.Errorf("video pricing cannot contain negative values")
	}
	if mode == video.PricingModeUpstreamEstimate && adapterType != video.AdapterTypeGeneric {
		return fmt.Errorf("upstream_estimate pricing requires the generic adapter")
	}
	return nil
}

func CreateVideoChannel(c *gin.Context) {
	var req createVideoChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if err := validateVideoChannelRequest(&req); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	ch := video.VideoChannel{
		Name:                  req.Name,
		AdapterType:           req.AdapterType,
		AdapterProfile:        req.AdapterProfile,
		BaseURL:               req.BaseURL,
		Status:                req.Status,
		Priority:              req.Priority,
		RequestTimeoutSeconds: req.RequestTimeoutSeconds,
		Models:                req.Models,
		Capabilities:          req.Capabilities,
		SupportsFirstFrame:    req.SupportsFirstFrame,
		SupportsLastFrame:     req.SupportsLastFrame,
		SupportsAudio:         req.SupportsAudio,
		SupportsWebSearch:     req.SupportsWebSearch,
		CancelMode:            req.CancelMode,
		Pricing:               req.Pricing,
		PricingMode:           req.PricingMode,
		FixedPrice:            decimal.NewFromFloat(*req.FixedPrice),
		MarkupRatio:           decimal.NewFromFloat(*req.MarkupRatio),
		AssetResolver:         req.AssetResolver,
		ResultStorageEnabled:  req.ResultStorageEnabled,
		ExtraConfig:           req.ExtraConfig,
	}
	if ch.Status == "" {
		ch.Status = "active"
	}
	if ch.AssetResolver == "" {
		ch.AssetResolver = video.AssetResolverDirectURL
	}
	if err := model.DB().Create(&ch).Error; err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, ch)
}

func UpdateVideoChannel(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var ch video.VideoChannel
	if err := model.DB().First(&ch, id).Error; err != nil {
		resp.NotFound(c, pkgErrors.ErrTaskNotFound)
		return
	}
	var req createVideoChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if err := validateVideoChannelRequest(&req); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	updates := map[string]any{
		"name":                    req.Name,
		"adapter_type":            req.AdapterType,
		"adapter_profile":         req.AdapterProfile,
		"base_url":                req.BaseURL,
		"status":                  req.Status,
		"priority":                req.Priority,
		"request_timeout_seconds": req.RequestTimeoutSeconds,
		"models":                  req.Models,
		"capabilities":            req.Capabilities,
		"supports_first_frame":    req.SupportsFirstFrame,
		"supports_last_frame":     req.SupportsLastFrame,
		"supports_audio":          req.SupportsAudio,
		"supports_web_search":     req.SupportsWebSearch,
		"cancel_mode":             req.CancelMode,
		"pricing":                 req.Pricing,
		"pricing_mode":            req.PricingMode,
		"fixed_price":             decimal.NewFromFloat(*req.FixedPrice),
		"markup_ratio":            decimal.NewFromFloat(*req.MarkupRatio),
		"asset_resolver":          req.AssetResolver,
		"result_storage_enabled":  req.ResultStorageEnabled,
		"extra_config":            req.ExtraConfig,
	}
	if err := model.DB().Model(&ch).Updates(updates).Error; err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	model.DB().First(&ch, id)
	resp.Success(c, ch)
}

func DeleteVideoChannel(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	active, err := hasActiveVideoTasks("channel_id", id)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	if active {
		resp.ErrorMsg(c, http.StatusConflict, http.StatusConflict, "video channel is used by active tasks")
		return
	}
	if err := model.DB().Delete(&video.VideoChannel{}, id).Error; err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, nil)
}

// ========== Video Channel Key ==========

type videoKeyResponse struct {
	ID                 uint   `json:"id"`
	ChannelID          uint   `json:"channel_id"`
	Label              string `json:"label"`
	MaskedKey          string `json:"masked_key"`
	Weight             int    `json:"weight"`
	MaxConcurrency     int    `json:"max_concurrency"`
	CurrentConcurrency int    `json:"current_concurrency"`
	Status             string `json:"status"`
	TotalCalls         int64  `json:"total_calls"`
}

func newVideoKeyResponse(k *video.VideoChannelKey) videoKeyResponse {
	return videoKeyResponse{
		ID: k.ID, ChannelID: k.ChannelID, Label: k.Label,
		MaskedKey: service.MaskCredential(k.APIKey),
		Weight:    k.Weight, MaxConcurrency: k.MaxConcurrency,
		CurrentConcurrency: k.CurrentConcurrency,
		Status:             k.Status, TotalCalls: k.TotalCalls,
	}
}

func ListVideoChannelKeys(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var keys []video.VideoChannelKey
	if err := model.DB().Where("channel_id = ?", id).Find(&keys).Error; err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	result := make([]videoKeyResponse, len(keys))
	for i := range keys {
		result[i] = newVideoKeyResponse(&keys[i])
	}
	resp.Success(c, result)
}

type createVideoKeyRequest struct {
	APIKey         string `json:"api_key" binding:"required"`
	Label          string `json:"label"`
	Weight         int    `json:"weight"`
	MaxConcurrency int    `json:"max_concurrency"`
	Status         string `json:"status"`
}

func CreateVideoChannelKey(c *gin.Context) {
	channelID, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var req createVideoKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if strings.TrimSpace(req.APIKey) == "" || req.Weight < 0 || req.MaxConcurrency < 0 ||
		(req.Status != "" && req.Status != "active" && req.Status != "inactive" && req.Status != "disabled") {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid key configuration"))
		return
	}
	key := video.VideoChannelKey{
		ChannelID:      channelID,
		APIKey:         req.APIKey,
		Label:          req.Label,
		Weight:         req.Weight,
		MaxConcurrency: req.MaxConcurrency,
		Status:         req.Status,
	}
	if key.Weight == 0 {
		key.Weight = 1
	}
	if key.Status == "" {
		key.Status = "active"
	}
	if err := model.DB().Create(&key).Error; err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, newVideoKeyResponse(&key))
}

func UpdateVideoKey(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var key video.VideoChannelKey
	if err := model.DB().First(&key, id).Error; err != nil {
		resp.NotFound(c, pkgErrors.ErrTaskNotFound)
		return
	}
	var req struct {
		Label          *string `json:"label"`
		Weight         *int    `json:"weight"`
		MaxConcurrency *int    `json:"max_concurrency"`
		Status         *string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	if (req.Weight != nil && *req.Weight < 0) || (req.MaxConcurrency != nil && *req.MaxConcurrency < 0) ||
		(req.Status != nil && *req.Status != "active" && *req.Status != "inactive" && *req.Status != "disabled") {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid key configuration"))
		return
	}
	updates := map[string]any{}
	if req.Label != nil {
		updates["label"] = *req.Label
	}
	if req.Weight != nil {
		updates["weight"] = *req.Weight
	}
	if req.MaxConcurrency != nil {
		updates["max_concurrency"] = *req.MaxConcurrency
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if len(updates) > 0 {
		if err := model.DB().Model(&key).Updates(updates).Error; err != nil {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
	}
	model.DB().First(&key, id)
	resp.Success(c, newVideoKeyResponse(&key))
}

func DeleteVideoKey(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	active, err := hasActiveVideoTasks("key_id", id)
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	if active {
		resp.ErrorMsg(c, http.StatusConflict, http.StatusConflict, "video channel key is used by active tasks")
		return
	}
	if err := model.DB().Delete(&video.VideoChannelKey{}, id).Error; err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, nil)
}

func hasActiveVideoTasks(column string, id uint) (bool, error) {
	var count int64
	err := model.DB().Model(&video.VideoTask{}).
		Where(column+" = ? AND status IN ?", id, []video.VideoTaskStatus{
			video.VideoTaskStatusQueued,
			video.VideoTaskStatusSubmitted,
			video.VideoTaskStatusTracking,
		}).Count(&count).Error
	return count > 0, err
}

// ========== Video Tasks ==========

type videoTaskListItem struct {
	ID             string                `json:"id"`
	CallID         string                `json:"call_id"`
	UserID         uint                  `json:"user_id"`
	TokenID        uint                  `json:"token_id"`
	Model          string                `json:"model"`
	VendorModel    string                `json:"vendor_model"`
	Status         video.VideoTaskStatus `json:"status"`
	Progress       int                   `json:"progress"`
	TaskMode       string                `json:"task_mode"`
	ServiceTier    string                `json:"service_tier"`
	Resolution     string                `json:"resolution"`
	Ratio          string                `json:"ratio"`
	Duration       int                   `json:"duration"`
	GenerateAudio  bool                  `json:"generate_audio"`
	ChannelID      uint                  `json:"channel_id"`
	KeyID          uint                  `json:"key_id"`
	AdapterType    string                `json:"adapter_type"`
	ProviderTaskID string                `json:"provider_task_id"`
	EstimatedCost  decimal.Decimal       `json:"estimated_cost"`
	FinalCost      decimal.Decimal       `json:"final_cost"`
	BillingStatus  string                `json:"billing_status"`
	ErrorMessage   string                `json:"error_message"`
	CreatedAt      time.Time             `json:"created_at"`
	SubmittedAt    *time.Time            `json:"submitted_at"`
	CompletedAt    *time.Time            `json:"completed_at"`
}

func ListVideoTasks(c *gin.Context) {
	page, pageSize := 1, 20
	if v := c.Query("page"); v != "" {
		var p int
		if _, err := fmt.Sscanf(v, "%d", &p); err == nil && p > 0 {
			page = p
		}
	}
	if v := c.Query("page_size"); v != "" {
		var s int
		if _, err := fmt.Sscanf(v, "%d", &s); err == nil && s > 0 && s <= 100 {
			pageSize = s
		}
	}

	snapshotAt := time.Now()
	if value := strings.TrimSpace(c.Query("snapshot_at")); value != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid snapshot_at"))
			return
		}
		snapshotAt = parsed
	}

	db := model.DB().Model(&video.VideoTask{}).Where("created_at <= ?", snapshotAt)
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		pattern := "%" + keyword + "%"
		db = db.Where("id LIKE ? OR call_id LIKE ? OR provider_task_id LIKE ?", pattern, pattern, pattern)
	}
	if status := c.Query("status"); status != "" {
		db = db.Where("status = ?", status)
	}
	if modelName := strings.TrimSpace(c.Query("model")); modelName != "" {
		db = db.Where("model LIKE ?", "%"+modelName+"%")
	}
	if taskMode := strings.TrimSpace(c.Query("task_mode")); taskMode != "" {
		db = db.Where("task_mode = ?", taskMode)
	}
	if serviceTier := strings.TrimSpace(c.Query("service_tier")); serviceTier != "" {
		db = db.Where("service_tier = ?", serviceTier)
	}
	for _, filter := range []struct {
		query  string
		column string
	}{
		{query: "channel_id", column: "channel_id"},
		{query: "user_id", column: "user_id"},
		{query: "token_id", column: "token_id"},
	} {
		value := strings.TrimSpace(c.Query(filter.query))
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil || parsed == 0 {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid "+filter.query))
			return
		}
		db = db.Where(filter.column+" = ?", parsed)
	}
	if value := strings.TrimSpace(c.Query("start_date")); value != "" {
		start, err := time.ParseInLocation("2006-01-02", value, time.Local)
		if err != nil {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid start_date"))
			return
		}
		db = db.Where("created_at >= ?", start)
	}
	if value := strings.TrimSpace(c.Query("end_date")); value != "" {
		end, err := time.ParseInLocation("2006-01-02", value, time.Local)
		if err != nil {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "invalid end_date"))
			return
		}
		db = db.Where("created_at < ?", end.AddDate(0, 0, 1))
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}

	var tasks []videoTaskListItem
	if err := db.Select(
		"id", "call_id", "user_id", "token_id", "model", "vendor_model", "status", "progress",
		"task_mode", "service_tier", "resolution", "ratio", "duration", "generate_audio",
		"channel_id", "key_id", "adapter_type", "provider_task_id", "estimated_cost", "final_cost",
		"billing_status", "error_message", "created_at", "submitted_at", "completed_at",
	).Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&tasks).Error; err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{
		"total":       total,
		"items":       tasks,
		"snapshot_at": snapshotAt.Format(time.RFC3339Nano),
	})
}

func GetVideoTask(c *gin.Context) {
	id := c.Param("id")
	var task video.VideoTask
	if err := model.DB().First(&task, "id = ?", id).Error; err != nil {
		resp.NotFound(c, pkgErrors.ErrTaskNotFound)
		return
	}
	callPayloads := make([]service.APICallPayloadDetail, 0)
	if task.CallID != "" {
		detail, err := service.NewAPICallService().GetCallDetail(task.CallID, task.UserID, true)
		if err != nil && !errors.Is(err, service.ErrAPICallNotFound) {
			resp.InternalError(c, pkgErrors.ErrInternalError)
			return
		}
		if detail != nil {
			for _, payload := range detail.Payloads {
				if payload.Kind == model.APICallPayloadRequest || payload.Kind == model.APICallPayloadUpstreamRequest {
					callPayloads = append(callPayloads, payload)
				}
			}
		}
	}
	resp.Success(c, struct {
		video.VideoTask
		ProviderResponse datatypes.JSON                 `json:"provider_response"`
		PollCount        int                            `json:"poll_count"`
		CallPayloads     []service.APICallPayloadDetail `json:"call_payloads"`
	}{
		VideoTask:        task,
		ProviderResponse: task.ProviderResponse,
		PollCount:        task.PollCount,
		CallPayloads:     callPayloads,
	})
}

// ========== Video Stats ==========

func GetVideoStats(c *gin.Context) {
	db := model.DB()
	var channelCount, keyCount, taskCount int64
	db.Model(&video.VideoChannel{}).Count(&channelCount)
	db.Model(&video.VideoChannelKey{}).Count(&keyCount)
	db.Model(&video.VideoTask{}).Count(&taskCount)

	var activeTaskCount int64
	db.Model(&video.VideoTask{}).Where("status IN ?", []string{"queued", "submitted", "tracking"}).Count(&activeTaskCount)

	resp.Success(c, gin.H{
		"channels":     channelCount,
		"keys":         keyCount,
		"total_tasks":  taskCount,
		"active_tasks": activeTaskCount,
	})
}
