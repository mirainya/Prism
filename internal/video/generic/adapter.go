package generic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/internal/video/taskhttp"
	"github.com/tidwall/gjson"
)

type Adapter struct {
	client    *taskhttp.Client
	config    adapterConfig
	apiKey    string
	configErr error
}

func NewAdapter(channel *video.VideoChannel, key *video.VideoChannelKey) video.Adapter {
	adapter := &Adapter{}
	if channel == nil || key == nil {
		adapter.configErr = errors.New("generic adapter requires a video channel and key")
		return adapter
	}
	config, err := parseConfig(channel)
	if err != nil {
		adapter.configErr = err
		return adapter
	}
	adapter.config = config
	adapter.apiKey = key.APIKey
	adapter.client = taskhttp.NewClient(taskhttp.Config{
		BaseURL: channel.BaseURL, APIKey: key.APIKey,
		AuthLocation: config.AuthLocation, AuthHeader: config.AuthKey, AuthPrefix: valueOrEmpty(config.AuthPrefix),
		HTTPClient: &http.Client{Timeout: time.Duration(config.TimeoutSeconds) * time.Second},
	})
	return adapter
}

func (a *Adapter) BuildRequest(_ context.Context, request *video.GenerateRequest) (*video.ProviderRequest, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, errors.New("generic video request is required")
	}
	body := cloneObject(a.config.Request.FixedBody)
	sources := map[string]struct {
		value   any
		present bool
	}{
		"model":      {value: request.Model, present: strings.TrimSpace(request.Model) != ""},
		"prompt":     {value: request.Prompt, present: strings.TrimSpace(request.Prompt) != ""},
		"resolution": {value: request.Resolution, present: strings.TrimSpace(request.Resolution) != ""},
		"ratio":      {value: request.Ratio, present: strings.TrimSpace(request.Ratio) != ""},
		"duration":   {value: request.Duration, present: request.Duration > 0},
		"audio":      {value: request.Audio, present: true},
		"task_mode":  {value: request.TaskMode, present: strings.TrimSpace(request.TaskMode) != ""},
	}
	if value := sources["task_mode"]; value.present && len(a.config.Request.TaskModeMap) > 0 {
		mapped, exists := a.config.Request.TaskModeMap[request.TaskMode]
		if !exists && isReferenceTaskMode(request.TaskMode) {
			mapped, exists = a.config.Request.TaskModeMap["references"]
		}
		if !exists {
			return nil, fmt.Errorf("generic adapter has no upstream task mode mapping for %q", request.TaskMode)
		}
		value.value = mapped
		sources["task_mode"] = value
	}
	for source, target := range a.config.Request.Fields {
		value := sources[source]
		if value.present {
			if err := setField(body, target, value.value); err != nil {
				return nil, fmt.Errorf("map request field %s: %w", source, err)
			}
		}
	}
	for _, item := range request.Content {
		if item.AssetID != "" {
			return nil, fmt.Errorf("content asset %s was not resolved", item.AssetID)
		}
	}
	if len(request.Content) > 0 && *a.config.Request.IncludeContent {
		content := make([]map[string]any, 0, len(request.Content))
		for _, item := range request.Content {
			mapped, err := a.mapContent(item, request.TaskMode)
			if err != nil {
				return nil, err
			}
			content = append(content, mapped)
		}
		if err := setField(body, a.config.Request.ContentPath, content); err != nil {
			return nil, fmt.Errorf("map request content: %w", err)
		}
	}
	if err := a.applyContentProjections(body, request.Model, request.Content); err != nil {
		return nil, err
	}
	if request.ServiceTier != "" {
		if tier, exists := a.config.ServiceTiers[strings.ToLower(request.ServiceTier)]; exists {
			for key, value := range tier.RequestParams {
				if _, present := body[key]; !present {
					if err := setField(body, key, cloneValue(value)); err != nil {
						return nil, fmt.Errorf("map service tier %s: %w", request.ServiceTier, err)
					}
				}
			}
		} else if len(a.config.ServiceTiers) > 0 {
			return nil, fmt.Errorf("service tier %q is not supported by this channel", request.ServiceTier)
		}
	}
	if a.config.Request.ParamsMode == "merge_missing" {
		for key, value := range request.Params {
			if _, exists := body[key]; !exists {
				body[key] = value
			}
		}
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if a.config.Request.RequestIDHeader != "" && request.TaskID != "" {
		headers[a.config.Request.RequestIDHeader] = request.TaskID
	}
	return &video.ProviderRequest{Body: body, Headers: headers}, nil
}

