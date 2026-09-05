package console

import (
	"context"
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
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/pkg/errors"
	"gorm.io/gorm"
)

var playgroundVideoEngine *video.Engine

type playgroundVideoModelOptions struct {
	Resolutions                   []string                           `json:"resolutions,omitempty"`
	Ratios                        []string                           `json:"ratios,omitempty"`
	DurationMin                   int                                `json:"duration_min,omitempty"`
	DurationMax                   int                                `json:"duration_max,omitempty"`
	DurationMaxWithVideoReference int                                `json:"duration_max_with_video_reference,omitempty"`
	DurationOptions               []int                              `json:"duration_options,omitempty"`
	TaskTypes                     []string                           `json:"task_types,omitempty"`
	ServiceTiers                  []string                           `json:"service_tiers,omitempty"`
	ServiceTierOptions            []playgroundVideoServiceTierOption `json:"service_tier_options,omitempty"`
	RequireVisualMediaWithAudio   bool                               `json:"require_visual_media_with_audio,omitempty"`
	AllowGeneratedAudio           *bool                              `json:"allow_generated_audio,omitempty"`
	AllowedRoles                  []string                           `json:"allowed_roles,omitempty"`
	MaxImages                     int                                `json:"max_images,omitempty"`
	MaxVideos                     int                                `json:"max_videos,omitempty"`
	MaxAudios                     int                                `json:"max_audios,omitempty"`
	MaxMedia                      int                                `json:"max_media,omitempty"`
	MediaDurationMin              float64                            `json:"media_duration_min,omitempty"`
	MediaDurationMax              float64                            `json:"media_duration_max,omitempty"`
	MaxVideoDuration              float64                            `json:"max_video_duration_total,omitempty"`
	MaxAudioDuration              float64                            `json:"max_audio_duration_total,omitempty"`
	Parameters                    []playgroundVideoParameter         `json:"parameters,omitempty"`
	AllowLocalCancel              bool                               `json:"allow_local_cancel"`
	CancelStatuses                []string                           `json:"cancel_statuses"`
}

type playgroundVideoServiceTierOption struct {
	Value            string   `json:"value"`
	Label            string   `json:"label"`
	AddsResolutions  []string `json:"adds_resolutions,omitempty"`
	SurchargePercent float64  `json:"surcharge_percent,omitempty"`
}

type playgroundVideoServiceTierSettings struct {
	Label            string         `json:"label"`
	AddsResolutions  []string       `json:"adds_resolutions"`
	SurchargePercent float64        `json:"surcharge_percent"`
	RequestParams    map[string]any `json:"request_params"`
}

type playgroundVideoParameterOption struct {
	Label           string   `json:"label"`
	Value           any      `json:"value"`
	AddsResolutions []string `json:"adds_resolutions,omitempty"`
}

type playgroundVideoParameter struct {
	Name          string                           `json:"name"`
	Label         string                           `json:"label"`
	Type          string                           `json:"type"`
	Default       any                              `json:"default,omitempty"`
	Min           *float64                         `json:"min,omitempty"`
	Max           *float64                         `json:"max,omitempty"`
	Options       []playgroundVideoParameterOption `json:"options"`
	TaskModes     []string                         `json:"task_modes,omitempty"`
	ConflictsWith []string                         `json:"conflicts_with,omitempty"`
}

type playgroundVideoChannelOption struct {
	ID           uint                                   `json:"id"`
	Name         string                                 `json:"name"`
	Models       []string                               `json:"models"`
	ModelOptions map[string]playgroundVideoModelOptions `json:"model_options"`
}

type playgroundVideoModelsResponse struct {
	Models       []string                               `json:"models"`
	ModelOptions map[string]playgroundVideoModelOptions `json:"model_options"`
	Channels     []playgroundVideoChannelOption         `json:"channels"`
}

