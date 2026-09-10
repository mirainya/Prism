package adapter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/runtime"
)

const OpenAIImagesAdapter = "openai_images@1"

const (
	ImagesGenerate = "images.generate"
	ImagesEdit     = "images.edit"

	maxImageInputs       = 16
	maxImageOutputBytes  = 64 << 20
	maxImageRequestBytes = 128 << 20
)

var (
	ErrInvalidImageRequest  = errors.New("openai images: invalid request")
	ErrInvalidImageResponse = errors.New("openai images: invalid response")
)

// OpenAIImagesRequest is the normalized request retained by the gateway. Input
// images are immutable storage URLs; image bytes never enter the SQL payload.
type OpenAIImagesRequest struct {
	Model             string   `json:"model"`
	Prompt            string   `json:"prompt"`
	ImageURLs         []string `json:"image_urls,omitempty"`
	MaskURLs          []string `json:"mask_urls,omitempty"`
	N                 int      `json:"n,omitempty"`
	Size              string   `json:"size,omitempty"`
	AspectRatio       string   `json:"aspect_ratio,omitempty"`
	Quality           string   `json:"quality,omitempty"`
	ResponseFormat    string   `json:"response_format,omitempty"`
	OutputFormat      string   `json:"output_format,omitempty"`
	OutputCompression *int     `json:"output_compression,omitempty"`
	Moderation        string   `json:"moderation,omitempty"`
	Style             string   `json:"style,omitempty"`
	Background        string   `json:"background,omitempty"`
	InputFidelity     string   `json:"input_fidelity,omitempty"`
	User              string   `json:"user,omitempty"`
	Stream            bool     `json:"stream,omitempty"`
	PartialImages     *int     `json:"partial_images,omitempty"`
}

type ImageAsset struct {
	Data        []byte
	ContentType string
}

type ImageAssetLoader interface {
	LoadImage(context.Context, string) (ImageAsset, error)
}

type PreparedImageRequest struct {
	Method, Path string
	Body         []byte
	Header       http.Header
	Streaming    bool
}

type OpenAIImageOutput struct {
	URL           string
	Data          []byte
	ContentType   string
	RevisedPrompt string
}

type OpenAIImagesUsage struct {
	InputTokens, OutputTokens, TotalTokens *int64
}

type OpenAIImagesObservation struct {
	Created int64
	Outputs []OpenAIImageOutput
	Usage   OpenAIImagesUsage
}

type OpenAIImages struct{}

// DecodeObservation converts an OpenAI Images response into the runtime
// observation contract. The persisted result contains only delivery metadata;
// URLs and inline bytes remain in Sources until the runtime creates protected
// deliveries.
func (OpenAIImages) DecodeObservation(body []byte, streaming bool, outputFormat string) (runtime.AsyncObservation, error) {
	observation, err := (OpenAIImages{}).Decode(body, streaming, outputFormat)
	if err != nil {
		return runtime.AsyncObservation{}, err
	}
	if len(observation.Outputs) == 0 {
		return runtime.AsyncObservation{}, ErrInvalidImageResponse
	}
	sources := make([]delivery.RemoteResult, 0, len(observation.Outputs))
	images := make([]delivery.ImageOutput, 0, len(observation.Outputs))
	for _, output := range observation.Outputs {
		source := delivery.RemoteResult{Role: "image", URL: output.URL, InlineData: append([]byte(nil), output.Data...), ContentType: output.ContentType}
		if !delivery.ValidSource(source) {
			return runtime.AsyncObservation{}, ErrInvalidImageResponse
		}
		sources = append(sources, source)
		images = append(images, delivery.ImageOutput{RevisedPrompt: output.RevisedPrompt})
	}
	result, err := json.Marshal(delivery.ImageResult{SchemaVersion: delivery.ResultSchemaVersion, Kind: delivery.ImageResultKind, Created: observation.Created, Images: images})
	if err != nil {
		return runtime.AsyncObservation{}, err
	}
	facts := billing.Facts{Events: map[billing.ChargeEvent]bool{
		billing.ChargeAccepted:  true,
		billing.ChargeSucceeded: true,
	}, Quantities: map[billing.QuantitySource]string{
		billing.QuantityGeneratedImages: strconv.Itoa(len(images)),
	}}
	if observation.Usage.InputTokens != nil {
		facts.Quantities[billing.QuantityInputTokens] = strconv.FormatInt(*observation.Usage.InputTokens, 10)
	}
	if observation.Usage.OutputTokens != nil {
		facts.Quantities[billing.QuantityOutputTokens] = strconv.FormatInt(*observation.Usage.OutputTokens, 10)
	}
	return runtime.AsyncObservation{State: execution.AsyncSucceeded, Result: result, Sources: sources, Facts: facts}, nil
}

