package generic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mirainya/Prism/internal/video"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

// RuntimeRequest is the provider request produced by the declaration-only
// generic codec. Credentials are intentionally described, not injected; the
// unified dispatcher adds the pinned credential immediately before sending.
type RuntimeRequest struct {
	Method           string
	Path             string
	Body             []byte
	Header           http.Header
	CredentialHeader string
	CredentialPrefix string
}

// RuntimeResponse retains the exact decimal duration alongside the legacy
// compatibility projection. Billing must never derive quantities from a
// float64 value.
type RuntimeResponse struct {
	ProviderTaskID string
	Status         video.VideoTaskStatus
	Percent        int
	Result         *video.GenerationResult
	Duration       string
	Error          string
}

// RuntimeCodec exposes the existing JSON-task declaration as a pure codec.
// It performs no network I/O and contains no provider credential.
type RuntimeCodec struct {
	adapter *Adapter
}

// NewRuntimeCodec validates the signed catalog configuration. The catalog
// value uses the same {"adapter": {...}} envelope as legacy generic channels,
// which permits a lossless migration without retaining the legacy tables.
func NewRuntimeCodec(baseURL string, catalogConfig []byte) (*RuntimeCodec, error) {
	channel := &video.VideoChannel{
		AdapterType: video.AdapterTypeGeneric,
		BaseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		ExtraConfig: append([]byte(nil), catalogConfig...),
	}
	config, err := parseConfig(channel)
	if err != nil {
		return nil, err
	}
	if config.AuthLocation != "header" {
		return nil, errors.New("generic runtime supports header authentication only")
	}
	return &RuntimeCodec{adapter: &Adapter{config: config}}, nil
}

// ValidateRuntimeCatalog verifies the cross-field contract that cannot be
// checked by JSON decoding alone: the fixed transport must match the declared
// submit operation and the product model must have a validation rule when the
// declaration contains a model matrix.
func ValidateRuntimeCatalog(baseURL, method, path, vendorModel string, catalogConfig []byte) error {
	codec, err := NewRuntimeCodec(baseURL, catalogConfig)
	if err != nil {
		return err
	}
	configuredMethod, configuredPath := codec.SubmitMethodPath()
	if strings.ToUpper(strings.TrimSpace(method)) != configuredMethod || strings.TrimSpace(path) != configuredPath {
		return errors.New("generic submit operation does not match the catalog transport")
	}
	vendorModel = strings.TrimSpace(vendorModel)
	if vendorModel == "" {
		return errors.New("generic vendor model is required")
	}
	if models := codec.adapter.config.Validation.Models; len(models) != 0 {
		if _, exists := models[vendorModel]; !exists {
			return fmt.Errorf("generic validation does not declare vendor model %q", vendorModel)
		}
	}
	return nil
}

// ValidatePublishedRuntimeCatalog is the stricter write-boundary check. The
// legacy migration uses ValidateRuntimeCatalog to preserve existing snapshots.
func ValidatePublishedRuntimeCatalog(baseURL, method, path, vendorModel string, catalogConfig []byte) error {
	if err := ValidateRuntimeCatalog(baseURL, method, path, vendorModel, catalogConfig); err != nil {
		return err
	}
	codec, err := NewRuntimeCodec(baseURL, catalogConfig)
	if err != nil {
		return err
	}
	if rule, exists := codec.adapter.config.Validation.Models[strings.TrimSpace(vendorModel)]; exists {
		return validatePublishedVideoCapabilities(strings.TrimSpace(vendorModel), catalogConfig, rule, codec.adapter.config.Request)
	}
	return errors.New("generic product requires a model validation rule")
}