type playgroundVideoModelValidation struct {
	Resolutions                   []string                   `json:"resolutions"`
	Ratios                        []string                   `json:"ratios"`
	DurationMin                   int                        `json:"duration_min"`
	DurationMax                   int                        `json:"duration_max"`
	DurationMaxWithVideoReference int                        `json:"duration_max_with_video_reference"`
	TaskModes                     []string                   `json:"task_modes"`
	RequireMedia                  bool                       `json:"require_media"`
	RequireVisualMediaWithAudio   bool                       `json:"require_visual_media_with_audio"`
	AllowGeneratedAudio           *bool                      `json:"allow_generated_audio"`
	AllowedRoles                  []string                   `json:"allowed_roles"`
	MaxImages                     int                        `json:"max_images"`
	MaxVideos                     int                        `json:"max_videos"`
	MaxAudios                     int                        `json:"max_audios"`
	MaxMedia                      int                        `json:"max_media"`
	MediaDurationMin              float64                    `json:"media_duration_min"`
	MediaDurationMax              float64                    `json:"media_duration_max"`
	MaxVideoDuration              float64                    `json:"max_video_duration_total"`
	MaxAudioDuration              float64                    `json:"max_audio_duration_total"`
	Parameters                    []playgroundVideoParameter `json:"parameters"`
	AvailableUntil                string                     `json:"available_until"`
}

type playgroundVideoAdapterSettings struct {
	ServiceTiers map[string]playgroundVideoServiceTierSettings `json:"service_tiers"`
	Validation   struct {
		Models map[string]playgroundVideoModelValidation `json:"models"`
	} `json:"validation"`
	Cancel struct {
		Enabled         bool     `json:"enabled"`
		AllowedStatuses []string `json:"allowed_statuses"`
	} `json:"cancel"`
	LocalCancel struct {
		Enabled        *bool    `json:"enabled"`
		DisabledModels []string `json:"disabled_models"`
	} `json:"local_cancel"`
}

var playgroundVideoTaskTypeOrder = []string{"text", "first_frame", "first_last_frame", "multimodal", "video_edit", "video_extension"}

func SetVideoEngine(e *video.Engine) {
	playgroundVideoEngine = e
}

// PlaygroundListVideoModels GET /api/playground/:token_id/videos/models
func PlaygroundListVideoModels(c *gin.Context) {
	if _, ok := getPlaygroundToken(c); !ok {
		return
	}
	result, err := listVideoModels(c.Request.Context())
	if err != nil {
		resp.InternalError(c, errors.ErrInternalError)
		return
	}
	resp.Success(c, result)
}

