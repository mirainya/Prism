package open

import (
	"context"
	"encoding/base64"
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
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/filestorage"
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
	User              string   `json:"user"`
	Stream            bool     `json:"stream"` // true=SSE 流式输出（OpenAI 标准）
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

type openAIImageCapabilityService interface {
	InvokeAndWait(context.Context, *service.InvokeRequest, int) (*service.ImageResult, error)
	CancelTaskForToken(context.Context, string, uint, uint) error
}

var openAIImageService openAIImageCapabilityService = capabilityService

type imageBase64Encoder func(context.Context, string) (string, error)

var openAIImageBase64Encoder imageBase64Encoder = service.DownloadImageAsBase64

type imageEditFileStorer func(context.Context, []byte, string, string) (string, error)

var openAIImageFileStorer imageEditFileStorer = filestorage.TransferBytes

var errOpenAIImageStorage = errors.New("image storage failed")

const (
	openAIImageEditMaxMemory       = 8 << 20
	openAIImageEditMaxFileBytes    = 20 << 20
	openAIImageEditMaxTotalBytes   = 32 << 20
	openAIImageEditMaxRequestBytes = 40 << 20
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

func buildOpenAIImageData(
	ctx context.Context,
	urls []string,
	revisedPrompt string,
	responseFormat string,
	encode imageBase64Encoder,
) ([]OpenAIImageData, error) {
	data := make([]OpenAIImageData, 0, len(urls))
	for _, imageURL := range urls {
		imageURL = strings.TrimSpace(imageURL)
		if imageURL == "" {
			continue
		}
		item := OpenAIImageData{RevisedPrompt: revisedPrompt}
		if responseFormat == "b64_json" {
			b64JSON, err := encode(ctx, imageURL)
			if err != nil {
				return nil, fmt.Errorf("encode generated image: %w", err)
			}
			item.B64JSON = b64JSON
		} else {
			item.URL = imageURL
		}
		data = append(data, item)
	}
	if len(data) == 0 {
		return nil, errors.New("image generation completed without output")
	}
	return data, nil
}

// CreateImageGenerationOpenAI POST /v1/images/generations
// 真正的 OpenAI 标准协议:同步返图,网关自动适配同步/异步渠道
func CreateImageGenerationOpenAI(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, openAIImageEditMaxRequestBytes)
	var req OpenAIImageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			openAIError(c, http.StatusRequestEntityTooLarge, "request is too large", "invalid_request_error")
			return
		}
		openAIError(c, http.StatusBadRequest, "invalid request body: "+err.Error(), "invalid_request_error")
		return
	}
	if req.Model == "" || req.Prompt == "" {
		openAIError(c, http.StatusBadRequest, "model and prompt are required", "invalid_request_error")
		return
	}
	responseFormat, ok := normalizeOpenAIImageResponseFormat(req.ResponseFormat)
	if !ok {
		openAIError(c, http.StatusBadRequest, "response_format must be url or b64_json", "invalid_request_error")
		return
	}
	if req.PartialImages != nil && *req.PartialImages < 0 {
		openAIError(c, http.StatusBadRequest, "partial_images must be non-negative", "invalid_request_error")
		return
	}

	// 组装 params: prompt 之外的 OpenAI 字段透传给渠道映射层
	params := map[string]any{"prompt": req.Prompt}
	imageURLs, err := storeOpenAIImageGenerationInputs(c.Request.Context(), req.ImageURLs)
	if err != nil {
		openAIImageFileError(c, err)
		return
	}
	if len(imageURLs) > 0 {
		params["image_urls"] = openAIImageFileParam(imageURLs)
	}
	if req.N > 0 {
		params["n"] = req.N
	}
	if req.Size != "" {
		params["size"] = req.Size
	}
	if req.AspectRatio != "" {
		params["aspect_ratio"] = req.AspectRatio
	}
	if req.Quality != "" {
		params["quality"] = req.Quality
	}
	if req.OutputFormat != "" {
		params["output_format"] = req.OutputFormat
	}
	if req.OutputCompression != nil {
		params["output_compression"] = *req.OutputCompression
	}
	if req.Moderation != "" {
		params["moderation"] = req.Moderation
	}
	if req.Style != "" {
		params["style"] = req.Style
	}
	if req.PartialImages != nil {
		params["partial_images"] = *req.PartialImages
	}
	operation := "images.generate"
	if len(imageURLs) > 0 {
		operation = "images.edit"
	}
	invokeOpenAIImage(c, req.Model, params, responseFormat, operation, req.Stream)
}

