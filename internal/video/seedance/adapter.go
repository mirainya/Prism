package seedance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/internal/video/taskhttp"
)

const (
	taskIDPlaceholder = "{task_id}"
	defaultTaskPath   = "/api/v3/contents/generations/tasks"
)

type adapterConfig struct {
	SubmitPath string `json:"submit_path"`
	PollPath   string `json:"poll_path"`
	CancelPath string `json:"cancel_path"`
}

// Codec implements the official Seedance wire format. Network execution is
// supplied by taskhttp.Adapter so compatible channels do not duplicate it.
type Codec struct{}

func NewAdapter(channel *video.VideoChannel, key *video.VideoChannelKey) video.Adapter {
	config := officialConfig()
	var initializationError error
	if channel == nil || key == nil {
		initializationError = errors.New("seedance channel and key are required")
		return taskhttp.NewAdapter(taskhttp.AdapterConfig{Codec: Codec{}, InitializationError: initializationError})
	}
	baseURL := strings.TrimRight(strings.TrimSpace(channel.BaseURL), "/")
	client := taskhttp.NewClient(taskhttp.Config{
		BaseURL: baseURL, APIKey: key.APIKey,
		AuthHeader: "Authorization", AuthPrefix: "Bearer ",
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	})
	initializationError = validateAdapterConfig(baseURL, config)
	return taskhttp.NewAdapter(taskhttp.AdapterConfig{
		Client: client, Codec: Codec{}, InitializationError: initializationError,
		Submit:           taskhttp.Operation{Method: http.MethodPost, Path: config.SubmitPath},
		Poll:             taskhttp.Operation{Method: http.MethodGet, Path: config.PollPath},
		Cancel:           taskhttp.Operation{Method: http.MethodDelete, Path: config.CancelPath},
		CancelStatuses:   []video.VideoTaskStatus{video.VideoTaskStatusSubmitted},
		AllowLocalCancel: true,
	})
}

func (Codec) ValidateRequest(ctx context.Context, req *video.GenerateRequest) error {
	if req == nil {
		return errors.New("seedance request is required")
	}
	return validateOfficialRequest(req)
}

func validateOfficialRequest(req *video.GenerateRequest) error {
	if err := validateOfficialParameters(req.Params); err != nil {
		return err
	}
	return validateReferenceLimits(req, 9, 3, 3, 15, false)
}

func validateOfficialParameters(parameters map[string]any) error {
	for name, value := range parameters {
		switch name {
		case "camera_fixed", "return_last_frame", "web_search":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("seedance parameter %q must be a boolean", name)
			}
		default:
			return fmt.Errorf("seedance parameter %q is not supported", name)
		}
	}
	return nil
}

func validateReferenceLimits(req *video.GenerateRequest, imageLimit, videoLimit, audioLimit int, maxDuration float64, requireMediaDuration bool) error {
	counts := map[string]int{}
	totals := map[string]float64{}
	for _, item := range req.Content {
		kind, ok := videoContentKind(item.Type)
		if !ok {
			continue
		}
		counts[kind]++
		if kind == "video" || kind == "audio" {
			if requireMediaDuration && item.DurationSeconds < 2 {
				return fmt.Errorf("%s reference duration is required and must be at least 2 seconds", kind)
			}
			if item.DurationSeconds > maxDuration {
				return fmt.Errorf("%s reference duration exceeds %.0f seconds", kind, maxDuration)
			}
			totals[kind] += item.DurationSeconds
		}
	}
	if counts["image"] > imageLimit || counts["video"] > videoLimit || counts["audio"] > audioLimit || len(req.Content) > imageLimit+videoLimit+audioLimit {
		return errors.New("reference count limit exceeded")
	}
	if totals["video"] > maxDuration || totals["audio"] > maxDuration {
		return fmt.Errorf("reference duration total exceeds %.0f seconds", maxDuration)
	}
	return nil
}

