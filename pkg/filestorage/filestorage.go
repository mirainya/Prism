package filestorage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/pkg/config"
)

type xfsResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

const (
	maxImportURLBytes       = 8192
	maxStoragePathBytes     = 1024
	maxStorageFilenameBytes = 255
	maxXFSResponseBytes     = int64(1 << 20)
)

type importURLRequest struct {
	URL              string `json:"url"`
	Path             string `json:"path,omitempty"`
	OriginalFilename string `json:"originalFilename,omitempty"`
}

type UploadHashInfo map[string]string

type UploadResult struct {
	ID           string         `json:"id"`
	URL          string         `json:"url"`
	RawURL       string         `json:"rawUrl"`
	Size         int64          `json:"size"`
	Filename     string         `json:"filename"`
	OriginalName string         `json:"originalFilename"`
	Path         string         `json:"path"`
	ContentType  string         `json:"contentType"`
	Platform     string         `json:"platform"`
	ObjectID     string         `json:"objectId"`
	ObjectType   string         `json:"objectType"`
	HashInfo     UploadHashInfo `json:"hashInfo"`
	UploadID     string         `json:"uploadId"`
	UploadStatus int            `json:"uploadStatus"`
}

// StorageLocator returns the durable XFS object locator. URL remains the
// browser-readable upload response for callers that immediately consume it.
func (r UploadResult) StorageLocator() string {
	if locator := normalizeStorageLocator(r.RawURL); locator != "" {
		return locator
	}
	return strings.TrimSpace(r.URL)
}

func (r UploadResult) SHA256() string {
	for name, value := range r.HashInfo {
		normalizedName := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(name)))
		if normalizedName != "sha256" {
			continue
		}
		digest := strings.ToLower(strings.TrimSpace(value))
		if len(digest) != sha256.Size*2 {
			return ""
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return ""
		}
		return digest
	}
	return ""
}

// WithAPIKey returns a storage client view that keeps the global endpoint and
// path policy while using the credential bound to one Prism API token.
type Client struct {
	config config.FileStorageConfig
}

func WithAPIKey(apiKey string) Client {
	cfg := config.FileStorageConfig{}
	if config.C != nil {
		cfg = config.C.FileStorage
	}
	cfg.APIKey = strings.TrimSpace(apiKey)
	return Client{config: cfg}
}

func DefaultClient() Client {
	cfg := config.FileStorageConfig{}
	if config.C != nil {
		cfg = config.C.FileStorage
	}
	return Client{config: cfg}
}

func (c Client) Configured() bool {
	return strings.TrimSpace(c.config.BaseURL) != "" && strings.TrimSpace(c.config.APIKey) != ""
}

func (r UploadResult) StorageKey() string {
	for _, candidate := range []string{
		joinStorageIdentity(r.Platform, r.ObjectID),
		joinStorageIdentity(r.Platform, r.ID),
		joinStorageIdentity(r.Platform, strings.TrimLeft(r.Path, "/")+r.Filename),
	} {
		if candidate != "" && len(candidate) <= 512 {
			return candidate
		}
	}
	digest := sha256.Sum256([]byte(r.StorageLocator()))
	return "xfs:sha256:" + hex.EncodeToString(digest[:])
}

func joinStorageIdentity(platform, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	platform = strings.TrimSpace(platform)
	if platform == "" {
		platform = "default"
	}
	return "xfs:" + platform + ":" + value
}

// IsBase64Data 判断字符串是否为 base64 图片数据
func IsBase64Data(s string) bool {
	if strings.HasPrefix(s, "data:") {
		return true
	}
	for _, prefix := range []string{"/9j/", "iVBOR", "R0lGOD", "Qk0", "UklGR"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// TransferBase64 解码 base64 数据并上传到 xfilestorage，返回最终 URL
func TransferBase64(ctx context.Context, b64 string, capabilityCode string) (string, error) {
	cfg := config.C.FileStorage
	if cfg.BaseURL == "" {
		return "", fmt.Errorf("file storage not configured")
	}

	raw, contentType := parseBase64(b64)
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	return upload(ctx, bytes.NewReader(decoded), contentType, capabilityCode)
}

// TransferBytes uploads in-memory file data to xfilestorage and returns its public URL.
func TransferBytes(ctx context.Context, data []byte, contentType string, capabilityCode string) (string, error) {
	cfg := config.C.FileStorage
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		return "", fmt.Errorf("file storage not configured")
	}
	if len(data) == 0 {
		return "", fmt.Errorf("file data is empty")
	}
	return upload(ctx, bytes.NewReader(data), contentType, capabilityCode)
}

// TransferReader streams file data to xfilestorage without buffering the
// multipart request in memory.
func TransferReader(ctx context.Context, data io.Reader, contentType string, capabilityCode string) (string, error) {
	cfg := config.C.FileStorage
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		return "", fmt.Errorf("file storage not configured")
	}
	if data == nil {
		return "", fmt.Errorf("file data is empty")
	}
	return upload(ctx, data, contentType, capabilityCode)
}

