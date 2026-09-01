package video

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mirainya/Prism/pkg/safeurl"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const presignedDispositionProfile = "disposition_v1"

var (
	errUploadSessionExpired = errors.New("presigned upload session expired")
	presignedJSONFieldName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
)

type presignedUploadConfig struct {
	Profile              string   `json:"profile"`
	ApplyPath            string   `json:"apply_path"`
	WaitPath             string   `json:"wait_path"`
	CompletePath         string   `json:"complete_path"`
	AbortPath            string   `json:"abort_path"`
	ResponseRoot         string   `json:"response_root"`
	SuccessCodePath      string   `json:"success_code_path"`
	ReferencePaths       []string `json:"reference_paths"`
	ReferenceExpiryPaths []string `json:"reference_expiry_paths"`
	IdempotencyHeader    string   `json:"idempotency_header"`
	IdempotencyBodyField string   `json:"idempotency_body_field"`
	AuthHeader           string   `json:"auth_header"`
	AuthPrefix           string   `json:"auth_prefix"`
	Purpose              string   `json:"purpose"`
	MaxWaitSeconds       int      `json:"max_wait_seconds"`
	PartConcurrency      int      `json:"part_concurrency"`
	ControlRetries       int      `json:"control_retries"`
	PartRetries          int      `json:"part_retries"`
	MaxSessionRestarts   int      `json:"max_session_restarts"`
}

func (c *presignedUploadConfig) defaults() {
	c.Profile = strings.TrimSpace(c.Profile)
	c.ApplyPath = strings.TrimSpace(c.ApplyPath)
	c.WaitPath = strings.TrimSpace(c.WaitPath)
	c.CompletePath = strings.TrimSpace(c.CompletePath)
	c.AbortPath = strings.TrimSpace(c.AbortPath)
	c.ResponseRoot = strings.TrimSpace(c.ResponseRoot)
	c.SuccessCodePath = strings.TrimSpace(c.SuccessCodePath)
	c.IdempotencyHeader = strings.TrimSpace(c.IdempotencyHeader)
	c.IdempotencyBodyField = strings.TrimSpace(c.IdempotencyBodyField)
	c.AuthHeader = strings.TrimSpace(c.AuthHeader)
	c.Purpose = strings.TrimSpace(c.Purpose)
	if c.ResponseRoot == "" {
		c.ResponseRoot = "data"
	}
	if c.SuccessCodePath == "" {
		c.SuccessCodePath = "code"
	}
	if len(c.ReferencePaths) == 0 {
		c.ReferencePaths = []string{"storage_object_id", "reference.storage_object_id", "reference.id", "reference"}
	}
	if len(c.ReferenceExpiryPaths) == 0 {
		c.ReferenceExpiryPaths = []string{"expires_at", "reference.expires_at"}
	}
	if c.IdempotencyHeader == "" {
		c.IdempotencyHeader = "Idempotency-Key"
	}
	if c.AuthHeader == "" {
		c.AuthHeader = "Authorization"
	}
	if c.AuthPrefix == "" {
		c.AuthPrefix = "Bearer "
	}
	if c.Purpose == "" {
		c.Purpose = "reference"
	}
	if c.MaxWaitSeconds <= 0 {
		c.MaxWaitSeconds = 120
	}
	if c.PartConcurrency <= 0 {
		c.PartConcurrency = 3
	}
	if c.ControlRetries <= 0 {
		c.ControlRetries = 2
	}
	if c.PartRetries <= 0 {
		c.PartRetries = 2
	}
	if c.MaxSessionRestarts <= 0 {
		c.MaxSessionRestarts = 1
	}
}