func storeOpenAIImageGenerationInputs(ctx context.Context, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	storedURLs := make([]string, 0, len(values))
	var totalBytes int64
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New("image_urls must not contain empty values")
		}
		if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
			storedURLs = append(storedURLs, value)
			continue
		}

		encoded := value
		if strings.HasPrefix(value, "data:") {
			parts := strings.SplitN(value, ",", 2)
			if len(parts) != 2 || !strings.Contains(parts[0], ";base64") {
				return nil, errors.New("image_urls contains an invalid image data URL")
			}
			encoded = parts[1]
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, errors.New("image_urls must contain image URLs or base64 images")
		}
		if len(data) == 0 {
			return nil, errors.New("image_urls must not contain empty images")
		}
		if len(data) > openAIImageEditMaxFileBytes {
			return nil, errors.New("an image_urls image exceeds the 20 MiB file limit")
		}
		totalBytes += int64(len(data))
		if totalBytes > openAIImageEditMaxTotalBytes {
			return nil, errors.New("image_urls images exceed the 32 MiB total limit")
		}

		contentType := http.DetectContentType(data)
		switch contentType {
		case "image/png", "image/jpeg", "image/webp":
		default:
			return nil, fmt.Errorf("image_urls must contain PNG, JPEG, or WebP images, got %s", contentType)
		}
		storedURL, err := openAIImageFileStorer(ctx, data, contentType, "text2img")
		if err != nil {
			return nil, fmt.Errorf("%w: store image_urls: %v", errOpenAIImageStorage, err)
		}
		storedURLs = append(storedURLs, storedURL)
	}
	return storedURLs, nil
}

// CreateImageEditOpenAI POST /v1/images/edits
// Multipart files are stored before task creation, so asynchronous task params
// contain URLs instead of embedded base64 payloads.
func CreateImageEditOpenAI(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, openAIImageEditMaxRequestBytes)
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

	images, err := storeOpenAIImageEditFiles(c.Request.Context(), c.Request.MultipartForm, "image")
	if err != nil {
		openAIImageFileError(c, err)
		return
	}
	if len(images) == 0 {
		openAIError(c, http.StatusBadRequest, "image is required", "invalid_request_error")
		return
	}

	params := map[string]any{
		"prompt":     prompt,
		"image_urls": openAIImageFileParam(images),
	}
	for _, field := range []string{"size", "aspect_ratio", "quality", "output_format", "moderation", "style"} {
		if value := strings.TrimSpace(c.PostForm(field)); value != "" {
			params[field] = value
		}
	}
	if value := strings.TrimSpace(c.PostForm("n")); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			openAIError(c, http.StatusBadRequest, "n must be a positive integer", "invalid_request_error")
			return
		}
		params["n"] = n
	}
	if value := strings.TrimSpace(c.PostForm("output_compression")); value != "" {
		compression, err := strconv.Atoi(value)
		if err != nil || compression < 0 || compression > 100 {
			openAIError(c, http.StatusBadRequest, "output_compression must be between 0 and 100", "invalid_request_error")
			return
		}
		params["output_compression"] = compression
	}
	if masks, err := storeOpenAIImageEditFiles(c.Request.Context(), c.Request.MultipartForm, "mask"); err != nil {
		openAIImageFileError(c, err)
		return
	} else if len(masks) > 0 {
		params["mask"] = openAIImageFileParam(masks)
	}

	invokeOpenAIImage(c, modelName, params, responseFormat, "images.edit", false)
}

func openAIImageFileError(c *gin.Context, err error) {
	if errors.Is(err, errOpenAIImageStorage) {
		openAIError(c, http.StatusBadGateway, err.Error(), "api_error")
		return
	}
	openAIError(c, http.StatusBadRequest, err.Error(), "invalid_request_error")
}

func storeOpenAIImageEditFiles(ctx context.Context, form *multipart.Form, field string) ([]string, error) {
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

	var totalBytes int64
	var storedURLs []string
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
			storedURL, err := openAIImageFileStorer(ctx, data, contentType, "text2img")
			if err != nil {
				return nil, fmt.Errorf("%w: store %s: %v", errOpenAIImageStorage, field, err)
			}
			storedURLs = append(storedURLs, storedURL)
		}
	}
	return storedURLs, nil
}

func openAIImageFileParam(files []string) any {
	values := make([]any, len(files))
	for i := range files {
		values[i] = files[i]
	}
	return values
}

func imageExecutionContext(requestCtx context.Context) context.Context {
	return context.WithoutCancel(requestCtx)
}

