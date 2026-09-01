package video

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/cache"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/queue"
	"github.com/mirainya/Prism/pkg/safeurl"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var (
	ErrInvalidTaskRequest   = errors.New("invalid video task request")
	ErrEngineUnavailable    = errors.New("video engine is unavailable")
	ErrEstimateNotSupported = errors.New("video estimate is not supported")
	ErrCancelNotSupported   = errors.New("video task cancellation is not supported")
	ErrCancelNotAllowed     = errors.New("video task cannot be cancelled in its current state")
	ErrActionNotSupported   = errors.New("video task action is not supported")
	ErrActionNotAllowed     = errors.New("video task action is not allowed in its current state")
)

type Engine struct {
	db       *gorm.DB
	redis    *redis.Client
	router   *Router
	registry *Registry
}

func NewEngine() *Engine {
	if !model.HasDB() {
		return nil
	}
	db := model.DB()
	rds := cache.Client
	return &Engine{
		db:       db,
		redis:    rds,
		router:   NewRouter(db, rds),
		registry: NewRegistry(),
	}
}

func (e *Engine) Router() *Router     { return e.router }
func (e *Engine) Registry() *Registry { return e.registry }
func (e *Engine) DB() *gorm.DB        { return e.db }

type CreateTaskRequest struct {
	UserID      uint
	TokenID     uint
	ChannelID   uint
	CallID      string
	RequestID   string
	Endpoint    string
	Operation   string
	Model       string
	Prompt      string
	Resolution  string
	Ratio       string
	Duration    int
	Audio       bool
	TaskMode    string
	ServiceTier string
	Content     []ContentItem
	Params      map[string]any
	Callback    string
}

type CreateTaskResult struct {
	TaskID string
}

type EstimateTaskResult struct {
	EstimatedCost    decimal.Decimal
	BaseCost         decimal.Decimal
	MarkupRatio      decimal.Decimal
	PricingMode      string
	PricingSnapshot  datatypes.JSON
	ProviderEstimate *ProviderEstimate
}

type preparedTaskRequest struct {
	channel     *VideoChannel
	key         *VideoChannelKey
	adapter     Adapter
	vendorModel string
	provider    *GenerateRequest
}

