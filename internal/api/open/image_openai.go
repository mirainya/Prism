package open

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/payloadview"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/pkg/filestorage"
	"github.com/mirainya/Prism/pkg/safeurl"
)

// OpenAIImageRequest OpenAI 标准图像生成请求
// 参考 https://platform.openai.com/docs/api-reference/images/create
type OpenAIImageRequest struct {
	PartialImages     *int     `json:"partial_images"`
	Model             string   `json:"model"`
	Prompt            string   `json:"prompt"`
	ImageURLs         []string `json:"image_urls"`
	N                 int      `json:"n"`
	Size              string   `json:"size"`
	AspectRatio       string   `json:"aspect_ratio"`
	Quality           string   `json:"quality"`
	ResponseFormat    string   `json:"response_format"` // url | b64_json
	OutputFormat      string   `json:"output_format"`
	OutputCompression *int     `json:"output_compression"`
	Moderation        string   `json:"moderation"`
	Style             string   `json:"style"`
	Background        string   `json:"background"`
	InputFidelity     string   `json:"input_fidelity"`
	User              string   `json:"user"`
	Stream            bool     `json:"stream"` // true=SSE 流式输出（OpenAI 标准）
	nSet              bool
}

func (r *OpenAIImageRequest) UnmarshalJSON(data []byte) error {
	type requestAlias OpenAIImageRequest
	var value requestAlias
	decoded := struct {
		*requestAlias
		N json.RawMessage `json:"n"`
	}{requestAlias: &value}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = OpenAIImageRequest(value)
	if decoded.N == nil {
		return nil
	}
	r.nSet = true
	return json.Unmarshal(decoded.N, &r.N)
}

// OpenAIImageData 单张图片结果
type OpenAIImageData struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// OpenAIImageResponse OpenAI 标准图像响应
type OpenAIImageResponse struct {
	Created int64             `json:"created"`
	Data    []OpenAIImageData `json:"data"`
}

var errOpenAIImageStorage = errors.New("image storage failed")

const (
	openAIImageEditMaxMemory     = 8 << 20
	openAIImageEditMaxFileBytes  = 20 << 20
	openAIImageEditMaxTotalBytes = 32 << 20
	openAIImageMaxRequestBytes   = 48 << 20
	openAIImageEditMaxInputs     = 16
	openAIImageEditMaxMasks      = 1
)

// openAIError 返回 OpenAI 风格的错误(不套 Prism {code,data} 外壳)
func openAIError(c *gin.Context, httpCode int, message, errType string) {
	c.JSON(httpCode, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
		},
	})
}

func normalizeOpenAIImageResponseFormat(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "url":
		return "url", true
	case "b64_json":
		return "b64_json", true
	default:
		return "", false
	}
}

// CreateImageGenerationOpenAI POST /v1/images/generations
// 真正的 OpenAI 标准协议:同步返图,网关自动适配同步/异步渠道
func CreateImageGenerationOpenAI(c *gin.Context) {
	createImageGenerationOpenAI(c, false)
}

// CreateImageGenerationAsync POST /v1/images/generations/async
// accepts the same JSON body as the synchronous endpoint and returns a Prism
// task as soon as the durable asynchronous submission has been committed.
func CreateImageGenerationAsync(c *gin.Context) {
	createImageGenerationOpenAI(c, true)
}

func createImageGenerationOpenAI(c *gin.Context, asyncResponse bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, openAIImageMaxRequestBytes)
	token := middleware.GetToken(c)
	if token == nil {
		openAIError(c, http.StatusUnauthorized, "unauthorized", "authentication_error")
		return
	}
	req, responseFormat, ok := bindOpenAIImageJSON(c, "generation")
	if !ok {
		return
	}
	if len(req.ImageURLs) > 0 {
		openAIError(c, http.StatusBadRequest, "image_urls is only supported by /v1/images/edits", "invalid_request_error")
		return
	}
	if strings.TrimSpace(req.InputFidelity) != "" {
		openAIError(c, http.StatusBadRequest, "input_fidelity is only supported by /v1/images/edits", "invalid_request_error")
		return
	}
	request := adaptOpenAIImageRequest(req, responseFormat, nil)
	if err := adapter.ValidateOpenAIImagesRequestShape(adapter.ImagesGenerate, request); err != nil {
		openAIError(c, http.StatusBadRequest, "invalid image generation options", "invalid_request_error")
		return
	}
	_ = invokeOpenAIImage(c, token, request, nil, adapter.ImagesGenerate, asyncResponse)
}

func bindOpenAIImageJSON(c *gin.Context, action string) (OpenAIImageRequest, string, bool) {
	var req OpenAIImageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			openAIError(c, http.StatusRequestEntityTooLarge, "request is too large", "invalid_request_error")
			return OpenAIImageRequest{}, "", false
		}
		openAIError(c, http.StatusBadRequest, "invalid request body: "+err.Error(), "invalid_request_error")
		return OpenAIImageRequest{}, "", false
	}
	req.Model = strings.TrimSpace(req.Model)
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Model == "" || req.Prompt == "" {
		openAIError(c, http.StatusBadRequest, "model and prompt are required", "invalid_request_error")
		return OpenAIImageRequest{}, "", false
	}
	responseFormat, ok := normalizeOpenAIImageResponseFormat(req.ResponseFormat)
	if !ok {
		openAIError(c, http.StatusBadRequest, "response_format must be url or b64_json", "invalid_request_error")
		return OpenAIImageRequest{}, "", false
	}
	if !req.nSet {
		req.N = 1
	}
	if req.N < 1 || req.N > 10 || req.OutputCompression != nil && (*req.OutputCompression < 0 || *req.OutputCompression > 100) || req.PartialImages != nil && (*req.PartialImages < 0 || *req.PartialImages > 3 || !req.Stream) {
		openAIError(c, http.StatusBadRequest, "invalid image "+action+" options", "invalid_request_error")
		return OpenAIImageRequest{}, "", false
	}
	return req, responseFormat, true
}

