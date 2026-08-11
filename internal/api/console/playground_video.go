package console

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/pkg/errors"
	"gorm.io/gorm"
)

var playgroundVideoEngine *video.Engine

type playgroundVideoModelOptions struct {
	Resolutions []string `json:"resolutions,omitempty"`
	TaskTypes   []string `json:"task_types,omitempty"`
}

type playgroundVideoChannelOption struct {
	ID           uint                                   `json:"id"`
	Name         string                                 `json:"name"`
	Models       []string                               `json:"models"`
	ModelOptions map[string]playgroundVideoModelOptions `json:"model_options"`
}

type playgroundVideoModelValidation struct {
	Resolutions  []string `json:"resolutions"`
	TaskModes    []string `json:"task_modes"`
	RequireMedia bool     `json:"require_media"`
	AllowedRoles []string `json:"allowed_roles"`
}

var playgroundVideoTaskTypeOrder = []string{"text", "first_frame", "first_last_frame", "multimodal"}

func SetVideoEngine(e *video.Engine) {
	playgroundVideoEngine = e
}

// PlaygroundListVideoModels GET /api/playground/:token_id/videos/models
func PlaygroundListVideoModels(c *gin.Context) {
	if _, ok := getPlaygroundToken(c); !ok {
		return
	}

	var channels []video.VideoChannel
	if err := model.DB().Where("status = ?", "active").Order("priority DESC, id ASC").Find(&channels).Error; err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}
	var activeKeyChannelIDs []uint
	if err := model.DB().Model(&video.VideoChannelKey{}).
		Where("status = ?", "active").Distinct("channel_id").Pluck("channel_id", &activeKeyChannelIDs).Error; err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}
	activeChannels := make(map[uint]struct{}, len(activeKeyChannelIDs))
	for _, channelID := range activeKeyChannelIDs {
		activeChannels[channelID] = struct{}{}
	}

	seen := make(map[string]bool)
	var models []string
	modelOptions := make(map[string]playgroundVideoModelOptions)
	channelOptions := make([]playgroundVideoChannelOption, 0, len(channels))
	for _, ch := range channels {
		if _, available := activeChannels[ch.ID]; !available {
			continue
		}
		var ms []string
		if json.Unmarshal(ch.Models, &ms) != nil || len(ms) == 0 {
			continue
		}
		for _, m := range ms {
			if !seen[m] {
				seen[m] = true
				models = append(models, m)
			}
		}
		var envelope struct {
			Adapter struct {
				Validation struct {
					Models map[string]playgroundVideoModelValidation `json:"models"`
				} `json:"validation"`
			} `json:"adapter"`
		}
		_ = json.Unmarshal(ch.ExtraConfig, &envelope)
		perChannelOptions := make(map[string]playgroundVideoModelOptions, len(ms))
		for _, modelName := range ms {
			options := envelope.Adapter.Validation.Models[modelName]
			channelModelOptions := playgroundVideoModelOptions{
				Resolutions: append([]string(nil), options.Resolutions...),
				TaskTypes:   orderVideoTaskTypes(videoTaskTypesForChannel(ch, options)),
			}
			if len(channelModelOptions.TaskTypes) == 0 {
				channelModelOptions.TaskTypes = []string{"text", "multimodal"}
			}
			perChannelOptions[modelName] = channelModelOptions

			current := modelOptions[modelName]
			current.Resolutions = appendUniqueOrdered(current.Resolutions, options.Resolutions...)
			current.TaskTypes = appendUniqueOrdered(current.TaskTypes, channelModelOptions.TaskTypes...)
			modelOptions[modelName] = current
		}
		channelOptions = append(channelOptions, playgroundVideoChannelOption{
			ID: ch.ID, Name: ch.Name, Models: ms, ModelOptions: perChannelOptions,
		})
	}
	for modelName, options := range modelOptions {
		if len(options.TaskTypes) == 0 {
			options.TaskTypes = []string{"text", "multimodal"}
		}
		options.TaskTypes = orderVideoTaskTypes(options.TaskTypes)
		modelOptions[modelName] = options
	}

	resp.Success(c, gin.H{"models": models, "model_options": modelOptions, "channels": channelOptions})
}

func videoTaskTypesForChannel(channel video.VideoChannel, rule playgroundVideoModelValidation) []string {
	var capabilities map[string]bool
	_ = json.Unmarshal(channel.Capabilities, &capabilities)

	supportsMode := func(mode string) bool {
		if len(rule.TaskModes) > 0 {
			for _, candidate := range rule.TaskModes {
				if candidate == mode {
					return true
				}
			}
			return false
		}
		return mode != "text" || !rule.RequireMedia
	}
	supportsRole := func(role string) bool {
		if len(rule.AllowedRoles) == 0 {
			return true
		}
		for _, candidate := range rule.AllowedRoles {
			if candidate == role {
				return true
			}
		}
		return false
	}

	types := make([]string, 0, len(playgroundVideoTaskTypeOrder))
	if supportsMode("text") {
		types = append(types, "text")
	}
	if !supportsMode("references") {
		return types
	}
	firstFrame := capabilities["first_frame"] && supportsRole("first_frame")
	lastFrame := capabilities["last_frame"] && supportsRole("last_frame")
	if firstFrame {
		types = append(types, "first_frame")
	}
	if firstFrame && lastFrame {
		types = append(types, "first_last_frame")
	}
	if supportsRole("reference_image") || supportsRole("reference_video") || supportsRole("reference_audio") {
		types = append(types, "multimodal")
	}
	return types
}