func isReferenceTaskMode(mode string) bool {
	return mode == "first_frame" || mode == "first_last_frame" || mode == "multimodal"
}

func (a *Adapter) applyContentProjections(body map[string]any, model string, content []video.ContentItem) error {
	for _, projection := range a.config.Request.ContentProjections {
		if !matchesSelector(model, projection.Models) {
			continue
		}
		value, present := projectedContentValue(content, projection)
		if !present {
			if projection.Required {
				return fmt.Errorf("content projection %s requires a matching item", projection.Target)
			}
			continue
		}
		if err := setField(body, projection.Target, value); err != nil {
			return fmt.Errorf("map content projection %s: %w", projection.Target, err)
		}
	}
	return nil
}

func projectedContentValue(content []video.ContentItem, projection contentProjection) (any, bool) {
	values := make([]any, 0, len(content))
	for _, item := range content {
		if !matchesSelector(item.Type, projection.Types) || !matchesSelector(item.Role, projection.Roles) {
			continue
		}
		value, present := contentValue(item, projection.Source)
		if present {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return nil, false
	}
	switch projection.Output {
	case "array":
		return values, true
	case "join":
		parts := make([]string, len(values))
		for index, value := range values {
			parts[index] = fmt.Sprint(value)
		}
		return strings.Join(parts, projection.Separator), true
	default:
		if projection.Index >= len(values) {
			return nil, false
		}
		return values[projection.Index], true
	}
}

func matchesSelector(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func contentValue(item video.ContentItem, source string) (any, bool) {
	switch source {
	case "type":
		return item.Type, item.Type != ""
	case "role":
		return item.Role, item.Role != ""
	case "text":
		return item.Text, item.Text != ""
	case "url":
		return item.URL, item.URL != ""
	case "provider_object":
		return item.StorageObjectID, item.StorageObjectID != ""
	case "duration":
		return item.DurationSeconds, item.DurationSeconds > 0
	case "client_ref_id":
		return item.ClientRefID, item.ClientRefID != ""
	default:
		return nil, false
	}
}

func (a *Adapter) mapContent(item video.ContentItem, taskMode string) (map[string]any, error) {
	if roleMap := a.config.Request.ContentRoleMap[taskMode]; len(roleMap) > 0 {
		if mappedRole, exists := roleMap[item.Role]; exists {
			item.Role = mappedRole
		}
	}
	mapped := make(map[string]any)
	for source, target := range a.config.Request.ContentFields {
		value, present := contentValue(item, source)
		if present {
			if err := setField(mapped, target, value); err != nil {
				return nil, fmt.Errorf("map content field %s: %w", source, err)
			}
		}
	}
	if len(mapped) == 0 {
		return nil, errors.New("generic adapter content item has no mapped fields")
	}
	return mapped, nil
}

func (a *Adapter) Submit(ctx context.Context, request *video.ProviderRequest) (*video.SubmitResult, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, errors.New("generic provider request is required")
	}
	body, err := a.marshalBody(request.Body)
	if err != nil {
		return nil, fmt.Errorf("marshal generic request: %w", err)
	}
	responseBody, err := a.do(ctx, "submit", a.config.Submit, body, request.Headers)
	if err != nil {
		return nil, err
	}
	parsed, err := a.parseResponse(responseBody, a.config.Response.SubmitDefaultStatus)
	if err != nil {
		return nil, video.NewAmbiguousProviderError("parse generic submit response", err)
	}
	if parsed.ProviderTaskID == "" && !parsed.Status.IsTerminal() {
		return nil, video.NewAmbiguousProviderError("parse generic submit response", errors.New("upstream response is missing task id"))
	}
	return &video.SubmitResult{
		ProviderTaskID: parsed.ProviderTaskID,
		Status:         parsed.Status,
		Result:         parsed.Result,
		Metadata:       parsed.Metadata,
	}, nil
}

func (a *Adapter) Estimate(ctx context.Context, request *video.GenerateRequest) (float64, error) {
	estimate, err := a.EstimateDetailed(ctx, request)
	if err != nil {
		return 0, err
	}
	return estimate.EstimatedCost, nil
}

func (a *Adapter) EstimateDetailed(ctx context.Context, request *video.GenerateRequest) (*video.ProviderEstimate, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	if !a.config.Estimate.Enabled {
		return nil, video.ErrEstimateNotSupported
	}
	providerRequest, err := a.BuildRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	body, err := a.marshalBody(providerRequest.Body)
	if err != nil {
		return nil, fmt.Errorf("marshal generic estimate request: %w", err)
	}
	responseBody, err := a.do(ctx, "estimate", a.config.Estimate, body, providerRequest.Headers)
	if err != nil {
		return nil, err
	}
	payload, err := a.responsePayload(responseBody)
	if err != nil {
		return nil, fmt.Errorf("parse generic estimate response: %w", err)
	}
	result := firstResult(payload, a.config.Response.EstimatedCostPaths)
	if !result.Exists() || result.Type == gjson.Null || result.IsObject() || result.IsArray() {
		return nil, errors.New("generic estimate response is missing estimated cost")
	}
	estimatedCost, err := strconv.ParseFloat(strings.TrimSpace(result.String()), 64)
	if err != nil || estimatedCost < 0 || math.IsNaN(estimatedCost) || math.IsInf(estimatedCost, 0) {
		return nil, errors.New("generic estimate response contains an invalid estimated cost")
	}
	return &video.ProviderEstimate{
		EstimatedCost: estimatedCost,
		ActualCost:    firstFloat(payload, a.config.Response.ActualCostPaths),
		UnitCost:      firstFloat(payload, a.config.Response.UnitCostPaths),
		Units:         firstFloat(payload, a.config.Response.UnitsPaths),
		BillingMode:   firstText(payload, a.config.Response.BillingModePaths),
		BillingTier:   firstText(payload, a.config.Response.BillingTierPaths),
		PricingSource: firstText(payload, a.config.Response.PricingSourcePaths),
		Currency:      firstText(payload, a.config.Response.CurrencyPaths),
	}, nil
}

func (a *Adapter) Poll(ctx context.Context, providerTaskID string) (*video.Progress, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(providerTaskID) == "" {
		return nil, errors.New("provider task id is required")
	}
	body, err := a.operationBody(a.config.Poll, providerTaskID)
	if err != nil {
		return nil, fmt.Errorf("build generic poll request: %w", err)
	}
	responseBody, err := a.do(ctx, "poll", a.config.Poll, body, nil, providerTaskID)
	if err != nil {
		return nil, err
	}
	parsed, err := a.parseResponse(responseBody, a.config.Response.PollDefaultStatus)
	if err != nil {
		return nil, video.NewRetryableProviderError("parse generic poll response", err)
	}
	return &video.Progress{
		Status: parsed.Status, Percent: parsed.Percent, Result: parsed.Result, Error: parsed.Error, Metadata: parsed.Metadata,
	}, nil
}

func (a *Adapter) CanCancel(status video.VideoTaskStatus) bool {
	if a == nil || a.configErr != nil || !a.config.Cancel.Enabled {
		return false
	}
	for _, allowed := range a.config.Cancel.AllowedStatuses {
		if video.VideoTaskStatus(allowed) == status {
			return true
		}
	}
	return false
}

func (a *Adapter) CanCancelLocal(task *video.VideoTask) bool {
	if a == nil || a.configErr != nil || task == nil || task.Status != video.VideoTaskStatusQueued ||
		a.config.LocalCancel.Enabled == nil || !*a.config.LocalCancel.Enabled {
		return false
	}
	modelName := task.VendorModel
	if modelName == "" {
		modelName = task.Model
	}
	for _, model := range a.config.LocalCancel.DisabledModels {
		if model == modelName {
			return false
		}
	}
	return true
}

func (a *Adapter) Cancel(ctx context.Context, providerTaskID string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if !a.config.Cancel.Enabled {
		return video.ErrCancelNotSupported
	}
	if strings.TrimSpace(providerTaskID) == "" {
		return errors.New("provider task id is required")
	}
	requestBody, err := a.operationBody(a.config.Cancel, providerTaskID)
	if err != nil {
		return fmt.Errorf("build generic cancel request: %w", err)
	}
	body, err := a.do(ctx, "cancel", a.config.Cancel, requestBody, nil, providerTaskID)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if _, err := a.responsePayload(body); err != nil {
			return fmt.Errorf("parse generic cancel response: %w", err)
		}
	}
	return nil
}