func adaptOpenAIImageRequest(req OpenAIImageRequest, responseFormat string, imageURLs []string) adapter.OpenAIImagesRequest {
	return adapter.OpenAIImagesRequest{
		Model: req.Model, Prompt: req.Prompt, ImageURLs: imageURLs, N: req.N,
		Size: req.Size, AspectRatio: req.AspectRatio, Quality: req.Quality,
		ResponseFormat: responseFormat, OutputFormat: req.OutputFormat,
		OutputCompression: req.OutputCompression, Moderation: req.Moderation,
		Style: req.Style, Background: req.Background, InputFidelity: req.InputFidelity,
		User: req.User, Stream: req.Stream, PartialImages: req.PartialImages,
	}
}

type storedImageInputs struct {
	URLs     []string
	AssetIDs []uint64
}

type preparedOpenAIImageInput struct {
	Data        []byte
	ContentType string
}

func prepareOpenAIImageValues(ctx context.Context, values []string) ([]preparedOpenAIImageInput, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > openAIImageEditMaxInputs {
		return nil, fmt.Errorf("image inputs must contain at most %d images", openAIImageEditMaxInputs)
	}
	prepared := make([]preparedOpenAIImageInput, 0, len(values))
	var totalBytes int64
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New("image inputs must not contain empty values")
		}
		var data []byte
		var contentType string
		if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
			downloaded, err := safeurl.Download(ctx, value, openAIImageEditMaxFileBytes)
			if err != nil {
				return nil, fmt.Errorf("download image input: %w", err)
			}
			data, contentType = downloaded.Data, downloaded.ContentType
		} else {
			encoded := value
			if strings.HasPrefix(value, "data:") {
				parts := strings.SplitN(value, ",", 2)
				if len(parts) != 2 || !strings.Contains(parts[0], ";base64") {
					return nil, errors.New("image input contains an invalid data URL")
				}
				encoded = parts[1]
			}
			var err error
			data, err = base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, errors.New("image inputs must be URLs or base64 images")
			}
			contentType = http.DetectContentType(data)
		}
		if len(data) == 0 {
			return nil, errors.New("image inputs must not contain empty images")
		}
		if len(data) > openAIImageEditMaxFileBytes {
			return nil, errors.New("an image exceeds the 20 MiB file limit")
		}
		totalBytes += int64(len(data))
		if totalBytes > openAIImageEditMaxTotalBytes {
			return nil, errors.New("images exceed the 32 MiB total limit")
		}
		contentType = strings.TrimSpace(strings.Split(contentType, ";")[0])
		detected := http.DetectContentType(data)
		if contentType == "" || contentType == "application/octet-stream" {
			contentType = detected
		}
		switch contentType {
		case "image/png", "image/jpeg", "image/webp":
		default:
			return nil, fmt.Errorf("images must be PNG, JPEG, or WebP, got %s", contentType)
		}
		if detected != contentType {
			return nil, errors.New("image content type does not match its bytes")
		}
		prepared = append(prepared, preparedOpenAIImageInput{Data: data, ContentType: contentType})
	}
	return prepared, nil
}

func persistOpenAIImageInputs(ctx context.Context, userID, tokenID uint, prepared []preparedOpenAIImageInput) (storedImageInputs, error) {
	if len(prepared) == 0 {
		return storedImageInputs{}, nil
	}
	if userID == 0 || tokenID == 0 || !model.HasDB() {
		return storedImageInputs{}, errors.New("invalid image inputs")
	}
	assetService := video.NewUnifiedAssetService(model.DB())
	result := storedImageInputs{URLs: make([]string, 0, len(prepared)), AssetIDs: make([]uint64, 0, len(prepared))}
	seen := make(map[uint64]struct{}, len(prepared))
	for _, input := range prepared {
		asset, err := persistOpenAIImageInput(ctx, assetService, userID, tokenID, input.Data, input.ContentType)
		if err != nil {
			cleanupStoredOpenAIImageInputs(ctx, tokenID, result.AssetIDs)
			return storedImageInputs{}, fmt.Errorf("%w: store image input: %v", errOpenAIImageStorage, err)
		}
		assetID, err := strconv.ParseUint(asset.ID, 10, 64)
		if err != nil || assetID == 0 || strings.TrimSpace(asset.StoragePath) == "" {
			cleanupStoredOpenAIImageInputs(ctx, tokenID, append(result.AssetIDs, assetID))
			return storedImageInputs{}, fmt.Errorf("%w: invalid stored image identity", errOpenAIImageStorage)
		}
		result.URLs = append(result.URLs, asset.StoragePath)
		if _, exists := seen[assetID]; !exists {
			result.AssetIDs = append(result.AssetIDs, assetID)
			seen[assetID] = struct{}{}
		}
	}
	return result, nil
}

func importOpenAIImageValues(ctx context.Context, userID, tokenID uint, values []string) (storedImageInputs, error) {
	prepared, err := prepareOpenAIImageValues(ctx, values)
	if err != nil {
		return storedImageInputs{}, err
	}
	return persistOpenAIImageInputs(ctx, userID, tokenID, prepared)
}

func cleanupStoredOpenAIImageInputs(ctx context.Context, tokenID uint, assetIDs []uint64) {
	if tokenID == 0 || len(assetIDs) == 0 || !model.HasDB() {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	assetService := video.NewUnifiedAssetService(model.DB())
	for index := len(assetIDs) - 1; index >= 0; index-- {
		if assetIDs[index] != 0 {
			_ = assetService.Delete(cleanupCtx, tokenID, strconv.FormatUint(assetIDs[index], 10))
		}
	}
}

func persistOpenAIImageInput(ctx context.Context, assetService *video.UnifiedAssetService, userID, tokenID uint, data []byte, contentType string) (*video.VideoAsset, error) {
	if assetService == nil {
		return nil, errors.New("image storage is unavailable")
	}
	return assetService.Create(ctx, &video.CreateAssetRequest{
		UserID: userID, TokenID: tokenID, Kind: "image", ContentType: contentType,
		Data: data, SizeBytes: int64(len(data)),
	})
}

func appendUniqueUint64(target []uint64, values ...uint64) []uint64 {
	seen := make(map[uint64]struct{}, len(target)+len(values))
	for _, value := range target {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		target = append(target, value)
		seen[value] = struct{}{}
	}
	return target
}

// CreateImageEditOpenAI POST /v1/images/edits
// Multipart files are stored before task creation, so asynchronous task params
// contain URLs instead of embedded base64 payloads.
func CreateImageEditOpenAI(c *gin.Context) {
	createImageEditOpenAI(c, false)
}

// CreateImageEditAsync POST /v1/images/edits/async accepts either the JSON or
// multipart edit shape and returns a Prism task without waiting for the image.
func CreateImageEditAsync(c *gin.Context) {
	createImageEditOpenAI(c, true)
}

func createImageEditOpenAI(c *gin.Context, asyncResponse bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, openAIImageMaxRequestBytes)
	token := middleware.GetToken(c)
	if token == nil {
		openAIError(c, http.StatusUnauthorized, "unauthorized", "authentication_error")
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(c.ContentType()))
	if contentType == "application/json" || strings.HasSuffix(contentType, "+json") {
		createImageEditJSON(c, token, asyncResponse)
		return
	}
	createImageEditMultipart(c, token, asyncResponse)
}