func appendUniqueOrdered(target []string, values ...string) []string {
	seen := make(map[string]struct{}, len(target)+len(values))
	for _, value := range target {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		target = append(target, value)
	}
	return target
}

func orderVideoTaskTypes(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	ordered := make([]string, 0, len(values))
	for _, value := range playgroundVideoTaskTypeOrder {
		if _, ok := seen[value]; ok {
			ordered = append(ordered, value)
		}
	}
	return ordered
}

// PlaygroundCreateVideo POST /api/playground/:token_id/videos/generations
func PlaygroundCreateVideo(c *gin.Context) {
	token, ok := usePlaygroundToken(c)
	if !ok {
		return
	}

	if playgroundVideoEngine == nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "video engine not initialized")
		return
	}

	var raw map[string]any
	if err := c.ShouldBindJSON(&raw); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	req, err := buildPlaygroundVideoRequest(raw, token)
	if err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	result, err := playgroundVideoEngine.CreateTask(c.Request.Context(), req)
	if err != nil {
		if stderrors.Is(err, video.ErrInvalidTaskRequest) || stderrors.Is(err, video.ErrInvalidAsset) || stderrors.Is(err, video.ErrAssetNotReady) {
			resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
			return
		}
		if stderrors.Is(err, video.ErrAssetNotFound) {
			resp.NotFound(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
			return
		}
		if stderrors.Is(err, video.ErrNoChannel) || stderrors.Is(err, video.ErrNoKey) {
			resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, err.Error())
			return
		}
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, gin.H{"id": result.TaskID, "status": "queued"})
}

// PlaygroundEstimateVideo POST /api/playground/:token_id/videos/estimate
func PlaygroundEstimateVideo(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}
	if playgroundVideoEngine == nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, "video engine not initialized")
		return
	}
	var raw map[string]any
	if err := c.ShouldBindJSON(&raw); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}
	req, err := buildPlaygroundVideoRequest(raw, token)
	if err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}
	estimate, err := playgroundVideoEngine.EstimateTask(c.Request.Context(), req)
	if err != nil {
		writePlaygroundVideoEstimateError(c, err)
		return
	}
	resp.Success(c, gin.H{
		"estimated_cost": estimate.EstimatedCost.String(),
		"base_cost":      estimate.BaseCost.String(),
		"markup_ratio":   estimate.MarkupRatio.String(),
		"pricing_mode":   estimate.PricingMode,
	})
}

func buildPlaygroundVideoRequest(raw map[string]any, token *model.Token) (*video.CreateTaskRequest, error) {
	if token == nil {
		return nil, video.ErrInvalidTaskRequest
	}
	modelName, _ := raw["model"].(string)
	prompt, _ := raw["prompt"].(string)
	if modelName == "" {
		return nil, fmt.Errorf("model is required")
	}
	req := &video.CreateTaskRequest{
		UserID: token.UserID, TokenID: token.ID, Model: modelName, Prompt: prompt,
		TaskMode: "text", Audio: true,
	}
	if value, exists := raw["channel_id"]; exists && value != nil {
		number, ok := value.(float64)
		if !ok || number < 0 || number > 9007199254740991 || math.Trunc(number) != number {
			return nil, fmt.Errorf("channel_id must be a non-negative integer")
		}
		req.ChannelID = uint(number)
	}
	if value, ok := raw["resolution"].(string); ok {
		req.Resolution = value
	}
	if value, ok := raw["ratio"].(string); ok {
		req.Ratio = value
	}
	if value, ok := raw["duration"].(float64); ok {
		req.Duration = int(value)
	}
	if value, ok := raw["generate_audio"].(bool); ok {
		req.Audio = value
	}
	if value, ok := raw["task_mode"].(string); ok {
		req.TaskMode = value
	}
	if rawContent, ok := raw["content"]; ok {
		encoded, err := json.Marshal(rawContent)
		if err != nil {
			return nil, fmt.Errorf("invalid content: %w", err)
		}
		if err := json.Unmarshal(encoded, &req.Content); err != nil {
			return nil, fmt.Errorf("invalid content: %w", err)
		}
	}
	params := make(map[string]any)
	reserved := map[string]bool{
		"model": true, "prompt": true, "callback_url": true, "channel_id": true,
		"resolution": true, "ratio": true, "duration": true,
		"generate_audio": true, "task_mode": true, "content": true, "params": true,
	}
	for key, value := range raw {
		if !reserved[key] {
			params[key] = value
		}
	}
	if nested, ok := raw["params"].(map[string]any); ok {
		for key, value := range nested {
			params[key] = value
		}
	} else if value, exists := raw["params"]; exists && value != nil {
		return nil, fmt.Errorf("params must be an object")
	}
	if len(params) > 0 {
		req.Params = params
	}
	return req, nil
}

