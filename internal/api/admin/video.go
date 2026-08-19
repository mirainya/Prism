package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/internal/video/generic"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
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
	Name          string         `json:"name" binding:"required"`
	AdapterType   string         `json:"adapter_type" binding:"required"`
	BaseURL       string         `json:"base_url" binding:"required"`
	Status        string         `json:"status"`
	Priority      int            `json:"priority"`
	Models        datatypes.JSON `json:"models"`
	Capabilities  datatypes.JSON `json:"capabilities"`
	Pricing       datatypes.JSON `json:"pricing"`
	AssetResolver string         `json:"asset_resolver"`
	ExtraConfig   datatypes.JSON `json:"extra_config"`
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
	if err := validateVideoPricing(req.Pricing, req.AdapterType); err != nil {
		return err
	}
	resolverChannel := &video.VideoChannel{
		BaseURL: req.BaseURL, Pricing: req.Pricing,
		AssetResolver: req.AssetResolver, ExtraConfig: req.ExtraConfig,
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

func validateVideoPricing(raw datatypes.JSON, adapterType string) error {
	var pricing struct {
		Mode        string  `json:"mode"`
		FixedPrice  float64 `json:"fixed_price"`
		MarkupRatio float64 `json:"markup_ratio"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &pricing); err != nil {
			return fmt.Errorf("invalid pricing: %w", err)
		}
	}
	if pricing.Mode == "" {
		pricing.Mode = "fixed"
	}
	if pricing.Mode != "fixed" && pricing.Mode != "upstream_estimate" {
		return fmt.Errorf("unsupported video pricing mode %q", pricing.Mode)
	}
	if pricing.FixedPrice < 0 || pricing.MarkupRatio < 0 {
		return fmt.Errorf("video pricing cannot contain negative values")
	}
	if pricing.Mode == "upstream_estimate" && adapterType != video.AdapterTypeGeneric {
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
		Name:          req.Name,
		AdapterType:   req.AdapterType,
		BaseURL:       req.BaseURL,
		Status:        req.Status,
		Priority:      req.Priority,
		Models:        req.Models,
		Capabilities:  req.Capabilities,
		Pricing:       req.Pricing,
		AssetResolver: req.AssetResolver,
		ExtraConfig:   req.ExtraConfig,
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
		"name":           req.Name,
		"adapter_type":   req.AdapterType,
		"base_url":       req.BaseURL,
		"status":         req.Status,
		"priority":       req.Priority,
		"models":         req.Models,
		"capabilities":   req.Capabilities,
		"pricing":        req.Pricing,
		"asset_resolver": req.AssetResolver,
		"extra_config":   req.ExtraConfig,
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

	db := model.DB().Model(&video.VideoTask{})
	if status := c.Query("status"); status != "" {
		db = db.Where("status = ?", status)
	}
	if model_ := c.Query("model"); model_ != "" {
		db = db.Where("model = ?", model_)
	}

	var total int64
	db.Count(&total)

	var tasks []video.VideoTask
	db.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&tasks)
	resp.Success(c, gin.H{"total": total, "items": tasks})
}

func GetVideoTask(c *gin.Context) {
	id := c.Param("id")
	var task video.VideoTask
	if err := model.DB().First(&task, "id = ?", id).Error; err != nil {
		resp.NotFound(c, pkgErrors.ErrTaskNotFound)
		return
	}
	resp.Success(c, task)
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