func (c presignedUploadConfig) validate() error {
	if c.Profile != presignedDispositionProfile {
		return fmt.Errorf("presigned_upload profile must be %q", presignedDispositionProfile)
	}
	paths := []struct {
		name  string
		value string
	}{
		{name: "apply_path", value: c.ApplyPath},
		{name: "wait_path", value: c.WaitPath},
		{name: "complete_path", value: c.CompletePath},
		{name: "abort_path", value: c.AbortPath},
	}
	for _, path := range paths {
		parsed, err := url.ParseRequestURI(path.value)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(path.value, "/") || strings.HasPrefix(path.value, "//") {
			return fmt.Errorf("presigned_upload %s must be an absolute URL path", path.name)
		}
	}
	if c.MaxWaitSeconds > 600 {
		return errors.New("presigned_upload max_wait_seconds cannot exceed 600")
	}
	if c.PartConcurrency > 8 {
		return errors.New("presigned_upload part_concurrency cannot exceed 8")
	}
	if c.ControlRetries > 5 || c.PartRetries > 5 || c.MaxSessionRestarts > 3 {
		return errors.New("presigned_upload retry limits are too high")
	}
	if len(c.ReferencePaths) == 0 || strings.TrimSpace(c.ReferencePaths[0]) == "" {
		return errors.New("presigned_upload reference_paths are required")
	}
	if c.IdempotencyBodyField != "" && !presignedJSONFieldName.MatchString(c.IdempotencyBodyField) {
		return errors.New("presigned_upload idempotency_body_field must be a JSON field name")
	}
	return nil
}

func parsePresignedUploadConfig(channel *VideoChannel) (presignedUploadConfig, error) {
	if channel == nil {
		return presignedUploadConfig{}, errors.New("presigned_upload requires a video channel")
	}
	var wrapper struct {
		AssetResolver json.RawMessage `json:"asset_resolver"`
	}
	if len(channel.ExtraConfig) == 0 || string(channel.ExtraConfig) == "null" {
		return presignedUploadConfig{}, errors.New("presigned_upload requires extra_config.asset_resolver")
	}
	if err := json.Unmarshal(channel.ExtraConfig, &wrapper); err != nil {
		return presignedUploadConfig{}, fmt.Errorf("parse presigned_upload extra_config: %w", err)
	}
	if len(wrapper.AssetResolver) == 0 || string(wrapper.AssetResolver) == "null" {
		return presignedUploadConfig{}, errors.New("presigned_upload requires extra_config.asset_resolver")
	}
	var config presignedUploadConfig
	if err := json.Unmarshal(wrapper.AssetResolver, &config); err != nil {
		return presignedUploadConfig{}, fmt.Errorf("parse presigned_upload config: %w", err)
	}
	config.defaults()
	if err := config.validate(); err != nil {
		return presignedUploadConfig{}, err
	}
	return config, nil
}

type presignedUploadPart struct {
	PartNumber int    `json:"part_number"`
	SizeBytes  int64  `json:"size_bytes"`
	UploadURL  string `json:"upload_url"`
}

type presignedUploadInstruction struct {
	SessionToken string                `json:"session_token"`
	Generation   int                   `json:"generation"`
	Mode         string                `json:"mode"`
	UploadURL    string                `json:"upload_url"`
	Parts        []presignedUploadPart `json:"parts"`
}

type presignedUploadDisposition struct {
	Disposition string                      `json:"disposition"`
	Ticket      string                      `json:"ticket"`
	RetryAfter  int                         `json:"retry_after_seconds"`
	Upload      *presignedUploadInstruction `json:"upload"`
	raw         json.RawMessage
}

type presignedCompletedPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

type preparedAssetSource struct {
	ReaderAt    io.ReaderAt
	SizeBytes   int64
	SHA256      string
	ContentType string
	Close       func() error
}

type presignedUploadResolver struct {
	baseURL       string
	apiKey        string
	requestID     string
	cacheScope    string
	config        presignedUploadConfig
	controlClient *http.Client
	uploadClient  *http.Client
	db            *gorm.DB
	openSource    func(context.Context, *VideoAsset, int64) (*preparedAssetSource, error)
}

func newPresignedUploadResolver(option AssetResolverOptions) (AssetResolver, error) {
	config, err := parsePresignedUploadConfig(option.Channel)
	if err != nil {
		return nil, err
	}
	resolver := &presignedUploadResolver{
		requestID: strings.TrimSpace(option.RequestID), config: config, db: option.DB,
	}
	if option.Channel != nil {
		resolver.baseURL = strings.TrimRight(strings.TrimSpace(option.Channel.BaseURL), "/")
	}
	if option.Key != nil {
		resolver.apiKey = option.Key.APIKey
		resolver.cacheScope = fmt.Sprintf("presigned_upload:%d:%d:%s", option.Channel.ID, option.Key.ID, config.Profile)
	}
	if option.Client != nil {
		resolver.controlClient = option.Client
		resolver.uploadClient = option.Client
	} else {
		resolver.controlClient = &http.Client{Timeout: 2 * time.Minute}
		resolver.uploadClient = safeurl.NewClient(5 * time.Minute)
	}
	resolver.openSource = openRemoteAssetSource
	return resolver, nil
}