func createImageEditJSON(c *gin.Context, token *model.Token, asyncResponse bool) {
	req, responseFormat, ok := bindOpenAIImageJSON(c, "edit")
	if !ok {
		return
	}
	if len(req.ImageURLs) == 0 {
		openAIError(c, http.StatusBadRequest, "image_urls is required", "invalid_request_error")
		return
	}
	request := adaptOpenAIImageRequest(req, responseFormat, req.ImageURLs)
	if err := adapter.ValidateOpenAIImagesRequestShape(adapter.ImagesEdit, request); err != nil {
		openAIError(c, http.StatusBadRequest, "invalid image edit options", "invalid_request_error")
		return
	}
	inputs, err := importOpenAIImageValues(c.Request.Context(), token.UserID, token.ID, req.ImageURLs)
	if err != nil {
		openAIImageFileError(c, err)
		return
	}
	request.ImageURLs = inputs.URLs
	if !invokeOpenAIImage(c, token, request, inputs.AssetIDs, adapter.ImagesEdit, asyncResponse) {
		cleanupStoredOpenAIImageInputs(c.Request.Context(), token.ID, inputs.AssetIDs)
	}
}

func createImageEditMultipart(c *gin.Context, token *model.Token, asyncResponse bool) {
	if err := c.Request.ParseMultipartForm(openAIImageEditMaxMemory); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			openAIError(c, http.StatusRequestEntityTooLarge, "multipart request is too large", "invalid_request_error")
			return
		}
		openAIError(c, http.StatusBadRequest, "invalid multipart request: "+err.Error(), "invalid_request_error")
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}

	modelName := strings.TrimSpace(c.PostForm("model"))
	prompt := strings.TrimSpace(c.PostForm("prompt"))
	if modelName == "" || prompt == "" {
		openAIError(c, http.StatusBadRequest, "model and prompt are required", "invalid_request_error")
		return
	}
	responseFormat, ok := normalizeOpenAIImageResponseFormat(c.PostForm("response_format"))
	if !ok {
		openAIError(c, http.StatusBadRequest, "response_format must be url or b64_json", "invalid_request_error")
		return
	}

	imageCount := countOpenAIImageEditFiles(c.Request.MultipartForm, "image")
	if imageCount == 0 {
		openAIError(c, http.StatusBadRequest, "image is required", "invalid_request_error")
		return
	}
	if imageCount > openAIImageEditMaxInputs {
		openAIError(c, http.StatusBadRequest, fmt.Sprintf("image must contain at most %d files", openAIImageEditMaxInputs), "invalid_request_error")
		return
	}
	maskCount := countOpenAIImageEditFiles(c.Request.MultipartForm, "mask")
	if maskCount > openAIImageEditMaxMasks {
		openAIError(c, http.StatusBadRequest, fmt.Sprintf("mask must contain at most %d file", openAIImageEditMaxMasks), "invalid_request_error")
		return
	}

	request := adapter.OpenAIImagesRequest{
		Model: modelName, Prompt: prompt, ImageURLs: make([]string, imageCount), MaskURLs: make([]string, maskCount), N: 1,
		Size: strings.TrimSpace(c.PostForm("size")), AspectRatio: strings.TrimSpace(c.PostForm("aspect_ratio")),
		Quality: strings.TrimSpace(c.PostForm("quality")), ResponseFormat: responseFormat,
		OutputFormat: strings.TrimSpace(c.PostForm("output_format")), Moderation: strings.TrimSpace(c.PostForm("moderation")),
		Style: strings.TrimSpace(c.PostForm("style")), Background: strings.TrimSpace(c.PostForm("background")),
		InputFidelity: strings.TrimSpace(c.PostForm("input_fidelity")), User: strings.TrimSpace(c.PostForm("user")),
	}
	if value := strings.TrimSpace(c.PostForm("stream")); value != "" {
		switch value {
		case "true":
			request.Stream = true
		case "false":
		default:
			openAIError(c, http.StatusBadRequest, "stream must be true or false", "invalid_request_error")
			return
		}
	}
	if value := strings.TrimSpace(c.PostForm("partial_images")); value != "" {
		partialImages, err := strconv.Atoi(value)
		if err != nil || partialImages < 0 || partialImages > 3 || !request.Stream {
			openAIError(c, http.StatusBadRequest, "partial_images must be between 0 and 3 and requires stream=true", "invalid_request_error")
			return
		}
		request.PartialImages = &partialImages
	}
	if value := strings.TrimSpace(c.PostForm("n")); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 10 {
			openAIError(c, http.StatusBadRequest, "n must be between 1 and 10", "invalid_request_error")
			return
		}
		request.N = n
	}
	if value := strings.TrimSpace(c.PostForm("output_compression")); value != "" {
		compression, err := strconv.Atoi(value)
		if err != nil || compression < 0 || compression > 100 {
			openAIError(c, http.StatusBadRequest, "output_compression must be between 0 and 100", "invalid_request_error")
			return
		}
		request.OutputCompression = &compression
	}
	if err := adapter.ValidateOpenAIImagesRequestShape(adapter.ImagesEdit, request); err != nil {
		openAIError(c, http.StatusBadRequest, "invalid image edit options", "invalid_request_error")
		return
	}
	preparedImages, err := prepareOpenAIImageEditFiles(c.Request.MultipartForm, "image", openAIImageEditMaxInputs)
	if err != nil {
		openAIImageFileError(c, err)
		return
	}
	preparedMasks, err := prepareOpenAIImageEditFiles(c.Request.MultipartForm, "mask", openAIImageEditMaxMasks)
	if err != nil {
		openAIImageFileError(c, err)
		return
	}
	images, err := persistOpenAIImageInputs(c.Request.Context(), token.UserID, token.ID, preparedImages)
	if err != nil {
		openAIImageFileError(c, err)
		return
	}
	request.ImageURLs = images.URLs
	assetIDs := append([]uint64(nil), images.AssetIDs...)
	masks, err := persistOpenAIImageInputs(c.Request.Context(), token.UserID, token.ID, preparedMasks)
	if err != nil {
		cleanupStoredOpenAIImageInputs(c.Request.Context(), token.ID, images.AssetIDs)
		openAIImageFileError(c, err)
		return
	}
	if len(masks.URLs) > 0 {
		request.MaskURLs = masks.URLs
		assetIDs = appendUniqueUint64(assetIDs, masks.AssetIDs...)
	}
	if !invokeOpenAIImage(c, token, request, assetIDs, adapter.ImagesEdit, asyncResponse) {
		cleanupStoredOpenAIImageInputs(c.Request.Context(), token.ID, assetIDs)
	}
}