// listVideoModels returns the public, configuration-driven video capabilities
// shared by the playground and the authenticated API documentation.
func listVideoModels(ctx context.Context) (*playgroundVideoModelsResponse, error) {

	var channels []video.VideoChannel
	if err := model.DB().Where("status = ?", "active").Order("priority DESC, id ASC").Find(&channels).Error; err != nil {
		return nil, err
	}
	var activeKeys []video.VideoChannelKey
	if err := model.DB().Where("status = ?", "active").Order("channel_id ASC, id ASC").Find(&activeKeys).Error; err != nil {
		return nil, err
	}
	representativeKeys := make(map[uint]*video.VideoChannelKey, len(activeKeys))
	channelKeys := make(map[uint][]*video.VideoChannelKey, len(activeKeys))
	for index := range activeKeys {
		key := &activeKeys[index]
		channelKeys[key.ChannelID] = append(channelKeys[key.ChannelID], key)
		if representativeKeys[key.ChannelID] == nil {
			representativeKeys[key.ChannelID] = key
		}
	}
	discoveredCapabilities := discoverPlaygroundVideoCapabilities(ctx, channels, channelKeys)

	seen := make(map[string]bool)
	var models []string
	modelOptions := make(map[string]playgroundVideoModelOptions)
	channelOptions := make([]playgroundVideoChannelOption, 0, len(channels))
	for _, ch := range channels {
		if representativeKeys[ch.ID] == nil {
			continue
		}
		mappings, err := video.ParseVideoModelMappings(ch.Models)
		if err != nil {
			continue
		}
		var envelope struct {
			Adapter playgroundVideoAdapterSettings `json:"adapter"`
		}
		_ = json.Unmarshal(ch.ExtraConfig, &envelope)
		discovered := discoveredCapabilities[ch.ID]
		availableMappings := make([]video.VideoModelMapping, 0, len(mappings))
		for _, mapping := range mappings {
			rule := envelope.Adapter.Validation.Models[mapping.VendorModel]
			if !playgroundVideoModelAvailable(rule.AvailableUntil) {
				continue
			}
			if len(discovered) > 0 {
				if _, exists := discovered[mapping.VendorModel]; !exists {
					continue
				}
			}
			availableMappings = append(availableMappings, mapping)
		}
		mappings = availableMappings
		if len(mappings) == 0 {
			continue
		}
		for _, mapping := range mappings {
			if !seen[mapping.ModelName] {
				seen[mapping.ModelName] = true
				models = append(models, mapping.ModelName)
			}
		}
		publicModels := make([]string, 0, len(mappings))
		perChannelOptions := make(map[string]playgroundVideoModelOptions, len(mappings))
		for _, mapping := range mappings {
			publicModels = append(publicModels, mapping.ModelName)
			options := envelope.Adapter.Validation.Models[mapping.VendorModel]
			channelModelOptions := playgroundVideoOptionsForChannel(ch, mapping.VendorModel, options, envelope.Adapter)
			if capability, exists := discovered[mapping.VendorModel]; exists {
				channelModelOptions = restrictPlaygroundVideoOptions(channelModelOptions, capability)
			}
			perChannelOptions[mapping.ModelName] = channelModelOptions

			current, exists := modelOptions[mapping.ModelName]
			if !exists {
				modelOptions[mapping.ModelName] = clonePlaygroundVideoOptions(channelModelOptions)
			} else {
				modelOptions[mapping.ModelName] = mergePlaygroundVideoOptions(current, channelModelOptions)
			}
		}
		channelOptions = append(channelOptions, playgroundVideoChannelOption{
			ID: ch.ID, Name: ch.Name, Models: publicModels, ModelOptions: perChannelOptions,
		})
	}
	for modelName, options := range modelOptions {
		options.TaskTypes = orderVideoTaskTypes(options.TaskTypes)
		modelOptions[modelName] = options
	}

	return &playgroundVideoModelsResponse{Models: models, ModelOptions: modelOptions, Channels: channelOptions}, nil
}

func playgroundVideoOptionsForChannel(
	channel video.VideoChannel,
	modelName string,
	rule playgroundVideoModelValidation,
	settings playgroundVideoAdapterSettings,
) playgroundVideoModelOptions {
	allowLocalCancel := true
	cancelStatuses := make([]string, 0)
	switch channel.AdapterType {
	case "generic":
		allowLocalCancel = channel.EffectiveCancelMode() != video.CancelModeDisabled
		for _, disabledModel := range settings.LocalCancel.DisabledModels {
			if disabledModel == modelName {
				allowLocalCancel = false
				break
			}
		}
		if channel.EffectiveCancelMode() == video.CancelModeProvider && settings.Cancel.Enabled {
			cancelStatuses = append([]string(nil), settings.Cancel.AllowedStatuses...)
			if len(cancelStatuses) == 0 {
				cancelStatuses = []string{string(video.VideoTaskStatusSubmitted), string(video.VideoTaskStatusTracking)}
			}
		}
	case "seedance":
		if channel.EffectiveCancelMode() == video.CancelModeProvider {
			cancelStatuses = []string{string(video.VideoTaskStatusSubmitted)}
		}
	}

	serviceTierOptions := configuredVideoServiceTierOptions(settings, rule.Parameters)
	return playgroundVideoModelOptions{
		Resolutions:                   append([]string(nil), rule.Resolutions...),
		Ratios:                        append([]string(nil), rule.Ratios...),
		DurationMin:                   rule.DurationMin,
		DurationMax:                   rule.DurationMax,
		DurationMaxWithVideoReference: rule.DurationMaxWithVideoReference,
		TaskTypes:                     orderVideoTaskTypes(videoTaskTypesForChannel(channel, rule)),
		ServiceTiers:                  videoServiceTierValues(serviceTierOptions),
		ServiceTierOptions:            serviceTierOptions,
		RequireVisualMediaWithAudio:   rule.RequireVisualMediaWithAudio,
		AllowGeneratedAudio:           cloneBool(rule.AllowGeneratedAudio),
		AllowedRoles:                  append([]string(nil), rule.AllowedRoles...),
		MaxImages:                     rule.MaxImages,
		MaxVideos:                     rule.MaxVideos,
		MaxAudios:                     rule.MaxAudios,
		MaxMedia:                      rule.MaxMedia,
		MediaDurationMin:              rule.MediaDurationMin,
		MediaDurationMax:              rule.MediaDurationMax,
		MaxVideoDuration:              rule.MaxVideoDuration,
		MaxAudioDuration:              rule.MaxAudioDuration,
		Parameters:                    filterServiceTierParameters(rule.Parameters, settings),
		AllowLocalCancel:              allowLocalCancel,
		CancelStatuses:                cancelStatuses,
	}
}