func (OpenAIImages) Prepare(ctx context.Context, operation, method, path, vendorModel string, payload []byte, loader ImageAssetLoader) (PreparedImageRequest, error) {
	if ctx == nil || method != http.MethodPost || !validImageRequestPath(path) || strings.TrimSpace(vendorModel) == "" {
		return PreparedImageRequest{}, ErrInvalidImageRequest
	}
	request, err := decodeOpenAIImagesRequest(payload)
	if err != nil || validateOpenAIImagesRequest(operation, request) != nil {
		return PreparedImageRequest{}, ErrInvalidImageRequest
	}
	request.Model = vendorModel
	if operation == ImagesGenerate {
		body, err := marshalImageGeneration(request)
		if err != nil {
			return PreparedImageRequest{}, err
		}
		header := http.Header{"Content-Type": []string{"application/json"}}
		if request.Stream {
			header.Set("Accept", "text/event-stream")
		}
		return PreparedImageRequest{Method: method, Path: path, Body: body, Header: header, Streaming: request.Stream}, nil
	}
	if loader == nil {
		return PreparedImageRequest{}, ErrInvalidImageRequest
	}
	body, contentType, err := marshalImageEdit(ctx, request, loader)
	if err != nil {
		return PreparedImageRequest{}, err
	}
	return PreparedImageRequest{Method: method, Path: path, Body: body, Header: http.Header{"Content-Type": []string{contentType}}, Streaming: request.Stream}, nil
}

func decodeOpenAIImagesRequest(payload []byte) (OpenAIImagesRequest, error) {
	if len(payload) == 0 || len(payload) > maxImageRequestBytes {
		return OpenAIImagesRequest{}, ErrInvalidImageRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var request OpenAIImagesRequest
	if err := decoder.Decode(&request); err != nil {
		return OpenAIImagesRequest{}, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return OpenAIImagesRequest{}, err
	}
	return request, nil
}

func validateOpenAIImagesRequest(operation string, request OpenAIImagesRequest) error {
	if operation != ImagesGenerate && operation != ImagesEdit || strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.Prompt) == "" || utf8.RuneCountInString(request.Prompt) > 32000 {
		return ErrInvalidImageRequest
	}
	if request.N < 1 || request.N > 10 || len(request.Size) > 32 || len(request.AspectRatio) > 16 || len(request.User) > 256 {
		return ErrInvalidImageRequest
	}
	if request.ResponseFormat != "url" && request.ResponseFormat != "b64_json" {
		return ErrInvalidImageRequest
	}
	if request.OutputCompression != nil && (*request.OutputCompression < 0 || *request.OutputCompression > 100) {
		return ErrInvalidImageRequest
	}
	if request.PartialImages != nil && (*request.PartialImages < 0 || *request.PartialImages > 3 || !request.Stream) {
		return ErrInvalidImageRequest
	}
	if len(request.ImageURLs) > maxImageInputs || len(request.MaskURLs) > 1 {
		return ErrInvalidImageRequest
	}
	if operation == ImagesGenerate && (len(request.ImageURLs) != 0 || len(request.MaskURLs) != 0) || operation == ImagesEdit && len(request.ImageURLs) == 0 {
		return ErrInvalidImageRequest
	}
	for _, values := range [][]string{request.ImageURLs, request.MaskURLs} {
		for _, value := range values {
			if !validRemoteImageURL(value) {
				return ErrInvalidImageRequest
			}
		}
	}
	return nil
}