func openAIImageFileError(c *gin.Context, err error) {
	if errors.Is(err, errOpenAIImageStorage) {
		openAIError(c, http.StatusBadGateway, err.Error(), "api_error")
		return
	}
	openAIError(c, http.StatusBadRequest, err.Error(), "invalid_request_error")
}

func countOpenAIImageEditFiles(form *multipart.Form, field string) int {
	if form == nil {
		return 0
	}
	count := 0
	for key, files := range form.File {
		if key == field || key == field+"[]" || strings.HasPrefix(key, field+"[") {
			count += len(files)
		}
	}
	return count
}

func prepareOpenAIImageEditFiles(form *multipart.Form, field string, maxFiles int) ([]preparedOpenAIImageInput, error) {
	if form == nil {
		return nil, nil
	}
	keys := make([]string, 0, len(form.File))
	for key := range form.File {
		if key == field || key == field+"[]" || strings.HasPrefix(key, field+"[") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	fileCount := 0
	for _, key := range keys {
		fileCount += len(form.File[key])
	}
	if fileCount > maxFiles {
		return nil, fmt.Errorf("%s must contain at most %d file(s)", field, maxFiles)
	}

	var totalBytes int64
	prepared := make([]preparedOpenAIImageInput, 0, fileCount)
	for _, key := range keys {
		for _, header := range form.File[key] {
			file, err := header.Open()
			if err != nil {
				return nil, fmt.Errorf("open %s: %w", field, err)
			}
			data, readErr := io.ReadAll(io.LimitReader(file, openAIImageEditMaxFileBytes+1))
			closeErr := file.Close()
			if readErr != nil {
				return nil, fmt.Errorf("read %s: %w", field, readErr)
			}
			if closeErr != nil {
				return nil, fmt.Errorf("close %s: %w", field, closeErr)
			}
			if len(data) == 0 {
				return nil, fmt.Errorf("%s must not be empty", field)
			}
			if len(data) > openAIImageEditMaxFileBytes {
				return nil, fmt.Errorf("%s exceeds the 20 MiB file limit", field)
			}
			totalBytes += int64(len(data))
			if totalBytes > openAIImageEditMaxTotalBytes {
				return nil, fmt.Errorf("%s files exceed the 32 MiB total limit", field)
			}
			contentType := http.DetectContentType(data)
			switch contentType {
			case "image/png", "image/jpeg", "image/webp":
			default:
				return nil, fmt.Errorf("%s must be PNG, JPEG, or WebP", field)
			}
			if field == "mask" && contentType != "image/png" {
				return nil, errors.New("mask must be PNG")
			}
			prepared = append(prepared, preparedOpenAIImageInput{Data: data, ContentType: contentType})
		}
	}
	return prepared, nil
}

func imageExecutionContext(requestCtx context.Context) context.Context {
	return context.WithoutCancel(requestCtx)
}

func invokeOpenAIImage(
	c *gin.Context,
	token *model.Token,
	request adapter.OpenAIImagesRequest,
	mediaAssetIDs []uint64,
	operation string,
	asyncResponse bool,
) bool {
	if token == nil {
		openAIError(c, http.StatusUnauthorized, "unauthorized", "authentication_error")
		return false
	}
	plan, err := planUnifiedOpenAIImage(c.Request.Context(), request, operation, asyncResponse)
	if err != nil {
		writeOpenAIImageExecutionError(c, err)
		return false
	}
	if asyncResponse && request.Stream {
		openAIError(c, http.StatusBadRequest, "stream is not supported by the asynchronous image endpoint", "invalid_request_error")
		return false
	}
	if asyncResponse && !plan.Asynchronous {
		openAIError(c, http.StatusBadRequest, "the selected model does not provide asynchronous image tasks", "invalid_request_error")
		return false
	}
	defer clear(plan.Prepared.Body)
	defer clear(plan.RequestPayload)
	payloadKEK, err := gatewayKey("PRISM_GATEWAY_PAYLOAD_KEK_B64")
	if err != nil {
		writeOpenAIImageExecutionError(c, err)
		return false
	}
	defer clear(payloadKEK)
	payloadHMAC, err := gatewayKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		writeOpenAIImageExecutionError(c, err)
		return false
	}
	defer clear(payloadHMAC)
	var keyringID uint64
	var keyringVersion uint32
	if err := unifiedVideoStore.DB().QueryRowContext(c.Request.Context(), `SELECT k.id,k.current_version FROM crypto_keyring_state k JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version AND v.status='current' WHERE k.purpose='gateway-payload'`).Scan(&keyringID, &keyringVersion); err != nil {
		writeOpenAIImageExecutionError(c, err)
		return false
	}
	publicID := uuid.NewString()
	idempotency, err := imageIdempotencyWithRequest(c.GetHeader("Idempotency-Key"), uint64(token.ID), uint64(plan.Route.OperationContractID), keyringVersion, payloadHMAC, plan.Policy.IdempotencyMode, plan.RequestPayload)
	if err != nil {
		writeOpenAIImageExecutionError(c, err)
		return false
	}
	runtimeService, err := gatewayruntime.New(unifiedVideoStore)
	if err != nil {
		writeOpenAIImageExecutionError(c, err)
		return false
	}
	summary := imageResourceSummary(request, operation, payloadHMAC)
	submission, err := runtimeService.SubmitCapability(c.Request.Context(), gatewayruntime.CapabilitySubmitInput{
		PublicID: publicID, RequestID: middleware.GetRequestID(c.Request.Context()), Endpoint: c.FullPath(), Operation: operation,
		UserID: uint64(token.UserID), TokenID: uint64(token.ID), ServiceTier: "standard",
		RequestPayload: plan.RequestPayload, PayloadKeyringID: keyringID, PayloadKEKVersion: keyringVersion,
		PayloadKEK: payloadKEK, PayloadHMAC: payloadHMAC, MediaAssetIDs: mediaAssetIDs,
		ResourceSummary: summary, Idempotency: idempotency,
		Route: gatewayruntime.RouteSnapshot{
			PublicModel: request.Model, VendorModel: plan.Route.VendorModel,
			ReleaseID: uint64(plan.Route.ReleaseID), OperationContractID: uint64(plan.Route.OperationContractID),
			ModelOperationID: uint64(plan.Route.ModelOperationID), SKUID: uint64(plan.Route.SKUID),
			RouteID: uint64(plan.Route.RouteID), OfferingID: uint64(plan.Route.OfferingID), CostPlanID: uint64(plan.Route.CostPlanID),
			ProductTransportID: uint64(plan.Route.ProductTransportID), CredentialPoolID: uint64(plan.Route.CredentialPoolID),
			CredentialID: uint64(plan.Route.CredentialID), CredentialVersionID: uint64(plan.Route.CredentialVersionID),
			PurposeGrantID: uint64(plan.Route.PurposeGrantID), Currency: plan.Route.Currency,
			CurrencyVersion: uint32(plan.Route.CurrencyVersion), Schedule: *plan.Route.SellSchedule,
			DeliveryMode: plan.Policy.DeliveryMode, IdempotencyMode: plan.Policy.IdempotencyMode,
			TaskScope: plan.Policy.TaskScope, CancelMode: plan.Policy.CancelMode,
			SourceURLPolicy: plan.Policy.SourceURLPolicy, UpstreamScopeKind: plan.Policy.UpstreamScopeKind,
			UpstreamScopeKey: plan.Policy.UpstreamScopeKey, ServiceTiers: plan.Policy.ServiceTiers,
		},
	})
	if err != nil {
		writeOpenAIImageExecutionError(c, err)
		return false
	}
	publicID = submission.PublicID
	c.Header(prismCallIDHeader, publicID)
	if asyncResponse {
		location := "/v1/images/tasks/" + publicID
		c.Header("Location", location)
		c.Header("Retry-After", "3")
		c.JSON(http.StatusAccepted, gin.H{
			"code": 0, "message": "success",
			"data": gin.H{"id": publicID, "status": "queued", "operation": operation, "location": location},
		})
		return true
	}
	var streamSession *imageSSESession
	if !submission.Reused && !plan.Asynchronous {
		credentialKEK, credentialHMAC, keyErr := legacyImageCredentialKeys(c.Request.Context())
		if keyErr != nil {
			_ = runtimeService.RejectCapability(imageExecutionContext(c.Request.Context()), submission.AttemptID, "credential_key_unavailable")
			writeOpenAIImageExecutionError(c, keyErr)
			return true
		}
		defer clear(credentialKEK)
		defer clear(credentialHMAC)
		dispatcher, dispatchErr := gatewayruntime.NewCapabilityDispatcher(runtimeService, nil, gatewayruntime.AsyncKeys{
			CredentialKEK: credentialKEK, CredentialHMAC: credentialHMAC,
			PayloadKEK: payloadKEK, PayloadHMAC: payloadHMAC,
		})
		if dispatchErr != nil {
			_ = runtimeService.RejectCapability(imageExecutionContext(c.Request.Context()), submission.AttemptID, "dispatcher_unavailable")
		} else {
			defer dispatcher.Close()
			if request.Stream {
				streamSession = beginOpenAIImageStreamResponse(c)
			}
			var observeResponse func([]byte) error
			if streamSession != nil {
				observeResponse = streamSession.Observe
			}
			dispatchErr = dispatcher.Dispatch(imageExecutionContext(c.Request.Context()), gatewayruntime.CapabilityDispatchInput{
				CallID: submission.CallID, AttemptID: submission.AttemptID, AdapterKey: adapter.OpenAIImagesAdapter,
				Prepared: gatewayruntime.AsyncRequest{Method: plan.Prepared.Method, Path: plan.Prepared.Path, Body: plan.Prepared.Body, Header: plan.Prepared.Header},
				Decode: func(body []byte) (gatewayruntime.AsyncObservation, error) {
					return (adapter.OpenAIImages{}).DecodeObservation(body, plan.Prepared.Streaming, request.OutputFormat)
				},
				ObserveResponse: observeResponse,
			})
		}
		if dispatchErr != nil {
			if streamSession != nil {
				streamSession.Fail(c.Writer, openAIImageStreamFailureMessage(dispatchErr))
				return true
			}
			writeOpenAIImageExecutionError(c, dispatchErr)
			return true
		}
	}
	if request.Stream && streamSession == nil {
		streamSession = beginOpenAIImageStreamResponse(c)
	}
	executionCtx := imageExecutionContext(c.Request.Context())
	var response OpenAIImageResponse
	if submission.Reused || plan.Asynchronous {
		response, err = waitUnifiedOpenAIImageResponse(executionCtx, submission.CallID, uint64(token.UserID), uint64(token.ID), request.ResponseFormat, 5*time.Minute)
	} else {
		response, err = readUnifiedOpenAIImageResponse(executionCtx, submission.CallID, uint64(token.UserID), uint64(token.ID), request.ResponseFormat)
	}
	if err != nil {
		if streamSession != nil {
			streamSession.Fail(c.Writer, openAIImageStreamFailureMessage(err))
			return true
		}
		writeOpenAIImageExecutionError(c, err)
		return true
	}
	if streamSession != nil {
		streamSession.Complete(c.Writer, response)
		return true
	}
	c.JSON(http.StatusOK, response)
	return true
}