func openRemoteAssetSource(ctx context.Context, asset *VideoAsset, maxBytes int64) (*preparedAssetSource, error) {
	if err := safeurl.Validate(ctx, asset.StoragePath); err != nil {
		return nil, fmt.Errorf("%w: unsafe stored URL: %v", ErrInvalidAsset, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.StoragePath, nil)
	if err != nil {
		return nil, err
	}
	response, err := safeurl.NewClient(5 * time.Minute).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if retryableHTTPStatus(response.StatusCode) {
			return nil, NewRetryableProviderError("download asset", fmt.Errorf("HTTP %d", response.StatusCode))
		}
		return nil, fmt.Errorf("download asset returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		return nil, ErrFileTooLarge
	}

	file, err := os.CreateTemp("", "prism-video-asset-*")
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		cleanup()
		return nil, err
	}
	if size > maxBytes {
		cleanup()
		return nil, ErrFileTooLarge
	}
	if asset.SizeBytes > 0 && asset.SizeBytes != size {
		cleanup()
		return nil, fmt.Errorf("%w: stored asset size changed", ErrInvalidAsset)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if asset.SizeBytes > 0 && len(asset.SHA256) == sha256.Size*2 && !strings.EqualFold(asset.SHA256, digest) {
		cleanup()
		return nil, fmt.Errorf("%w: stored asset checksum changed", ErrInvalidAsset)
	}
	contentType := canonicalContentType(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if contentType == "" || contentType == "application/octet-stream" {
		buffer := make([]byte, 512)
		count, _ := file.ReadAt(buffer, 0)
		contentType = canonicalContentType(http.DetectContentType(buffer[:count]))
	}
	return &preparedAssetSource{
		ReaderAt: file, SizeBytes: size, SHA256: digest, ContentType: contentType,
		Close: func() error {
			closeErr := file.Close()
			removeErr := os.Remove(file.Name())
			if closeErr != nil {
				return closeErr
			}
			return removeErr
		},
	}, nil
}

func (r *presignedUploadResolver) Prepare(ctx context.Context, asset *VideoAsset) (*ResolvedAsset, error) {
	if asset == nil || asset.Status != VideoAssetStatusReady || strings.TrimSpace(asset.StoragePath) == "" {
		return nil, ErrAssetNotReady
	}
	if r == nil || r.baseURL == "" || strings.TrimSpace(r.apiKey) == "" || r.controlClient == nil || r.uploadClient == nil {
		return nil, fmt.Errorf("%w: presigned_upload requires a channel base URL and API key", ErrAssetResolver)
	}
	if cached := r.cachedReference(asset); cached != nil {
		return cached, nil
	}

	source, err := r.openSource(ctx, asset, MaxAssetUploadBytes())
	if err != nil {
		if errors.Is(err, ErrInvalidAsset) || errors.Is(err, ErrFileTooLarge) || IsRetryableProviderError(err) {
			return nil, err
		}
		return nil, NewRetryableProviderError("open asset source", err)
	}
	if source.Close != nil {
		defer source.Close()
	}
	contentType := canonicalContentType(asset.ContentType)
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = canonicalContentType(source.ContentType)
	}
	if !mimeMatchesKind(asset.Kind, contentType) || !mimeMatchesKind(asset.Kind, source.ContentType) {
		return nil, fmt.Errorf("%w: content type does not match asset kind", ErrInvalidAsset)
	}

	idempotencyKey := r.requestID
	if idempotencyKey == "" {
		idempotencyKey = "asset"
	}
	idempotencyKey += ":" + asset.ID
	var disposition *presignedUploadDisposition
	for restart := 0; restart <= r.config.MaxSessionRestarts; restart++ {
		key := idempotencyKey
		if restart > 0 {
			key = fmt.Sprintf("%s:restart:%d", idempotencyKey, restart)
		}
		disposition, err = r.apply(ctx, asset, source, contentType, key)
		if !errors.Is(err, errUploadSessionExpired) {
			break
		}
	}
	if err != nil {
		if errors.Is(err, errUploadSessionExpired) {
			return nil, NewRetryableProviderError("restart presigned upload", err)
		}
		return nil, err
	}
	reference, expiresAt, err := r.reference(disposition)
	if err != nil {
		return nil, err
	}
	resolved := &ResolvedAsset{RefType: ResolvedAssetRefProviderObject, RefValue: reference}
	r.storeCachedReference(asset, resolved, expiresAt)
	return resolved, nil
}

func (r *presignedUploadResolver) apply(
	ctx context.Context,
	asset *VideoAsset,
	source *preparedAssetSource,
	contentType string,
	idempotencyKey string,
) (*presignedUploadDisposition, error) {
	body := map[string]any{
		"sha256": source.SHA256, "size_bytes": source.SizeBytes, "kind": asset.Kind,
		"content_type": contentType, "purpose": r.config.Purpose,
	}
	if r.config.IdempotencyBodyField != "" {
		body[r.config.IdempotencyBodyField] = idempotencyKey
	}
	disposition, err := r.control(ctx, r.config.ApplyPath, body, idempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("apply presigned upload: %w", err)
	}
	waitDeadline := time.Now().Add(time.Duration(r.config.MaxWaitSeconds) * time.Second)
	for strings.EqualFold(disposition.Disposition, "waiting") {
		if strings.TrimSpace(disposition.Ticket) == "" {
			return nil, errors.New("presigned upload wait response is missing ticket")
		}
		delay := disposition.RetryAfter
		if delay <= 0 {
			delay = 1
		} else if delay > 10 {
			delay = 10
		}
		if time.Now().Add(time.Duration(delay) * time.Second).After(waitDeadline) {
			return nil, NewRetryableProviderError("wait for presigned upload", errors.New("wait deadline exceeded"))
		}
		timer := time.NewTimer(time.Duration(delay) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		disposition, err = r.control(ctx, r.config.WaitPath, map[string]any{"ticket": disposition.Ticket}, "")
		if err != nil {
			return nil, fmt.Errorf("wait for presigned upload: %w", err)
		}
	}
	if strings.EqualFold(disposition.Disposition, "reused") {
		return disposition, nil
	}
	if !strings.EqualFold(disposition.Disposition, "owner") || disposition.Upload == nil {
		return nil, fmt.Errorf("unsupported presigned upload disposition %q", disposition.Disposition)
	}

	upload := disposition.Upload
	completed := false
	defer func() {
		if !completed {
			r.abort(upload)
		}
	}()
	var parts []presignedCompletedPart
	switch strings.ToLower(strings.TrimSpace(upload.Mode)) {
	case "single":
		_, err = r.putRange(ctx, upload.UploadURL, source.ReaderAt, 0, source.SizeBytes, contentType, true, false)
	case "multipart":
		parts, err = r.putParts(ctx, upload, source)
	default:
		return nil, fmt.Errorf("unsupported presigned upload mode %q", upload.Mode)
	}
	if err != nil {
		return nil, err
	}
	completeBody := map[string]any{"session_token": upload.SessionToken, "generation": upload.Generation}
	if len(parts) > 0 {
		completeBody["parts"] = parts
	}
	result, err := r.control(ctx, r.config.CompletePath, completeBody, "")
	if err != nil {
		return nil, fmt.Errorf("complete presigned upload: %w", err)
	}
	completed = true
	return result, nil
}

func (r *presignedUploadResolver) putParts(ctx context.Context, upload *presignedUploadInstruction, source *preparedAssetSource) ([]presignedCompletedPart, error) {
	parts := append([]presignedUploadPart(nil), upload.Parts...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })
	if len(parts) == 0 {
		return nil, errors.New("presigned multipart upload has no parts")
	}
	if len(parts) > 10000 {
		return nil, errors.New("presigned multipart upload has too many parts")
	}
	offsets := make([]int64, len(parts))
	offset := int64(0)
	for index, part := range parts {
		if part.PartNumber != index+1 || part.SizeBytes <= 0 || strings.TrimSpace(part.UploadURL) == "" {
			return nil, errors.New("presigned multipart upload contains invalid parts")
		}
		offsets[index] = offset
		offset += part.SizeBytes
		if offset > source.SizeBytes {
			return nil, errors.New("presigned multipart upload exceeds source size")
		}
	}
	if offset != source.SizeBytes {
		return nil, errors.New("presigned multipart total size does not match source")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	completed := make([]presignedCompletedPart, len(parts))
	semaphore := make(chan struct{}, r.config.PartConcurrency)
	var wait sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for index := range parts {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			part := parts[index]
			etag, uploadErr := r.putRange(ctx, part.UploadURL, source.ReaderAt, offsets[index], part.SizeBytes, "", false, true)
			if uploadErr != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("upload part %d: %w", part.PartNumber, uploadErr)
					cancel()
				}
				errMu.Unlock()
				return
			}
			completed[index] = presignedCompletedPart{PartNumber: part.PartNumber, ETag: etag}
		}()
	}
	wait.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return completed, nil
}

