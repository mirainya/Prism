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
	UserID     uint
	TokenID    uint
	ChannelID  uint
	CallID     string
	RequestID  string
	Endpoint   string
	Operation  string
	Model      string
	Prompt     string
	Resolution string
	Ratio      string
	Duration   int
	Audio      bool
	TaskMode   string
	Content    []ContentItem
	Params     map[string]any
	Callback   string
}

type CreateTaskResult struct {
	TaskID string
}

type EstimateTaskResult struct {
	EstimatedCost   decimal.Decimal
	BaseCost        decimal.Decimal
	MarkupRatio     decimal.Decimal
	PricingMode     string
	PricingSnapshot datatypes.JSON
}

type preparedTaskRequest struct {
	channel  *VideoChannel
	key      *VideoChannelKey
	adapter  Adapter
	provider *GenerateRequest
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

	task := &VideoTask{
		ID:            taskID,
		CallID:        callID,
		UserID:        req.UserID,
		TokenID:       req.TokenID,
		Model:         req.Model,
		Status:        VideoTaskStatusQueued,
		TaskMode:      req.TaskMode,
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
			ResourceType: "video_task", ResourceID: taskID,
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
		adapter := e.registry.Get(channel.AdapterType, channel, key)
		if adapter == nil {
			validationErr = fmt.Errorf("video adapter %q is not registered", channel.AdapterType)
			return false
		}
		providerRequest := &GenerateRequest{
			Model: req.Model, Prompt: req.Prompt, Resolution: req.Resolution,
			Ratio: req.Ratio, Duration: req.Duration, Audio: req.Audio,
			TaskMode: req.TaskMode, Content: append([]ContentItem(nil), req.Content...), Params: req.Params,
			TaskID: requestID, TokenID: req.TokenID, Channel: channel, Key: key,
		}
		if validator, ok := adapter.(RequestValidator); ok {
			if validateErr := validator.ValidateRequest(ctx, providerRequest); validateErr != nil {
				validationErr = validateErr
				return false
			}
		}
		prepared = &preparedTaskRequest{channel: channel, key: key, adapter: adapter, provider: providerRequest}
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

func (e *Engine) validateContent(ctx context.Context, tokenID uint, requestedMode string, content []ContentItem) ([]ContentItem, string, RequiredCaps, error) {
	mode := strings.TrimSpace(requestedMode)
	if mode != "" && mode != "text" && mode != "references" && mode != "video_extension" {
		return nil, "", RequiredCaps{}, fmt.Errorf("%w: unsupported task_mode %q", ErrInvalidTaskRequest, mode)
	}
	result := append([]ContentItem(nil), content...)
	caps := RequiredCaps{}
	hasReference := false
	counts := map[string]int{"image": 0, "video": 0, "audio": 0}
	assets := NewAssetService(e.db)
	for i := range result {
		item := &result[i]
		item.Type = strings.TrimSpace(item.Type)
		item.Role = strings.TrimSpace(item.Role)
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
		if hasReference {
			mode = "references"
		}
	}
	if mode == "text" && hasReference {
		return nil, "", caps, fmt.Errorf("%w: text mode cannot contain reference media", ErrInvalidTaskRequest)
	}
	if mode == "references" && !hasReference {
		return nil, "", caps, fmt.Errorf("%w: references mode requires reference media", ErrInvalidTaskRequest)
	}
	if mode == "video_extension" && !hasReference {
		return nil, "", caps, fmt.Errorf("%w: video_extension mode requires reference media", ErrInvalidTaskRequest)
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
		return role == "reference_video"
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
	if channel != nil && len(channel.Pricing) > 0 {
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
	if pricing.Mode == "upstream_estimate" {
		estimator, ok := adapter.(Estimator)
		if !ok {
			return nil, ErrEstimateNotSupported
		}
		// Upstream estimates use the same material references as submission.
		// Resolvers cache provider objects on the asset, so submission reuses them.
		if err := ResolveGenerateRequestAssets(ctx, db, channel, key, request.TaskID, request.TokenID, request); err != nil {
			return nil, fmt.Errorf("resolve estimate assets: %w", err)
		}
		upstreamCost, err := estimator.Estimate(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("estimate upstream video cost: %w", err)
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
	}
	snapshot, _ := json.Marshal(snapshotValues)
	return &EstimateTaskResult{
		EstimatedCost: estimated, BaseCost: base, MarkupRatio: markup,
		PricingMode: pricing.Mode, PricingSnapshot: datatypes.JSON(snapshot),
	}, nil
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