// This check runs when a product is created or edited, not while loading an
// existing release. It compares only the selected vendor model, since an
// adapter declaration can contain rules for other, unrelated models.
func validatePublishedVideoCapabilities(model string, raw []byte, rule validationRule, request requestConfig) error {
	var catalog struct {
		TaskTypes []string `json:"task_types"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return err
	}
	published := make(map[string]bool, len(catalog.TaskTypes))
	for _, mode := range catalog.TaskTypes {
		if mode == "" || published[mode] {
			return fmt.Errorf("generic product %q has an invalid task_types declaration", model)
		}
		published[mode] = true
	}
	validated := make(map[string]bool, len(rule.TaskModes))
	for _, mode := range rule.TaskModes {
		if mode == "" || validated[mode] {
			return fmt.Errorf("generic product %q has an invalid validation task_modes declaration", model)
		}
		validated[mode] = true
	}
	if len(published) == 0 || len(validated) != len(published) {
		return fmt.Errorf("generic product %q task_types must match validation task_modes", model)
	}
	for mode := range published {
		if !validated[mode] && !(isReferenceTaskMode(mode) && validated["references"]) {
			return fmt.Errorf("generic product %q task_types must match validation task_modes", model)
		}
	}
	media := map[string]int{}
	for _, mode := range catalog.TaskTypes {
		switch mode {
		case "first_frame", "first_last_frame":
			media["image_url"] = max(media["image_url"], 1)
		case "video_extension", "video_edit":
			media["video_url"] = max(media["video_url"], 1)
		case "multimodal", "references":
			if rule.MaxImages > 0 {
				media["image_url"] = max(media["image_url"], rule.MaxImages)
			}
			if rule.MaxVideos > 0 {
				media["video_url"] = max(media["video_url"], rule.MaxVideos)
			}
			if rule.MaxAudios > 0 {
				media["audio_url"] = max(media["audio_url"], rule.MaxAudios)
			}
		}
	}
	if *request.IncludeContent && len(media) > 0 && request.ContentFields["url"] == "" && request.ContentFields["provider_object"] == "" {
		return fmt.Errorf("generic product %q includes content without an upstream media URL field", model)
	}
	if !*request.IncludeContent {
		for contentType, limit := range media {
			if !hasProjectionCapacity(request.ContentProjections, model, contentType, limit) {
				return fmt.Errorf("generic product %q declares %d %s inputs without sufficient upstream content mapping", model, limit, contentType)
			}
		}
	}
	return nil
}

func hasProjectionCapacity(projections []contentProjection, model, contentType string, limit int) bool {
	scalarSlots := make(map[string]map[int]bool)
	for _, projection := range projections {
		if (projection.Source != "url" && projection.Source != "provider_object") ||
			!matchesSelector(model, projection.Models) || !matchesSelector(contentType, projection.Types) {
			continue
		}
		if projection.Output != "scalar" {
			return true
		}
		roles := strings.Join(projection.Roles, ",")
		if scalarSlots[roles] == nil {
			scalarSlots[roles] = make(map[int]bool)
		}
		scalarSlots[roles][projection.Index] = true
	}
	capacity := 0
	for _, indexes := range scalarSlots {
		for index := 0; indexes[index]; index++ {
			capacity++
		}
	}
	return capacity >= limit
}

func (c *RuntimeCodec) PrepareSubmit(_ context.Context, request *video.GenerateRequest) (RuntimeRequest, error) {
	if c == nil || c.adapter == nil {
		return RuntimeRequest{}, errors.New("generic runtime codec is not initialized")
	}
	if err := c.adapter.validateRequest(request); err != nil {
		return RuntimeRequest{}, err
	}
	providerRequest, err := c.adapter.buildRequest(request)
	if err != nil {
		return RuntimeRequest{}, err
	}
	body, err := json.Marshal(providerRequest.Body)
	if err != nil {
		return RuntimeRequest{}, err
	}
	return c.runtimeRequest(c.adapter.config.Submit, body, providerRequest.Headers, ""), nil
}

func (c *RuntimeCodec) PrepareQuery(providerTaskID string) (RuntimeRequest, error) {
	if c == nil || c.adapter == nil {
		return RuntimeRequest{}, errors.New("generic runtime codec is not initialized")
	}
	providerTaskID = strings.TrimSpace(providerTaskID)
	if providerTaskID == "" || providerTaskID == "." || providerTaskID == ".." {
		return RuntimeRequest{}, errors.New("provider task id is required")
	}
	body, err := c.adapter.operationBody(c.adapter.config.Poll, providerTaskID)
	if err != nil {
		return RuntimeRequest{}, err
	}
	path := strings.ReplaceAll(c.adapter.config.Poll.Path, taskIDPlaceholder, url.PathEscape(providerTaskID))
	operation := c.adapter.config.Poll
	operation.Path = path
	headers := map[string]string(nil)
	if len(body) != 0 {
		headers = map[string]string{"Content-Type": "application/json"}
	}
	return c.runtimeRequest(operation, body, headers, providerTaskID), nil
}

func (c *RuntimeCodec) DecodeSubmit(body []byte) (RuntimeResponse, error) {
	if c == nil || c.adapter == nil {
		return RuntimeResponse{}, errors.New("generic runtime codec is not initialized")
	}
	return c.decode(body, c.adapter.config.Response.SubmitDefaultStatus)
}

func (c *RuntimeCodec) DecodePoll(body []byte) (RuntimeResponse, error) {
	if c == nil || c.adapter == nil {
		return RuntimeResponse{}, errors.New("generic runtime codec is not initialized")
	}
	return c.decode(body, c.adapter.config.Response.PollDefaultStatus)
}

func (c *RuntimeCodec) SubmitMethodPath() (string, string) {
	if c == nil || c.adapter == nil {
		return "", ""
	}
	return c.adapter.config.Submit.Method, c.adapter.config.Submit.Path
}

func (c *RuntimeCodec) runtimeRequest(operation operationConfig, body []byte, headers map[string]string, _ string) RuntimeRequest {
	header := make(http.Header, len(headers))
	for name, value := range headers {
		header.Set(name, value)
	}
	return RuntimeRequest{
		Method: operation.Method, Path: operation.Path, Body: body, Header: header,
		CredentialHeader: c.adapter.config.AuthKey,
		CredentialPrefix: valueOrEmpty(c.adapter.config.AuthPrefix),
	}
}

func (c *RuntimeCodec) decode(body []byte, defaultStatus string) (RuntimeResponse, error) {
	parsed, err := c.adapter.parseResponse(body, defaultStatus)
	if err != nil {
		return RuntimeResponse{}, err
	}
	duration, err := c.exactDuration(body)
	if err != nil {
		return RuntimeResponse{}, err
	}
	return RuntimeResponse{
		ProviderTaskID: parsed.ProviderTaskID,
		Status:         parsed.Status,
		Percent:        parsed.Percent,
		Result:         parsed.Result,
		Duration:       duration,
		Error:          parsed.Error,
	}, nil
}

func (c *RuntimeCodec) exactDuration(body []byte) (string, error) {
	payload, err := c.adapter.responsePayload(body)
	if err != nil {
		return "", err
	}
	value := firstResult(payload, c.adapter.config.Response.DurationPaths)
	if !value.Exists() {
		return "", nil
	}
	raw := strings.TrimSpace(value.Raw)
	if value.Type == gjson.String {
		raw = strings.TrimSpace(value.String())
	}
	amount, err := decimal.NewFromString(raw)
	if err != nil || amount.IsNegative() {
		return "", errors.New("generic provider duration is invalid")
	}
	return amount.String(), nil
}
