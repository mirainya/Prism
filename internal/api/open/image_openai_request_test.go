package open

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/routing"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/model"
)

type recordingImageRouteSelector struct {
	requirements routing.RouteRequirements
	options      routing.RouteOptions
}

func (selector *recordingImageRouteSelector) SelectTransport(_ context.Context, _ string, requirements routing.RouteRequirements, options routing.RouteOptions) (*routing.RouteResult, error) {
	selector.requirements = requirements
	selector.options = options
	return &routing.RouteResult{}, nil
}

func TestImageRouteSelectionUsesOperationInsteadOfOptionalCapabilityTag(t *testing.T) {
	for _, test := range []struct {
		operation string
		path      string
		async     bool
		taskScope string
	}{
		{operation: adapter.ImagesGenerate, path: "/v1/images/generations"},
		{operation: adapter.ImagesGenerate, path: "/v1/images/generations", async: true, taskScope: "task"},
		{operation: adapter.ImagesEdit, path: "/v1/images/edits"},
		{operation: adapter.ImagesEdit, path: "/v1/images/edits", async: true, taskScope: "task"},
	} {
		t.Run(test.operation+"/async="+strconv.FormatBool(test.async), func(t *testing.T) {
			selector := &recordingImageRouteSelector{}
			if _, err := selectUnifiedOpenAIImageRoute(context.Background(), selector, "image-model", test.operation, test.async); err != nil {
				t.Fatal(err)
			}
			if len(selector.requirements) != 0 {
				t.Fatalf("requirements = %#v, want operation-specific routing without generic tags", selector.requirements)
			}
			if selector.options.OperationMethod != http.MethodPost || selector.options.OperationPath != test.path {
				t.Fatalf("operation = %s %s", selector.options.OperationMethod, selector.options.OperationPath)
			}
			if len(selector.options.AllowedTransports) != 1 || selector.options.AllowedTransports[0] != model.UpstreamTransportOpenAIImages {
				t.Fatalf("allowed transports = %#v", selector.options.AllowedTransports)
			}
			if selector.options.RequiredTaskScope != test.taskScope {
				t.Fatalf("required task scope = %q, want %q", selector.options.RequiredTaskScope, test.taskScope)
			}
		})
	}
}

