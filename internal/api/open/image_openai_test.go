package open

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
)

type fakeOpenAIImageService struct {
	result        *service.ImageResult
	err           error
	invokeCount   int
	invokeReq     *service.InvokeRequest
	cancelErr     error
	cancelCount   int
	cancelTaskNo  string
	cancelUserID  uint
	cancelTokenID uint
	events        [][]byte
}

func (f *fakeOpenAIImageService) InvokeAndWait(
	_ context.Context,
	req *service.InvokeRequest,
	_ int,
) (*service.ImageResult, error) {
	f.invokeCount++
	f.invokeReq = req
	if req.EventSink != nil {
		for _, event := range f.events {
			req.EventSink <- event
		}
	}
	return f.result, f.err
}

func TestCreateImageGenerationOpenAIStreamReportsFailureAfterUpstreamCompleted(t *testing.T) {
	fake := &fakeOpenAIImageService{
		err:    errors.New("persist completed image: storage failed"),
		events: [][]byte{[]byte(`{"type":"image_generation.completed","b64_json":"aW1hZ2U="}`)},
	}
	recorder := serveOpenAIImageRequest(t, fake, nil, `{
		"model":"gpt-image-prism","prompt":"draw","stream":true
	}`)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, "storage failed") || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("status = %d, body = %s", recorder.Code, body)
	}
	if strings.Contains(body, "image_generation.completed") {
		t.Fatalf("unprocessed upstream completed event was forwarded: %s", body)
	}
}

func (f *fakeOpenAIImageService) CancelTaskForToken(
	_ context.Context,
	taskNo string,
	userID uint,
	tokenID uint,
) error {
	f.cancelCount++
	f.cancelTaskNo = taskNo
	f.cancelUserID = userID
	f.cancelTokenID = tokenID
	return f.cancelErr
}