func (e *Engine) CreateTask(ctx context.Context, req *CreateTaskRequest) (*CreateTaskResult, error) {
	if e == nil || e.db == nil || e.router == nil || e.registry == nil {
		return nil, ErrEngineUnavailable
	}
	if req == nil {
		return nil, fmt.Errorf("%w: request is required", ErrInvalidTaskRequest)
	}
	if err := validateVideoCallback(ctx, req.Callback); err != nil {
		return nil, err
	}
	taskID := generateID()
	prepared, err := e.prepareTaskRequest(ctx, req, taskID, true)
	if err != nil {
		return nil, err
	}
	channel, key := prepared.channel, prepared.key
	callID := strings.TrimSpace(req.CallID)
	if callID == "" {
		callID = service.GenerateAPICallID()
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = service.GenerateRequestID()
	}

	var contentJSON, paramsJSON []byte
	if len(req.Content) > 0 {
		contentJSON, err = json.Marshal(req.Content)
		if err != nil {
			e.router.ReleaseConcurrency(ctx, key.ID)
			return nil, fmt.Errorf("encode content: %w", err)
		}
	}
	if len(req.Params) > 0 {
		paramsJSON, err = json.Marshal(req.Params)
		if err != nil {
			e.router.ReleaseConcurrency(ctx, key.ID)
			return nil, fmt.Errorf("encode params: %w", err)
		}
	}
	price, err := videoPrice(ctx, e.db, channel, key, prepared.adapter, prepared.provider)
	if err != nil {
		e.router.ReleaseConcurrency(ctx, key.ID)
		return nil, err
	}
	routePlan, err := BuildVideoRoutePlan(channel, key, req.Model, prepared.vendorModel)
	if err != nil {
		e.router.ReleaseConcurrency(ctx, key.ID)
		return nil, err
	}

	task := &VideoTask{
		ID:            taskID,
		CallID:        callID,
		UserID:        req.UserID,
		TokenID:       req.TokenID,
		Model:         req.Model,
		VendorModel:   prepared.vendorModel,
		Status:        VideoTaskStatusQueued,
		TaskMode:      req.TaskMode,
		ServiceTier:   req.ServiceTier,
		Prompt:        req.Prompt,
		Resolution:    req.Resolution,
		Ratio:         req.Ratio,
		Duration:      req.Duration,
		GenerateAudio: req.Audio,
		ContentJSON:   contentJSON,
		ParamsJSON:    paramsJSON,
		ChannelID:     channel.ID,
		KeyID:         key.ID,
		AdapterType:   channel.AdapterType,
		RoutePlan:     routePlan,
		EstimatedCost: price.EstimatedCost,
		MarkupRatio:   price.MarkupRatio,
		BillingStatus: "reserved",
		CallbackURL:   req.Callback,
	}

	err = e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		endpoint := strings.TrimSpace(req.Endpoint)
		if endpoint == "" {
			endpoint = "/v1/videos/generations"
		}
		operation := strings.TrimSpace(req.Operation)
		if operation == "" {
			operation = "videos.generate"
		}
		if _, err := service.NewAPICallService().StartCallTx(tx, &service.StartCallRequest{
			ID: callID, RequestID: requestID, UserID: req.UserID, TokenID: req.TokenID,
			Endpoint: endpoint, Operation: operation, Model: req.Model, Background: true,
			RetainPayload: boolPointer(true), ResourceType: "video_task", ResourceID: taskID,
		}); err != nil {
			return err
		}
		if err := service.NewBillingService().DeductWithBillingContextTx(
			tx, req.TokenID, req.UserID, price.EstimatedCost, taskID+":reserve",
			service.BillingContext{CallID: callID, Phase: model.BillingPhaseReserve, PricingSnapshot: price.PricingSnapshot},
		); err != nil {
			return err
		}
		return tx.Create(task).Error
	})
	if err != nil {
		e.router.ReleaseConcurrency(ctx, key.ID)
		return nil, fmt.Errorf("create task: %w", err)
	}
	RecordCallPayloadBestEffort(callID, 0, model.APICallPayloadRequest, map[string]any{
		"model": req.Model, "prompt": req.Prompt, "resolution": req.Resolution,
		"ratio": req.Ratio, "duration": req.Duration, "generate_audio": req.Audio,
		"task_mode": req.TaskMode, "service_tier": req.ServiceTier,
		"content": req.Content, "params": req.Params, "callback_url": req.Callback,
	})

	if err := queue.EnqueueVideoSubmit(taskID); err != nil {
		logger.Error("enqueue video submit failed", zap.String("task_id", taskID), zap.Error(err))
	}

	return &CreateTaskResult{TaskID: taskID}, nil
}

func (e *Engine) EstimateTask(ctx context.Context, req *CreateTaskRequest) (*EstimateTaskResult, error) {
	if e == nil || e.db == nil || e.router == nil || e.registry == nil {
		return nil, ErrEngineUnavailable
	}
	prepared, err := e.prepareTaskRequest(ctx, req, generateID(), false)
	if err != nil {
		return nil, err
	}
	return videoPrice(ctx, e.db, prepared.channel, prepared.key, prepared.adapter, prepared.provider)
}

// ActionVideoTask executes a configured provider action for an existing task.
func (e *Engine) ActionVideoTask(ctx context.Context, task *VideoTask, action string) (*ProviderMetadata, error) {
	if e == nil || e.db == nil || e.registry == nil {
		return nil, ErrEngineUnavailable
	}
	if task == nil || strings.TrimSpace(task.ID) == "" || strings.TrimSpace(action) == "" {
		return nil, ErrInvalidTaskRequest
	}
	if task.Status.IsTerminal() || task.ProviderTaskID == "" {
		return nil, ErrActionNotAllowed
	}
	channel, key, _, err := LoadVideoTaskRoute(e.db.WithContext(ctx), task)
	if err != nil {
		return nil, err
	}
	adapter := e.registry.Get(channel.AdapterType, channel, key)
	actioner, ok := adapter.(Actioner)
	if !ok {
		return nil, ErrActionNotSupported
	}
	if !actioner.CanAction(action, task.Status) {
		return nil, ErrActionNotAllowed
	}
	actionCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return actioner.Action(actionCtx, action, task.ProviderTaskID)
}