func configuredVideoServiceTierOptions(
	settings playgroundVideoAdapterSettings,
	parameters []playgroundVideoParameter,
) []playgroundVideoServiceTierOption {
	if len(settings.ServiceTiers) == 0 {
		return []playgroundVideoServiceTierOption{{Value: "standard", Label: "标准队列"}}
	}
	result := make([]playgroundVideoServiceTierOption, 0, len(settings.ServiceTiers))
	for _, tier := range []string{"standard", "priority", "vip"} {
		definition, exists := settings.ServiceTiers[tier]
		if !exists {
			continue
		}
		label := definition.Label
		if label == "" {
			label = map[string]string{"standard": "标准队列", "priority": "优先队列", "vip": "积分 VIP"}[tier]
		}
		addsResolutions := append([]string(nil), definition.AddsResolutions...)
		if len(addsResolutions) == 0 {
			addsResolutions = serviceTierParameterResolutions(definition.RequestParams, parameters)
		}
		result = append(result, playgroundVideoServiceTierOption{
			Value: tier, Label: label, AddsResolutions: addsResolutions,
			SurchargePercent: definition.SurchargePercent,
		})
	}
	if len(result) == 0 {
		return []playgroundVideoServiceTierOption{{Value: "standard", Label: "标准队列"}}
	}
	return result
}

func videoServiceTierValues(options []playgroundVideoServiceTierOption) []string {
	result := make([]string, 0, len(options))
	for _, option := range options {
		result = append(result, option.Value)
	}
	return result
}

func serviceTierParameterResolutions(requestParams map[string]any, parameters []playgroundVideoParameter) []string {
	result := make([]string, 0)
	for name, requestValue := range requestParams {
		for _, parameter := range parameters {
			if parameter.Name != name {
				continue
			}
			requestJSON, _ := json.Marshal(requestValue)
			for _, option := range parameter.Options {
				optionJSON, _ := json.Marshal(option.Value)
				if string(requestJSON) == string(optionJSON) {
					result = appendUniqueOrdered(result, option.AddsResolutions...)
				}
			}
		}
	}
	return result
}

func filterServiceTierParameters(
	parameters []playgroundVideoParameter,
	settings playgroundVideoAdapterSettings,
) []playgroundVideoParameter {
	controlled := make(map[string]struct{})
	for _, tier := range settings.ServiceTiers {
		for name := range tier.RequestParams {
			controlled[name] = struct{}{}
		}
	}
	result := make([]playgroundVideoParameter, 0, len(parameters))
	for _, parameter := range parameters {
		if _, exists := controlled[parameter.Name]; exists {
			continue
		}
		result = append(result, parameter)
	}
	return clonePlaygroundVideoParameters(result)
}