func (a *Adapter) CanAction(action string, status video.VideoTaskStatus) bool {
	if a == nil || a.configErr != nil {
		return false
	}
	operation, exists := a.config.Actions[strings.ToLower(strings.TrimSpace(action))]
	if !exists || !operation.Enabled {
		return false
	}
	if len(operation.AllowedStatuses) == 0 {
		return true
	}
	for _, allowed := range operation.AllowedStatuses {
		if video.VideoTaskStatus(allowed) == status {
			return true
		}
	}
	return false
}

func (a *Adapter) ActionSurchargePercent(action string) float64 {
	if a == nil || a.configErr != nil {
		return 0
	}
	if action == "priority_queue" {
		if tier, ok := a.config.ServiceTiers["priority"]; ok && tier.SurchargePercent > 0 {
			return tier.SurchargePercent
		}
	}
	return 0
}

func (a *Adapter) Action(ctx context.Context, action, providerTaskID string) (*video.ProviderMetadata, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	action = strings.ToLower(strings.TrimSpace(action))
	operation, exists := a.config.Actions[action]
	if !exists || !operation.Enabled {
		return nil, video.ErrActionNotSupported
	}
	if strings.TrimSpace(providerTaskID) == "" {
		return nil, errors.New("provider task id is required")
	}
	body, err := a.operationBody(operation, providerTaskID)
	if err != nil {
		return nil, fmt.Errorf("build generic action request: %w", err)
	}
	responseBody, err := a.do(ctx, "action."+action, operation, body, nil, providerTaskID)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(responseBody)) > 0 {
		payload, err := a.responsePayload(responseBody)
		if err != nil {
			return nil, fmt.Errorf("parse generic action response: %w", err)
		}
		return a.parseProviderMetadata(payload), nil
	}
	return nil, nil
}