func (e *Engine) UpgradeVideoTaskPriority(ctx context.Context, task *VideoTask) (*ProviderMetadata, error) {
	if task == nil || task.ServiceTier != "standard" {
		return nil, ErrActionNotAllowed
	}
	previous := DecodeProviderMetadata(task.ProviderMetadata)
	if previous != nil && previous.PointsVIP != nil && *previous.PointsVIP {
		return nil, ErrActionNotAllowed
	}
	channel, key, _, err := LoadVideoTaskRoute(e.db.WithContext(ctx), task)
	if err != nil {
		return nil, err
	}
	metadata, err := e.ActionVideoTask(ctx, task, "priority_queue")
	if err != nil {
		return nil, err
	}
	surcharge := 0.0
	if adapter := e.registry.Get(channel.AdapterType, channel, key); adapter != nil {
		if pricing, ok := adapter.(ActionPricing); ok {
			surcharge = pricing.ActionSurchargePercent("priority_queue")
		}
	}
	if metadata != nil {
		if metadata.PrioritySurchargePercent > 0 {
			surcharge = metadata.PrioritySurchargePercent
		}
	}
	if surcharge <= 0 && previous != nil {
		surcharge = previous.PrioritySurchargePercent
	}
	newCost := task.EstimatedCost
	if metadata != nil && metadata.EstimatedCost > 0 {
		markup := task.MarkupRatio
		if !markup.IsPositive() {
			markup = decimal.NewFromInt(1)
		}
		newCost = decimal.NewFromFloat(metadata.EstimatedCost).Mul(markup)
	} else if surcharge > 0 {
		newCost = task.EstimatedCost.Mul(decimal.NewFromFloat(1 + surcharge/100))
	}
	if newCost.LessThan(task.EstimatedCost) {
		newCost = task.EstimatedCost
	}
	if metadata == nil {
		metadata = &ProviderMetadata{}
	}
	priority := true
	metadata.PriorityQueue = &priority
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	err = e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&VideoTask{}).
			Where("id = ? AND service_tier = ? AND status IN ?", task.ID, "standard", []VideoTaskStatus{VideoTaskStatusSubmitted, VideoTaskStatusTracking}).
			Updates(map[string]any{"service_tier": "priority", "estimated_cost": newCost, "provider_metadata": datatypes.JSON(encoded)})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrActionNotAllowed
		}
		return service.NewBillingService().SettleReservationWithBillingContextTx(
			tx, task.TokenID, task.UserID, task.EstimatedCost, newCost,
			task.ID+":priority", service.BillingContext{},
		)
	})
	if err != nil {
		return nil, err
	}
	return metadata, nil
}

func (e *Engine) prepareTaskRequest(ctx context.Context, req *CreateTaskRequest, requestID string, acquireConcurrency bool) (*preparedTaskRequest, error) {
	if req == nil || req.TokenID == 0 || strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("%w: token and model are required", ErrInvalidTaskRequest)
	}
	content, taskMode, caps, err := e.validateContent(ctx, req.TokenID, req.TaskMode, req.Content)
	if err != nil {
		return nil, err
	}
	req.Content = content
	req.TaskMode = taskMode
	req.ServiceTier = strings.ToLower(strings.TrimSpace(req.ServiceTier))
	if req.ServiceTier == "" {
		req.ServiceTier = "standard"
	}
	if req.ServiceTier != "standard" && req.ServiceTier != "priority" && req.ServiceTier != "vip" {
		return nil, fmt.Errorf("%w: unsupported service_tier %q", ErrInvalidTaskRequest, req.ServiceTier)
	}
	if strings.TrimSpace(req.Prompt) == "" && len(content) == 0 {
		return nil, fmt.Errorf("%w: prompt or reference media is required", ErrInvalidTaskRequest)
	}
	caps.Audio = req.Audio
	caps.WebSearch = enabledVideoParam(req.Params, "web_search")
	if enabledVideoParam(req.Params, "return_last_frame") {
		caps.LastFrame = true
	}

	var prepared *preparedTaskRequest
	var validationErr error
	_, _, err = e.router.SelectCompatible(ctx, req.Model, caps, req.ChannelID, acquireConcurrency, func(channel *VideoChannel, key *VideoChannelKey) bool {
		vendorModel, supported := ResolveVideoVendorModel(channel.Models, req.Model)
		if !supported {
			return false
		}
		adapter := e.registry.Get(channel.AdapterType, channel, key)
		if adapter == nil {
			validationErr = fmt.Errorf("video adapter %q is not registered", channel.AdapterType)
			return false
		}
		providerRequest := &GenerateRequest{
			Model: vendorModel, Prompt: req.Prompt, Resolution: req.Resolution,
			Ratio: req.Ratio, Duration: req.Duration, Audio: req.Audio,
			TaskMode: req.TaskMode, Content: append([]ContentItem(nil), req.Content...), Params: req.Params,
			ServiceTier: req.ServiceTier,
			TaskID:      requestID, TokenID: req.TokenID, Channel: channel, Key: key,
		}
		if req.ServiceTier != "standard" && channel.AdapterType != AdapterTypeGeneric {
			validationErr = fmt.Errorf("service tier %q is not supported by adapter %q", req.ServiceTier, channel.AdapterType)
			return false
		}
		if validator, ok := adapter.(RequestValidator); ok {
			if validateErr := validator.ValidateRequest(ctx, providerRequest); validateErr != nil {
				validationErr = validateErr
				return false
			}
		}
		prepared = &preparedTaskRequest{
			channel: channel, key: key, adapter: adapter,
			vendorModel: vendorModel, provider: providerRequest,
		}
		return true
	})
	if err != nil {
		if validationErr != nil {
			return nil, fmt.Errorf("%w: no compatible video channel: %v", ErrInvalidTaskRequest, validationErr)
		}
		return nil, fmt.Errorf("route: %w", err)
	}
	return prepared, nil
}