func openAIImageStreamFailureMessage(err error) string {
	var providerErr *gatewayruntime.ProviderCapabilityError
	if !errors.As(err, &providerErr) {
		return "upstream image generation failed"
	}
	message := payloadview.ExtractFailureMessage([]byte(providerErr.Message))
	if message == "" {
		return "upstream image generation failed"
	}
	return message
}

func legacyImageCredentialKeys(ctx context.Context) ([]byte, []byte, error) {
	required, err := repository.LegacyCredentialCryptoRequired(ctx, unifiedVideoStore.DB())
	if err != nil || !required {
		return nil, nil, err
	}
	kek, err := gatewayKey("PRISM_GATEWAY_KEK_B64")
	if err != nil {
		return nil, nil, err
	}
	hmacKey, err := gatewayKey("PRISM_GATEWAY_HMAC_B64")
	if err != nil {
		clear(kek)
		return nil, nil, err
	}
	return kek, hmacKey, nil
}

type unifiedOpenAIImagePlan struct {
	Route          *routing.RouteResult
	Policy         repository.RoutePolicy
	RequestPayload []byte
	Prepared       adapter.PreparedImageRequest
	Asynchronous   bool
}

type openAIImageRouteSelector interface {
	SelectTransport(context.Context, string, routing.RouteRequirements, routing.RouteOptions) (*routing.RouteResult, error)
}