func (a *Adapter) RequestPath() string {
	if a == nil {
		return ""
	}
	return a.config.Submit.Path
}

func (a *Adapter) ready() error {
	if a == nil {
		return errors.New("generic adapter is nil")
	}
	if a.configErr != nil {
		return a.configErr
	}
	return a.client.Ready()
}

func (a *Adapter) operationBody(config operationConfig, taskID string) ([]byte, error) {
	var body map[string]any
	if config.TaskIDBodyPath != "" {
		body = make(map[string]any)
		if err := setField(body, config.TaskIDBodyPath, taskID); err != nil {
			return nil, err
		}
	}
	return a.marshalBody(body)
}

func (a *Adapter) marshalBody(body map[string]any) ([]byte, error) {
	if body == nil && a.config.AuthLocation != "body" {
		return nil, nil
	}
	payload := cloneObject(body)
	if a.config.AuthLocation == "body" {
		if err := setField(payload, a.config.AuthKey, valueOrEmpty(a.config.AuthPrefix)+a.apiKey); err != nil {
			return nil, fmt.Errorf("map body authentication: %w", err)
		}
	}
	return json.Marshal(payload)
}

func (a *Adapter) do(ctx context.Context, operation string, config operationConfig, body []byte, headers map[string]string, taskID ...string) ([]byte, error) {
	if body != nil {
		if headers == nil {
			headers = make(map[string]string)
		}
		if _, exists := headers["Content-Type"]; !exists {
			headers["Content-Type"] = "application/json"
		}
	}
	return a.client.Do(ctx, operation, taskhttp.Operation{Method: config.Method, Path: config.Path}, body, headers, taskID...)
}

type parsedResponse struct {
	ProviderTaskID string
	Status         video.VideoTaskStatus
	Percent        int
	Result         *video.GenerationResult
	Error          string
	Metadata       *video.ProviderMetadata
}

func (a *Adapter) parseResponse(body []byte, defaultStatus string) (*parsedResponse, error) {
	payload, err := a.responsePayload(body)
	if err != nil {
		return nil, err
	}
	providerTaskID := parseTaskID(firstResult(payload, a.config.Response.TaskIDPaths))
	upstreamStatus := strings.ToLower(firstText(payload, a.config.Response.StatusPaths))
	status := video.VideoTaskStatus(defaultStatus)
	if upstreamStatus != "" {
		mapped, exists := a.config.Response.StatusMap[upstreamStatus]
		if !exists {
			mapped = a.config.Response.UnknownStatus
		}
		status = video.VideoTaskStatus(mapped)
	}
	percent := firstInt(payload, a.config.Response.ProgressPaths)
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}
	if status == video.VideoTaskStatusCompleted {
		percent = 100
	}
	videoURL := firstText(payload, a.config.Response.VideoURLPaths)
	var result *video.GenerationResult
	if videoURL != "" {
		result = &video.GenerationResult{
			VideoURL: videoURL, ThumbnailURL: firstText(payload, a.config.Response.ThumbnailURLPaths),
			Duration: firstFloat(payload, a.config.Response.DurationPaths),
		}
	}
	if status == video.VideoTaskStatusCompleted && result == nil {
		return nil, errors.New("completed upstream response is missing video URL")
	}
	return &parsedResponse{
		ProviderTaskID: providerTaskID, Status: status, Percent: percent, Result: result,
		Error:    firstText(payload, a.config.Response.ErrorPaths),
		Metadata: a.parseProviderMetadata(payload),
	}, nil
}