// UploadReader streams an object and returns the durable storage identity and
// locator reported by x-file-storage.
func UploadReader(ctx context.Context, data io.Reader, contentType, capabilityCode string) (UploadResult, error) {
	cfg := config.C.FileStorage
	storagePath := fmt.Sprintf("%s%s/%s/", cfg.UploadPath, capabilityCode, time.Now().Format("2006/01/02"))
	return UploadReaderAtPath(ctx, data, contentType, storagePath)
}

// UploadReaderAtPath is used when the caller has already allocated a globally
// unique logical object path before starting the external write.
func UploadReaderAtPath(ctx context.Context, data io.Reader, contentType, storagePath string) (UploadResult, error) {
	return DefaultClient().UploadReaderAtPath(ctx, data, contentType, storagePath)
}

func (c Client) UploadReaderAtPath(ctx context.Context, data io.Reader, contentType, storagePath string) (UploadResult, error) {
	return c.uploadReaderAtPath(ctx, data, contentType, storagePath, uuid.New().String()+extensionForContentType(contentType))
}

// UploadReaderAtPathWithFilename writes to a caller-allocated object name.
// Retrying the same path and filename lets recovery reconcile an upload whose
// database acknowledgement failed.
func UploadReaderAtPathWithFilename(ctx context.Context, data io.Reader, contentType, storagePath, filename string) (UploadResult, error) {
	return DefaultClient().UploadReaderAtPathWithFilename(ctx, data, contentType, storagePath, filename)
}

func (c Client) UploadReaderAtPathWithFilename(ctx context.Context, data io.Reader, contentType, storagePath, filename string) (UploadResult, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" || len(filename) > 255 || filename == "." || filename == ".." || strings.ContainsAny(filename, `/\\`) || strings.ContainsAny(filename, "\r\n\x00") {
		return UploadResult{}, fmt.Errorf("invalid storage filename")
	}
	return c.uploadReaderAtPath(ctx, data, contentType, storagePath, filename)
}

func (c Client) uploadReaderAtPath(ctx context.Context, data io.Reader, contentType, storagePath, filename string) (UploadResult, error) {
	cfg := c.config
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		return UploadResult{}, fmt.Errorf("file storage not configured")
	}
	if data == nil {
		return UploadResult{}, fmt.Errorf("file data is empty")
	}
	storagePath = strings.TrimLeft(strings.TrimSpace(storagePath), "/")
	if storagePath == "" || strings.Contains(storagePath, "..") {
		return UploadResult{}, fmt.Errorf("invalid storage path")
	}
	if !strings.HasSuffix(storagePath, "/") {
		storagePath += "/"
	}
	return uploadResultWithConfig(ctx, cfg, data, contentType, storagePath, filename)
}

// ImportURLAtPath asks x-file-storage to fetch a public HTTP(S) object and
// write it directly to the storage platform bound to this client's API key.
func ImportURLAtPath(ctx context.Context, originURL, storagePath, originalFilename string) (UploadResult, error) {
	return DefaultClient().ImportURLAtPath(ctx, originURL, storagePath, originalFilename)
}