func videoContentKind(contentType string) (string, bool) {
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

func (Codec) BuildRequest(ctx context.Context, req *video.GenerateRequest) (*video.ProviderRequest, error) {
	if req == nil {
		return nil, errors.New("seedance request is required")
	}
	body, err := buildOfficialBody(req)
	if err != nil {
		return nil, err
	}
	if err := validateOfficialParameters(req.Params); err != nil {
		return nil, err
	}
	for key, value := range req.Params {
		if _, exists := body[key]; !exists {
			body[key] = value
		}
	}
	return &video.ProviderRequest{
		Body: body,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"X-Request-ID": req.TaskID,
		},
	}, nil
}

func buildOfficialBody(req *video.GenerateRequest) (map[string]any, error) {
	content := make([]map[string]any, 0, len(req.Content)+1)
	if prompt := strings.TrimSpace(req.Prompt); prompt != "" {
		content = append(content, map[string]any{
			"type": "text",
			"text": prompt,
		})
	}
	for _, item := range req.Content {
		if item.AssetID != "" || item.StorageObjectID != "" {
			return nil, fmt.Errorf("content asset %s was not resolved", item.AssetID)
		}
		if item.Type == "text" {
			if text := strings.TrimSpace(item.Text); text != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": text,
				})
			}
			continue
		}
		if item.Type != "image_url" && item.Type != "video_url" && item.Type != "audio_url" {
			return nil, fmt.Errorf("unsupported seedance content type %q", item.Type)
		}
		value := map[string]any{
			"type":    item.Type,
			item.Type: map[string]any{"url": item.URL},
		}
		if item.Role != "" {
			value["role"] = item.Role
		}
		content = append(content, value)
	}
	if len(content) == 0 {
		return nil, errors.New("seedance content is required")
	}
	body := map[string]any{
		"model":          req.Model,
		"content":        content,
		"generate_audio": req.Audio,
	}
	if req.Resolution != "" {
		body["resolution"] = req.Resolution
	}
	if req.Ratio != "" {
		body["ratio"] = req.Ratio
	}
	if req.Duration > 0 {
		body["duration"] = req.Duration
	}
	return body, nil
}

func (Codec) DecodeSubmit(responseBody []byte) (*video.SubmitResult, error) {
	data, err := unwrapProviderData(responseBody)
	if err != nil {
		return nil, fmt.Errorf("parse submit response: %w", err)
	}
	var result struct {
		ID      json.RawMessage `json:"id"`
		Status  string          `json:"status"`
		Content *struct {
			VideoURL     string `json:"video_url"`
			LastFrameURL string `json:"last_frame_url"`
		} `json:"content"`
		Duration float64 `json:"duration"`
		Result   *struct {
			VideoURL     string  `json:"video_url"`
			ThumbnailURL string  `json:"thumbnail_url"`
			Duration     float64 `json:"duration"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse submit data: %w", err)
	}
	providerTaskID, err := parseProviderTaskID(result.ID)
	if err != nil {
		return nil, fmt.Errorf("parse provider task id: %w", err)
	}
	status := mapStatus(result.Status)
	if strings.TrimSpace(result.Status) == "" {
		status = video.VideoTaskStatusSubmitted
	}
	if providerTaskID == "" && !status.IsTerminal() {
		return nil, errors.New("upstream submit response is missing task id")
	}
	submit := &video.SubmitResult{ProviderTaskID: providerTaskID, Status: status}
	if result.Content != nil {
		submit.Result = &video.GenerationResult{
			VideoURL: result.Content.VideoURL, ThumbnailURL: result.Content.LastFrameURL,
			Duration: result.Duration,
		}
	} else if result.Result != nil {
		submit.Result = &video.GenerationResult{
			VideoURL: result.Result.VideoURL, ThumbnailURL: result.Result.ThumbnailURL,
			Duration: result.Result.Duration,
		}
	}
	return submit, nil
}

func (Codec) DecodePoll(body []byte) (*video.Progress, error) {
	data, err := unwrapProviderData(body)
	if err != nil {
		return nil, fmt.Errorf("parse poll response: %w", err)
	}
	var result struct {
		Status   string `json:"status"`
		Progress int    `json:"progress"`
		Content  *struct {
			VideoURL     string `json:"video_url"`
			LastFrameURL string `json:"last_frame_url"`
		} `json:"content"`
		Duration float64         `json:"duration"`
		Error    json.RawMessage `json:"error"`
		Result   *struct {
			VideoURL     string  `json:"video_url"`
			ThumbnailURL string  `json:"thumbnail_url"`
			Duration     float64 `json:"duration"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse poll data: %w", err)
	}
	percent := result.Progress
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}
	status := mapStatus(result.Status)
	if status == video.VideoTaskStatusCompleted {
		percent = 100
	}
	progress := &video.Progress{Status: status, Percent: percent, Error: providerErrorMessage(result.Error)}
	if strings.EqualFold(strings.TrimSpace(result.Status), "expired") && progress.Error == "" {
		progress.Error = "upstream video task expired"
	}
	if result.Content != nil {
		progress.Result = &video.GenerationResult{
			VideoURL: result.Content.VideoURL, ThumbnailURL: result.Content.LastFrameURL,
			Duration: result.Duration,
		}
	} else if result.Result != nil {
		progress.Result = &video.GenerationResult{
			VideoURL: result.Result.VideoURL, ThumbnailURL: result.Result.ThumbnailURL,
			Duration: result.Result.Duration,
		}
	}
	return progress, nil
}