func validImageRequestPath(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !parsed.IsAbs() && parsed.Host == "" && parsed.Fragment == ""
}

func validRemoteImageURL(value string) bool {
	if strings.TrimSpace(value) != value || len(value) == 0 || len(value) > 8192 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == ""
}

type imageGenerationPayload struct {
	Model             string `json:"model"`
	Prompt            string `json:"prompt"`
	N                 int    `json:"n"`
	Size              string `json:"size,omitempty"`
	AspectRatio       string `json:"aspect_ratio,omitempty"`
	Quality           string `json:"quality,omitempty"`
	ResponseFormat    string `json:"response_format,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	OutputCompression *int   `json:"output_compression,omitempty"`
	Moderation        string `json:"moderation,omitempty"`
	Style             string `json:"style,omitempty"`
	Background        string `json:"background,omitempty"`
	User              string `json:"user,omitempty"`
	Stream            bool   `json:"stream,omitempty"`
	PartialImages     *int   `json:"partial_images,omitempty"`
}

func marshalImageGeneration(request OpenAIImagesRequest) ([]byte, error) {
	return json.Marshal(imageGenerationPayload{
		Model: request.Model, Prompt: request.Prompt, N: request.N, Size: request.Size,
		AspectRatio: request.AspectRatio, Quality: request.Quality, ResponseFormat: "url",
		OutputFormat: request.OutputFormat, OutputCompression: request.OutputCompression,
		Moderation: request.Moderation, Style: request.Style, Background: request.Background,
		User: request.User, Stream: request.Stream, PartialImages: request.PartialImages,
	})
}

func marshalImageEdit(ctx context.Context, request OpenAIImagesRequest, loader ImageAssetLoader) ([]byte, string, error) {
	images, err := loadImageAssets(ctx, request.ImageURLs, false, loader)
	if err != nil {
		return nil, "", err
	}
	masks, err := loadImageAssets(ctx, request.MaskURLs, true, loader)
	if err != nil {
		return nil, "", err
	}
	boundary := imageMultipartBoundary(request, images, masks)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, "", err
	}
	fields := [][2]string{{"model", request.Model}, {"prompt", request.Prompt}, {"n", strconv.Itoa(request.N)}, {"size", request.Size}, {"aspect_ratio", request.AspectRatio}, {"quality", request.Quality}, {"response_format", "url"}, {"output_format", request.OutputFormat}, {"moderation", request.Moderation}, {"style", request.Style}, {"background", request.Background}, {"input_fidelity", request.InputFidelity}, {"user", request.User}}
	if request.OutputCompression != nil {
		fields = append(fields, [2]string{"output_compression", strconv.Itoa(*request.OutputCompression)})
	}
	if request.Stream {
		fields = append(fields, [2]string{"stream", "true"})
	}
	if request.PartialImages != nil {
		fields = append(fields, [2]string{"partial_images", strconv.Itoa(*request.PartialImages)})
	}
	for _, field := range fields {
		if field[1] != "" {
			if err := writer.WriteField(field[0], field[1]); err != nil {
				return nil, "", err
			}
		}
	}
	for index, asset := range images {
		if err := writeImagePart(writer, "image", fmt.Sprintf("image-%d%s", index+1, imageExtension(asset.ContentType)), asset); err != nil {
			return nil, "", err
		}
	}
	for index, asset := range masks {
		if err := writeImagePart(writer, "mask", fmt.Sprintf("mask-%d.png", index+1), asset); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	if body.Len() > maxImageRequestBytes {
		return nil, "", ErrInvalidImageRequest
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func loadImageAssets(ctx context.Context, locations []string, mask bool, loader ImageAssetLoader) ([]ImageAsset, error) {
	assets := make([]ImageAsset, 0, len(locations))
	var total int
	for _, location := range locations {
		asset, err := loader.LoadImage(ctx, location)
		if err != nil || len(asset.Data) == 0 || len(asset.Data) > maxImageOutputBytes {
			return nil, ErrInvalidImageRequest
		}
		asset.ContentType = strings.TrimSpace(strings.Split(asset.ContentType, ";")[0])
		if detected := http.DetectContentType(asset.Data); detected != asset.ContentType || !supportedImageContentType(detected) || mask && detected != "image/png" {
			return nil, ErrInvalidImageRequest
		}
		total += len(asset.Data)
		if total > maxImageRequestBytes {
			return nil, ErrInvalidImageRequest
		}
		asset.Data = bytes.Clone(asset.Data)
		assets = append(assets, asset)
	}
	return assets, nil
}

func imageMultipartBoundary(request OpenAIImagesRequest, groups ...[]ImageAsset) string {
	hash := sha256.New()
	encoded, _ := json.Marshal(request)
	_, _ = hash.Write(encoded)
	for _, assets := range groups {
		for _, asset := range assets {
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write([]byte(asset.ContentType))
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write(asset.Data)
		}
	}
	return "prism-" + hex.EncodeToString(hash.Sum(nil))
}

func writeImagePart(writer *multipart.Writer, field, filename string, asset ImageAsset) error {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, filename))
	header.Set("Content-Type", asset.ContentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = part.Write(asset.Data)
	return err
}

func imageExtension(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func supportedImageContentType(contentType string) bool {
	return contentType == "image/png" || contentType == "image/jpeg" || contentType == "image/webp"
}

func (OpenAIImages) Decode(body []byte, streaming bool, outputFormat string) (OpenAIImagesObservation, error) {
	if len(body) == 0 || len(body) > maxImageRequestBytes {
		return OpenAIImagesObservation{}, ErrInvalidImageResponse
	}
	if streaming {
		if result, err := decodeImageSSE(body, outputFormat); err == nil {
			return result, nil
		}
		if bytes.HasPrefix(bytes.TrimSpace(body), []byte{'{'}) {
			return decodeImageJSON(body, outputFormat)
		}
		return OpenAIImagesObservation{}, ErrInvalidImageResponse
	}
	return decodeImageJSON(body, outputFormat)
}

type rawImagesResponse struct {
	Created int64 `json:"created"`
	Data    []struct {
		URL           string `json:"url"`
		B64JSON       string `json:"b64_json"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	Usage json.RawMessage `json:"usage"`
}