func enabledVideoParam(params map[string]any, name string) bool {
	value, ok := params[name].(bool)
	return ok && value
}

func boolPointer(value bool) *bool { return &value }

func (e *Engine) validateContent(ctx context.Context, tokenID uint, requestedMode string, content []ContentItem) ([]ContentItem, string, RequiredCaps, error) {
	mode := strings.TrimSpace(requestedMode)
	if mode != "" && mode != "text" && mode != "references" && mode != "first_frame" &&
		mode != "first_last_frame" && mode != "multimodal" && mode != "video_edit" && mode != "video_extension" {
		return nil, "", RequiredCaps{}, fmt.Errorf("%w: unsupported task_mode %q", ErrInvalidTaskRequest, mode)
	}
	result := append([]ContentItem(nil), content...)
	caps := RequiredCaps{}
	hasReference := false
	counts := map[string]int{"image": 0, "video": 0, "audio": 0}
	roleCounts := make(map[string]int)
	clientReferenceIDs := make(map[string]struct{})
	assets := NewAssetService(e.db)
	for i := range result {
		item := &result[i]
		item.Type = strings.TrimSpace(item.Type)
		item.Role = strings.TrimSpace(item.Role)
		item.ClientRefID = strings.TrimSpace(item.ClientRefID)
		item.AssetID = strings.TrimSpace(item.AssetID)
		item.URL = strings.TrimSpace(item.URL)
		if item.Type == "text" {
			if item.Text == "" || item.Role != "" || item.AssetID != "" || item.URL != "" {
				return nil, "", caps, fmt.Errorf("%w: invalid text content at index %d", ErrInvalidTaskRequest, i)
			}
			continue
		}
		expectedKind, ok := contentKind(item.Type)
		if !ok {
			return nil, "", caps, fmt.Errorf("%w: unsupported content type %q", ErrInvalidTaskRequest, item.Type)
		}
		hasReference = true
		counts[expectedKind]++
		roleCounts[item.Role]++
		if item.ClientRefID == "" {
			item.ClientRefID = fmt.Sprintf("ref_%d", i+1)
		}
		if _, exists := clientReferenceIDs[item.ClientRefID]; exists {
			return nil, "", caps, fmt.Errorf("%w: content %d has duplicate client_ref_id %q", ErrInvalidTaskRequest, i, item.ClientRefID)
		}
		clientReferenceIDs[item.ClientRefID] = struct{}{}
		limits := map[string]int{"image": 30, "video": 10, "audio": 10}
		if counts[expectedKind] > limits[expectedKind] {
			return nil, "", caps, fmt.Errorf("%w: at most %d %s references are allowed", ErrInvalidTaskRequest, limits[expectedKind], expectedKind)
		}
		if counts["image"]+counts["video"]+counts["audio"] > 50 {
			return nil, "", caps, fmt.Errorf("%w: at most 50 reference items are allowed", ErrInvalidTaskRequest)
		}
		if !validContentRole(expectedKind, item.Role) {
			return nil, "", caps, fmt.Errorf("%w: content %d has invalid role %q", ErrInvalidTaskRequest, i, item.Role)
		}
		if item.Text != "" {
			return nil, "", caps, fmt.Errorf("%w: media content %d cannot contain text", ErrInvalidTaskRequest, i)
		}
		if (item.AssetID == "") == (item.URL == "") {
			return nil, "", caps, fmt.Errorf("%w: content %d requires exactly one of asset_id or url", ErrInvalidTaskRequest, i)
		}
		if item.AssetID != "" {
			asset, err := assets.GetReady(ctx, tokenID, item.AssetID)
			if err != nil {
				return nil, "", caps, err
			}
			if asset.Kind != expectedKind {
				return nil, "", caps, fmt.Errorf("%w: content %d kind does not match asset", ErrInvalidTaskRequest, i)
			}
			if item.DurationSeconds == 0 && asset.DurationSeconds != nil {
				item.DurationSeconds = *asset.DurationSeconds
			}
		} else if err := safeurl.Validate(ctx, item.URL); err != nil {
			return nil, "", caps, fmt.Errorf("%w: unsafe content url at index %d: %v", ErrInvalidTaskRequest, i, err)
		}
		if item.DurationSeconds < 0 {
			return nil, "", caps, fmt.Errorf("%w: duration_seconds must be positive at content %d", ErrInvalidTaskRequest, i)
		}
		switch item.Role {
		case "first_frame":
			if expectedKind != "image" {
				return nil, "", caps, fmt.Errorf("%w: first_frame must be an image", ErrInvalidTaskRequest)
			}
			if caps.FirstFrame {
				return nil, "", caps, fmt.Errorf("%w: first_frame may only appear once", ErrInvalidTaskRequest)
			}
			caps.FirstFrame = true
		case "last_frame":
			if expectedKind != "image" {
				return nil, "", caps, fmt.Errorf("%w: last_frame must be an image", ErrInvalidTaskRequest)
			}
			if caps.LastFrame {
				return nil, "", caps, fmt.Errorf("%w: last_frame may only appear once", ErrInvalidTaskRequest)
			}
			caps.LastFrame = true
		}
	}
	if mode == "" {
		mode = "text"
		switch {
		case roleCounts["edit_source"] > 0:
			mode = "video_edit"
		case roleCounts["source_video"] > 0:
			mode = "video_extension"
		case roleCounts["first_frame"] == 1 && roleCounts["last_frame"] == 1 && len(result) == 2:
			mode = "first_last_frame"
		case roleCounts["first_frame"] == 1 && len(result) == 1:
			mode = "first_frame"
		case hasReference:
			mode = "multimodal"
		}
	}
	if mode == "text" && hasReference {
		return nil, "", caps, fmt.Errorf("%w: text mode cannot contain reference media", ErrInvalidTaskRequest)
	}
	if mode == "references" && !hasReference {
		return nil, "", caps, fmt.Errorf("%w: references mode requires reference media", ErrInvalidTaskRequest)
	}
	if mode == "first_frame" && (roleCounts["first_frame"] != 1 || len(result) != 1) {
		return nil, "", caps, fmt.Errorf("%w: first_frame mode requires exactly one first_frame image", ErrInvalidTaskRequest)
	}
	if mode == "first_last_frame" && (roleCounts["first_frame"] != 1 || roleCounts["last_frame"] != 1 || len(result) != 2) {
		return nil, "", caps, fmt.Errorf("%w: first_last_frame mode requires one first_frame and one last_frame image", ErrInvalidTaskRequest)
	}
	if mode == "multimodal" && !hasReference {
		return nil, "", caps, fmt.Errorf("%w: multimodal mode requires reference media", ErrInvalidTaskRequest)
	}
	if mode == "video_edit" && (roleCounts["edit_source"] != 1 || counts["video"] != 1) {
		return nil, "", caps, fmt.Errorf("%w: video_edit mode requires exactly one edit_source video", ErrInvalidTaskRequest)
	}
	if mode == "video_extension" && !hasReference {
		return nil, "", caps, fmt.Errorf("%w: video_extension mode requires reference media", ErrInvalidTaskRequest)
	}
	if mode == "video_extension" && (roleCounts["source_video"] != 1 || counts["video"] != 1) {
		return nil, "", caps, fmt.Errorf("%w: video_extension mode requires exactly one source_video", ErrInvalidTaskRequest)
	}
	return result, mode, caps, nil
}