func selectUnifiedOpenAIImageRoute(ctx context.Context, selector openAIImageRouteSelector, modelName, operation string, asyncResponse bool) (*routing.RouteResult, error) {
	operationPath := "/v1/images/generations"
	if operation == adapter.ImagesEdit {
		operationPath = "/v1/images/edits"
	} else if operation != adapter.ImagesGenerate {
		return nil, repository.ErrInvalidInput
	}

	// The public image operation and the declared openai_images transport already
	// prove image support. Legacy image models may not carry the optional generic
	// image_generation tag, so requiring it here would reject an otherwise valid
	// operation-specific route before the provider request is sent.
	options := routing.RouteOptions{
		SelectionKey: uuid.NewString(), OperationMethod: http.MethodPost, OperationPath: operationPath,
		AllowedTransports:   []model.UpstreamTransport{model.UpstreamTransportOpenAIImages},
		PreferredTransports: []model.UpstreamTransport{model.UpstreamTransportOpenAIImages},
	}
	if asyncResponse {
		options.RequiredTaskScope = "task"
	}
	return selector.SelectTransport(ctx, modelName, nil, options)
}

func planUnifiedOpenAIImage(ctx context.Context, request adapter.OpenAIImagesRequest, operation string, asyncResponse bool) (unifiedOpenAIImagePlan, error) {
	if unifiedVideoStore == nil || unifiedVideoRouter == nil {
		return unifiedOpenAIImagePlan{}, gatewayruntime.ErrNotReady
	}
	if operation != adapter.ImagesGenerate && operation != adapter.ImagesEdit {
		return unifiedOpenAIImagePlan{}, repository.ErrInvalidInput
	}
	if err := gatewayruntime.RequireConfiguredReadiness(ctx, unifiedVideoStore.DB()); err != nil {
		return unifiedOpenAIImagePlan{}, err
	}
	route, err := selectUnifiedOpenAIImageRoute(ctx, unifiedVideoRouter, request.Model, operation, asyncResponse)
	if err != nil {
		return unifiedOpenAIImagePlan{}, err
	}
	if route == nil || route.ReleaseID == 0 || route.Transport != model.UpstreamTransportOpenAIImages || route.SellSchedule == nil {
		return unifiedOpenAIImagePlan{}, routing.ErrNoRoute
	}
	policy, err := unifiedVideoStore.ReadRoutePolicy(ctx, uint64(route.ReleaseID), uint64(route.SKUID), uint64(route.ProductTransportID))
	if err != nil {
		return unifiedOpenAIImagePlan{}, err
	}
	if fmt.Sprintf("%s@%d", policy.AdapterCode, policy.AdapterVersion) != adapter.OpenAIImagesAdapter || policy.CancelMode != "none" {
		return unifiedOpenAIImagePlan{}, repository.ErrConflict
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return unifiedOpenAIImagePlan{}, err
	}
	requestPath, _ := route.TransportConfig["request_path"].(string)
	if policy.TaskScope == "task" {
		err := (adapter.AsyncOpenAIImages{}).ValidateSubmit(repository.AsyncDispatch{
			Method: routeMethod(route), Path: requestPath, VendorModel: route.VendorModel,
			AdapterConfig: policy.AdapterConfig,
		}, payload)
		if err != nil {
			return unifiedOpenAIImagePlan{}, fmt.Errorf("%w: %v", repository.ErrInvalidInput, err)
		}
		return unifiedOpenAIImagePlan{Route: route, Policy: policy, RequestPayload: payload, Asynchronous: true}, nil
	}
	if policy.TaskScope != "none" && policy.TaskScope != "request" {
		return unifiedOpenAIImagePlan{}, repository.ErrConflict
	}
	prepared, err := (adapter.OpenAIImages{}).PrepareWithConfig(ctx, operation, http.MethodPost, requestPath, route.VendorModel, payload, policy.AdapterConfig, storedOpenAIImageLoader{})
	if err != nil {
		return unifiedOpenAIImagePlan{}, fmt.Errorf("%w: %v", repository.ErrInvalidInput, err)
	}
	return unifiedOpenAIImagePlan{Route: route, Policy: policy, RequestPayload: payload, Prepared: prepared}, nil
}