func decodeImageJSON(body []byte, outputFormat string) (OpenAIImagesObservation, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var response rawImagesResponse
	if err := decoder.Decode(&response); err != nil || requireJSONEOF(decoder) != nil {
		return OpenAIImagesObservation{}, ErrInvalidImageResponse
	}
	return normalizeImageResponse(response, outputFormat)
}

func decodeImageSSE(body []byte, outputFormat string) (OpenAIImagesObservation, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64<<10), maxImageRequestBytes)
	var terminal *rawImagesResponse
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var envelope struct {
			Type          string          `json:"type"`
			Object        string          `json:"object"`
			B64JSON       string          `json:"b64_json"`
			URL           string          `json:"url"`
			RevisedPrompt string          `json:"revised_prompt"`
			CreatedAt     int64           `json:"created_at"`
			Data          json.RawMessage `json:"data"`
			Usage         json.RawMessage `json:"usage"`
			Error         json.RawMessage `json:"error"`
		}
		if json.Unmarshal([]byte(data), &envelope) != nil {
			return OpenAIImagesObservation{}, ErrInvalidImageResponse
		}
		if envelope.Type == "error" || envelope.Type == "api_error" || len(envelope.Error) != 0 {
			return OpenAIImagesObservation{}, ErrInvalidImageResponse
		}
		if strings.HasSuffix(envelope.Type, ".completed") {
			value := rawImagesResponse{Created: envelope.CreatedAt, Usage: envelope.Usage}
			value.Data = append(value.Data, struct {
				URL           string `json:"url"`
				B64JSON       string `json:"b64_json"`
				RevisedPrompt string `json:"revised_prompt"`
			}{URL: envelope.URL, B64JSON: envelope.B64JSON, RevisedPrompt: envelope.RevisedPrompt})
			terminal = &value
			continue
		}
		if strings.HasSuffix(envelope.Object, ".result") || envelope.Type == "image_generation.result" || envelope.Type == "image_edit.result" {
			var value rawImagesResponse
			if json.Unmarshal([]byte(data), &value) != nil {
				return OpenAIImagesObservation{}, ErrInvalidImageResponse
			}
			terminal = &value
		}
	}
	if scanner.Err() != nil || terminal == nil {
		return OpenAIImagesObservation{}, ErrInvalidImageResponse
	}
	return normalizeImageResponse(*terminal, outputFormat)
}