func (r *presignedUploadResolver) putRange(
	ctx context.Context,
	endpoint string,
	reader io.ReaderAt,
	offset, size int64,
	contentType string,
	single, requireETag bool,
) (string, error) {
	if strings.TrimSpace(endpoint) == "" {
		return "", errors.New("presigned upload URL is missing")
	}
	var lastErr error
	for attempt := 0; attempt <= r.config.PartRetries; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, io.NewSectionReader(reader, offset, size))
		if err != nil {
			return "", err
		}
		request.ContentLength = size
		if single {
			request.Header.Set("Content-Type", contentType)
			request.Header.Set("If-None-Match", "*")
		}
		response, err := r.uploadClient.Do(request)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if attempt < r.config.PartRetries {
				if err := waitForRetry(ctx, attempt); err != nil {
					return "", err
				}
				continue
			}
			return "", NewRetryableProviderError("upload presigned object", lastErr)
		}
		message, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			etag := strings.TrimSpace(response.Header.Get("ETag"))
			if requireETag && etag == "" {
				return "", errors.New("presigned multipart response is missing ETag")
			}
			return etag, nil
		}
		lastErr = fmt.Errorf("object upload HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
		if response.StatusCode == http.StatusForbidden {
			return "", errUploadSessionExpired
		}
		if !retryableHTTPStatus(response.StatusCode) {
			return "", lastErr
		}
		if attempt < r.config.PartRetries {
			if err := waitForRetry(ctx, attempt); err != nil {
				return "", err
			}
		}
	}
	return "", NewRetryableProviderError("upload presigned object", lastErr)
}