func mergePlaygroundVideoOptions(current, next playgroundVideoModelOptions) playgroundVideoModelOptions {
	current.Resolutions = mergeOptionalStringOptions(current.Resolutions, next.Resolutions)
	current.Ratios = mergeOptionalStringOptions(current.Ratios, next.Ratios)
	current.DurationMin = mergeMinimumConstraint(current.DurationMin, next.DurationMin)
	current.DurationMax = mergeMaximumConstraint(current.DurationMax, next.DurationMax)
	current.DurationMaxWithVideoReference = mergeMaximumConstraint(current.DurationMaxWithVideoReference, next.DurationMaxWithVideoReference)
	current.DurationOptions = mergeOptionalIntOptions(current.DurationOptions, next.DurationOptions)
	current.TaskTypes = appendUniqueOrdered(current.TaskTypes, next.TaskTypes...)
	current.ServiceTiers = appendUniqueOrdered(current.ServiceTiers, next.ServiceTiers...)
	current.ServiceTierOptions = mergePlaygroundVideoServiceTierOptions(current.ServiceTierOptions, next.ServiceTierOptions)
	current.RequireVisualMediaWithAudio = current.RequireVisualMediaWithAudio && next.RequireVisualMediaWithAudio
	current.AllowGeneratedAudio = mergeOptionalBool(current.AllowGeneratedAudio, next.AllowGeneratedAudio)
	current.AllowedRoles = mergeOptionalStringOptions(current.AllowedRoles, next.AllowedRoles)
	current.MaxImages = mergeMaximumConstraint(current.MaxImages, next.MaxImages)
	current.MaxVideos = mergeMaximumConstraint(current.MaxVideos, next.MaxVideos)
	current.MaxAudios = mergeMaximumConstraint(current.MaxAudios, next.MaxAudios)
	current.MaxMedia = mergeMaximumConstraint(current.MaxMedia, next.MaxMedia)
	current.MediaDurationMin = mergeMinimumFloatConstraint(current.MediaDurationMin, next.MediaDurationMin)
	current.MediaDurationMax = mergeMaximumFloatConstraint(current.MediaDurationMax, next.MediaDurationMax)
	current.MaxVideoDuration = mergeMaximumFloatConstraint(current.MaxVideoDuration, next.MaxVideoDuration)
	current.MaxAudioDuration = mergeMaximumFloatConstraint(current.MaxAudioDuration, next.MaxAudioDuration)
	current.Parameters = mergePlaygroundVideoParameters(current.Parameters, next.Parameters)
	current.AllowLocalCancel = current.AllowLocalCancel || next.AllowLocalCancel
	current.CancelStatuses = appendUniqueOrdered(current.CancelStatuses, next.CancelStatuses...)
	return current
}

func mergeOptionalStringOptions(current, next []string) []string {
	if len(current) == 0 || len(next) == 0 {
		return nil
	}
	return appendUniqueOrdered(current, next...)
}