func contentKind(contentType string) (string, bool) {
	switch contentType {
	case "image_url":
		return "image", true
	case "video_url":
		return "video", true
	case "audio_url":
		return "audio", true
	default:
		return "", false
	}
}

func validContentRole(kind, role string) bool {
	switch kind {
	case "image":
		return role == "first_frame" || role == "last_frame" || role == "reference_image"
	case "video":
		return role == "reference_video" || role == "source_video" || role == "edit_source"
	case "audio":
		return role == "reference_audio"
	default:
		return false
	}
}

func validateVideoCallback(ctx context.Context, callback string) error {
	callback = strings.TrimSpace(callback)
	if callback == "" {
		return nil
	}
	if err := safeurl.Validate(ctx, callback); err != nil {
		return fmt.Errorf("%w: invalid callback url: %v", ErrInvalidTaskRequest, err)
	}
	return nil
}

func videoPrice(
	ctx context.Context,
	db *gorm.DB,
	channel *VideoChannel,
	key *VideoChannelKey,
	adapter Adapter,
	request *GenerateRequest,
) (*EstimateTaskResult, error) {
	type pricingConfig struct {
		Mode        string  `json:"mode"`
		FixedPrice  float64 `json:"fixed_price"`
		MarkupRatio float64 `json:"markup_ratio"`
	}
	pricing := pricingConfig{Mode: "fixed", MarkupRatio: 1}
	formalPricing := channel != nil && strings.TrimSpace(channel.PricingMode) != ""
	if formalPricing {
		pricing.Mode = strings.TrimSpace(channel.PricingMode)
		pricing.FixedPrice, _ = channel.FixedPrice.Float64()
		pricing.MarkupRatio, _ = channel.MarkupRatio.Float64()
	} else if channel != nil && len(channel.Pricing) > 0 {
		if err := json.Unmarshal(channel.Pricing, &pricing); err != nil {
			return nil, fmt.Errorf("invalid video pricing: %w", err)
		}
	}
	if pricing.Mode == "" {
		pricing.Mode = "fixed"
	}
	if pricing.Mode != "fixed" && pricing.Mode != "upstream_estimate" {
		return nil, fmt.Errorf("unsupported video pricing mode %q", pricing.Mode)
	}
	if pricing.FixedPrice < 0 || pricing.MarkupRatio < 0 {
		return nil, fmt.Errorf("invalid negative video pricing")
	}
	if pricing.MarkupRatio == 0 {
		pricing.MarkupRatio = 1
	}
	base := decimal.NewFromFloat(pricing.FixedPrice)
	var providerEstimate *ProviderEstimate
	if pricing.Mode == "upstream_estimate" {
		var estimateErr error
		estimator, estimatorOK := adapter.(Estimator)
		detailedEstimator, detailedOK := adapter.(DetailedEstimator)
		if !estimatorOK && !detailedOK {
			return nil, ErrEstimateNotSupported
		}
		// Upstream estimates use the same material references as submission.
		// Resolvers cache provider objects on the asset, so submission reuses them.
		if err := ResolveGenerateRequestAssets(ctx, db, channel, key, request.TaskID, request.TokenID, request); err != nil {
			return nil, fmt.Errorf("resolve estimate assets: %w", err)
		}
		var upstreamCost float64
		if detailedOK {
			providerEstimate, estimateErr = detailedEstimator.EstimateDetailed(ctx, request)
			if providerEstimate != nil {
				upstreamCost = providerEstimate.EstimatedCost
			}
		} else {
			upstreamCost, estimateErr = estimator.Estimate(ctx, request)
		}
		if estimateErr != nil {
			return nil, fmt.Errorf("estimate upstream video cost: %w", estimateErr)
		}
		if upstreamCost < 0 || math.IsNaN(upstreamCost) || math.IsInf(upstreamCost, 0) {
			return nil, errors.New("upstream video estimate cannot be negative")
		}
		base = decimal.NewFromFloat(upstreamCost)
	}
	markup := decimal.NewFromFloat(pricing.MarkupRatio)
	estimated := base.Mul(markup)
	snapshotValues := map[string]any{
		"mode":         pricing.Mode,
		"markup_ratio": markup.String(), "reserved_cost": estimated.String(),
	}
	if pricing.Mode == "fixed" {
		snapshotValues["fixed_price"] = base.String()
	} else {
		snapshotValues["upstream_estimated_cost"] = base.String()
		if providerEstimate != nil {
			snapshotValues["provider_estimate"] = providerEstimate
		}
	}
	snapshot, _ := json.Marshal(snapshotValues)
	return &EstimateTaskResult{
		EstimatedCost: estimated, BaseCost: base, MarkupRatio: markup,
		PricingMode: pricing.Mode, PricingSnapshot: datatypes.JSON(snapshot),
		ProviderEstimate: providerEstimate,
	}, nil
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