func (r *presignedUploadResolver) control(ctx context.Context, path string, body any, idempotencyKey string) (*presignedUploadDisposition, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt <= r.config.ControlRetries; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+path, strings.NewReader(string(encoded)))
		if err != nil {
			return nil, err
		}
		request.Header.Set(r.config.AuthHeader, r.config.AuthPrefix+r.apiKey)
		request.Header.Set("Content-Type", "application/json")
		if idempotencyKey != "" {
			request.Header.Set(r.config.IdempotencyHeader, idempotencyKey)
		}
		response, err := r.controlClient.Do(request)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < r.config.ControlRetries {
				if err := waitForRetry(ctx, attempt); err != nil {
					return nil, err
				}
				continue
			}
			return nil, NewRetryableProviderError("presigned upload control", lastErr)
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < r.config.ControlRetries {
				continue
			}
			return nil, NewRetryableProviderError("read presigned upload response", readErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			lastErr = fmt.Errorf("presigned upload HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
			if response.StatusCode == http.StatusForbidden || uploadSessionExpired(responseBody) {
				return nil, errUploadSessionExpired
			}
			if retryableHTTPStatus(response.StatusCode) && attempt < r.config.ControlRetries {
				if err := waitForRetry(ctx, attempt); err != nil {
					return nil, err
				}
				continue
			}
			if retryableHTTPStatus(response.StatusCode) {
				return nil, NewRetryableProviderError("presigned upload control", lastErr)
			}
			return nil, lastErr
		}
		if r.config.SuccessCodePath != "" {
			code := gjson.GetBytes(responseBody, r.config.SuccessCodePath)
			if !code.Exists() {
				return nil, errors.New("presigned upload response is missing success code")
			}
			if code.Int() != 0 {
				if uploadSessionExpired(responseBody) {
					return nil, errUploadSessionExpired
				}
				return nil, fmt.Errorf("presigned upload code %d: %s", code.Int(), strings.TrimSpace(string(responseBody)))
			}
		}
		payload := json.RawMessage(responseBody)
		if r.config.ResponseRoot != "" {
			root := gjson.GetBytes(responseBody, r.config.ResponseRoot)
			if !root.Exists() || root.Type == gjson.Null {
				return nil, errors.New("presigned upload response is missing data")
			}
			payload = json.RawMessage(root.Raw)
		}
		var result presignedUploadDisposition
		if err := json.Unmarshal(payload, &result); err != nil {
			return nil, err
		}
		result.raw = append(json.RawMessage(nil), payload...)
		return &result, nil
	}
	return nil, NewRetryableProviderError("presigned upload control", lastErr)
}