func TestCreateImageGenerationOpenAIReturnsURLResponse(t *testing.T) {
	fake := &fakeOpenAIImageService{result: &service.ImageResult{
		Done: true, Success: true, URLs: []string{"https://images.example/one.png"}, RevisedPrompt: "revised",
	}}
	recorder := serveOpenAIImageRequest(t, fake, nil, `{
		"model":"gpt-image-prism","prompt":"draw","n":1,"size":"1024x1024","response_format":"url"
	}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response OpenAIImageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].URL != "https://images.example/one.png" ||
		response.Data[0].B64JSON != "" || response.Data[0].RevisedPrompt != "revised" {
		t.Fatalf("response = %#v", response)
	}
	if fake.invokeReq == nil || fake.invokeReq.Capability != "text2img" ||
		fake.invokeReq.Model != "gpt-image-prism" || fake.invokeReq.Operation != "images.generate" ||
		fake.invokeReq.RouteOperation != service.RouteOperationImagesGenerate ||
		fake.invokeReq.UserID != 41 || fake.invokeReq.TokenID != 52 {
		t.Fatalf("invoke request = %#v", fake.invokeReq)
	}
}

func TestCreateImageGenerationOpenAIStreamPassesStreamingOptionsUpstream(t *testing.T) {
	fake := &fakeOpenAIImageService{result: &service.ImageResult{
		Done: true, Success: true, URLs: []string{"https://images.example/one.png"},
	}}
	recorder := serveOpenAIImageRequest(t, fake, nil, `{
		"model":"gpt-image-prism","prompt":"draw","stream":true,"partial_images":3
	}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if fake.invokeReq == nil || fake.invokeReq.Params["stream"] != true || fake.invokeReq.Params["partial_images"] != 3 {
		t.Fatalf("upstream streaming params = %#v", fake.invokeReq.Params)
	}
}

func TestCreateImageGenerationOpenAIForwardsReferenceURLsAsEdit(t *testing.T) {
	fake := &fakeOpenAIImageService{result: &service.ImageResult{
		Done: true, Success: true, URLs: []string{"https://images.example/edited.png"},
	}}
	recorder := serveOpenAIImageRequest(t, fake, nil, `{
		"model":"gpt-image-prism",
		"prompt":"change only the subject color",
		"image_urls":["https://storage.example/reference.png"],
		"aspect_ratio":"16:9"
	}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if fake.invokeReq == nil || fake.invokeReq.Operation != "images.edit" ||
		fake.invokeReq.RouteOperation != service.RouteOperationImagesEdit {
		t.Fatalf("invoke request = %#v", fake.invokeReq)
	}
	images, ok := fake.invokeReq.Params["image_urls"].([]any)
	if !ok || len(images) != 1 || images[0] != "https://storage.example/reference.png" {
		t.Fatalf("image_urls = %#v", fake.invokeReq.Params["image_urls"])
	}
	if fake.invokeReq.Params["aspect_ratio"] != "16:9" {
		t.Fatalf("aspect_ratio = %#v", fake.invokeReq.Params["aspect_ratio"])
	}
}

func TestCreateImageGenerationOpenAIStoresBase64BeforeInvoke(t *testing.T) {
	fake := &fakeOpenAIImageService{result: &service.ImageResult{
		Done: true, Success: true, URLs: []string{"https://images.example/edited.png"},
	}}
	pngData, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	previousStorer := openAIImageFileStorer
	var storedData []byte
	openAIImageFileStorer = func(_ context.Context, data []byte, contentType, capabilityCode string) (string, error) {
		storedData = append([]byte(nil), data...)
		if contentType != "image/png" || capabilityCode != "text2img" {
			t.Fatalf("storage metadata = %q %q", contentType, capabilityCode)
		}
		return "https://storage.example/reference.png", nil
	}
	t.Cleanup(func() { openAIImageFileStorer = previousStorer })
	body, err := json.Marshal(map[string]any{
		"model":      "gpt-image-prism",
		"prompt":     "change only the subject color",
		"image_urls": []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData)},
	})
	if err != nil {
		t.Fatal(err)
	}

	recorder := serveOpenAIImageRequest(t, fake, nil, string(body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Equal(storedData, pngData) {
		t.Fatal("stored image mismatch")
	}
	images, ok := fake.invokeReq.Params["image_urls"].([]any)
	if !ok || len(images) != 1 || images[0] != "https://storage.example/reference.png" {
		t.Fatalf("image_urls = %#v", fake.invokeReq.Params["image_urls"])
	}
}

func TestCreateImageGenerationOpenAIReturnsBase64Response(t *testing.T) {
	fake := &fakeOpenAIImageService{result: &service.ImageResult{
		Done: true, Success: true, URLs: []string{"https://images.example/one.png"},
	}}
	encoder := func(_ context.Context, imageURL string) (string, error) {
		if imageURL != "https://images.example/one.png" {
			t.Fatalf("image URL = %q", imageURL)
		}
		return "encoded-image", nil
	}
	recorder := serveOpenAIImageRequest(t, fake, encoder, `{
		"model":"gpt-image-prism","prompt":"draw","response_format":"b64_json"
	}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response OpenAIImageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].B64JSON != "encoded-image" || response.Data[0].URL != "" {
		t.Fatalf("response = %#v", response)
	}
}

func TestCreateImageGenerationOpenAIForwardsUpstreamFailureStatus(t *testing.T) {
	message := `API Error: openai returned 451: {"error":{"message":"unsafe image"}}`
	fake := &fakeOpenAIImageService{result: &service.ImageResult{
		Done: true, Success: false, Status: "failed", Error: message, HTTPStatus: 451,
	}}
	recorder := serveOpenAIImageRequest(t, fake, nil, `{
		"model":"gpt-image-prism","prompt":"draw"
	}`)

	if recorder.Code != http.StatusUnavailableForLegalReasons {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Message != message || response.Error.Type != "api_error" {
		t.Fatalf("response = %#v", response)
	}
}

func TestCreateImageGenerationOpenAIForwardsUpstreamBadRequestStatus(t *testing.T) {
	message := `API Error: openai returned 400: {"error":{"message":"The generated images appear to be unsafe. Try modifying the prompts or the seeds.","type":"invalid_request_error","param":"","code":"ERR-5CCF05E363"}}`
	fake := &fakeOpenAIImageService{result: &service.ImageResult{
		Done: true, Success: false, Status: "failed", Error: message, HTTPStatus: 400,
	}}
	recorder := serveOpenAIImageRequest(t, fake, nil, `{
		"model":"gpt-image-prism","prompt":"draw"
	}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "ERR-5CCF05E363") ||
		!strings.Contains(recorder.Body.String(), "invalid_request_error") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestCreateImageGenerationOpenAIPassesThroughUpstreamErrorBody(t *testing.T) {
	errorBody := json.RawMessage(`{"error":{"message":"unsafe image","type":"invalid_request_error","code":"ERR-TEST"}}`)
	fake := &fakeOpenAIImageService{result: &service.ImageResult{
		Done: true, Success: false, Status: "failed", Error: "wrapped error", HTTPStatus: 400, ErrorBody: errorBody,
	}}
	recorder := serveOpenAIImageRequest(t, fake, nil, `{
		"model":"gpt-image-prism","prompt":"draw"
	}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var actual any
	var expected any
	if err := json.Unmarshal(recorder.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(errorBody, &expected); err != nil {
		t.Fatal(err)
	}
	actualJSON, _ := json.Marshal(actual)
	expectedJSON, _ := json.Marshal(expected)
	if string(actualJSON) != string(expectedJSON) {
		t.Fatalf("body = %s, want %s", actualJSON, expectedJSON)
	}
}

func TestCreateImageGenerationOpenAICancelsTimedOutTask(t *testing.T) {
	fake := &fakeOpenAIImageService{result: &service.ImageResult{
		Done: false, TaskNo: "task-timeout", Status: "processing",
	}}
	recorder := serveOpenAIImageRequest(t, fake, nil, `{
		"model":"gpt-image-prism","prompt":"draw"
	}`)

	if recorder.Code != http.StatusGatewayTimeout || !strings.Contains(recorder.Body.String(), "image generation timed out") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if fake.cancelCount != 1 || fake.cancelTaskNo != "task-timeout" ||
		fake.cancelUserID != 41 || fake.cancelTokenID != 52 {
		t.Fatalf("cancel = count %d task %q user %d token %d",
			fake.cancelCount, fake.cancelTaskNo, fake.cancelUserID, fake.cancelTokenID)
	}
}

func TestCreateImageGenerationOpenAIRejectsInvalidResponseFormat(t *testing.T) {
	fake := &fakeOpenAIImageService{}
	recorder := serveOpenAIImageRequest(t, fake, nil, `{
		"model":"gpt-image-prism","prompt":"draw","response_format":"binary"
	}`)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_request_error") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if fake.invokeCount != 0 {
		t.Fatalf("invoke count = %d", fake.invokeCount)
	}
}

func TestCreateImageGenerationOpenAIReturnsErrorWhenTimeoutCancellationFails(t *testing.T) {
	fake := &fakeOpenAIImageService{
		result:    &service.ImageResult{Done: false, TaskNo: "task-timeout"},
		cancelErr: errors.New("cancel failed"),
	}
	recorder := serveOpenAIImageRequest(t, fake, nil, `{
		"model":"gpt-image-prism","prompt":"draw"
	}`)

	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "cancellation failed") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateImageEditOpenAIInvokesExistingImageCapability(t *testing.T) {
	fake := &fakeOpenAIImageService{result: &service.ImageResult{
		Done: true, Success: true, URLs: []string{"https://images.example/edited.png"},
	}}
	pngData, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	previousStorer := openAIImageFileStorer
	var storedData []byte
	openAIImageFileStorer = func(_ context.Context, data []byte, contentType, capabilityCode string) (string, error) {
		storedData = append([]byte(nil), data...)
		if contentType != "image/png" || capabilityCode != "text2img" {
			t.Fatalf("storage metadata = %q %q", contentType, capabilityCode)
		}
		return "https://storage.example/reference.png", nil
	}
	t.Cleanup(func() { openAIImageFileStorer = previousStorer })
	recorder := serveOpenAIImageEditRequest(t, fake, map[string]string{
		"model": "gpt-image-prism", "prompt": "make it blue", "size": "1024x1024",
		"aspect_ratio": "16:9", "response_format": "url",
	}, "reference.png", pngData)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if fake.invokeReq == nil || fake.invokeReq.Capability != "text2img" ||
		fake.invokeReq.Model != "gpt-image-prism" || fake.invokeReq.Operation != "images.edit" ||
		fake.invokeReq.RouteOperation != service.RouteOperationImagesEdit {
		t.Fatalf("invoke request = %#v", fake.invokeReq)
	}
	if fake.invokeReq.Params["prompt"] != "make it blue" || fake.invokeReq.Params["size"] != "1024x1024" ||
		fake.invokeReq.Params["aspect_ratio"] != "16:9" {
		t.Fatalf("params = %#v", fake.invokeReq.Params)
	}
	images, ok := fake.invokeReq.Params["image_urls"].([]any)
	if !ok || len(images) != 1 {
		t.Fatalf("image_urls param = %#v", fake.invokeReq.Params["image_urls"])
	}
	storedURL, ok := images[0].(string)
	if !ok || storedURL != "https://storage.example/reference.png" {
		t.Fatalf("image item = %#v", images[0])
	}
	if !bytes.Equal(storedData, pngData) {
		t.Fatal("stored image mismatch")
	}
}

func TestCreateImageEditOpenAIRequiresImage(t *testing.T) {
	fake := &fakeOpenAIImageService{}
	recorder := serveOpenAIImageEditRequest(t, fake, map[string]string{
		"model": "gpt-image-prism", "prompt": "make it blue",
	}, "", nil)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "image is required") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if fake.invokeCount != 0 {
		t.Fatalf("invoke count = %d", fake.invokeCount)
	}
}

func serveOpenAIImageRequest(
	t *testing.T,
	fake openAIImageCapabilityService,
	encoder imageBase64Encoder,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	previousService := openAIImageService
	previousEncoder := openAIImageBase64Encoder
	openAIImageService = fake
	if encoder != nil {
		openAIImageBase64Encoder = encoder
	}
	t.Cleanup(func() {
		openAIImageService = previousService
		openAIImageBase64Encoder = previousEncoder
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyToken, &model.Token{
			BaseModel: model.BaseModel{ID: 52},
			UserID:    41,
		})
		c.Next()
	})
	router.POST("/v1/images/generations", CreateImageGenerationOpenAI)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func serveOpenAIImageEditRequest(
	t *testing.T,
	fake openAIImageCapabilityService,
	fields map[string]string,
	filename string,
	image []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	previousService := openAIImageService
	openAIImageService = fake
	t.Cleanup(func() { openAIImageService = previousService })

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if filename != "" {
		part, err := writer.CreateFormFile("image", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(image); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyToken, &model.Token{
			BaseModel: model.BaseModel{ID: 52},
			UserID:    41,
		})
		c.Next()
	})
	router.POST("/v1/images/edits", CreateImageEditOpenAI)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(recorder, request)
	return recorder
}
