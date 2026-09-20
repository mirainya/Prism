package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/runtime"
)

func asyncImageTestConfig() []byte {
	return []byte(`{
  "async_image": {
    "profile": "json_image_task_v1",
    "submit": {"method": "POST", "path": "/v1/images/generations?async=true"},
    "poll": {"method": "GET", "path": "/v1/tasks/{task_id}"},
    "request": {
      "fields": {
        "model": "model",
        "prompt": "prompt",
        "image_urls": "image",
        "aspect_ratio": "size",
        "response_format": "response_format"
      },
      "fixed_body": {"async": true}
    },
    "response": {
      "task_id_paths": ["id"],
      "status_paths": ["state"],
      "image_url_paths": ["data.images.#.url"],
      "input_token_paths": ["usage.input_tokens"],
      "output_token_paths": ["usage.output_tokens"],
      "status_map": {
        "pending": "accepted",
        "running": "running",
        "succeeded": "succeeded",
        "error": "failed"
      }
    }
  }
}`)
}

func sub2APIAsyncImageGenerationConfig() []byte {
	return []byte(`{
  "async_image": {
    "profile": "json_image_task_v1",
    "submit": {"method": "POST", "path": "/v1/images/generations/async"},
    "poll": {"method": "GET", "path": "/v1/images/tasks/{task_id}"},
    "request": {
      "fields": {
        "model": "model",
        "prompt": "prompt",
        "n": "n",
        "size_or_aspect_ratio": "size",
        "quality": "quality",
        "response_format": "response_format",
        "output_format": "output_format",
        "output_compression": "output_compression",
        "moderation": "moderation",
        "style": "style",
        "background": "background"
      }
    },
    "response": {
      "task_id_paths": ["task_id", "id"],
      "status_paths": ["status"],
      "image_url_paths": ["image_url", "result.data.#.url"],
      "error_code_paths": ["error.code", "error.type"],
      "error_message_paths": ["error.message"],
      "http_status_paths": ["http_status"],
      "require_poll_status": true,
      "status_map": {
        "processing": "running",
        "completed": "succeeded",
        "failed": "failed"
      }
    }
  }
}`)
}

func sub2APIAsyncImageEditConfig() []byte {
	return []byte(`{
  "async_image": {
    "profile": "json_image_task_v1",
    "submit": {"method": "POST", "path": "/v1/images/edits/async"},
    "poll": {"method": "GET", "path": "/v1/images/tasks/{task_id}"},
    "request": {"input_mode": "multipart"},
    "response": {
      "task_id_paths": ["task_id", "id"],
      "status_paths": ["status"],
      "image_url_paths": ["image_url", "result.data.#.url"],
      "error_code_paths": ["error.code", "error.type"],
      "error_message_paths": ["error.message"],
      "http_status_paths": ["http_status"],
      "require_poll_status": true,
      "status_map": {
        "processing": "running",
        "completed": "succeeded",
        "failed": "failed"
      }
    }
  }
}`)
}