func invokeOpenAIImage(
	c *gin.Context,
	modelName string,
	params map[string]any,
	responseFormat string,
	operation string,
	stream bool,
) {
	token := middleware.GetToken(c)
	if token == nil {
		openAIError(c, http.StatusUnauthorized, "unauthorized", "authentication_error")
		return
	}

	if stream {
		invokeOpenAIImageStream(c, token, modelName, params, responseFormat, operation)
		return
	}

	invokeReq := &service.InvokeRequest{
		UserID:         token.UserID,
		TokenID:        token.ID,
		Capability:     "text2img",
		RouteOperation: operation,
		Model:          modelName,
		Params:         params,
	}
	attachCapabilityCallIdentity(c, invokeReq, operation)
	executionCtx := imageExecutionContext(c.Request.Context())
	result, err := openAIImageService.InvokeAndWait(executionCtx, invokeReq, 0)
	if err != nil {
		if errors.Is(err, service.ErrInsufficientTokenBalance) || errors.Is(err, service.ErrInsufficientUserBalance) {
			openAIError(c, http.StatusBadRequest, err.Error(), "insufficient_quota")
			return
		}
		openAIError(c, http.StatusInternalServerError, err.Error(), "api_error")
		return
	}

	// 明确失败
	if result.Done && !result.Success {
		statusCode := result.HTTPStatus
		if statusCode < 400 || statusCode >= 600 {
			statusCode = http.StatusBadGateway
		}
		if len(result.ErrorBody) > 0 {
			c.Data(statusCode, "application/json; charset=utf-8", result.ErrorBody)
			return
		}
		openAIError(c, statusCode, result.Error, "api_error")
		return
	}

	// OpenAI Images is synchronous. Cancel timed-out internal tasks so callers never
	// receive a Prism-specific 202 response that an OpenAI-compatible proxy cannot use.
	if !result.Done {
		if result.TaskNo == "" || openAIImageService.CancelTaskForToken(
			c.Request.Context(), result.TaskNo, token.UserID, token.ID,
		) != nil {
			openAIError(c, http.StatusInternalServerError, "image generation timed out and cancellation failed", "api_error")
			return
		}
		openAIError(c, http.StatusGatewayTimeout, "image generation timed out", "api_error")
		return
	}

	data, err := buildOpenAIImageData(
		c.Request.Context(), result.URLs, result.RevisedPrompt, responseFormat, openAIImageBase64Encoder,
	)
	if err != nil {
		openAIError(c, http.StatusBadGateway, err.Error(), "api_error")
		return
	}

	c.JSON(http.StatusOK, OpenAIImageResponse{
		Created: time.Now().Unix(),
		Data:    data,
	})
}

// invokeOpenAIImageStream 流式路径：设置 SSE 响应头，透传上游进度事件，最终发
// image_generation.completed 帧后发 [DONE]。
// 上游为 SSE 渠道时 partial_image 事件实时推送；同步/异步渠道则等结果后直发 completed。
func invokeOpenAIImageStream(
	c *gin.Context,
	token *model.Token,
	modelName string,
	params map[string]any,
	responseFormat string,
	operation string,
) {
	// events 缓冲 64 帧——SSE 上游连续推 partial 时不阻塞 Submit 的 SSE 读取循环。
	events := make(chan []byte, 64)
	streamParams := make(map[string]any, len(params)+1)
	for key, value := range params {
		streamParams[key] = value
	}
	// The downstream stream flag must reach the provider so it requests upstream SSE.
	streamParams["stream"] = true
	invokeReq := &service.InvokeRequest{
		UserID:         token.UserID,
		TokenID:        token.ID,
		Capability:     "text2img",
		RouteOperation: operation,
		Model:          modelName,
		Params:         streamParams,
		EventSink:      events,
	}
	attachCapabilityCallIdentity(c, invokeReq, operation)

	type invokeOutcome struct {
		result *service.ImageResult
		err    error
	}
	outcomeCh := make(chan invokeOutcome, 1)
	executionCtx := imageExecutionContext(c.Request.Context())
	go func() {
		result, err := openAIImageService.InvokeAndWait(executionCtx, invokeReq, 0)
		close(events) // 通知读端 SSE 事件已全部写完
		outcomeCh <- invokeOutcome{result, err}
	}()

	// 一旦写出首帧 HTTP 就已提交响应，不能再改 status code，所以先锁定 SSE 头。
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	w := c.Writer

	// 转发上游 partial_image 等事件；函数返回后 events channel 已关闭。
	errorForwarded := forwardImageSSEEvents(w, events)
	flushImageSSE(w)

	// 等待 InvokeAndWait 最终结果。
	outcome := <-outcomeCh
	if outcome.err != nil {
		if !errorForwarded {
			msg := outcome.err.Error()
			if errors.Is(outcome.err, service.ErrInsufficientTokenBalance) ||
				errors.Is(outcome.err, service.ErrInsufficientUserBalance) {
				msg = outcome.err.Error()
			}
			writeImageSSEError(w, msg, "api_error")
		}
		writeImageSSEDone(w)
		flushImageSSE(w)
		return
	}

	result := outcome.result
	if result != nil && result.Done && !result.Success {
		if !errorForwarded {
			statusMsg := result.Error
			if statusMsg == "" {
				statusMsg = "image generation failed"
			}
			writeImageSSEError(w, statusMsg, "api_error")
		}
		writeImageSSEDone(w)
		flushImageSSE(w)
		return
	}

	if result != nil && !result.Done {
		// 超时降级：取消内部任务，告知客户端超时。
		_ = openAIImageService.CancelTaskForToken(c.Request.Context(), result.TaskNo, token.UserID, token.ID)
		writeImageSSEError(w, "image generation timed out", "api_error")
		writeImageSSEDone(w)
		flushImageSSE(w)
		return
	}

	// 发最终 completed 帧（含已解析 URL 或 b64，格式对齐 responseFormat）。
	_ = writeImageCompletedSSE(c.Request.Context(), w, result, responseFormat, openAIImageBase64Encoder)
	writeImageSSEDone(w)
	flushImageSSE(w)
}