func normalizeImageResponse(response rawImagesResponse, outputFormat string) (OpenAIImagesObservation, error) {
	if len(response.Data) == 0 || len(response.Data) > 10 {
		return OpenAIImagesObservation{}, ErrInvalidImageResponse
	}
	result := OpenAIImagesObservation{Created: response.Created}
	usage, err := decodeImageUsage(response.Usage)
	if err != nil {
		return OpenAIImagesObservation{}, err
	}
	result.Usage = usage
	for _, item := range response.Data {
		if (item.URL == "") == (item.B64JSON == "") {
			return OpenAIImagesObservation{}, ErrInvalidImageResponse
		}
		output := OpenAIImageOutput{URL: item.URL, RevisedPrompt: item.RevisedPrompt}
		if item.URL != "" {
			if !validRemoteImageURL(item.URL) {
				return OpenAIImagesObservation{}, ErrInvalidImageResponse
			}
		} else {
			data, err := base64.StdEncoding.DecodeString(item.B64JSON)
			if err != nil || len(data) == 0 || len(data) > maxImageOutputBytes {
				return OpenAIImagesObservation{}, ErrInvalidImageResponse
			}
			contentType := http.DetectContentType(data)
			if !supportedImageContentType(contentType) || outputFormat != "" && contentType != outputFormatContentType(outputFormat) {
				return OpenAIImagesObservation{}, ErrInvalidImageResponse
			}
			output.Data, output.ContentType = data, contentType
		}
		result.Outputs = append(result.Outputs, output)
	}
	return result, nil
}

func outputFormatContentType(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "png":
		return "image/png"
	default:
		return ""
	}
}

func decodeImageUsage(raw json.RawMessage) (OpenAIImagesUsage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return OpenAIImagesUsage{}, nil
	}
	var payload struct {
		InputTokens  *json.Number `json:"input_tokens"`
		OutputTokens *json.Number `json:"output_tokens"`
		TotalTokens  *json.Number `json:"total_tokens"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&payload) != nil || requireJSONEOF(decoder) != nil {
		return OpenAIImagesUsage{}, ErrInvalidImageResponse
	}
	result := OpenAIImagesUsage{}
	for source, target := range map[*json.Number]**int64{payload.InputTokens: &result.InputTokens, payload.OutputTokens: &result.OutputTokens, payload.TotalTokens: &result.TotalTokens} {
		if source == nil {
			continue
		}
		value, err := strconv.ParseInt(source.String(), 10, 64)
		if err != nil || value < 0 {
			return OpenAIImagesUsage{}, ErrInvalidImageResponse
		}
		copy := value
		*target = &copy
	}
	return result, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return ErrInvalidImageRequest
		}
		return err
	}
	return nil
}