func TestAsyncOpenAIImagesPreparesDeclaredSubmitAndPoll(t *testing.T) {
	fixed := repository.AsyncDispatch{
		Method: "POST", Path: "/v1/images/generations?async=true",
		VendorModel: "gpt-image-2", AdapterConfig: asyncImageTestConfig(),
	}
	payload, err := json.Marshal(OpenAIImagesRequest{
		Model: "gpt-image-2-duomi", Prompt: "draw", ImageURLs: []string{"https://assets.example/input.png"},
		N: 1, AspectRatio: "16:9", ResponseFormat: "b64_json",
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := (AsyncOpenAIImages{}).Prepare(context.Background(), "submit", fixed, payload, "")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Method != http.MethodPost || prepared.Path != fixed.Path || prepared.CredentialHeader != "Authorization" || prepared.CredentialPrefix != "Bearer " {
		t.Fatalf("unexpected prepared request: %+v", prepared)
	}
	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "gpt-image-2" || body["prompt"] != "draw" || body["size"] != "16:9" || body["response_format"] != "url" || body["async"] != true {
		t.Fatalf("unexpected submit body: %#v", body)
	}
	images, ok := body["image"].([]any)
	if !ok || len(images) != 1 || images[0] != "https://assets.example/input.png" {
		t.Fatalf("unexpected image mapping: %#v", body["image"])
	}

	poll, err := (AsyncOpenAIImages{}).Prepare(context.Background(), "query", fixed, payload, "task/1")
	if err != nil {
		t.Fatal(err)
	}
	if poll.Method != http.MethodGet || poll.Path != "/v1/tasks/task%2F1" || len(poll.Body) != 0 {
		t.Fatalf("unexpected poll request: %+v", poll)
	}
}

func TestAsyncOpenAIImagesPreparesSub2APIGeneration(t *testing.T) {
	config := sub2APIAsyncImageGenerationConfig()
	if err := ValidateCatalogProduct(
		"openai_images", 1, "https://sub2api.mirainya.com", http.MethodPost,
		"/v1/images/generations/async", "gpt-image-2", "task", config,
	); err != nil {
		t.Fatalf("Sub2API generation catalog declaration rejected: %v", err)
	}
	prepared, err := (AsyncOpenAIImages{}).Prepare(context.Background(), "submit", repository.AsyncDispatch{
		Method: http.MethodPost, Path: "/v1/images/generations/async",
		VendorModel: "gpt-image-2", AdapterConfig: config,
	}, []byte(`{
  "model":"public-image",
  "prompt":"draw a lighthouse",
  "aspect_ratio":"16:9",
  "n":1,
  "response_format":"b64_json"
}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	if prepared.Path != "/v1/images/generations/async" || body["model"] != "gpt-image-2" || body["size"] != "16:9" || body["response_format"] != "url" {
		t.Fatalf("unexpected Sub2API generation: request=%+v body=%#v", prepared, body)
	}
}

func TestAsyncOpenAIImagesPreparesSub2APIMultipartEdit(t *testing.T) {
	if err := ValidateCatalogProduct(
		"openai_images", 1, "https://sub2api.mirainya.com", http.MethodPost,
		"/v1/images/edits/async", "gpt-image-2", "task", sub2APIAsyncImageEditConfig(),
	); err != nil {
		t.Fatalf("Sub2API edit catalog declaration rejected: %v", err)
	}
	fixed := repository.AsyncDispatch{
		Method: http.MethodPost, Path: "/v1/images/edits/async",
		VendorModel: "gpt-image-2", AdapterConfig: sub2APIAsyncImageEditConfig(),
	}
	payload := []byte(`{
  "model":"public-image",
  "prompt":"replace the background",
  "image_urls":["https://assets.example/one.png","https://assets.example/two.png"],
	"mask_urls":["https://assets.example/mask.png"],
	"aspect_ratio":"16:9",
  "n":1,
  "response_format":"b64_json"
}`)
	if err := (AsyncOpenAIImages{}).ValidateSubmit(fixed, payload); err != nil {
		t.Fatalf("multipart submit validation failed: %v", err)
	}
	assets := map[string]runtime.AsyncAsset{
		"https://assets.example/one.png":  {Data: onePixelPNG, ContentType: "image/png"},
		"https://assets.example/two.png":  {Data: onePixelPNG, ContentType: "image/png"},
		"https://assets.example/mask.png": {Data: onePixelPNG, ContentType: "image/png"},
	}
	loader := runtime.AsyncAssetLoaderFunc(func(_ context.Context, location string) (runtime.AsyncAsset, error) {
		asset, ok := assets[location]
		if !ok {
			return runtime.AsyncAsset{}, errors.New("missing test asset")
		}
		return asset, nil
	})
	prepared, err := (AsyncOpenAIImages{}).PrepareWithAssets(context.Background(), "submit", fixed, payload, "", loader)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Method != http.MethodPost || prepared.Path != "/v1/images/edits/async" {
		t.Fatalf("unexpected prepared request: %+v", prepared)
	}
	request := httptest.NewRequest(http.MethodPost, "https://sub2api.mirainya.com/v1/images/edits/async", bytes.NewReader(prepared.Body))
	request.Header = prepared.Header
	if err := request.ParseMultipartForm(128 << 20); err != nil {
		t.Fatal(err)
	}
	defer request.MultipartForm.RemoveAll()
	if request.FormValue("model") != "gpt-image-2" || request.FormValue("prompt") != "replace the background" ||
		request.FormValue("aspect_ratio") != "16:9" || request.FormValue("response_format") != "url" {
		t.Fatalf("unexpected multipart fields: %#v", request.MultipartForm.Value)
	}
	if len(request.MultipartForm.File["image"]) != 2 || len(request.MultipartForm.File["mask"]) != 1 {
		t.Fatalf("unexpected multipart files: %#v", request.MultipartForm.File)
	}
	for _, field := range []string{"image", "mask"} {
		for _, header := range request.MultipartForm.File[field] {
			file, err := header.Open()
			if err != nil {
				t.Fatal(err)
			}
			data := make([]byte, header.Size)
			_, readErr := file.Read(data)
			_ = file.Close()
			if readErr != nil || !bytes.Equal(data, onePixelPNG) || header.Header.Get("Content-Type") != "image/png" {
				t.Fatalf("unexpected %s file: header=%#v read_error=%v", field, header.Header, readErr)
			}
		}
	}

	completed, err := (AsyncOpenAIImages{}).DecodeWithDispatch("query", fixed, nil, []byte(`{
  "id":"imgtask_1",
  "task_id":"imgtask_1",
  "status":"completed",
  "image_url":"https://storage.example/result.png",
  "result":{"data":[{"url":"https://storage.example/result.png"}]}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != execution.AsyncSucceeded || len(completed.Sources) != 1 || completed.Sources[0].URL != "https://storage.example/result.png" {
		t.Fatalf("unexpected Sub2API completion: %+v", completed)
	}
}

func TestAsyncOpenAIImagesPreservesTemporaryAssetFailure(t *testing.T) {
	fixed := repository.AsyncDispatch{
		Method: http.MethodPost, Path: "/v1/images/edits/async",
		VendorModel: "gpt-image-2", AdapterConfig: sub2APIAsyncImageEditConfig(),
	}
	loader := runtime.AsyncAssetLoaderFunc(func(context.Context, string) (runtime.AsyncAsset, error) {
		return runtime.AsyncAsset{}, errors.Join(runtime.ErrAsyncAssetUnavailable, errors.New("storage timeout"))
	})
	_, err := (AsyncOpenAIImages{}).PrepareWithAssets(context.Background(), "submit", fixed, []byte(`{
  "model":"public-image",
  "prompt":"replace the background",
  "image_urls":["https://assets.example/one.png"],
  "n":1,
  "response_format":"url"
}`), "", loader)
	if !errors.Is(err, runtime.ErrAsyncAssetUnavailable) {
		t.Fatalf("asset failure = %v, want temporary asset error", err)
	}
}

func TestAsyncOpenAIImagesDecodesSub2APIProviderFailure(t *testing.T) {
	observed, err := (AsyncOpenAIImages{}).DecodeWithDispatch("query", repository.AsyncDispatch{
		AdapterConfig: sub2APIAsyncImageGenerationConfig(),
	}, nil, []byte(`{
  "task_id":"imgtask_1",
  "status":"failed",
  "http_status":422,
  "error":{
    "type":"invalid_request_error",
    "code":"bad_image",
    "message":"unsupported image"
  },
  "usage":{"input_tokens":[]}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != execution.AsyncFailed || observed.ProviderErrorCode != "bad_image" || observed.ProviderErrorMessage != "unsupported image" ||
		observed.ProviderHTTPStatus == nil || *observed.ProviderHTTPStatus != http.StatusUnprocessableEntity {
		t.Fatalf("unexpected Sub2API failure: %+v", observed)
	}
}

func TestAsyncOpenAIImagesNormalizesSub2APIProtocolLessResultURL(t *testing.T) {
	observed, err := (AsyncOpenAIImages{}).DecodeWithDispatch("query", repository.AsyncDispatch{
		AdapterConfig: sub2APIAsyncImageGenerationConfig(),
	}, nil, []byte(`{
  "task_id":"imgtask_1",
  "status":"completed",
  "image_url":"imagebuckets.mirainya.com/images/result.png",
  "result":{"data":[{"url":"https://imagebuckets.mirainya.com/images/result.png"}]}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != execution.AsyncSucceeded || len(observed.Sources) != 1 ||
		observed.Sources[0].URL != "https://imagebuckets.mirainya.com/images/result.png" {
		t.Fatalf("unexpected normalized Sub2API result: %+v", observed)
	}
}

func TestAsyncOpenAIImagesRejectsProtocolLessRelativeResultURL(t *testing.T) {
	_, err := (AsyncOpenAIImages{}).DecodeWithDispatch("query", repository.AsyncDispatch{
		AdapterConfig: sub2APIAsyncImageGenerationConfig(),
	}, nil, []byte(`{
  "task_id":"imgtask_1",
  "status":"completed",
  "image_url":"/images/result.png"
}`))
	if err != delivery.ErrInvalidResult {
		t.Fatalf("relative image result error = %v, want %v", err, delivery.ErrInvalidResult)
	}
}

func TestAsyncOpenAIImagesDecodesCustomProviderFailurePaths(t *testing.T) {
	var envelope map[string]any
	if err := json.Unmarshal(sub2APIAsyncImageGenerationConfig(), &envelope); err != nil {
		t.Fatal(err)
	}
	response := envelope["async_image"].(map[string]any)["response"].(map[string]any)
	response["error_code_paths"] = []string{"failure.code"}
	response["error_message_paths"] = []string{"missing.message", "failure.detail"}
	response["http_status_paths"] = []string{"failure.http_status"}
	config, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}

	observed, err := (AsyncOpenAIImages{}).DecodeWithDispatch("query", repository.AsyncDispatch{
		AdapterConfig: config,
	}, nil, []byte(`{
  "task_id":"imgtask_1",
  "status":"failed",
  "failure":{
    "code":"custom_bad_image",
    "detail":"custom mapped failure",
    "http_status":422
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != execution.AsyncFailed || observed.ProviderErrorCode != "custom_bad_image" || observed.ProviderErrorMessage != "custom mapped failure" ||
		observed.ProviderHTTPStatus == nil || *observed.ProviderHTTPStatus != http.StatusUnprocessableEntity {
		t.Fatalf("unexpected custom provider failure: %+v", observed)
	}
}

func TestAsyncOpenAIImagesDecodesNon2xxFailureWithoutTaskStatus(t *testing.T) {
	var envelope map[string]any
	if err := json.Unmarshal(sub2APIAsyncImageGenerationConfig(), &envelope); err != nil {
		t.Fatal(err)
	}
	response := envelope["async_image"].(map[string]any)["response"].(map[string]any)
	response["error_code_paths"] = []string{"failure.code"}
	response["error_message_paths"] = []string{"failure.detail"}
	response["http_status_paths"] = []string{"failure.http_status"}
	config, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}

	observed, err := (AsyncOpenAIImages{}).DecodeFailureWithDispatch(repository.AsyncDispatch{
		AdapterConfig: config,
	}, []byte(`{"failure":{"code":"invalid_source","detail":"source image was rejected","http_status":409}}`), http.StatusUnprocessableEntity)
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != execution.AsyncFailed || observed.ProviderErrorCode != "invalid_source" || observed.ProviderErrorMessage != "source image was rejected" ||
		observed.ProviderHTTPStatus == nil || *observed.ProviderHTTPStatus != http.StatusUnprocessableEntity {
		t.Fatalf("unexpected non-2xx provider failure: %+v", observed)
	}
}

func TestAsyncOpenAIImagesRequiresSub2APIPollStatus(t *testing.T) {
	_, err := (AsyncOpenAIImages{}).DecodeWithDispatch("query", repository.AsyncDispatch{
		AdapterConfig: sub2APIAsyncImageGenerationConfig(),
	}, nil, []byte(`{"task_id":"imgtask_1"}`))
	if err != ErrInvalidImageResponse {
		t.Fatalf("missing Sub2API poll status error = %v, want %v", err, ErrInvalidImageResponse)
	}
}

func TestAsyncOpenAIImagesRejectsInvalidProviderHTTPStatus(t *testing.T) {
	_, err := (AsyncOpenAIImages{}).DecodeWithDispatch("query", repository.AsyncDispatch{
		AdapterConfig: sub2APIAsyncImageGenerationConfig(),
	}, nil, []byte(`{"task_id":"imgtask_1","status":"failed","http_status":700}`))
	if err != ErrInvalidImageResponse {
		t.Fatalf("invalid provider HTTP status error = %v, want %v", err, ErrInvalidImageResponse)
	}
}

func TestAsyncOpenAIImagesDecodesTaskAndImageResults(t *testing.T) {
	fixed := repository.AsyncDispatch{AdapterConfig: asyncImageTestConfig()}
	codec := AsyncOpenAIImages{}
	submitted, err := codec.DecodeWithDispatch("submit", fixed, nil, []byte(`{"id":"task-1","state":"pending"}`))
	if err != nil {
		t.Fatal(err)
	}
	if submitted.TaskID != "task-1" || submitted.State != execution.AsyncAccepted {
		t.Fatalf("unexpected submission: %+v", submitted)
	}

	completed, err := codec.DecodeWithDispatch("query", fixed, nil, []byte(`{
  "id":"task-1",
  "state":"succeeded",
  "usage":{"input_tokens":120,"output_tokens":"24"},
  "data":{"images":[{"url":"https://cdn.example/one.png"},{"url":"https://cdn.example/two.png"}]}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != execution.AsyncSucceeded || len(completed.Sources) != 2 || len(completed.Result) == 0 {
		t.Fatalf("unexpected completion: %+v", completed)
	}
	if completed.Facts.Quantities[billing.QuantityGeneratedImages] != "2" {
		t.Fatalf("generated image facts: %#v", completed.Facts.Quantities)
	}
	if completed.Facts.Quantities[billing.QuantityInputTokens] != "120" || completed.Facts.Quantities[billing.QuantityOutputTokens] != "24" {
		t.Fatalf("token facts: %#v", completed.Facts.Quantities)
	}
	if _, ok := completed.Facts.Expr.Vars["success"]; !completed.Facts.Events[billing.ChargeSucceeded] || !ok {
		t.Fatalf("missing success facts: %#v", completed.Facts)
	}
	expression, err := billing.ParseExpression("input_tokens + output_tokens", completed.Facts.Expr.Declared)
	if err != nil {
		t.Fatal(err)
	}
	total, err := expression.Evaluate(completed.Facts.Expr)
	if err != nil || total.String() != "144" {
		t.Fatalf("token expression facts total=%s err=%v", total.String(), err)
	}
}

func TestAsyncOpenAIImagesUsesCanonicalDefaultStatusesWithoutMapEntries(t *testing.T) {
	var envelope map[string]any
	if err := json.Unmarshal(asyncImageTestConfig(), &envelope); err != nil {
		t.Fatal(err)
	}
	config := envelope["async_image"].(map[string]any)
	delete(config["response"].(map[string]any), "status_map")
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	codec := AsyncOpenAIImages{}
	fixed := repository.AsyncDispatch{AdapterConfig: raw}
	for _, test := range []struct {
		action string
		want   execution.AsyncState
	}{
		{action: "submit", want: execution.AsyncAccepted},
		{action: "query", want: execution.AsyncRunning},
	} {
		observed, err := codec.DecodeWithDispatch(test.action, fixed, nil, []byte(`{"id":"task-default"}`))
		if err != nil {
			t.Fatalf("%s: %v", test.action, err)
		}
		if observed.State != test.want {
			t.Fatalf("%s state=%s, want %s", test.action, observed.State, test.want)
		}
	}
}

func TestAsyncOpenAIImagesPreservesFixedNestedFields(t *testing.T) {
	var envelope map[string]any
	if err := json.Unmarshal(asyncImageTestConfig(), &envelope); err != nil {
		t.Fatal(err)
	}
	config := envelope["async_image"].(map[string]any)
	request := config["request"].(map[string]any)
	request["fixed_body"] = map[string]any{"options": map[string]any{"mode": "async"}}
	request["fields"].(map[string]any)["prompt"] = "options.prompt"
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := (AsyncOpenAIImages{}).Prepare(context.Background(), "submit", repository.AsyncDispatch{
		Method: "POST", Path: "/v1/images/generations?async=true", VendorModel: "gpt-image-2", AdapterConfig: raw,
	}, []byte(`{"model":"public-image","prompt":"draw","n":1,"response_format":"url"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	options := body["options"].(map[string]any)
	if options["mode"] != "async" || options["prompt"] != "draw" {
		t.Fatalf("nested fields were not merged: %#v", options)
	}
}

func TestValidateOpenAIImagesCatalogRejectsRequestPathConflicts(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{
			name: "dynamic_parent_child",
			mutate: func(fields, _ map[string]any) {
				fields["quality"] = "options"
				fields["style"] = "options.style"
			},
		},
		{
			name: "fixed_exact",
			mutate: func(fields, request map[string]any) {
				fields["quality"] = "options.mode"
				request["fixed_body"] = map[string]any{"options": map[string]any{"mode": "async"}}
			},
		},
		{
			name: "dynamic_overwrites_fixed_parent",
			mutate: func(fields, request map[string]any) {
				fields["quality"] = "options"
				request["fixed_body"] = map[string]any{"options": map[string]any{"mode": "async"}}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var envelope map[string]any
			if err := json.Unmarshal(asyncImageTestConfig(), &envelope); err != nil {
				t.Fatal(err)
			}
			config := envelope["async_image"].(map[string]any)
			request := config["request"].(map[string]any)
			test.mutate(request["fields"].(map[string]any), request)
			raw, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateOpenAIImagesCatalog("POST", "/v1/images/generations?async=true", "gpt-image-2", "task", raw); err == nil {
				t.Fatal("conflicting request paths were accepted")
			}
		})
	}
}

func TestAsyncOpenAIImagesRejectsInvalidTokenUsage(t *testing.T) {
	_, err := (AsyncOpenAIImages{}).DecodeWithDispatch("query", repository.AsyncDispatch{AdapterConfig: asyncImageTestConfig()}, nil, []byte(`{
  "id":"task-1","state":"succeeded","usage":{"input_tokens":-1},
  "data":{"images":[{"url":"https://cdn.example/one.png"}]}
}`))
	if err == nil {
		t.Fatal("negative token usage was accepted")
	}
}

func TestValidateOpenAIImagesCatalogRejectsBrokenAsyncDeclaration(t *testing.T) {
	if err := ValidateOpenAIImagesCatalog("POST", "/v1/images/generations?async=true", "gpt-image-2", "task", asyncImageTestConfig()); err != nil {
		t.Fatal(err)
	}
	broken := []byte(`{"async_image":{"profile":"json_image_task_v1","submit":{"method":"POST","path":"/submit"},"poll":{"method":"GET","path":"/tasks"}}}`)
	if err := ValidateOpenAIImagesCatalog("POST", "/submit", "gpt-image-2", "task", broken); err == nil {
		t.Fatal("broken async image declaration was accepted")
	}
	if err := ValidateOpenAIImagesCatalog("POST", "/v1/images/generations", "gpt-image-2", "request", asyncImageTestConfig()); err == nil {
		t.Fatal("async declaration was accepted for a synchronous product")
	}
	if err := ValidateOpenAIImagesCatalog("POST", "/v1/images/generations", "gpt-image-2", "task", []byte(`{}`)); err == nil {
		t.Fatal("task-scoped product without an async declaration was accepted")
	}
}

func TestValidateOpenAIImagesCatalogAcceptsConfiguredSynchronousJSONEdit(t *testing.T) {
	config := []byte(`{
  "image_edit": {
    "enabled": true,
    "input_mode": "url",
    "field_mapping": {"prompt":"prompt", "image_urls":"image"},
    "fixed_body": {"model":"legacy", "stream":false, "response_format":"url"}
  }
}`)
	if err := ValidateOpenAIImagesCatalog("POST", "/api/v3/images/generations", "doubao-seedream-5-0-260128", "request", config); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOpenAIImagesCatalog("GET", "/api/v3/images/generations", "doubao-seedream-5-0-260128", "request", config); err == nil {
		t.Fatal("accepted non-POST synchronous image edit")
	}
}

func TestRestoredImageProductsMatchExecutableAdapterContracts(t *testing.T) {
	for _, test := range []struct {
		name        string
		baseURL     string
		vendorModel string
		constraints []byte
	}{
		{
			name:        "doubao",
			baseURL:     "https://ark.cn-beijing.volces.com",
			vendorModel: "doubao-seedream-5-0-260128",
			constraints: []byte(`{"sizes":["1024x1024","1536x1024","1024x1536","1792x1024","1024x1792","auto"],"qualities":["auto","high","medium","low"]}`),
		},
		{
			name:        "grok",
			baseURL:     "https://image.mirainya.com",
			vendorModel: "grok-imagine-image",
			constraints: []byte(`{}`),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateCatalogProduct(
				"openai_images", 1, test.baseURL, http.MethodPost,
				"/v1/images/generations", test.vendorModel, "request", test.constraints,
			); err != nil {
				t.Fatal(err)
			}
			prepared, err := (OpenAIImages{}).Prepare(
				context.Background(), ImagesGenerate, http.MethodPost,
				"/v1/images/generations", test.vendorModel,
				[]byte(`{"model":"public-image","prompt":"draw","n":1,"response_format":"url"}`), nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(prepared.Body, &body); err != nil {
				t.Fatal(err)
			}
			if prepared.Path != "/v1/images/generations" || body["model"] != test.vendorModel {
				t.Fatalf("prepared=%+v body=%#v", prepared, body)
			}
		})
	}

	duomiConfig := asyncImageTestConfig()
	if err := ValidateCatalogProduct(
		"openai_images", 1, "https://duomiapi.com", http.MethodPost,
		"/v1/images/generations?async=true", "gpt-image-2", "task", duomiConfig,
	); err != nil {
		t.Fatal(err)
	}
	prepared, err := (AsyncOpenAIImages{}).Prepare(context.Background(), "submit", repository.AsyncDispatch{
		Method: http.MethodPost, Path: "/v1/images/generations?async=true",
		VendorModel: "gpt-image-2", AdapterConfig: duomiConfig,
	}, []byte(`{"model":"gpt-image-2-duomi","prompt":"draw","n":1,"response_format":"url"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "gpt-image-2" || body["response_format"] != "url" {
		t.Fatalf("submit body=%#v", body)
	}
	completed, err := (AsyncOpenAIImages{}).DecodeWithDispatch("query", repository.AsyncDispatch{
		AdapterConfig: duomiConfig,
	}, nil, []byte(`{"id":"task-1","state":"succeeded","data":{"images":[{"url":"https://cdn3.dmiapi.com/result.png"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != execution.AsyncSucceeded || len(completed.Sources) != 1 || completed.Sources[0].URL != "https://cdn3.dmiapi.com/result.png" {
		t.Fatalf("completion=%+v", completed)
	}
}