func validateAdapterConfig(baseURL string, config adapterConfig) error {
	if baseURL == "" {
		return errors.New("seedance base URL is required")
	}
	for name, path := range map[string]string{
		"submit_path": config.SubmitPath, "poll_path": config.PollPath, "cancel_path": config.CancelPath,
	} {
		if !strings.HasPrefix(path, "/") {
			return fmt.Errorf("seedance %s must start with /", name)
		}
	}
	if !strings.Contains(config.PollPath, taskIDPlaceholder) || !strings.Contains(config.CancelPath, taskIDPlaceholder) {
		return fmt.Errorf("seedance poll_path and cancel_path must contain %s", taskIDPlaceholder)
	}
	return nil
}

func officialConfig() adapterConfig {
	return adapterConfig{
		SubmitPath: defaultTaskPath,
		PollPath:   defaultTaskPath + "/" + taskIDPlaceholder,
		CancelPath: defaultTaskPath + "/" + taskIDPlaceholder,
	}
}

func unwrapProviderData(body []byte) (json.RawMessage, error) {
	var envelope struct {
		Code    *int            `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code == nil {
		return json.RawMessage(body), nil
	}
	if *envelope.Code != 0 {
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = truncate(body, 512)
		}
		return nil, fmt.Errorf("upstream code %d: %s", *envelope.Code, message)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, errors.New("upstream response envelope is missing data")
	}
	return envelope.Data, nil
}

func parseProviderTaskID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return strings.TrimSpace(value), nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.String(), nil
	}
	return "", fmt.Errorf("invalid provider task id %s", strconv.Quote(string(raw)))
}

func mapStatus(upstream string) video.VideoTaskStatus {
	switch strings.ToLower(strings.TrimSpace(upstream)) {
	case "queued", "created", "submitted", "pending", "waiting":
		return video.VideoTaskStatusSubmitted
	case "running", "processing":
		return video.VideoTaskStatusTracking
	case "succeeded", "completed", "success":
		return video.VideoTaskStatusCompleted
	case "failed", "error", "expired":
		return video.VideoTaskStatusFailed
	case "cancel", "canceled", "cancelled":
		return video.VideoTaskStatusCancelled
	default:
		return video.VideoTaskStatusTracking
	}
}

func providerErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var message string
	if json.Unmarshal(raw, &message) == nil {
		return strings.TrimSpace(message)
	}
	var value struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &value) == nil {
		message = strings.TrimSpace(value.Message)
		if message != "" {
			return message
		}
		return strings.TrimSpace(value.Code)
	}
	return truncate(raw, 512)
}

func truncate(value []byte, max int) string {
	if len(value) <= max {
		return string(value)
	}
	return string(value[:max]) + "..."
}