func (c Client) ImportURLAtPath(ctx context.Context, originURL, storagePath, originalFilename string) (UploadResult, error) {
	if !c.Configured() {
		return UploadResult{}, fmt.Errorf("file storage not configured")
	}

	if strings.ContainsAny(originURL, "\r\n\x00") {
		return UploadResult{}, fmt.Errorf("invalid import URL")
	}
	originURL = strings.TrimSpace(originURL)
	parsed, err := url.Parse(originURL)
	scheme := ""
	if parsed != nil {
		scheme = strings.ToLower(parsed.Scheme)
	}
	if err != nil || originURL == "" || len(originURL) > maxImportURLBytes || strings.ContainsRune(originURL, '\x00') ||
		parsed == nil || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" ||
		(scheme != "http" && scheme != "https") {
		return UploadResult{}, fmt.Errorf("invalid import URL")
	}

	if strings.ContainsAny(storagePath, "\\\r\n\x00") {
		return UploadResult{}, fmt.Errorf("invalid storage path")
	}
	storagePath = strings.TrimLeft(strings.TrimSpace(storagePath), "/")
	if storagePath == "" {
		return UploadResult{}, fmt.Errorf("invalid storage path")
	}
	for _, segment := range strings.Split(storagePath, "/") {
		if segment == "." || segment == ".." {
			return UploadResult{}, fmt.Errorf("invalid storage path")
		}
	}
	if !strings.HasSuffix(storagePath, "/") {
		storagePath += "/"
	}
	if len(storagePath) > maxStoragePathBytes {
		return UploadResult{}, fmt.Errorf("invalid storage path")
	}

	if strings.ContainsAny(originalFilename, "/\\\r\n\x00") {
		return UploadResult{}, fmt.Errorf("invalid storage filename")
	}
	originalFilename = strings.TrimSpace(originalFilename)
	if len(originalFilename) > maxStorageFilenameBytes || originalFilename == "." || originalFilename == ".." ||
		strings.ContainsAny(originalFilename, "/\\") {
		return UploadResult{}, fmt.Errorf("invalid storage filename")
	}

	body, err := json.Marshal(importURLRequest{
		URL:              originURL,
		Path:             storagePath,
		OriginalFilename: originalFilename,
	})
	if err != nil {
		return UploadResult{}, fmt.Errorf("encode import request: %w", err)
	}
	endpoint := strings.TrimRight(c.config.BaseURL, "/") + "/api/v1/upload-url"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return UploadResult{}, fmt.Errorf("create import request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", c.config.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return UploadResult{}, fmt.Errorf("import request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxXFSResponseBytes+1))
	if err != nil {
		return UploadResult{}, fmt.Errorf("read import response: %w", err)
	}
	if int64(len(responseBody)) > maxXFSResponseBytes {
		return UploadResult{}, fmt.Errorf("xfilestorage import response exceeds size limit")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return UploadResult{}, fmt.Errorf("xfilestorage import returned HTTP %d", resp.StatusCode)
	}
	return parseUploadResult(responseBody)
}

func upload(ctx context.Context, data io.Reader, contentType string, capabilityCode string) (string, error) {
	cfg := config.C.FileStorage
	storagePath := fmt.Sprintf("%s%s/%s/", cfg.UploadPath, capabilityCode, time.Now().Format("2006/01/02"))
	result, err := uploadResult(ctx, data, contentType, storagePath, uuid.New().String()+extensionForContentType(contentType))
	return result.URL, err
}

func extensionForContentType(contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	ext := ".png"
	if strings.Contains(contentType, "jpeg") || strings.Contains(contentType, "jpg") {
		ext = ".jpg"
	} else if strings.Contains(contentType, "webp") {
		ext = ".webp"
	} else if strings.Contains(contentType, "mp4") || strings.Contains(contentType, "video") {
		ext = ".mp4"
	} else if strings.HasPrefix(contentType, "audio/") {
		switch {
		case strings.Contains(contentType, "mpeg"), strings.Contains(contentType, "mp3"):
			ext = ".mp3"
		case strings.Contains(contentType, "wav"), strings.Contains(contentType, "wave"):
			ext = ".wav"
		case strings.Contains(contentType, "ogg"):
			ext = ".ogg"
		case strings.Contains(contentType, "aac"):
			ext = ".aac"
		}
	}
	return ext
}

func uploadResult(ctx context.Context, data io.Reader, contentType, storagePath, filename string) (UploadResult, error) {
	cfg := config.C.FileStorage
	return uploadResultWithConfig(ctx, cfg, data, contentType, storagePath, filename)
}

func uploadResultWithConfig(ctx context.Context, cfg config.FileStorageConfig, data io.Reader, contentType, storagePath, filename string) (UploadResult, error) {
	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/api/v1/upload"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pipeReader)
	if err != nil {
		_ = pipeReader.Close()
		_ = pipeWriter.Close()
		return UploadResult{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Api-Key", cfg.APIKey)
	go writeMultipartUpload(pipeWriter, writer, data, filename, storagePath)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = pipeReader.CloseWithError(err)
		return UploadResult{}, fmt.Errorf("upload request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxXFSResponseBytes))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return UploadResult{}, fmt.Errorf("xfilestorage upload returned HTTP %d", resp.StatusCode)
	}
	return parseUploadResult(body)
}

func parseUploadResult(body []byte) (UploadResult, error) {
	var xfsResp xfsResponse
	if err := json.Unmarshal(body, &xfsResp); err != nil {
		return UploadResult{}, fmt.Errorf("parse response: %w", err)
	}
	if xfsResp.Code != 200 {
		return UploadResult{}, fmt.Errorf("xfilestorage error: %s", xfsResp.Message)
	}

	var result UploadResult
	if err := json.Unmarshal(xfsResp.Data, &result); err != nil {
		return UploadResult{}, fmt.Errorf("parse upload result: %w", err)
	}

	result.URL = normalizePublicURL(result.URL)
	result.RawURL = normalizeStorageLocator(result.RawURL)
	if result.RawURL == "" {
		result.RawURL = result.URL
	}
	if result.URL == "" {
		return UploadResult{}, fmt.Errorf("xfilestorage returned an empty durable locator")
	}
	return result, nil
}

func writeMultipartUpload(pipeWriter *io.PipeWriter, writer *multipart.Writer, data io.Reader, filename, storagePath string) {
	var writeErr error
	defer func() {
		if closeErr := writer.Close(); writeErr == nil {
			writeErr = closeErr
		}
		_ = pipeWriter.CloseWithError(writeErr)
	}()

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		writeErr = fmt.Errorf("create form file: %w", err)
		return
	}
	if _, err := io.Copy(part, data); err != nil {
		writeErr = fmt.Errorf("copy form file: %w", err)
		return
	}
	if err := writer.WriteField("path", storagePath); err != nil {
		writeErr = fmt.Errorf("write storage path: %w", err)
	}
}

// DeleteURL removes a file previously uploaded with the configured API key.
// A missing file is treated as success so cleanup can be retried safely.
func DeleteURL(ctx context.Context, rawURL string) error {
	return DefaultClient().DeleteURL(ctx, rawURL)
}

func (c Client) DeleteURL(ctx context.Context, rawURL string) error {
	cfg := c.config
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		return fmt.Errorf("file storage not configured")
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/api/v1/file/delete?url=" + url.QueryEscape(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create delete request: %w", err)
	}
	req.Header.Set("X-Api-Key", cfg.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("xfilestorage delete returned HTTP %d", resp.StatusCode)
	}
	var xfsResp xfsResponse
	if err := json.Unmarshal(body, &xfsResp); err != nil {
		return fmt.Errorf("parse delete response: %w", err)
	}
	if xfsResp.Code != http.StatusOK && xfsResp.Code != http.StatusNotFound {
		return fmt.Errorf("xfilestorage error: %s", xfsResp.Message)
	}
	return nil
}

// PresignedURL returns a browser-readable URL without exposing the XFS API
// key. Private platforms may store only a relative object locator, so callers
// must resolve it through XFS before returning it to clients.
func (c Client) PresignedURL(ctx context.Context, rawURL string, expiration time.Duration) (string, error) {
	if !c.Configured() {
		return "", fmt.Errorf("file storage not configured")
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("file storage locator is empty")
	}
	seconds := int64(expiration / time.Second)
	if seconds <= 0 || seconds > int64(7*24*time.Hour/time.Second) {
		return "", fmt.Errorf("presigned URL expiration is invalid")
	}
	endpoint := strings.TrimRight(c.config.BaseURL, "/") + "/api/v1/file/presigned-url?url=" +
		url.QueryEscape(rawURL) + "&expiration=" + fmt.Sprintf("%d", seconds)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create presigned URL request: %w", err)
	}
	req.Header.Set("X-Api-Key", c.config.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("presigned URL request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("xfilestorage presigned URL returned HTTP %d", resp.StatusCode)
	}
	var xfsResp xfsResponse
	if err := json.Unmarshal(body, &xfsResp); err != nil {
		return "", fmt.Errorf("parse presigned URL response: %w", err)
	}
	if xfsResp.Code != http.StatusOK {
		return "", fmt.Errorf("xfilestorage error: %s", xfsResp.Message)
	}
	var result string
	if err := json.Unmarshal(xfsResp.Data, &result); err != nil {
		return "", fmt.Errorf("parse presigned URL: %w", err)
	}
	parsed, err := url.Parse(strings.TrimSpace(result))
	if err != nil || parsed.User != nil || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return "", fmt.Errorf("xfilestorage returned an invalid presigned URL")
	}
	return result, nil
}