func routeMethod(route *routing.RouteResult) string {
	if route == nil {
		return ""
	}
	method, _ := route.TransportConfig["request_method"].(string)
	return method
}

type storedOpenAIImageLoader struct{}

func (storedOpenAIImageLoader) LoadImage(ctx context.Context, location string) (adapter.ImageAsset, error) {
	data, err := filestorage.ReadURL(ctx, location, 64<<20)
	if err != nil {
		downloaded, downloadErr := safeurl.Download(ctx, location, 64<<20)
		if downloadErr != nil {
			return adapter.ImageAsset{}, errors.Join(err, downloadErr)
		}
		return adapter.ImageAsset{Data: downloaded.Data, ContentType: downloaded.ContentType}, nil
	}
	return adapter.ImageAsset{Data: data, ContentType: http.DetectContentType(data)}, nil
}

func imageIdempotencyWithRequest(raw string, tokenID, operationContractID uint64, keyVersion uint32, hmacKey []byte, mode string, requestBytes []byte) (*repository.IdempotencyInput, error) {
	key := strings.TrimSpace(raw)
	if mode == "required" && key == "" || mode == "forbidden" && key != "" || len(key) > 128 {
		return nil, repository.ErrInvalidInput
	}
	if key == "" {
		return nil, nil
	}
	return buildRotationAwareIdempotency(tokenID, operationContractID, keyVersion, hmacKey, key, requestBytes)
}

// buildRotationAwareIdempotency constructs an IdempotencyInput whose primary
// KeyHMAC/RequestHMAC pair uses the current payload HMAC version and whose
// KeyAliases carry every extra readable version. This keeps a client's
// Idempotency-Key resolvable during a HMAC key rotation window: the fact stored
// under the old version is still found via its alias. requestBytes may be nil;
// when nil the caller must fill RequestHMAC (and matching alias RequestHMACs)
// later using the actual request payload.
func buildRotationAwareIdempotency(tokenID, operationContractID uint64, currentVersion uint32, currentKey []byte, keyText string, requestBytes []byte) (*repository.IdempotencyInput, error) {
	keyDigest := security.HMACSHA256(currentKey, []byte("gateway-idempotency-v1:"+keyText))
	now := time.Now().UTC()
	input := &repository.IdempotencyInput{
		TokenID: tokenID, OperationContractID: operationContractID,
		KeyHMAC: hex.EncodeToString(keyDigest[:]), HMACKeyVersion: currentVersion,
		ReplayExpiresAt: ptrTime(now.Add(24 * time.Hour)), KeyReuseAfter: ptrTime(now.Add(48 * time.Hour)),
	}
	if len(requestBytes) > 0 {
		requestDigest := security.HMACSHA256(currentKey, requestBytes)
		input.RequestHMAC = hex.EncodeToString(requestDigest[:])
	}
	if store := unifiedVideoStore; store != nil {
		versions, err := store.PayloadHMACKeyVersions(context.Background(), store.DB())
		if err == nil && len(versions) > 1 {
			for _, version := range versions {
				if version == currentVersion {
					continue
				}
				aliasKey, err := security.LoadVersionedHMACKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64", version)
				if err != nil {
					continue
				}
				aliasKeyDigest := security.HMACSHA256(aliasKey, []byte("gateway-idempotency-v1:"+keyText))
				alias := repository.IdempotencyKeyAlias{
					KeyHMAC: hex.EncodeToString(aliasKeyDigest[:]), HMACKeyVersion: version,
				}
				if len(requestBytes) > 0 {
					aliasRequestDigest := security.HMACSHA256(aliasKey, requestBytes)
					alias.RequestHMAC = hex.EncodeToString(aliasRequestDigest[:])
				}
				clear(aliasKey)
				input.KeyAliases = append(input.KeyAliases, alias)
			}
		}
	}
	return input, nil
}

func imageResourceSummary(request adapter.OpenAIImagesRequest, operation string, hmacKey []byte) map[string]any {
	digest := security.DomainDigest(hmacKey, "image-resource-prompt-v1", []byte(request.Prompt))
	return map[string]any{
		"model": request.Model, "operation": operation, "n": request.N, "size": request.Size,
		"aspect_ratio": request.AspectRatio, "quality": request.Quality, "output_format": request.OutputFormat,
		"response_format": request.ResponseFormat, "stream": request.Stream,
		"input_count": len(request.ImageURLs), "mask_count": len(request.MaskURLs),
		"prompt_length": len([]byte(request.Prompt)), "prompt_hmac": hex.EncodeToString(digest[:]),
	}
}

func readUnifiedOpenAIImageResponse(ctx context.Context, callID, userID, tokenID uint64, responseFormat string) (OpenAIImageResponse, error) {
	if callID == 0 || userID == 0 || tokenID == 0 {
		return OpenAIImageResponse{}, repository.ErrInvalidInput
	}
	var payloadID uint64
	var createdAt time.Time
	err := unifiedVideoStore.DB().QueryRowContext(ctx, `SELECT c.result_payload_id,c.created_at FROM gw_api_calls c JOIN gw_api_resources r ON r.call_id=c.id AND r.resource_kind='capability_task' WHERE c.id=? AND c.user_id=? AND c.token_id=? AND c.status='completed'`, callID, userID, tokenID).Scan(&payloadID, &createdAt)
	if err != nil {
		return OpenAIImageResponse{}, err
	}
	plain, err := readUnifiedPayload(ctx, payloadID, callID, "result")
	if err != nil {
		return OpenAIImageResponse{}, err
	}
	var result delivery.ImageResult
	if json.Unmarshal(plain, &result) != nil || result.SchemaVersion != delivery.ResultSchemaVersion || result.Kind != delivery.ImageResultKind || len(result.Images) == 0 {
		return OpenAIImageResponse{}, delivery.ErrInvalidResult
	}
	created := result.Created
	if created == 0 {
		created = createdAt.UTC().Unix()
	}
	response := OpenAIImageResponse{Created: created, Data: make([]OpenAIImageData, 0, len(result.Images))}
	for _, image := range result.Images {
		if image.DeliveryID == 0 {
			return OpenAIImageResponse{}, delivery.ErrInvalidResult
		}
		item := OpenAIImageData{RevisedPrompt: image.RevisedPrompt}
		if responseFormat == "b64_json" {
			data, err := readUnifiedImageBytes(ctx, image.DeliveryID, callID, userID, tokenID)
			if err != nil {
				return OpenAIImageResponse{}, err
			}
			item.B64JSON = base64.StdEncoding.EncodeToString(data)
		} else {
			item.URL, err = readUnifiedDeliveryURL(ctx, image.DeliveryID, callID, uint(userID), uint(tokenID))
			if err != nil {
				return OpenAIImageResponse{}, err
			}
		}
		response.Data = append(response.Data, item)
	}
	return response, nil
}

