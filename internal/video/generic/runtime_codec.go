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