// Probe validates the permissions Prism needs before a token binding is made
// active. The temporary object is removed even when download or signing fails.
func (c Client) Probe(ctx context.Context) error {
	if !c.Configured() {
		return fmt.Errorf("file storage not configured")
	}
	payload := []byte("prism-xfs-binding-probe")
	digest := sha256.Sum256(payload)
	path := strings.Trim(strings.TrimSpace(c.config.UploadPath), "/")
	if path != "" {
		path += "/"
	}
	path += "system-check/" + time.Now().UTC().Format("2006-01-02") + "/"
	result, err := c.UploadReaderAtPath(ctx, bytes.NewReader(payload), "text/plain", path)
	if err != nil {
		return err
	}
	locator := result.StorageLocator()
	operationErr := c.VerifyURL(ctx, locator, int64(len(payload)), hex.EncodeToString(digest[:]))
	if operationErr == nil {
		_, operationErr = c.PresignedURL(ctx, locator, time.Minute)
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cleanupCancel()
	deleteErr := c.DeleteURL(cleanupCtx, locator)
	if operationErr != nil {
		return operationErr
	}
	if deleteErr != nil {
		return fmt.Errorf("delete storage probe: %w", deleteErr)
	}
	return nil
}

// OpenDownload opens a private, authenticated stream through x-file-storage.
// The supplied locator is only sent as a query value to the configured storage
// service, so callers never connect directly to a database-controlled host.
func OpenDownload(ctx context.Context, rawURL string) (*http.Response, error) {
	return DefaultClient().OpenDownload(ctx, rawURL)
}

func (c Client) OpenDownload(ctx context.Context, rawURL string) (*http.Response, error) {
	cfg := c.config
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		return nil, fmt.Errorf("file storage not configured")
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("file storage locator is empty")
	}
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/api/v1/file/download?url=" + url.QueryEscape(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("X-Api-Key", cfg.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download request: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("xfilestorage download returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func ReadURL(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	return DefaultClient().ReadURL(ctx, rawURL, maxBytes)
}

func (c Client) ReadURL(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("download size limit must be positive")
	}
	resp, err := c.OpenDownload(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read download: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("download exceeds %d bytes", maxBytes)
	}
	return data, nil
}

