package console

import (
	"context"
	"encoding/json"
	"math"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/open"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/errors"
)

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
}

type playgroundVideoServiceTierOption struct {
	Value            string   `json:"value"`
	Label            string   `json:"label"`
	AddsResolutions  []string `json:"adds_resolutions,omitempty"`
	SurchargePercent float64  `json:"surcharge_percent,omitempty"`
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

type playgroundVideoModelsResponse struct {
	Models       []string                               `json:"models"`
	ModelOptions map[string]playgroundVideoModelOptions `json:"model_options"`
}

var playgroundVideoTaskTypeOrder = []string{"text", "first_frame", "first_last_frame", "multimodal", "video_edit", "video_extension"}

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
	db, err := model.DB().DB()
	if err != nil {
		return nil, err
	}
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	rows, err := store.ListActivePublicCatalog(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0)
	seen := make(map[string]bool)
	modelOptions := make(map[string]playgroundVideoModelOptions)
	for _, row := range rows {
		if row.HTTPMethod != "POST" || row.RouteTemplate != "/v1/videos/generations" {
			continue
		}
		var tiers []string
		if json.Unmarshal(row.ServiceTiers, &tiers) != nil {
			return nil, errors.ErrInternalError
		}
		var options playgroundVideoModelOptions
		if len(row.CapabilityConstraints) > 0 && json.Unmarshal(row.CapabilityConstraints, &options) != nil {
			return nil, errors.ErrInternalError
		}
		options.ServiceTiers = appendUniqueOrdered(options.ServiceTiers, tiers...)
		if !seen[row.APIName] {
			seen[row.APIName] = true
			models = append(models, row.APIName)
			modelOptions[row.APIName] = clonePlaygroundVideoOptions(options)
		} else {
			modelOptions[row.APIName] = mergePlaygroundVideoOptions(modelOptions[row.APIName], options)
		}
	}
	for modelName, options := range modelOptions {
		options.TaskTypes = orderVideoTaskTypes(options.TaskTypes)
		modelOptions[modelName] = options
	}
	return &playgroundVideoModelsResponse{Models: models, ModelOptions: modelOptions}, nil
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
	setPlaygroundVideoToken(c, token)
	open.CreateVideoGeneration(c)
}

// PlaygroundEstimateVideo POST /api/playground/:token_id/videos/estimate
func PlaygroundEstimateVideo(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}
	setPlaygroundVideoToken(c, token)
	open.EstimateVideoGeneration(c)
}

// PlaygroundGetVideo GET /api/playground/:token_id/videos/generations/:id
func PlaygroundGetVideo(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}
	setPlaygroundVideoToken(c, token)
	open.GetVideoGeneration(c)
}

// PlaygroundListVideos GET /api/playground/:token_id/videos/generations
func PlaygroundListVideos(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}
	setPlaygroundVideoToken(c, token)
	open.ListVideoGenerations(c)
}

func setPlaygroundVideoToken(c *gin.Context, token *model.Token) {
	c.Set(middleware.ContextKeyTokenID, token.ID)
	c.Set(middleware.ContextKeyToken, token)
}