func TestCreateImageGenerationRejectsEditInputs(t *testing.T) {
	recorder := performOpenAIImageRequest(t, CreateImageGenerationOpenAI, "application/json", `{
		"model":"image-model",
		"prompt":"edit this image",
		"image_urls":["https://media.example/input.png"]
	}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "/v1/images/edits") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestWriteOpenAIImageExecutionErrorUsesProviderMessage(t *testing.T) {
	context, recorder := newOpenAIImageTestContext("application/json", `{}`)
	writeOpenAIImageExecutionError(context, &gatewayruntime.ProviderCapabilityError{
		HTTPStatus: http.StatusBadRequest,
		Code:       "provider_http_error",
		Message:    "invalid image size",
	})

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "invalid image size") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestWriteOpenAIImageExecutionErrorFallsBackWithoutProviderMessage(t *testing.T) {
	context, recorder := newOpenAIImageTestContext("application/json", `{}`)
	writeOpenAIImageExecutionError(context, &gatewayruntime.ProviderCapabilityError{
		HTTPStatus: http.StatusBadGateway,
		Code:       "provider_request_failed",
	})

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "upstream image generation failed") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestCreateImageEditJSONRequiresImageURLs(t *testing.T) {
	recorder := performOpenAIImageRequest(t, CreateImageEditOpenAI, "application/json; charset=utf-8", `{
		"model":"image-model",
		"prompt":"edit this image"
	}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "image_urls is required") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestCreateImageEditJSONAcceptsVendorJSONContentType(t *testing.T) {
	recorder := performOpenAIImageRequest(t, CreateImageEditOpenAI, "application/vnd.prism+json", `{}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "model and prompt are required") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestBindOpenAIImageJSONDefaultsNWhenOmitted(t *testing.T) {
	context, recorder := newOpenAIImageTestContext("application/json", `{
		"model":"image-model",
		"prompt":"draw an image"
	}`)

	request, _, ok := bindOpenAIImageJSON(context, "generation")
	if !ok {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	if request.N != 1 {
		t.Fatalf("n = %d, want 1", request.N)
	}
}

func TestCreateImageGenerationRejectsExplicitZeroN(t *testing.T) {
	recorder := performOpenAIImageRequest(t, CreateImageGenerationOpenAI, "application/json", `{
		"model":"image-model",
		"prompt":"draw an image",
		"n":0
	}`)

	assertOpenAIImageBadRequest(t, recorder, "invalid image generation options")
}

func TestCreateImageEditJSONRejectsMoreThanSixteenImagesBeforePersistence(t *testing.T) {
	imageURLs := make([]string, openAIImageEditMaxInputs+1)
	for index := range imageURLs {
		imageURLs[index] = "not-read-before-count-validation"
	}
	body, err := json.Marshal(OpenAIImageRequest{
		Model: "image-model", Prompt: "edit these images", ImageURLs: imageURLs, N: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	recorder := performOpenAIImageRequest(t, CreateImageEditOpenAI, "application/json", string(body))
	assertOpenAIImageBadRequest(t, recorder, "invalid image edit options")
}

func TestCreateImageEditJSONRejectsSizeAndAspectRatio(t *testing.T) {
	image := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testOpenAIImagePNG())
	body, err := json.Marshal(map[string]any{
		"model": "image-model", "prompt": "edit this image", "image_urls": []string{image},
		"size": "1024x1024", "aspect_ratio": "1:1",
	})
	if err != nil {
		t.Fatal(err)
	}

	recorder := performOpenAIImageRequest(t, CreateImageEditOpenAI, "application/json", string(body))
	assertOpenAIImageBadRequest(t, recorder, "invalid image edit options")
}

func TestCreateImageEditMultipartRejectsMoreThanSixteenImagesBeforePersistence(t *testing.T) {
	files := make([]openAIImageMultipartFile, openAIImageEditMaxInputs+1)
	for index := range files {
		files[index] = openAIImageMultipartFile{Field: "image", Name: "input.png", Data: testOpenAIImagePNG()}
	}

	recorder := performOpenAIImageMultipartRequest(t, files, nil)
	assertOpenAIImageBadRequest(t, recorder, "image must contain at most 16 files")
}

func TestCreateImageEditMultipartRejectsMoreThanOneMaskBeforePersistence(t *testing.T) {
	files := []openAIImageMultipartFile{
		{Field: "image", Name: "input.png", Data: testOpenAIImagePNG()},
		{Field: "mask", Name: "first-mask.png", Data: testOpenAIImagePNG()},
		{Field: "mask", Name: "second-mask.png", Data: testOpenAIImagePNG()},
	}

	recorder := performOpenAIImageMultipartRequest(t, files, nil)
	assertOpenAIImageBadRequest(t, recorder, "mask must contain at most 1 file")
}

func TestCreateImageEditMultipartRejectsSizeAndAspectRatioBeforePersistence(t *testing.T) {
	files := []openAIImageMultipartFile{{Field: "image", Name: "input.png", Data: testOpenAIImagePNG()}}
	recorder := performOpenAIImageMultipartRequest(t, files, map[string]string{
		"size": "1024x1024", "aspect_ratio": "1:1",
	})

	assertOpenAIImageBadRequest(t, recorder, "invalid image edit options")
}

func TestPrepareOpenAIImageEditFilesRejectsLaterInvalidFileWithoutPreparedInputs(t *testing.T) {
	body, contentType := encodeOpenAIImageMultipart(t, []openAIImageMultipartFile{
		{Field: "image", Name: "valid.png", Data: testOpenAIImagePNG()},
		{Field: "image", Name: "invalid.txt", Data: []byte("not an image")},
	}, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	if err := request.ParseMultipartForm(openAIImageEditMaxMemory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = request.MultipartForm.RemoveAll() })

	prepared, err := prepareOpenAIImageEditFiles(request.MultipartForm, "image", openAIImageEditMaxInputs)
	if err == nil {
		t.Fatal("expected invalid later file to fail validation")
	}
	if prepared != nil {
		t.Fatalf("prepared inputs = %#v, want nil", prepared)
	}
}

type openAIImageMultipartFile struct {
	Field string
	Name  string
	Data  []byte
}

func performOpenAIImageMultipartRequest(t *testing.T, files []openAIImageMultipartFile, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := encodeOpenAIImageMultipart(t, files, fields)
	return performOpenAIImageRequest(t, CreateImageEditOpenAI, contentType, string(body))
}

func encodeOpenAIImageMultipart(t *testing.T, files []openAIImageMultipartFile, fields map[string]string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"model": "image-model", "prompt": "edit this image"} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		part, err := writer.CreateFormFile(file.Field, file.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(file.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func testOpenAIImagePNG() []byte {
	data, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	return data
}

func assertOpenAIImageBadRequest(t *testing.T, recorder *httptest.ResponseRecorder, message string) {
	t.Helper()
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), message) {
		t.Fatalf("body = %s, want message containing %q", recorder.Body.String(), message)
	}
}

func newOpenAIImageTestContext(contentType, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/images", strings.NewReader(body))
	context.Request.Header.Set("Content-Type", contentType)
	return context, recorder
}

func performOpenAIImageRequest(t *testing.T, handler gin.HandlerFunc, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	context, recorder := newOpenAIImageTestContext(contentType, body)
	context.Set(middleware.ContextKeyToken, &model.Token{BaseModel: model.BaseModel{ID: 7}, UserID: 11})
	handler(context)
	return recorder
}