func (r *presignedUploadResolver) abort(upload *presignedUploadInstruction) {
	if upload == nil || strings.TrimSpace(upload.SessionToken) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = r.control(ctx, r.config.AbortPath, map[string]any{
		"session_token": upload.SessionToken,
		"generation":    upload.Generation,
	}, "")
}

func (r *presignedUploadResolver) reference(disposition *presignedUploadDisposition) (string, *time.Time, error) {
	if disposition == nil || len(disposition.raw) == 0 {
		return "", nil, errors.New("presigned upload response is empty")
	}
	var reference string
	for _, path := range r.config.ReferencePaths {
		value := gjson.GetBytes(disposition.raw, strings.TrimSpace(path))
		if value.Exists() && value.Type != gjson.Null && strings.TrimSpace(value.String()) != "" {
			reference = strings.TrimSpace(value.String())
			break
		}
	}
	if reference == "" {
		return "", nil, errors.New("presigned upload response is missing provider reference")
	}
	var expiresAt *time.Time
	for _, path := range r.config.ReferenceExpiryPaths {
		value := strings.TrimSpace(gjson.GetBytes(disposition.raw, strings.TrimSpace(path)).String())
		if value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err == nil {
			expiresAt = &parsed
			break
		}
	}
	return reference, expiresAt, nil
}

type cachedAssetReference struct {
	RefType   string    `json:"ref_type"`
	RefValue  string    `json:"ref_value"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (r *presignedUploadResolver) cachedReference(asset *VideoAsset) *ResolvedAsset {
	if r == nil || asset == nil || r.cacheScope == "" || len(asset.UpstreamRefs) == 0 {
		return nil
	}
	var references map[string]cachedAssetReference
	if json.Unmarshal(asset.UpstreamRefs, &references) != nil {
		return nil
	}
	reference, ok := references[r.cacheScope]
	if !ok || reference.RefValue == "" || !reference.ExpiresAt.After(time.Now().Add(time.Minute)) {
		return nil
	}
	return &ResolvedAsset{RefType: reference.RefType, RefValue: reference.RefValue}
}

func (r *presignedUploadResolver) storeCachedReference(asset *VideoAsset, resolved *ResolvedAsset, expiresAt *time.Time) {
	if r == nil || r.db == nil || asset == nil || resolved == nil || r.cacheScope == "" || expiresAt == nil || !expiresAt.After(time.Now()) {
		return
	}
	_ = r.db.Transaction(func(tx *gorm.DB) error {
		var current VideoAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", asset.ID).Error; err != nil {
			return err
		}
		references := make(map[string]cachedAssetReference)
		if len(current.UpstreamRefs) > 0 {
			_ = json.Unmarshal(current.UpstreamRefs, &references)
		}
		references[r.cacheScope] = cachedAssetReference{
			RefType: resolved.RefType, RefValue: resolved.RefValue, ExpiresAt: *expiresAt,
		}
		encoded, err := json.Marshal(references)
		if err != nil {
			return err
		}
		if err := tx.Model(&current).UpdateColumn("upstream_refs", encoded).Error; err != nil {
			return err
		}
		asset.UpstreamRefs = append(asset.UpstreamRefs[:0], encoded...)
		return nil
	})
}

func retryableHTTPStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests || status >= 500
}

func uploadSessionExpired(body []byte) bool {
	value := strings.ToUpper(string(body))
	return strings.Contains(value, "VIDEO_UPLOAD_STALE") || strings.Contains(value, "VIDEO_UPLOAD_EXPIRED")
}

func waitForRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(100*(1<<min(attempt, 4))) * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