// VerifyURL reads the stored object through the authenticated storage proxy
// and verifies the immutable length and SHA-256 before callers publish it.
func VerifyURL(ctx context.Context, rawURL string, expectedBytes int64, expectedSHA256 string) error {
	return DefaultClient().VerifyURL(ctx, rawURL, expectedBytes, expectedSHA256)
}

func (c Client) VerifyURL(ctx context.Context, rawURL string, expectedBytes int64, expectedSHA256 string) error {
	if expectedBytes <= 0 {
		return fmt.Errorf("expected object size must be positive")
	}
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if len(expectedSHA256) != sha256.Size*2 {
		return fmt.Errorf("expected object SHA-256 is invalid")
	}
	if _, err := hex.DecodeString(expectedSHA256); err != nil {
		return fmt.Errorf("expected object SHA-256 is invalid")
	}
	response, err := c.OpenDownload(ctx, rawURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.ContentLength >= 0 && response.ContentLength != expectedBytes {
		return fmt.Errorf("stored object length is %d, expected %d", response.ContentLength, expectedBytes)
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(response.Body, expectedBytes+1))
	if err != nil {
		return fmt.Errorf("read stored object: %w", err)
	}
	if count != expectedBytes {
		return fmt.Errorf("stored object length is %d, expected %d", count, expectedBytes)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expectedSHA256 {
		return fmt.Errorf("stored object SHA-256 does not match")
	}
	return nil
}

func normalizeStorageLocator(rawURL string) string {
	return strings.TrimSpace(rawURL)
}

// normalizePublicURL remains for callers and tests that need a browser URL.
// XFS upload locators use normalizeStorageLocator because private platforms
// legitimately return an object path rather than an absolute URL.
func normalizePublicURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if strings.HasPrefix(rawURL, "http://") {
		return "https://" + strings.TrimPrefix(rawURL, "http://")
	}
	if rawURL != "" && !strings.HasPrefix(rawURL, "https://") {
		return "https://" + rawURL
	}
	return rawURL
}

func parseBase64(s string) (data string, contentType string) {
	if strings.HasPrefix(s, "data:") {
		parts := strings.SplitN(s, ",", 2)
		if len(parts) == 2 {
			meta := strings.TrimPrefix(parts[0], "data:")
			meta = strings.TrimSuffix(meta, ";base64")
			return parts[1], meta
		}
	}
	return s, "image/png"
}

// GenerateStoragePath 生成存储路径（供外部使用）
func GenerateStoragePath(capabilityCode string, originURL string) string {
	ext := filepath.Ext(originURL)
	if ext == "" || len(ext) > 10 {
		if strings.Contains(capabilityCode, "video") {
			ext = ".mp4"
		} else {
			ext = ".png"
		}
	}
	if idx := strings.Index(ext, "?"); idx > 0 {
		ext = ext[:idx]
	}
	return fmt.Sprintf("%s/%s/%s%s", capabilityCode, time.Now().Format("2006/01/02"), uuid.New().String(), ext)
}
