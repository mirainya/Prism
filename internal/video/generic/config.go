package generic

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/mirainya/Prism/internal/video"
)

const (
	ProfileJSONTaskV1 = "json_task_v1"
	taskIDPlaceholder = "{task_id}"
)

var jsonFieldSegment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

type operationConfig struct {
	Enabled         bool     `json:"enabled"`
	Method          string   `json:"method"`
	Path            string   `json:"path"`
	TaskIDBodyPath  string   `json:"task_id_body_path"`
	AllowedStatuses []string `json:"allowed_statuses"`
}

type requestConfig struct {
	Fields             map[string]string   `json:"fields"`
	ContentPath        string              `json:"content_path"`
	ContentFields      map[string]string   `json:"content_fields"`
	ContentProjections []contentProjection `json:"content_projections"`
	IncludeContent     *bool               `json:"include_content"`
	FixedBody          map[string]any      `json:"fixed_body"`
	ParamsMode         string              `json:"params_mode"`
	RequestIDHeader    string              `json:"request_id_header"`
}

// contentProjection selects one content item and writes one of its values to
// a normal request field. It keeps upstream-specific nesting in channel data.
type contentProjection struct {
	Source    string   `json:"source"`
	Target    string   `json:"target"`
	Output    string   `json:"output"`
	Separator string   `json:"separator"`
	Models    []string `json:"models"`
	Types     []string `json:"types"`
	Roles     []string `json:"roles"`
	Index     int      `json:"index"`
	Required  bool     `json:"required"`
}

type responseConfig struct {
	Root                string            `json:"root"`
	SuccessCodePath     string            `json:"success_code_path"`
	SuccessCodeValues   []string          `json:"success_code_values"`
	SuccessCodeOptional bool              `json:"success_code_optional"`
	EstimatedCostPaths  []string          `json:"estimated_cost_paths"`
	TaskIDPaths         []string          `json:"task_id_paths"`
	StatusPaths         []string          `json:"status_paths"`
	ProgressPaths       []string          `json:"progress_paths"`
	VideoURLPaths       []string          `json:"video_url_paths"`
	ThumbnailURLPaths   []string          `json:"thumbnail_url_paths"`
	DurationPaths       []string          `json:"duration_paths"`
	ErrorPaths          []string          `json:"error_paths"`
	StatusMap           map[string]string `json:"status_map"`
	SubmitDefaultStatus string            `json:"submit_default_status"`
	PollDefaultStatus   string            `json:"poll_default_status"`
	UnknownStatus       string            `json:"unknown_status"`
}

type parameterOption struct {
	Label string `json:"label"`
	Value any    `json:"value"`
}

type parameterRule struct {
	Name    string            `json:"name"`
	Label   string            `json:"label"`
	Type    string            `json:"type"`
	Default any               `json:"default"`
	Options []parameterOption `json:"options"`
}

type validationRule struct {
	DurationMin                 int             `json:"duration_min"`
	DurationMax                 int             `json:"duration_max"`
	Resolutions                 []string        `json:"resolutions"`
	Ratios                      []string        `json:"ratios"`
	TaskModes                   []string        `json:"task_modes"`
	RequireMedia                bool            `json:"require_media"`
	RequireVisualMediaWithAudio bool            `json:"require_visual_media_with_audio"`
	AllowGeneratedAudio         *bool           `json:"allow_generated_audio"`
	AllowedRoles                []string        `json:"allowed_roles"`
	MaxImages                   int             `json:"max_images"`
	MaxVideos                   int             `json:"max_videos"`
	MaxAudios                   int             `json:"max_audios"`
	MaxMedia                    int             `json:"max_media"`
	MediaDurationMin            float64         `json:"media_duration_min"`
	MediaDurationMax            float64         `json:"media_duration_max"`
	MaxVideoDuration            float64         `json:"max_video_duration_total"`
	MaxAudioDuration            float64         `json:"max_audio_duration_total"`
	Parameters                  []parameterRule `json:"parameters"`
}

type validationConfig struct {
	Models map[string]validationRule `json:"models"`
}

type localCancelConfig struct {
	Enabled        *bool    `json:"enabled"`
	DisabledModels []string `json:"disabled_models"`
}

