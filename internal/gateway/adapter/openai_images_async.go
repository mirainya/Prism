package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/tidwall/gjson"
)

const asyncOpenAIImagesProfile = "json_image_task_v1"

var imageJSONPath = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.(?:[A-Za-z0-9_-]+|#))*$`)

type asyncImageOperation struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type imageJSONRequestMapping struct {
	InputMode string            `json:"input_mode"`
	Fields    map[string]string `json:"fields"`
	FixedBody map[string]any    `json:"fixed_body"`
}

type asyncImageResponseMapping struct {
	TaskIDPaths         []string          `json:"task_id_paths"`
	StatusPaths         []string          `json:"status_paths"`
	ImageURLPaths       []string          `json:"image_url_paths"`
	ErrorCodePaths      []string          `json:"error_code_paths"`
	ErrorMessagePaths   []string          `json:"error_message_paths"`
	HTTPStatusPaths     []string          `json:"http_status_paths"`
	InputTokenPaths     []string          `json:"input_token_paths"`
	OutputTokenPaths    []string          `json:"output_token_paths"`
	StatusMap           map[string]string `json:"status_map"`
	SubmitDefaultStatus string            `json:"submit_default_status"`
	PollDefaultStatus   string            `json:"poll_default_status"`
	RequirePollStatus   bool              `json:"require_poll_status"`
}

type asyncImageConfig struct {
	Profile    string                    `json:"profile"`
	AuthHeader string                    `json:"auth_header"`
	AuthPrefix string                    `json:"auth_prefix"`
	Submit     asyncImageOperation       `json:"submit"`
	Poll       asyncImageOperation       `json:"poll"`
	Request    imageJSONRequestMapping   `json:"request"`
	Response   asyncImageResponseMapping `json:"response"`
}

type asyncImageEnvelope struct {
	AsyncImage *asyncImageConfig `json:"async_image"`
}

var imageJSONRequestFields = map[string]func(OpenAIImagesRequest) (any, bool){
	"model":                func(value OpenAIImagesRequest) (any, bool) { return value.Model, value.Model != "" },
	"prompt":               func(value OpenAIImagesRequest) (any, bool) { return value.Prompt, value.Prompt != "" },
	"image_urls":           func(value OpenAIImagesRequest) (any, bool) { return value.ImageURLs, len(value.ImageURLs) != 0 },
	"image_url_objects":    imageURLObjects,
	"mask_urls":            func(value OpenAIImagesRequest) (any, bool) { return value.MaskURLs, len(value.MaskURLs) != 0 },
	"mask_url_object":      maskURLObject,
	"n":                    func(value OpenAIImagesRequest) (any, bool) { return value.N, value.N != 0 },
	"size":                 func(value OpenAIImagesRequest) (any, bool) { return value.Size, value.Size != "" },
	"aspect_ratio":         func(value OpenAIImagesRequest) (any, bool) { return value.AspectRatio, value.AspectRatio != "" },
	"size_or_aspect_ratio": imageSizeOrAspectRatio,
	"quality":              func(value OpenAIImagesRequest) (any, bool) { return value.Quality, value.Quality != "" },
	"response_format":      func(value OpenAIImagesRequest) (any, bool) { return value.ResponseFormat, value.ResponseFormat != "" },
	"output_format":        func(value OpenAIImagesRequest) (any, bool) { return value.OutputFormat, value.OutputFormat != "" },
	"output_compression":   func(value OpenAIImagesRequest) (any, bool) { return optionalInt(value.OutputCompression) },
	"moderation":           func(value OpenAIImagesRequest) (any, bool) { return value.Moderation, value.Moderation != "" },
	"style":                func(value OpenAIImagesRequest) (any, bool) { return value.Style, value.Style != "" },
	"background":           func(value OpenAIImagesRequest) (any, bool) { return value.Background, value.Background != "" },
	"input_fidelity":       func(value OpenAIImagesRequest) (any, bool) { return value.InputFidelity, value.InputFidelity != "" },
	"user":                 func(value OpenAIImagesRequest) (any, bool) { return value.User, value.User != "" },
}

func imageURLObjects(value OpenAIImagesRequest) (any, bool) {
	if len(value.ImageURLs) == 0 {
		return nil, false
	}
	images := make([]map[string]string, len(value.ImageURLs))
	for index, location := range value.ImageURLs {
		images[index] = map[string]string{"image_url": location}
	}
	return images, true
}

func maskURLObject(value OpenAIImagesRequest) (any, bool) {
	if len(value.MaskURLs) == 0 {
		return nil, false
	}
	return map[string]string{"image_url": value.MaskURLs[0]}, true
}

func imageSizeOrAspectRatio(value OpenAIImagesRequest) (any, bool) {
	if value.Size != "" {
		return value.Size, true
	}
	return value.AspectRatio, value.AspectRatio != ""
}

func optionalInt(value *int) (any, bool) {
	if value == nil {
		return nil, false
	}
	return *value, true
}

// AsyncOpenAIImages executes an immutable JSON task declaration for providers
// that expose OpenAI-shaped image input but return an asynchronous task ID.
type AsyncOpenAIImages struct{}

func ValidateOpenAIImagesCatalog(method, path, vendorModel, taskScope string, constraints []byte) error {
	config, configured, err := parseAsyncImageConfig(constraints)
	if err != nil {
		return repository.ErrInvalidInput
	}
	if taskScope != "task" {
		if configured {
			return repository.ErrInvalidInput
		}
		_, syncConfigured, syncErr := parseSynchronousImageEditConfig(constraints)
		if syncErr != nil {
			return repository.ErrInvalidInput
		}
		if syncConfigured && (strings.ToUpper(strings.TrimSpace(method)) != http.MethodPost || !validImageRequestPath(strings.TrimSpace(path)) || strings.TrimSpace(vendorModel) == "") {
			return repository.ErrInvalidInput
		}
		return nil
	}
	if !configured {
		return repository.ErrInvalidInput
	}
	if strings.ToUpper(strings.TrimSpace(method)) != config.Submit.Method || strings.TrimSpace(path) != config.Submit.Path || strings.TrimSpace(vendorModel) == "" {
		return repository.ErrInvalidInput
	}
	return nil
}

func (AsyncOpenAIImages) Prepare(ctx context.Context, action string, fixed repository.AsyncDispatch, payload []byte, taskID string) (runtime.AsyncRequest, error) {
	if ctx == nil {
		return runtime.AsyncRequest{}, repository.ErrInvalidInput
	}
	return prepareAsyncOpenAIImages(ctx, action, fixed, payload, taskID, nil)
}

func (AsyncOpenAIImages) PrepareWithAssets(ctx context.Context, action string, fixed repository.AsyncDispatch, payload []byte, taskID string, loader runtime.AsyncAssetLoader) (runtime.AsyncRequest, error) {
	if ctx == nil || loader == nil {
		return runtime.AsyncRequest{}, repository.ErrInvalidInput
	}
	return prepareAsyncOpenAIImages(ctx, action, fixed, payload, taskID, asyncImageAssetLoader{loader: loader})
}

func prepareAsyncOpenAIImages(ctx context.Context, action string, fixed repository.AsyncDispatch, payload []byte, taskID string, loader ImageAssetLoader) (runtime.AsyncRequest, error) {
	config, configured, err := parseAsyncImageConfig(fixed.AdapterConfig)
	if err != nil || !configured {
		return runtime.AsyncRequest{}, repository.ErrInvalidInput
	}
	request := runtime.AsyncRequest{CredentialHeader: config.AuthHeader, CredentialPrefix: config.AuthPrefix}
	switch action {
	case "submit":
		input, operation, err := validateAsyncOpenAIImageSubmit(config, fixed, payload, taskID)
		if err != nil {
			return runtime.AsyncRequest{}, err
		}
		switch config.Request.InputMode {
		case "json":
			body, err := buildMappedImageJSONRequest(input, config.Request)
			if err != nil {
				return runtime.AsyncRequest{}, err
			}
			request.Body = body
			request.Header = http.Header{"Content-Type": []string{"application/json"}}
		case "multipart":
			if operation != ImagesEdit || loader == nil {
				return runtime.AsyncRequest{}, repository.ErrInvalidInput
			}
			body, contentType, err := marshalImageEdit(ctx, input, loader)
			if err != nil {
				return runtime.AsyncRequest{}, err
			}
			request.Body = body
			request.Header = http.Header{"Content-Type": []string{contentType}}
		default:
			return runtime.AsyncRequest{}, repository.ErrInvalidInput
		}
		request.Method, request.Path = config.Submit.Method, config.Submit.Path
	case "query":
		if strings.TrimSpace(taskID) == "" {
			return runtime.AsyncRequest{}, repository.ErrInvalidInput
		}
		request.Method = config.Poll.Method
		request.Path = strings.ReplaceAll(config.Poll.Path, "{task_id}", url.PathEscape(taskID))
	default:
		return runtime.AsyncRequest{}, repository.ErrInvalidInput
	}
	return request, nil
}

func (AsyncOpenAIImages) ValidateSubmit(fixed repository.AsyncDispatch, payload []byte) error {
	config, configured, err := parseAsyncImageConfig(fixed.AdapterConfig)
	if err != nil || !configured {
		return repository.ErrInvalidInput
	}
	input, _, err := validateAsyncOpenAIImageSubmit(config, fixed, payload, "")
	if err != nil {
		return err
	}
	if config.Request.InputMode == "json" {
		_, err = buildMappedImageJSONRequest(input, config.Request)
	}
	return err
}

func validateAsyncOpenAIImageSubmit(config asyncImageConfig, fixed repository.AsyncDispatch, payload []byte, taskID string) (OpenAIImagesRequest, string, error) {
	if taskID != "" || fixed.Method != config.Submit.Method || fixed.Path != config.Submit.Path {
		return OpenAIImagesRequest{}, "", repository.ErrConflict
	}
	input, err := decodeOpenAIImagesRequest(payload)
	if err != nil {
		return OpenAIImagesRequest{}, "", err
	}
	operation := ImagesGenerate
	if len(input.ImageURLs) != 0 {
		operation = ImagesEdit
	}
	if err := validateOpenAIImagesRequest(operation, input); err != nil {
		return OpenAIImagesRequest{}, "", err
	}
	if config.Request.InputMode == "multipart" && operation != ImagesEdit {
		return OpenAIImagesRequest{}, "", repository.ErrInvalidInput
	}
	input.Model = fixed.VendorModel
	// Async results must remain URL-addressable so the delivery pipeline can
	// validate and import them without persisting provider-owned base64 data.
	input.ResponseFormat = "url"
	return input, operation, nil
}

type asyncImageAssetLoader struct {
	loader runtime.AsyncAssetLoader
}

func (loader asyncImageAssetLoader) LoadImage(ctx context.Context, location string) (ImageAsset, error) {
	asset, err := loader.loader.LoadAsyncAsset(ctx, location)
	if err != nil {
		return ImageAsset{}, err
	}
	return ImageAsset{Data: asset.Data, ContentType: asset.ContentType}, nil
}

func (AsyncOpenAIImages) Decode(string, []byte, []byte) (runtime.AsyncObservation, error) {
	return runtime.AsyncObservation{}, repository.ErrInvalidInput
}

// DecodeFailureWithDispatch extracts the declared provider failure fields from
// a non-2xx response without applying the normal task-state defaults.
func (AsyncOpenAIImages) DecodeFailureWithDispatch(fixed repository.AsyncDispatch, response []byte, transportStatus uint16) (runtime.AsyncObservation, error) {
	config, configured, err := parseAsyncImageConfig(fixed.AdapterConfig)
	if err != nil || !configured || transportStatus < 100 || transportStatus > 599 {
		return runtime.AsyncObservation{}, repository.ErrInvalidInput
	}
	if !gjson.ValidBytes(response) {
		return runtime.AsyncObservation{}, ErrInvalidImageResponse
	}
	out := runtime.AsyncObservation{State: execution.AsyncFailed}
	if err := applyAsyncImageFailure(&out, response, config.Response, &transportStatus); err != nil {
		return runtime.AsyncObservation{}, err
	}
	// The actual non-2xx exchange status is authoritative. A mapped status is
	// only needed when a successful poll envelope reports a terminal failure.
	out.ProviderHTTPStatus = &transportStatus
	return out, nil
}

func (AsyncOpenAIImages) DecodeWithDispatch(action string, fixed repository.AsyncDispatch, request, response []byte) (runtime.AsyncObservation, error) {
	config, configured, err := parseAsyncImageConfig(fixed.AdapterConfig)
	if err != nil || !configured {
		return runtime.AsyncObservation{}, repository.ErrInvalidInput
	}
	if !gjson.ValidBytes(response) {
		return runtime.AsyncObservation{}, ErrInvalidImageResponse
	}
	defaultStatus := config.Response.SubmitDefaultStatus
	if action == "query" {
		defaultStatus = config.Response.PollDefaultStatus
	} else if action != "submit" {
		return runtime.AsyncObservation{}, repository.ErrInvalidInput
	}
	taskID := firstAsyncImageText(response, config.Response.TaskIDPaths)
	status := strings.ToLower(firstAsyncImageText(response, config.Response.StatusPaths))
	if status == "" {
		if action == "query" && config.Response.RequirePollStatus {
			return runtime.AsyncObservation{}, ErrInvalidImageResponse
		}
		status = defaultStatus
	}
	mappedStatus, mapped := config.Response.StatusMap[status]
	if mapped {
		status = mappedStatus
	}
	state, ok := asyncImageState(status)
	if !ok {
		return runtime.AsyncObservation{}, ErrInvalidImageResponse
	}
	usage, usageErr := asyncImageUsage(response, config.Response)
	if usageErr != nil {
		if state != execution.AsyncFailed {
			return runtime.AsyncObservation{}, usageErr
		}
		usage = OpenAIImagesUsage{}
	}
	out := runtime.AsyncObservation{TaskID: taskID, State: state, Facts: billing.Facts{
		Events:     map[billing.ChargeEvent]bool{billing.ChargeAccepted: true},
		Quantities: make(map[billing.QuantitySource]string),
	}}
	if state == execution.AsyncFailed {
		if err := applyAsyncImageFailure(&out, response, config.Response, nil); err != nil {
			return runtime.AsyncObservation{}, err
		}
	}
	if usage.InputTokens != nil {
		out.Facts.Quantities[billing.QuantityInputTokens] = strconv.FormatInt(*usage.InputTokens, 10)
	}
	if usage.OutputTokens != nil {
		out.Facts.Quantities[billing.QuantityOutputTokens] = strconv.FormatInt(*usage.OutputTokens, 10)
	}
	if state == execution.AsyncSucceeded {
		urls, urlErr := asyncImageURLs(response, config.Response.ImageURLPaths)
		if urlErr != nil {
			return runtime.AsyncObservation{}, urlErr
		}
		if len(urls) == 0 || len(urls) > 10 {
			return runtime.AsyncObservation{}, delivery.ErrInvalidResult
		}
		images := make([]delivery.ImageOutput, len(urls))
		for _, location := range urls {
			out.Sources = append(out.Sources, delivery.RemoteResult{Role: "image", URL: location})
		}
		out.Result, err = json.Marshal(delivery.ImageResult{
			SchemaVersion: delivery.ResultSchemaVersion,
			Kind:          delivery.ImageResultKind,
			Images:        images,
		})
		if err != nil {
			return runtime.AsyncObservation{}, err
		}
		out.Facts.Events[billing.ChargeSucceeded] = true
		out.Facts.Quantities[billing.QuantityGeneratedImages] = strconv.Itoa(len(images))
		out.Facts.Expr, err = imageBillingFactsForOutcome(len(images), usage, true)
	} else {
		out.Facts.Expr, err = imageBillingFactsForOutcome(0, usage, false)
	}
	return out, err
}

func applyAsyncImageFailure(out *runtime.AsyncObservation, response []byte, mapping asyncImageResponseMapping, fallbackStatus *uint16) error {
	if out == nil {
		return repository.ErrInvalidInput
	}
	out.ProviderErrorCode = firstAsyncImageText(response, mapping.ErrorCodePaths)
	out.ProviderErrorMessage = truncateAsyncImageErrorMessage(firstAsyncImageText(response, mapping.ErrorMessagePaths))
	status, err := firstAsyncImageHTTPStatus(response, mapping.HTTPStatusPaths)
	if err != nil {
		return err
	}
	if status == nil && fallbackStatus != nil {
		value := *fallbackStatus
		status = &value
	}
	out.ProviderHTTPStatus = status
	return nil
}

func parseAsyncImageConfig(raw []byte) (asyncImageConfig, bool, error) {
	var envelope asyncImageEnvelope
	if len(raw) == 0 || json.Unmarshal(raw, &envelope) != nil {
		return asyncImageConfig{}, false, repository.ErrInvalidInput
	}
	if envelope.AsyncImage == nil {
		return asyncImageConfig{}, false, nil
	}
	config := *envelope.AsyncImage
	config.Profile = strings.TrimSpace(config.Profile)
	config.Request.InputMode = strings.ToLower(strings.TrimSpace(config.Request.InputMode))
	if config.Request.InputMode == "" {
		config.Request.InputMode = "json"
	}
	config.AuthHeader = strings.TrimSpace(config.AuthHeader)
	if config.AuthHeader == "" {
		config.AuthHeader = "Authorization"
	}
	if config.AuthPrefix == "" {
		config.AuthPrefix = "Bearer "
	}
	config.Submit.Method = strings.ToUpper(strings.TrimSpace(config.Submit.Method))
	config.Submit.Path = strings.TrimSpace(config.Submit.Path)
	config.Poll.Method = strings.ToUpper(strings.TrimSpace(config.Poll.Method))
	config.Poll.Path = strings.TrimSpace(config.Poll.Path)
	config.Response.SubmitDefaultStatus = strings.ToLower(strings.TrimSpace(config.Response.SubmitDefaultStatus))
	config.Response.PollDefaultStatus = strings.ToLower(strings.TrimSpace(config.Response.PollDefaultStatus))
	if config.Response.SubmitDefaultStatus == "" {
		config.Response.SubmitDefaultStatus = "accepted"
	}
	if config.Response.PollDefaultStatus == "" {
		config.Response.PollDefaultStatus = "running"
	}
	normalized := make(map[string]string, len(config.Response.StatusMap))
	for source, target := range config.Response.StatusMap {
		normalized[strings.ToLower(strings.TrimSpace(source))] = strings.ToLower(strings.TrimSpace(target))
	}
	config.Response.StatusMap = normalized
	if err := validateAsyncImageConfig(config); err != nil {
		return asyncImageConfig{}, false, err
	}
	return config, true, nil
}

func validateAsyncImageConfig(config asyncImageConfig) error {
	if config.Profile != asyncOpenAIImagesProfile || config.Submit.Method != http.MethodPost || !validImageRequestPath(config.Submit.Path) ||
		config.Poll.Method != http.MethodGet || !validImageRequestPath(config.Poll.Path) || strings.Count(config.Poll.Path, "{task_id}") != 1 ||
		len(config.Response.TaskIDPaths) == 0 || len(config.Response.StatusPaths) == 0 || len(config.Response.ImageURLPaths) == 0 ||
		len(config.AuthHeader) > 128 || strings.ContainsAny(config.AuthHeader+config.AuthPrefix, "\r\n") {
		return repository.ErrInvalidInput
	}
	if config.Request.InputMode == "multipart" {
		if len(config.Request.Fields) != 0 || len(config.Request.FixedBody) != 0 {
			return repository.ErrInvalidInput
		}
		return validateAsyncImageResponseConfig(config)
	}
	if config.Request.InputMode != "json" || len(config.Request.Fields) == 0 {
		return repository.ErrInvalidInput
	}
	if _, ok := config.Request.Fields["model"]; !ok {
		return repository.ErrInvalidInput
	}
	if _, ok := config.Request.Fields["prompt"]; !ok {
		return repository.ErrInvalidInput
	}
	targets := make([][]string, 0, len(config.Request.Fields))
	for source, target := range config.Request.Fields {
		if _, ok := imageJSONRequestFields[source]; !ok || !imageJSONPath.MatchString(target) {
			return repository.ErrInvalidInput
		}
		segments := strings.Split(target, ".")
		for _, existing := range targets {
			if imageJSONPathsConflict(existing, segments) {
				return repository.ErrInvalidInput
			}
		}
		targets = append(targets, segments)
	}
	for _, fixedPath := range imageJSONFixedLeafPaths(config.Request.FixedBody) {
		for _, target := range targets {
			if imageJSONPathsConflict(fixedPath, target) {
				return repository.ErrInvalidInput
			}
		}
	}
	if err := validateAsyncImageResponseConfig(config); err != nil {
		return err
	}
	encoded, err := json.Marshal(config.Request.FixedBody)
	if err != nil || len(encoded) > 16<<10 {
		return repository.ErrInvalidInput
	}
	return nil
}

func validateAsyncImageResponseConfig(config asyncImageConfig) error {
	for _, paths := range [][]string{
		config.Response.TaskIDPaths,
		config.Response.StatusPaths,
		config.Response.ImageURLPaths,
		config.Response.ErrorCodePaths,
		config.Response.ErrorMessagePaths,
		config.Response.HTTPStatusPaths,
		config.Response.InputTokenPaths,
		config.Response.OutputTokenPaths,
	} {
		for _, path := range paths {
			if !imageJSONPath.MatchString(path) {
				return repository.ErrInvalidInput
			}
		}
	}
	for source, value := range config.Response.StatusMap {
		if source == "" {
			return repository.ErrInvalidInput
		}
		if _, ok := asyncImageState(value); !ok {
			return repository.ErrInvalidInput
		}
	}
	for _, value := range []string{config.Response.SubmitDefaultStatus, config.Response.PollDefaultStatus} {
		if _, ok := asyncImageState(value); !ok {
			return repository.ErrInvalidInput
		}
	}
	return nil
}

func buildMappedImageJSONRequest(input OpenAIImagesRequest, mapping imageJSONRequestMapping) ([]byte, error) {
	body := cloneImageJSONObject(mapping.FixedBody)
	sources := make([]string, 0, len(mapping.Fields))
	for source := range mapping.Fields {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	for _, source := range sources {
		target := mapping.Fields[source]
		value, present := imageJSONRequestFields[source](input)
		if !present {
			continue
		}
		if err := setImageJSONField(body, target, value); err != nil {
			return nil, err
		}
	}
	return json.Marshal(body)
}

func imageJSONPathsConflict(left, right []string) bool {
	shorter := len(left)
	if len(right) < shorter {
		shorter = len(right)
	}
	for index := 0; index < shorter; index++ {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func imageJSONFixedLeafPaths(source map[string]any) [][]string {
	paths := make([][]string, 0, len(source))
	var walk func(any, []string)
	walk = func(value any, path []string) {
		object, ok := value.(map[string]any)
		if !ok || len(object) == 0 {
			paths = append(paths, append([]string(nil), path...))
			return
		}
		for key, child := range object {
			walk(child, append(path, key))
		}
	}
	for key, value := range source {
		walk(value, []string{key})
	}
	return paths
}

func cloneImageJSONObject(source map[string]any) map[string]any {
	if len(source) == 0 {
		return make(map[string]any)
	}
	encoded, _ := json.Marshal(source)
	var result map[string]any
	_ = json.Unmarshal(encoded, &result)
	return result
}

func setImageJSONField(target map[string]any, path string, value any) error {
	segments := strings.Split(path, ".")
	current := target
	for _, segment := range segments[:len(segments)-1] {
		next, exists := current[segment]
		if !exists {
			child := make(map[string]any)
			current[segment] = child
			current = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return errors.New("async image request path conflicts with a fixed value")
		}
		current = child
	}
	current[segments[len(segments)-1]] = value
	return nil
}

func firstAsyncImageText(body []byte, paths []string) string {
	for _, path := range paths {
		value := gjson.GetBytes(body, path)
		if value.Exists() && strings.TrimSpace(value.String()) != "" {
			return strings.TrimSpace(value.String())
		}
	}
	return ""
}

func asyncImageURLs(body []byte, paths []string) ([]string, error) {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		value := gjson.GetBytes(body, path)
		values := []gjson.Result{value}
		if value.IsArray() {
			values = value.Array()
		}
		for _, item := range values {
			location := strings.TrimSpace(item.String())
			if location == "" {
				continue
			}
			location = normalizeAsyncImageURL(location)
			if !delivery.ValidRemoteURL(location) {
				return nil, delivery.ErrInvalidResult
			}
			if _, exists := seen[location]; exists {
				continue
			}
			seen[location] = struct{}{}
			result = append(result, location)
		}
	}
	return result, nil
}

func normalizeAsyncImageURL(location string) string {
	if delivery.ValidRemoteURL(location) {
		return location
	}
	candidate := "https://" + location
	if strings.HasPrefix(location, "//") {
		candidate = "https:" + location
	}
	if delivery.ValidRemoteURL(candidate) {
		return candidate
	}
	return location
}

func firstAsyncImageHTTPStatus(body []byte, paths []string) (*uint16, error) {
	for _, path := range paths {
		value := gjson.GetBytes(body, path)
		if !value.Exists() || value.Type == gjson.Null {
			continue
		}
		var raw string
		switch value.Type {
		case gjson.Number:
			raw = value.Raw
		case gjson.String:
			raw = strings.TrimSpace(value.String())
		default:
			return nil, ErrInvalidImageResponse
		}
		status, err := strconv.ParseUint(raw, 10, 16)
		if err != nil || status < 100 || status > 599 {
			return nil, ErrInvalidImageResponse
		}
		result := uint16(status)
		return &result, nil
	}
	return nil, nil
}

func truncateAsyncImageErrorMessage(value string) string {
	const maxRunes = 500
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return value
}

func asyncImageUsage(body []byte, mapping asyncImageResponseMapping) (OpenAIImagesUsage, error) {
	inputTokens, err := firstAsyncImageTokenCount(body, mapping.InputTokenPaths)
	if err != nil {
		return OpenAIImagesUsage{}, err
	}
	outputTokens, err := firstAsyncImageTokenCount(body, mapping.OutputTokenPaths)
	if err != nil {
		return OpenAIImagesUsage{}, err
	}
	return OpenAIImagesUsage{InputTokens: inputTokens, OutputTokens: outputTokens}, nil
}

func firstAsyncImageTokenCount(body []byte, paths []string) (*int64, error) {
	for _, path := range paths {
		value := gjson.GetBytes(body, path)
		if !value.Exists() || value.Type == gjson.Null {
			continue
		}
		var raw string
		switch value.Type {
		case gjson.Number:
			raw = value.Raw
		case gjson.String:
			raw = strings.TrimSpace(value.String())
		default:
			return nil, ErrInvalidImageResponse
		}
		if raw == "" {
			continue
		}
		count, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || count < 0 {
			return nil, ErrInvalidImageResponse
		}
		return &count, nil
	}
	return nil, nil
}

func asyncImageState(value string) (execution.AsyncState, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "accepted":
		return execution.AsyncAccepted, true
	case "running":
		return execution.AsyncRunning, true
	case "succeeded":
		return execution.AsyncSucceeded, true
	case "failed":
		return execution.AsyncFailed, true
	case "cancelled":
		return execution.AsyncCancelled, true
	default:
		return "", false
	}
}

var _ runtime.AsyncCodec = AsyncOpenAIImages{}
var _ runtime.AssetAwareAsyncCodec = AsyncOpenAIImages{}
var _ runtime.DispatchAwareAsyncCodec = AsyncOpenAIImages{}