func writePlaygroundVideoEstimateError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, video.ErrInvalidTaskRequest), stderrors.Is(err, video.ErrInvalidAsset), stderrors.Is(err, video.ErrAssetNotReady):
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
	case stderrors.Is(err, video.ErrAssetNotFound):
		resp.NotFound(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
	case stderrors.Is(err, video.ErrNoChannel), stderrors.Is(err, video.ErrNoKey), stderrors.Is(err, video.ErrEngineUnavailable), stderrors.Is(err, video.ErrEstimateNotSupported):
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, err.Error())
	default:
		resp.ErrorMsg(c, http.StatusBadGateway, 502, err.Error())
	}
}

// PlaygroundGetVideo GET /api/playground/:token_id/videos/generations/:id
func PlaygroundGetVideo(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	taskID := c.Param("id")
	var task video.VideoTask
	if err := model.DB().Where("id = ? AND token_id = ?", taskID, token.ID).First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			resp.NotFound(c, errors.ErrTaskNotFound)
			return
		}
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	detail := gin.H{
		"id":         task.ID,
		"model":      task.Model,
		"status":     string(task.Status),
		"progress":   task.Progress,
		"prompt":     task.Prompt,
		"resolution": task.Resolution,
		"ratio":      task.Ratio,
		"duration":   task.Duration,
		"created_at": task.CreatedAt.Format(time.RFC3339),
	}
	if task.ErrorMessage != "" {
		detail["error_message"] = task.ErrorMessage
	}
	if task.ResultJSON != nil && len(task.ResultJSON) > 2 {
		var result any
		if json.Unmarshal(task.ResultJSON, &result) == nil {
			detail["result"] = result
		}
	}
	if task.CompletedAt != nil {
		detail["completed_at"] = task.CompletedAt.Format(time.RFC3339)
	}
	if middleware.GetUserRole(c) == string(model.UserRoleAdmin) {
		if task.ProviderResponse != nil && len(task.ProviderResponse) > 2 {
			var vendor any
			if json.Unmarshal(task.ProviderResponse, &vendor) == nil {
				detail["vendor_response"] = vendor
			}
		}
		detail["provider_task_id"] = task.ProviderTaskID
		detail["channel_id"] = task.ChannelID
		detail["key_id"] = task.KeyID
	}

	resp.Success(c, detail)
}

// PlaygroundListVideos GET /api/playground/:token_id/videos/generations
func PlaygroundListVideos(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	var tasks []video.VideoTask
	query := model.DB().Where("token_id = ?", token.ID).Order("created_at DESC").Limit(50)
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}
	if err := query.Find(&tasks).Error; err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	items := make([]gin.H, 0, len(tasks))
	for _, t := range tasks {
		item := gin.H{
			"id":         t.ID,
			"model":      t.Model,
			"status":     string(t.Status),
			"progress":   t.Progress,
			"prompt":     t.Prompt,
			"created_at": t.CreatedAt.Format(time.RFC3339),
		}
		if t.ErrorMessage != "" {
			item["error_message"] = t.ErrorMessage
		}
		if t.ResultJSON != nil && len(t.ResultJSON) > 2 {
			var result any
			if json.Unmarshal(t.ResultJSON, &result) == nil {
				item["result"] = result
			}
		}
		if t.CompletedAt != nil {
			item["completed_at"] = t.CompletedAt.Format(time.RFC3339)
		}
		items = append(items, item)
	}

	resp.Success(c, gin.H{"items": items, "total": len(items)})
}

// PlaygroundCancelVideo POST /api/playground/:token_id/videos/generations/:id/cancel
func PlaygroundCancelVideo(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	taskID := c.Param("id")
	var task video.VideoTask
	if err := model.DB().Where("id = ? AND token_id = ?", taskID, token.ID).First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			resp.NotFound(c, errors.ErrTaskNotFound)
			return
		}
		resp.InternalError(c, errors.ErrInternalError)
		return
	}

	if task.Status.IsTerminal() {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "task already in terminal state"))
		return
	}

	cancelled, err := playgroundVideoEngine.CancelVideoTask(c.Request.Context(), &task)
	if err != nil {
		if stderrors.Is(err, video.ErrCancelNotSupported) || stderrors.Is(err, video.ErrCancelNotAllowed) {
			resp.ErrorMsg(c, http.StatusConflict, 409, err.Error())
			return
		}
		resp.ErrorMsg(c, http.StatusBadGateway, 502, err.Error())
		return
	}
	status := video.VideoTaskStatusCancelled
	if !cancelled {
		if loadErr := model.DB().Select("status").First(&task, "id = ?", task.ID).Error; loadErr != nil {
			resp.InternalError(c, errors.ErrInternalError)
			return
		}
		status = task.Status
	}
	resp.Success(c, gin.H{"id": task.ID, "status": status})
}