func mergeOptionalIntOptions(current, next []int) []int {
	if len(current) == 0 || len(next) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(current)+len(next))
	result := make([]int, 0, len(current)+len(next))
	for _, values := range [][]int{current, next} {
		for _, value := range values {
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func mergeMinimumConstraint(current, next int) int {
	if current == 0 || next == 0 {
		return 0
	}
	return min(current, next)
}

func mergeMaximumConstraint(current, next int) int {
	if current == 0 || next == 0 {
		return 0
	}
	return max(current, next)
}

func mergeMinimumFloatConstraint(current, next float64) float64 {
	if current == 0 || next == 0 {
		return 0
	}
	return math.Min(current, next)
}

func mergeMaximumFloatConstraint(current, next float64) float64 {
	if current == 0 || next == 0 {
		return 0
	}
	return math.Max(current, next)
}

func mergeOptionalBool(current, next *bool) *bool {
	if current == nil || next == nil {
		return nil
	}
	value := *current || *next
	return &value
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePlaygroundVideoParameters(parameters []playgroundVideoParameter) []playgroundVideoParameter {
	result := make([]playgroundVideoParameter, len(parameters))
	for index, parameter := range parameters {
		result[index] = parameter
		result[index].Options = append([]playgroundVideoParameterOption(nil), parameter.Options...)
		for optionIndex := range result[index].Options {
			result[index].Options[optionIndex].AddsResolutions = append([]string(nil), parameter.Options[optionIndex].AddsResolutions...)
		}
		result[index].TaskModes = append([]string(nil), parameter.TaskModes...)
		result[index].ConflictsWith = append([]string(nil), parameter.ConflictsWith...)
	}
	return result
}

func clonePlaygroundVideoOptions(options playgroundVideoModelOptions) playgroundVideoModelOptions {
	options.Resolutions = append([]string(nil), options.Resolutions...)
	options.Ratios = append([]string(nil), options.Ratios...)
	options.DurationOptions = append([]int(nil), options.DurationOptions...)
	options.TaskTypes = append([]string(nil), options.TaskTypes...)
	options.ServiceTiers = append([]string(nil), options.ServiceTiers...)
	options.ServiceTierOptions = clonePlaygroundVideoServiceTierOptions(options.ServiceTierOptions)
	options.AllowGeneratedAudio = cloneBool(options.AllowGeneratedAudio)
	options.AllowedRoles = append([]string(nil), options.AllowedRoles...)
	options.Parameters = clonePlaygroundVideoParameters(options.Parameters)
	options.CancelStatuses = append([]string{}, options.CancelStatuses...)
	return options
}

func clonePlaygroundVideoServiceTierOptions(options []playgroundVideoServiceTierOption) []playgroundVideoServiceTierOption {
	result := append([]playgroundVideoServiceTierOption(nil), options...)
	for index := range result {
		result[index].AddsResolutions = append([]string(nil), result[index].AddsResolutions...)
	}
	return result
}

func mergePlaygroundVideoServiceTierOptions(current, next []playgroundVideoServiceTierOption) []playgroundVideoServiceTierOption {
	result := clonePlaygroundVideoServiceTierOptions(current)
	indexes := make(map[string]int, len(result))
	for index, option := range result {
		indexes[option.Value] = index
	}
	for _, option := range next {
		index, exists := indexes[option.Value]
		if !exists {
			indexes[option.Value] = len(result)
			result = append(result, clonePlaygroundVideoServiceTierOptions([]playgroundVideoServiceTierOption{option})[0])
			continue
		}
		result[index].AddsResolutions = appendUniqueOrdered(result[index].AddsResolutions, option.AddsResolutions...)
		if result[index].Label == "" {
			result[index].Label = option.Label
		}
		if result[index].SurchargePercent == 0 {
			result[index].SurchargePercent = option.SurchargePercent
		}
	}
	return result
}

func mergePlaygroundVideoParameters(current, next []playgroundVideoParameter) []playgroundVideoParameter {
	result := clonePlaygroundVideoParameters(current)
	indexes := make(map[string]int, len(result))
	for index, parameter := range result {
		indexes[parameter.Name] = index
	}
	for _, parameter := range next {
		index, exists := indexes[parameter.Name]
		if !exists {
			indexes[parameter.Name] = len(result)
			result = append(result, clonePlaygroundVideoParameters([]playgroundVideoParameter{parameter})[0])
			continue
		}
		seenValues := make(map[string]struct{}, len(result[index].Options))
		for _, option := range result[index].Options {
			encoded, _ := json.Marshal(option.Value)
			seenValues[string(encoded)] = struct{}{}
		}
		for _, option := range parameter.Options {
			encoded, _ := json.Marshal(option.Value)
			if _, found := seenValues[string(encoded)]; found {
				continue
			}
			seenValues[string(encoded)] = struct{}{}
			result[index].Options = append(result[index].Options, option)
		}
	}
	return result
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
		switch mode {
		case "text":
			return !rule.RequireMedia
		case "references":
			return true
		default:
			return false
		}
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
	if supportsMode("video_extension") {
		types = append(types, "video_extension")
	}
	if supportsMode("video_edit") {
		types = append(types, "video_edit")
	}
	if !supportsMode("references") {
		return orderVideoTaskTypes(types)
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
	return orderVideoTaskTypes(types)
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

	resp.Success(c, gin.H{"id": result.TaskID, "status": "queued", "service_tier": req.ServiceTier})
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
	response := gin.H{
		"estimated_cost": estimate.EstimatedCost.String(),
		"base_cost":      estimate.BaseCost.String(),
		"markup_ratio":   estimate.MarkupRatio.String(),
		"pricing_mode":   estimate.PricingMode,
		"service_tier":   req.ServiceTier,
	}
	if detail := estimate.ProviderEstimate; detail != nil {
		response["unit_cost"] = detail.UnitCost
		response["units"] = detail.Units
		response["billing_mode"] = detail.BillingMode
		response["billing_tier"] = detail.BillingTier
		response["pricing_source"] = detail.PricingSource
		response["currency"] = detail.Currency
	}
	resp.Success(c, response)
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
	if value, ok := raw["service_tier"].(string); ok {
		req.ServiceTier = value
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
		"service_tier": true,
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
		"id":           task.ID,
		"model":        task.Model,
		"service_tier": task.ServiceTier,
		"status":       string(task.Status),
		"progress":     task.Progress,
		"prompt":       task.Prompt,
		"resolution":   task.Resolution,
		"ratio":        task.Ratio,
		"duration":     task.Duration,
		"created_at":   task.CreatedAt.Format(time.RFC3339),
	}
	if metadata := video.DecodeProviderMetadata(task.ProviderMetadata); metadata != nil {
		detail["queue_status"] = metadata.QueueStatus
		detail["queue_position"] = metadata.QueuePosition
		detail["queue_limit"] = metadata.QueueLimit
		detail["priority_queue"] = metadata.PriorityQueue
		detail["h_channel_points_vip"] = metadata.PointsVIP
		detail["priority_surcharge_percent"] = metadata.PrioritySurchargePercent
	}
	if task.ErrorMessage != "" {
		detail["error_message"] = task.ErrorMessage
	}
	if len(task.ResultJSON) > 2 {
		var result any
		if json.Unmarshal(task.ResultJSON, &result) == nil {
			detail["result"] = result
		}
	}
	if task.CompletedAt != nil {
		detail["completed_at"] = task.CompletedAt.Format(time.RFC3339)
	}
	if middleware.GetUserRole(c) == string(model.UserRoleAdmin) {
		if len(task.ProviderResponse) > 2 {
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
			"id":           t.ID,
			"channel_id":   t.ChannelID,
			"model":        t.Model,
			"status":       string(t.Status),
			"progress":     t.Progress,
			"service_tier": t.ServiceTier,
			"prompt":       t.Prompt,
			"resolution":   t.Resolution,
			"ratio":        t.Ratio,
			"duration":     t.Duration,
			"created_at":   t.CreatedAt.Format(time.RFC3339),
		}
		if t.ErrorMessage != "" {
			item["error_message"] = t.ErrorMessage
		}
		if metadata := video.DecodeProviderMetadata(t.ProviderMetadata); metadata != nil {
			item["queue_status"] = metadata.QueueStatus
			item["queue_position"] = metadata.QueuePosition
			item["queue_limit"] = metadata.QueueLimit
			item["priority_queue"] = metadata.PriorityQueue
			item["h_channel_points_vip"] = metadata.PointsVIP
			item["priority_surcharge_percent"] = metadata.PrioritySurchargePercent
		}
		if len(t.ResultJSON) > 2 {
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

// PlaygroundPriorityQueueVideo upgrades a submitted video task using the
// channel's configured priority_queue action.
func PlaygroundPriorityQueueVideo(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}
	var task video.VideoTask
	if err := model.DB().Where("id = ? AND token_id = ?", c.Param("id"), token.ID).First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			resp.NotFound(c, errors.ErrTaskNotFound)
			return
		}
		resp.InternalError(c, errors.ErrInternalError)
		return
	}
	if playgroundVideoEngine == nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, "video engine not initialized")
		return
	}
	metadata, err := playgroundVideoEngine.UpgradeVideoTaskPriority(c.Request.Context(), &task)
	if err != nil {
		if stderrors.Is(err, service.ErrInsufficientTokenBalance) || stderrors.Is(err, service.ErrInsufficientUserBalance) {
			resp.BadRequest(c, errors.WithMessage(errors.ErrInsufficientQuota, err.Error()))
			return
		}
		if stderrors.Is(err, video.ErrActionNotSupported) {
			resp.ErrorMsg(c, http.StatusNotImplemented, 501, err.Error())
			return
		}
		if stderrors.Is(err, video.ErrActionNotAllowed) {
			resp.ErrorMsg(c, http.StatusConflict, 409, err.Error())
			return
		}
		resp.ErrorMsg(c, http.StatusBadGateway, 502, err.Error())
		return
	}
	resp.Success(c, gin.H{"id": task.ID, "action": "priority_queue", "service_tier": "priority", "queue": metadata})
}