func (a *Adapter) parseProviderMetadata(payload []byte) *video.ProviderMetadata {
	metadata := &video.ProviderMetadata{
		QueueStatus:              firstText(payload, a.config.Response.QueueStatusPaths),
		QueuePosition:            firstInt(payload, a.config.Response.QueuePositionPaths),
		QueueLimit:               firstInt(payload, a.config.Response.QueueLimitPaths),
		PriorityQueue:            firstBool(payload, a.config.Response.PriorityQueuePaths),
		PointsVIP:                firstBool(payload, a.config.Response.PointsVIPPaths),
		PrioritySurchargePercent: firstFloat(payload, a.config.Response.PrioritySurchargePercentPaths),
		EstimatedCost:            firstFloat(payload, a.config.Response.EstimatedCostPaths),
	}
	if metadata.QueueStatus == "" && metadata.QueuePosition == 0 && metadata.QueueLimit == 0 &&
		metadata.PriorityQueue == nil && metadata.PointsVIP == nil && metadata.PrioritySurchargePercent == 0 && metadata.EstimatedCost == 0 {
		return nil
	}
	return metadata
}

func (a *Adapter) responsePayload(body []byte) ([]byte, error) {
	if !gjson.ValidBytes(body) {
		return nil, errors.New("upstream response is not valid JSON")
	}
	if path := strings.TrimSpace(a.config.Response.SuccessCodePath); path != "" {
		result := gjson.GetBytes(body, path)
		if !result.Exists() {
			if !a.config.Response.SuccessCodeOptional {
				return nil, errors.New("upstream response is missing success code")
			}
		} else {
			code := result.String()
			matched := false
			for _, allowed := range a.config.Response.SuccessCodeValues {
				if code == allowed {
					matched = true
					break
				}
			}
			if !matched {
				return nil, fmt.Errorf("upstream response code %s: %s", code, truncate(body, 512))
			}
		}
	}
	if root := strings.TrimSpace(a.config.Response.Root); root != "" {
		result := gjson.GetBytes(body, root)
		if !result.Exists() || result.Type == gjson.Null || !result.IsObject() {
			return nil, errors.New("upstream response is missing configured root object")
		}
		return []byte(result.Raw), nil
	}
	return body, nil
}

func firstResult(body []byte, paths []string) gjson.Result {
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		result := gjson.GetBytes(body, path)
		if result.Exists() && result.Type != gjson.Null {
			return result
		}
	}
	return gjson.Result{}
}

func firstText(body []byte, paths []string) string {
	result := firstResult(body, paths)
	if !result.Exists() {
		return ""
	}
	if result.IsObject() || result.IsArray() {
		return strings.TrimSpace(result.Raw)
	}
	return strings.TrimSpace(result.String())
}

func firstInt(body []byte, paths []string) int {
	result := firstResult(body, paths)
	if !result.Exists() {
		return 0
	}
	return int(result.Int())
}

func firstFloat(body []byte, paths []string) float64 {
	result := firstResult(body, paths)
	if !result.Exists() {
		return 0
	}
	return result.Float()
}

func firstBool(body []byte, paths []string) *bool {
	result := firstResult(body, paths)
	if !result.Exists() || (result.Type != gjson.True && result.Type != gjson.False) {
		return nil
	}
	value := result.Bool()
	return &value
}

func setField(target map[string]any, path string, value any) error {
	parts := strings.Split(path, ".")
	current := target
	for _, part := range parts[:len(parts)-1] {
		next, exists := current[part]
		if !exists {
			child := make(map[string]any)
			current[part] = child
			current = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("field %q conflicts with a non-object value", part)
		}
		current = child
	}
	current[parts[len(parts)-1]] = value
	return nil
}

func cloneObject(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = cloneValue(value)
	}
	return result
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneObject(typed)
	case []any:
		result := make([]any, len(typed))
		for index := range typed {
			result[index] = cloneValue(typed[index])
		}
		return result
	default:
		return value
	}
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func truncate(value []byte, max int) string {
	text := strings.TrimSpace(string(value))
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

func parseTaskID(value gjson.Result) string {
	if value.Type == gjson.Number {
		return strconv.FormatInt(value.Int(), 10)
	}
	return strings.TrimSpace(value.String())
}