type adapterConfig struct {
	Profile        string            `json:"profile"`
	AuthLocation   string            `json:"auth_location"`
	AuthKey        string            `json:"auth_key"`
	AuthHeader     string            `json:"auth_header"`
	AuthPrefix     *string           `json:"auth_prefix"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Submit         operationConfig   `json:"submit"`
	Estimate       operationConfig   `json:"estimate"`
	Poll           operationConfig   `json:"poll"`
	Cancel         operationConfig   `json:"cancel"`
	Request        requestConfig     `json:"request"`
	Response       responseConfig    `json:"response"`
	Validation     validationConfig  `json:"validation"`
	LocalCancel    localCancelConfig `json:"local_cancel"`
}

func (c *adapterConfig) defaults() {
	c.Profile = strings.TrimSpace(c.Profile)
	c.AuthLocation = strings.ToLower(strings.TrimSpace(c.AuthLocation))
	if c.AuthLocation == "" {
		c.AuthLocation = "header"
	}
	c.AuthKey = strings.TrimSpace(c.AuthKey)
	c.AuthHeader = strings.TrimSpace(c.AuthHeader)
	if c.AuthKey == "" {
		c.AuthKey = c.AuthHeader
	}
	if c.AuthKey == "" {
		c.AuthKey = "Authorization"
	}
	c.AuthHeader = c.AuthKey
	if c.AuthPrefix == nil {
		prefix := "Bearer "
		c.AuthPrefix = &prefix
	}
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = 30
	}
	c.Submit.Method = normalizeMethod(c.Submit.Method, http.MethodPost)
	c.Estimate.Method = normalizeMethod(c.Estimate.Method, http.MethodPost)
	c.Poll.Method = normalizeMethod(c.Poll.Method, http.MethodGet)
	c.Cancel.Method = normalizeMethod(c.Cancel.Method, http.MethodPost)
	c.Submit.Path = strings.TrimSpace(c.Submit.Path)
	c.Estimate.Path = strings.TrimSpace(c.Estimate.Path)
	c.Poll.Path = strings.TrimSpace(c.Poll.Path)
	c.Cancel.Path = strings.TrimSpace(c.Cancel.Path)
	c.Submit.TaskIDBodyPath = strings.TrimSpace(c.Submit.TaskIDBodyPath)
	c.Estimate.TaskIDBodyPath = strings.TrimSpace(c.Estimate.TaskIDBodyPath)
	c.Poll.TaskIDBodyPath = strings.TrimSpace(c.Poll.TaskIDBodyPath)
	c.Cancel.TaskIDBodyPath = strings.TrimSpace(c.Cancel.TaskIDBodyPath)
	if c.Request.Fields == nil {
		c.Request.Fields = map[string]string{
			"model": "model", "prompt": "prompt", "resolution": "resolution", "ratio": "ratio",
			"duration": "duration", "audio": "generate_audio", "task_mode": "task_mode",
		}
	}
	if c.Request.ContentFields == nil {
		c.Request.ContentFields = map[string]string{
			"type": "type", "role": "role", "text": "text", "url": "url",
			"provider_object": "storage_object_id", "duration": "duration_seconds",
		}
	}
	c.Request.Fields = normalizeMappings(c.Request.Fields)
	c.Request.ContentFields = normalizeMappings(c.Request.ContentFields)
	c.Request.ContentPath = strings.TrimSpace(c.Request.ContentPath)
	if c.Request.ContentPath == "" {
		c.Request.ContentPath = "content"
	}
	if c.Request.IncludeContent == nil {
		include := true
		c.Request.IncludeContent = &include
	}
	for index := range c.Request.ContentProjections {
		projection := &c.Request.ContentProjections[index]
		projection.Source = strings.TrimSpace(projection.Source)
		projection.Target = strings.TrimSpace(projection.Target)
		projection.Output = strings.ToLower(strings.TrimSpace(projection.Output))
		if projection.Output == "" {
			projection.Output = "scalar"
		}
		projection.Models = normalizeStrings(projection.Models)
		projection.Types = normalizeStrings(projection.Types)
		projection.Roles = normalizeStrings(projection.Roles)
	}
	c.Request.ParamsMode = strings.ToLower(strings.TrimSpace(c.Request.ParamsMode))
	if c.Request.ParamsMode == "" {
		c.Request.ParamsMode = "merge_missing"
	}
	if c.Request.RequestIDHeader == "" {
		c.Request.RequestIDHeader = "X-Request-ID"
	}
	if len(c.Response.SuccessCodeValues) == 0 && c.Response.SuccessCodePath != "" {
		c.Response.SuccessCodeValues = []string{"0"}
	}
	if c.Response.StatusMap == nil {
		c.Response.StatusMap = defaultStatusMap()
	}
	normalizedStatuses := make(map[string]string, len(c.Response.StatusMap))
	for upstream, status := range c.Response.StatusMap {
		normalizedStatuses[strings.ToLower(strings.TrimSpace(upstream))] = strings.TrimSpace(status)
	}
	c.Response.StatusMap = normalizedStatuses
	if c.Response.SubmitDefaultStatus == "" {
		c.Response.SubmitDefaultStatus = string(video.VideoTaskStatusSubmitted)
	}
	if c.Response.PollDefaultStatus == "" {
		c.Response.PollDefaultStatus = string(video.VideoTaskStatusTracking)
	}
	if c.Response.UnknownStatus == "" {
		c.Response.UnknownStatus = string(video.VideoTaskStatusTracking)
	}
	if len(c.Cancel.AllowedStatuses) == 0 {
		c.Cancel.AllowedStatuses = []string{string(video.VideoTaskStatusSubmitted), string(video.VideoTaskStatusTracking)}
	}
	if c.LocalCancel.Enabled == nil {
		enabled := true
		c.LocalCancel.Enabled = &enabled
	}
	for index := range c.LocalCancel.DisabledModels {
		c.LocalCancel.DisabledModels[index] = strings.TrimSpace(c.LocalCancel.DisabledModels[index])
	}
	for modelName, rule := range c.Validation.Models {
		for index := range rule.Parameters {
			parameter := &rule.Parameters[index]
			parameter.Name = strings.TrimSpace(parameter.Name)
			parameter.Label = strings.TrimSpace(parameter.Label)
			parameter.Type = strings.ToLower(strings.TrimSpace(parameter.Type))
			if parameter.Label == "" {
				parameter.Label = parameter.Name
			}
			for optionIndex := range parameter.Options {
				parameter.Options[optionIndex].Label = strings.TrimSpace(parameter.Options[optionIndex].Label)
			}
		}
		c.Validation.Models[modelName] = rule
	}
}

func normalizeMappings(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for name, path := range source {
		result[strings.TrimSpace(name)] = strings.TrimSpace(path)
	}
	return result
}

func normalizeMethod(value, fallback string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return value
}

func defaultStatusMap() map[string]string {
	return map[string]string{
		"queued": "submitted", "created": "submitted", "submitted": "submitted", "pending": "submitted", "waiting": "submitted",
		"running": "tracking", "processing": "tracking", "in_progress": "tracking",
		"succeeded": "completed", "success": "completed", "completed": "completed",
		"failed": "failed", "error": "failed", "expired": "failed",
		"canceled": "cancelled", "cancelled": "cancelled",
	}
}

func parseConfig(channel *video.VideoChannel) (adapterConfig, error) {
	if channel == nil {
		return adapterConfig{}, errors.New("generic adapter requires a video channel")
	}
	var envelope struct {
		Adapter json.RawMessage `json:"adapter"`
	}
	if len(channel.ExtraConfig) == 0 || string(channel.ExtraConfig) == "null" {
		return adapterConfig{}, errors.New("generic adapter requires extra_config.adapter")
	}
	if err := json.Unmarshal(channel.ExtraConfig, &envelope); err != nil {
		return adapterConfig{}, fmt.Errorf("parse generic extra_config: %w", err)
	}
	if len(envelope.Adapter) == 0 || string(envelope.Adapter) == "null" {
		return adapterConfig{}, errors.New("generic adapter requires extra_config.adapter")
	}
	var config adapterConfig
	if err := json.Unmarshal(envelope.Adapter, &config); err != nil {
		return adapterConfig{}, fmt.Errorf("parse generic adapter config: %w", err)
	}
	config.defaults()
	if err := config.validate(channel.BaseURL); err != nil {
		return adapterConfig{}, err
	}
	if channelUsesUpstreamEstimate(channel) && !config.Estimate.Enabled {
		return adapterConfig{}, errors.New("generic adapter estimate operation is required for upstream_estimate pricing")
	}
	return config, nil
}

func channelUsesUpstreamEstimate(channel *video.VideoChannel) bool {
	if channel == nil || len(channel.Pricing) == 0 || string(channel.Pricing) == "null" {
		return false
	}
	var pricing struct {
		Mode string `json:"mode"`
	}
	return json.Unmarshal(channel.Pricing, &pricing) == nil && strings.TrimSpace(pricing.Mode) == "upstream_estimate"
}

// ValidateChannelConfig validates a generic adapter without requiring an API key.
func ValidateChannelConfig(channel *video.VideoChannel) error {
	_, err := parseConfig(channel)
	return err
}

func (c adapterConfig) validate(baseURL string) error {
	if c.Profile != ProfileJSONTaskV1 {
		return fmt.Errorf("generic adapter profile must be %q", ProfileJSONTaskV1)
	}
	parsedBase, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsedBase.Host == "" || (parsedBase.Scheme != "http" && parsedBase.Scheme != "https") {
		return errors.New("generic adapter base URL must be an HTTP or HTTPS URL")
	}
	if c.TimeoutSeconds > 300 {
		return errors.New("generic adapter timeout_seconds cannot exceed 300")
	}
	if c.AuthLocation != "header" && c.AuthLocation != "body" && c.AuthLocation != "query" {
		return errors.New("generic adapter auth_location must be header, body, or query")
	}
	if strings.ContainsAny(c.AuthKey, "\r\n") || strings.TrimSpace(c.AuthKey) == "" {
		return errors.New("generic adapter auth_key is invalid")
	}
	if c.AuthLocation == "body" {
		if err := validateJSONFieldPath(c.AuthKey); err != nil {
			return fmt.Errorf("generic adapter auth_key: %w", err)
		}
	}
	if err := validateOperation("submit", c.Submit, true, false); err != nil {
		return err
	}
	if err := validateOperation("estimate", c.Estimate, c.Estimate.Enabled, false); err != nil {
		return err
	}
	if err := validateOperation("poll", c.Poll, true, true); err != nil {
		return err
	}
	if err := validateOperation("cancel", c.Cancel, c.Cancel.Enabled, true); err != nil {
		return err
	}
	if err := validateRequestConfig(c.Request); err != nil {
		return err
	}
	if err := validateResponseConfig(c.Response); err != nil {
		return err
	}
	if c.Estimate.Enabled {
		if len(c.Response.EstimatedCostPaths) == 0 {
			return errors.New("generic adapter response.estimated_cost_paths are required when estimate is enabled")
		}
		for _, path := range c.Response.EstimatedCostPaths {
			if err := validateJSONFieldPath(path); err != nil {
				return fmt.Errorf("generic adapter response.estimated_cost_paths: %w", err)
			}
		}
	}
	for _, model := range c.LocalCancel.DisabledModels {
		if model == "" {
			return errors.New("generic adapter local_cancel.disabled_models cannot contain empty names")
		}
	}
	for model, rule := range c.Validation.Models {
		if strings.TrimSpace(model) == "" {
			return errors.New("generic adapter validation model name cannot be empty")
		}
		if err := validateRule(model, rule); err != nil {
			return err
		}
	}
	return nil
}

func validateOperation(name string, operation operationConfig, required, requireTaskID bool) error {
	allowedMethods := map[string]bool{http.MethodGet: true, http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true}
	if !allowedMethods[operation.Method] {
		return fmt.Errorf("generic adapter %s method %q is not supported", name, operation.Method)
	}
	if !required && operation.Path == "" {
		return nil
	}
	parsed, err := url.ParseRequestURI(operation.Path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(operation.Path, "/") || strings.HasPrefix(operation.Path, "//") {
		return fmt.Errorf("generic adapter %s path must be an absolute URL path", name)
	}
	if operation.TaskIDBodyPath != "" {
		if err := validateJSONFieldPath(operation.TaskIDBodyPath); err != nil {
			return fmt.Errorf("generic adapter %s task_id_body_path: %w", name, err)
		}
		if operation.Method == http.MethodGet {
			return fmt.Errorf("generic adapter %s task_id_body_path requires a non-GET method", name)
		}
	}
	if requireTaskID && !strings.Contains(operation.Path, taskIDPlaceholder) && operation.TaskIDBodyPath == "" {
		return fmt.Errorf("generic adapter %s requires %s in path or task_id_body_path", name, taskIDPlaceholder)
	}
	for _, status := range operation.AllowedStatuses {
		if !validTaskStatus(status) {
			return fmt.Errorf("generic adapter %s has invalid allowed status %q", name, status)
		}
	}
	return nil
}

func validateRequestConfig(config requestConfig) error {
	allowedFields := map[string]bool{
		"model": true, "prompt": true, "resolution": true, "ratio": true,
		"duration": true, "audio": true, "task_mode": true,
	}
	allowedContentFields := map[string]bool{
		"type": true, "role": true, "text": true, "url": true, "provider_object": true, "duration": true,
	}
	if err := validateMappings("request.fields", config.Fields, allowedFields); err != nil {
		return err
	}
	if err := validateMappings("request.content_fields", config.ContentFields, allowedContentFields); err != nil {
		return err
	}
	if err := validateJSONFieldPath(config.ContentPath); err != nil {
		return fmt.Errorf("generic adapter request.content_path: %w", err)
	}
	if config.IncludeContent == nil {
		return errors.New("generic adapter request.include_content must be specified after defaults")
	}
	if err := validateContentProjections(config.ContentProjections); err != nil {
		return err
	}
	if config.ParamsMode != "merge_missing" && config.ParamsMode != "ignore" {
		return errors.New("generic adapter request.params_mode must be merge_missing or ignore")
	}
	if strings.ContainsAny(config.RequestIDHeader, "\r\n") {
		return errors.New("generic adapter request_id_header is invalid")
	}
	return nil
}

func validateContentProjections(projections []contentProjection) error {
	allowedSources := map[string]bool{
		"type": true, "role": true, "text": true, "url": true,
		"provider_object": true, "duration": true,
	}
	seenTargets := make(map[string]bool, len(projections))
	for index, projection := range projections {
		if !allowedSources[projection.Source] {
			return fmt.Errorf("generic adapter request.content_projections[%d] source %q is not supported", index, projection.Source)
		}
		if err := validateJSONFieldPath(projection.Target); err != nil {
			return fmt.Errorf("generic adapter request.content_projections[%d] target: %w", index, err)
		}
		if seenTargets[projection.Target] {
			return fmt.Errorf("generic adapter request.content_projections target %q is duplicated", projection.Target)
		}
		seenTargets[projection.Target] = true
		if projection.Output != "scalar" && projection.Output != "array" && projection.Output != "join" {
			return fmt.Errorf("generic adapter request.content_projections[%d] output %q is not supported", index, projection.Output)
		}
		if projection.Output != "scalar" && projection.Index != 0 {
			return fmt.Errorf("generic adapter request.content_projections[%d] index requires scalar output", index)
		}
		if projection.Index < 0 || projection.Index > 1000 {
			return fmt.Errorf("generic adapter request.content_projections[%d] index is invalid", index)
		}
		selectors := append(append(append([]string{}, projection.Models...), projection.Types...), projection.Roles...)
		for _, value := range selectors {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("generic adapter request.content_projections[%d] selector cannot be empty", index)
			}
		}
	}
	return nil
}

func normalizeStrings(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = strings.TrimSpace(value)
	}
	return result
}

func validateMappings(name string, mappings map[string]string, allowed map[string]bool) error {
	seen := make(map[string]bool, len(mappings))
	for source, target := range mappings {
		if !allowed[source] {
			return fmt.Errorf("generic adapter %s source %q is not supported", name, source)
		}
		if err := validateJSONFieldPath(target); err != nil {
			return fmt.Errorf("generic adapter %s target %q: %w", name, target, err)
		}
		if seen[target] {
			return fmt.Errorf("generic adapter %s target %q is duplicated", name, target)
		}
		seen[target] = true
	}
	return nil
}

func validateJSONFieldPath(path string) error {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) == 0 || len(parts) > 12 {
		return errors.New("must contain between 1 and 12 field segments")
	}
	for _, part := range parts {
		if !jsonFieldSegment.MatchString(part) {
			return errors.New("contains an invalid field segment")
		}
	}
	return nil
}

func validateResponseConfig(config responseConfig) error {
	if len(config.TaskIDPaths) == 0 || len(config.StatusPaths) == 0 {
		return errors.New("generic adapter response task_id_paths and status_paths are required")
	}
	if config.SuccessCodePath != "" && len(config.SuccessCodeValues) == 0 {
		return errors.New("generic adapter response success_code_values are required")
	}
	if config.SuccessCodeOptional && config.SuccessCodePath == "" {
		return errors.New("generic adapter response success_code_optional requires success_code_path")
	}
	for upstream, status := range config.StatusMap {
		if strings.TrimSpace(upstream) == "" || !validTaskStatus(status) {
			return fmt.Errorf("generic adapter response status_map entry %q=%q is invalid", upstream, status)
		}
	}
	for name, status := range map[string]string{
		"submit_default_status": config.SubmitDefaultStatus,
		"poll_default_status":   config.PollDefaultStatus,
		"unknown_status":        config.UnknownStatus,
	} {
		if !validTaskStatus(status) {
			return fmt.Errorf("generic adapter response %s %q is invalid", name, status)
		}
	}
	return nil
}

func validTaskStatus(status string) bool {
	switch video.VideoTaskStatus(strings.TrimSpace(status)) {
	case video.VideoTaskStatusQueued, video.VideoTaskStatusSubmitted, video.VideoTaskStatusTracking,
		video.VideoTaskStatusCompleted, video.VideoTaskStatusFailed, video.VideoTaskStatusCancelled:
		return true
	default:
		return false
	}
}

func validateRule(model string, rule validationRule) error {
	if rule.DurationMin < 0 || rule.DurationMax < 0 || rule.DurationMax > 3600 ||
		(rule.DurationMax > 0 && rule.DurationMin > rule.DurationMax) {
		return fmt.Errorf("generic adapter validation for %s has invalid duration bounds", model)
	}
	for _, value := range []int{rule.MaxImages, rule.MaxVideos, rule.MaxAudios, rule.MaxMedia} {
		if value < 0 || value > 1000 {
			return fmt.Errorf("generic adapter validation for %s has invalid media limits", model)
		}
	}
	if rule.MediaDurationMin < 0 || rule.MediaDurationMax < 0 ||
		(rule.MediaDurationMax > 0 && rule.MediaDurationMin > rule.MediaDurationMax) ||
		rule.MaxVideoDuration < 0 || rule.MaxAudioDuration < 0 {
		return fmt.Errorf("generic adapter validation for %s has invalid media duration limits", model)
	}
	seenParameters := make(map[string]struct{}, len(rule.Parameters))
	for _, parameter := range rule.Parameters {
		if !jsonFieldSegment.MatchString(parameter.Name) {
			return fmt.Errorf("generic adapter validation for %s has invalid parameter name %q", model, parameter.Name)
		}
		if _, exists := seenParameters[parameter.Name]; exists {
			return fmt.Errorf("generic adapter validation for %s has duplicate parameter %q", model, parameter.Name)
		}
		seenParameters[parameter.Name] = struct{}{}
		if parameter.Type != "select" {
			return fmt.Errorf("generic adapter validation for %s parameter %q must use select type", model, parameter.Name)
		}
		if len(parameter.Options) == 0 {
			return fmt.Errorf("generic adapter validation for %s parameter %q requires options", model, parameter.Name)
		}
		seenOptions := make(map[string]struct{}, len(parameter.Options))
		for _, option := range parameter.Options {
			if option.Label == "" || !validParameterValue(option.Value) {
				return fmt.Errorf("generic adapter validation for %s parameter %q has an invalid option", model, parameter.Name)
			}
			encoded, err := json.Marshal(option.Value)
			if err != nil {
				return fmt.Errorf("generic adapter validation for %s parameter %q has an invalid option value", model, parameter.Name)
			}
			key := string(encoded)
			if _, exists := seenOptions[key]; exists {
				return fmt.Errorf("generic adapter validation for %s parameter %q has duplicate options", model, parameter.Name)
			}
			seenOptions[key] = struct{}{}
		}
		if parameter.Default != nil {
			if !validParameterValue(parameter.Default) {
				return fmt.Errorf("generic adapter validation for %s parameter %q has an invalid default", model, parameter.Name)
			}
			encoded, err := json.Marshal(parameter.Default)
			if err != nil {
				return fmt.Errorf("generic adapter validation for %s parameter %q has an invalid default", model, parameter.Name)
			}
			if _, exists := seenOptions[string(encoded)]; !exists {
				return fmt.Errorf("generic adapter validation for %s parameter %q default is not an option", model, parameter.Name)
			}
		}
	}
	return nil
}

func validParameterValue(value any) bool {
	switch value.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
		return true
	default:
		return false
	}
}