func waitUnifiedOpenAIImageResponse(ctx context.Context, callID, userID, tokenID uint64, responseFormat string, timeout time.Duration) (OpenAIImageResponse, error) {
	if timeout <= 0 {
		return OpenAIImageResponse{}, repository.ErrInvalidInput
	}
	deadline := time.Now().Add(timeout)
	for {
		response, err := readUnifiedOpenAIImageResponse(ctx, callID, userID, tokenID, responseFormat)
		if err == nil {
			return response, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return OpenAIImageResponse{}, err
		}
		var status string
		if queryErr := unifiedVideoStore.DB().QueryRowContext(ctx, `SELECT status FROM gw_api_calls WHERE id=? AND user_id=? AND token_id=?`, callID, userID, tokenID).Scan(&status); queryErr != nil {
			return OpenAIImageResponse{}, queryErr
		}
		switch status {
		case "failed", "cancelled":
			providerErr := &gatewayruntime.ProviderCapabilityError{
				HTTPStatus: http.StatusBadGateway,
				Code:       "provider_request_failed",
			}
			failure, failureErr := payloadview.ReadLatestRequestFailure(ctx, unifiedVideoStore, callID)
			if failureErr == nil {
				if code := strings.TrimSpace(failure.Code); code != "" {
					providerErr.Code = code
				}
				if failure.HTTPStatus > 0 && failure.HTTPStatus <= 999 {
					providerErr.HTTPStatus = int(failure.HTTPStatus)
				}
				providerErr.Message = failure.Message
			}
			return OpenAIImageResponse{}, providerErr
		case "indeterminate":
			return OpenAIImageResponse{}, &gatewayruntime.ProviderCapabilityError{HTTPStatus: http.StatusGatewayTimeout, Code: "provider_exchange_unknown"}
		}
		if !time.Now().Before(deadline) {
			return OpenAIImageResponse{}, &gatewayruntime.ProviderCapabilityError{HTTPStatus: http.StatusGatewayTimeout, Code: "provider_exchange_unknown"}
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return OpenAIImageResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func readUnifiedImageBytes(ctx context.Context, deliveryID, callID, userID, tokenID uint64) ([]byte, error) {
	record, err := unifiedVideoStore.ReadResultDeliveryByID(ctx, deliveryID, callID, userID, tokenID)
	if err != nil || record.State != "ready" {
		return nil, errors.Join(repository.ErrConflict, err)
	}
	if record.Mode == "managed_copy" {
		storage, storageErr := unifiedVideoStore.ReadAttemptFileStorage(ctx, record.AttemptID)
		if storageErr != nil || storage.TokenID != tokenID || storage.APIKey == "" {
			return nil, errors.Join(repository.ErrConflict, storageErr)
		}
		data, err := readManagedResult(ctx, storage.APIKey, record.MediaLocator, 64<<20)
		return validateUnifiedImageBytes(data, err)
	}
	location, err := readUnifiedDeliveryURL(ctx, deliveryID, callID, uint(userID), uint(tokenID))
	if err != nil {
		return nil, err
	}
	downloaded, err := safeurl.Download(ctx, location, 64<<20)
	if err != nil {
		return nil, err
	}
	return validateUnifiedImageBytes(downloaded.Data, nil)
}

func validateUnifiedImageBytes(data []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	switch http.DetectContentType(data) {
	case "image/png", "image/jpeg", "image/webp":
		return data, nil
	default:
		return nil, delivery.ErrInvalidResult
	}
}

func beginOpenAIImageStreamResponse(c *gin.Context) *imageSSESession {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flushImageSSE(c.Writer)
	return startImageSSESession(c.Writer)
}

func writeOpenAIImageExecutionError(c *gin.Context, err error) {
	var providerErr *gatewayruntime.ProviderCapabilityError
	switch {
	case errors.Is(err, service.ErrInsufficientTokenBalance), errors.Is(err, service.ErrInsufficientUserBalance), errors.Is(err, repository.ErrInsufficient):
		openAIError(c, http.StatusBadRequest, "insufficient quota", "insufficient_quota")
	case errors.Is(err, repository.ErrInvalidInput), errors.Is(err, adapter.ErrInvalidImageRequest):
		openAIError(c, http.StatusBadRequest, "invalid image request", "invalid_request_error")
	case errors.Is(err, repository.ErrIdempotencyConflict):
		openAIError(c, http.StatusConflict, "idempotency key conflicts with an existing request", "invalid_request_error")
	case errors.Is(err, gatewayruntime.ErrNotReady), errors.Is(err, routing.ErrNoRoute), errors.Is(err, routing.ErrNoCompatibleTransport), errors.Is(err, routing.ErrCapabilityUnavailable):
		openAIError(c, http.StatusServiceUnavailable, "image generation is unavailable", "service_unavailable_error")
	case errors.As(err, &providerErr):
		status := http.StatusBadGateway
		if providerErr.HTTPStatus == http.StatusTooManyRequests {
			status = http.StatusTooManyRequests
		} else if providerErr.Code == "provider_exchange_unknown" {
			status = http.StatusGatewayTimeout
		}
		message := strings.TrimSpace(providerErr.Message)
		if message == "" {
			message = "upstream image generation failed"
		}
		openAIError(c, status, message, "api_error")
	default:
		openAIError(c, http.StatusInternalServerError, "image generation failed", "api_error")
	}
}
