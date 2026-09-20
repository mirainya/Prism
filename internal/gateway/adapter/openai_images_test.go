package adapter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/delivery"
)

var onePixelPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")

type imageLoader map[string]ImageAsset

func (l imageLoader) LoadImage(_ context.Context, value string) (ImageAsset, error) {
	return l[value], nil
}

func TestOpenAIImagesGenerationPinsVendorModel(t *testing.T) {
	payload := []byte(`{"model":"public","prompt":"draw","n":2,"response_format":"url","output_format":"png"}`)
	prepared, err := (OpenAIImages{}).Prepare(context.Background(), ImagesGenerate, http.MethodPost, "/v1/images/generations", "vendor", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if json.Unmarshal(prepared.Body, &body) != nil || body["model"] != "vendor" || body["prompt"] != "draw" || body["n"] != float64(2) || body["response_format"] != "url" {
		t.Fatalf("body=%s", prepared.Body)
	}
	if prepared.Header.Get("Content-Type") != "application/json" || prepared.Streaming {
		t.Fatalf("prepared=%+v", prepared)
	}
}

func TestOpenAIImagesGenerationCanForceUpstreamBase64(t *testing.T) {
	payload := []byte(`{"model":"public","prompt":"draw","n":1,"response_format":"url"}`)
	prepared, err := (OpenAIImages{}).PrepareWithConfig(
		context.Background(), ImagesGenerate, http.MethodPost, "/v1/images/generations", "vendor",
		payload, []byte(`{"upstream_response_format":"b64_json"}`), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if json.Unmarshal(prepared.Body, &body) != nil || body["response_format"] != "b64_json" {
		t.Fatalf("body=%s", prepared.Body)
	}
	var downstream map[string]any
	if json.Unmarshal(payload, &downstream) != nil || downstream["response_format"] != "url" {
		t.Fatalf("downstream payload changed: %s", payload)
	}
}

func TestOpenAIImagesEditBuildsDeterministicMultipart(t *testing.T) {
	payload := []byte(`{"model":"public","prompt":"edit","image_urls":["https://assets.example/image.png"],"mask_urls":["https://assets.example/mask.png"],"n":1,"response_format":"b64_json","output_format":"png"}`)
	loader := imageLoader{
		"https://assets.example/image.png": {Data: onePixelPNG, ContentType: "image/png"},
		"https://assets.example/mask.png":  {Data: onePixelPNG, ContentType: "image/png"},
	}
	first, err := (OpenAIImages{}).Prepare(context.Background(), ImagesEdit, http.MethodPost, "/v1/images/edits", "vendor", payload, loader)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (OpenAIImages{}).Prepare(context.Background(), ImagesEdit, http.MethodPost, "/v1/images/edits", "vendor", payload, loader)
	if err != nil || !bytes.Equal(first.Body, second.Body) || first.Header.Get("Content-Type") != second.Header.Get("Content-Type") {
		t.Fatalf("multipart is not deterministic: %v", err)
	}
	_, params, found := strings.Cut(first.Header.Get("Content-Type"), "boundary=")
	if !found {
		t.Fatal("missing multipart boundary")
	}
	reader := multipart.NewReader(bytes.NewReader(first.Body), params)
	fields := map[string]int{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		fields[part.FormName()]++
		body, _ := io.ReadAll(part)
		if part.FormName() == "model" && string(body) != "vendor" {
			t.Fatalf("model=%q", body)
		}
	}
	if fields["image"] != 1 || fields["mask"] != 1 || fields["model"] != 1 || fields["prompt"] != 1 {
		t.Fatalf("fields=%v", fields)
	}
}

func TestOpenAIImagesMultipartEditCanForceUpstreamBase64(t *testing.T) {
	payload := []byte(`{"model":"public","prompt":"edit","image_urls":["https://assets.example/image.png"],"n":1,"response_format":"url"}`)
	loader := imageLoader{"https://assets.example/image.png": {Data: onePixelPNG, ContentType: "image/png"}}
	prepared, err := (OpenAIImages{}).PrepareWithConfig(
		context.Background(), ImagesEdit, http.MethodPost, "/v1/images/edits", "vendor",
		payload, []byte(`{"upstream_response_format":"b64_json"}`), loader,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, params, found := strings.Cut(prepared.Header.Get("Content-Type"), "boundary=")
	if !found {
		t.Fatal("missing multipart boundary")
	}
	reader := multipart.NewReader(bytes.NewReader(prepared.Body), params)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(part)
		if part.FormName() == "response_format" {
			if string(body) != "b64_json" {
				t.Fatalf("response_format=%q", body)
			}
			return
		}
	}
	t.Fatal("missing response_format field")
}

func TestOpenAIImagesEditBuildsConfiguredJSONURLRequest(t *testing.T) {
	payload := []byte(`{
  "model":"public",
  "prompt":"edit",
  "image_urls":["https://assets.example/one.png","https://assets.example/two.png"],
  "n":1,
  "size":"1024x1024",
  "response_format":"url",
  "stream":true
}`)
	config := []byte(`{
	"upstream_response_format": "b64_json",
  "image_edit": {
    "enabled": true,
    "input_mode": "url",
    "request": {
      "fields": {
        "model": "model",
        "prompt": "prompt",
        "image_urls": "image",
        "n": "n",
        "size": "size"
      },
      "fixed_body": {
        "model": "legacy-fixed-model",
        "stream": false,
        "response_format": "url"
      }
    }
  }
}`)

	prepared, err := (OpenAIImages{}).PrepareWithConfig(
		context.Background(), ImagesEdit, http.MethodPost,
		"/api/v3/images/generations", "doubao-seedream-5-0-260128",
		payload, config, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Path != "/api/v3/images/generations" || prepared.Header.Get("Content-Type") != "application/json" || prepared.Streaming {
		t.Fatalf("prepared=%+v", prepared)
	}
	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "doubao-seedream-5-0-260128" || body["prompt"] != "edit" || body["response_format"] != "b64_json" || body["stream"] != false {
		t.Fatalf("body=%#v", body)
	}
	images, ok := body["image"].([]any)
	if !ok || len(images) != 2 || images[0] != "https://assets.example/one.png" || images[1] != "https://assets.example/two.png" {
		t.Fatalf("image=%#v", body["image"])
	}
}

func TestOpenAIImagesEditAcceptsTopLevelMappingAliases(t *testing.T) {
	payload := []byte(`{"model":"public","prompt":"edit","image_urls":["https://assets.example/image.png"],"n":1,"response_format":"url"}`)
	config := []byte(`{
  "image_edit": {
    "enabled": true,
    "input_mode": "url",
    "field_mapping": {"prompt":"prompt", "image_urls":"image"},
    "fixed_body": {"model":"old", "stream":false, "response_format":"url"}
  }
}`)
	prepared, err := (OpenAIImages{}).PrepareWithConfig(context.Background(), ImagesEdit, http.MethodPost, "/api/v3/images/generations", "vendor", payload, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "vendor" || body["stream"] != false || body["response_format"] != "url" {
		t.Fatalf("body=%#v", body)
	}
}

func TestOpenAIImagesEditRejectsBrokenJSONURLMapping(t *testing.T) {
	payload := []byte(`{"model":"public","prompt":"edit","image_urls":["https://assets.example/image.png"],"n":1,"response_format":"url"}`)
	config := []byte(`{"image_edit":{"enabled":true,"input_mode":"url","request":{"fields":{"prompt":"prompt"}}}}`)
	if _, err := (OpenAIImages{}).PrepareWithConfig(context.Background(), ImagesEdit, http.MethodPost, "/images", "vendor", payload, config, nil); err == nil {
		t.Fatal("accepted URL edit mapping without image_urls")
	}
}

func TestOpenAIImagesRejectsUnknownUpstreamResponseFormat(t *testing.T) {
	payload := []byte(`{"model":"public","prompt":"draw","n":1,"response_format":"url"}`)
	if _, err := (OpenAIImages{}).PrepareWithConfig(
		context.Background(), ImagesGenerate, http.MethodPost, "/v1/images/generations", "vendor",
		payload, []byte(`{"upstream_response_format":"data_url"}`), nil,
	); err == nil {
		t.Fatal("accepted unsupported upstream response format")
	}
}

func TestOpenAIImagesDecodesURLAndBase64Results(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(onePixelPNG)
	for _, test := range []struct {
		body   string
		stream bool
	}{
		{body: `{"created":42,"data":[{"url":"https://images.example/result.png","revised_prompt":"revised"}],"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}`},
		{stream: true, body: "data: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"" + encoded + "\"}\n\ndata: {\"type\":\"image_generation.completed\",\"created_at\":42,\"b64_json\":\"" + encoded + "\",\"usage\":{\"input_tokens\":3,\"output_tokens\":4,\"total_tokens\":7}}\n\ndata: [DONE]\n\n"},
	} {
		result, err := (OpenAIImages{}).Decode([]byte(test.body), test.stream, "png")
		if err != nil || result.Created != 42 || len(result.Outputs) != 1 || result.Usage.TotalTokens == nil || *result.Usage.TotalTokens != 7 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if test.stream && !bytes.Equal(result.Outputs[0].Data, onePixelPNG) {
			t.Fatal("decoded image mismatch")
		}
	}
}

func TestOpenAIImagesStreamingAcceptsJSONProviderFallback(t *testing.T) {
	body := []byte(`{"created":42,"data":[{"url":"https://images.example/result.png"}]}`)
	result, err := (OpenAIImages{}).Decode(body, true, "png")
	if err != nil || result.Created != 42 || len(result.Outputs) != 1 || result.Outputs[0].URL == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestOpenAIImagesDecodeObservationBuildsDeliverySources(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(onePixelPNG)
	body := []byte(`{"created":42,"data":[{"url":"https://images.example/result.png","revised_prompt":"revised"},{"b64_json":"` + encoded + `"}]}`)
	observation, err := (OpenAIImages{}).DecodeObservation(body, false, "png")
	if err != nil {
		t.Fatal(err)
	}
	if string(observation.State) != "succeeded" || len(observation.Sources) != 2 || len(observation.Result) == 0 {
		t.Fatalf("observation=%+v", observation)
	}
	if observation.Sources[0].Role != "image" || observation.Sources[0].URL == "" {
		t.Fatalf("url source=%+v", observation.Sources[0])
	}
	if observation.Sources[1].Role != "image" || len(observation.Sources[1].InlineData) == 0 || observation.Sources[1].ContentType != "image/png" {
		t.Fatalf("inline source=%+v", observation.Sources[1])
	}
	if got := observation.Facts.Quantities["result.images"]; got != "2" {
		t.Fatalf("image quantity=%q", got)
	}
	if err := delivery.ValidateResult(delivery.ResourceCapabilityTask, observation.Result, observation.Sources); err != nil {
		t.Fatalf("result contract rejected: %v", err)
	}
}

func TestOpenAIImagesDecodeObservationExposesTokenBillingFacts(t *testing.T) {
	body := []byte(`{"data":[{"url":"https://images.example/result.png"}],"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}`)
	observation, err := (OpenAIImages{}).DecodeObservation(body, false, "png")
	if err != nil {
		t.Fatal(err)
	}
	if observation.Facts.Quantities["usage.input_tokens"] != "3" || observation.Facts.Quantities["usage.output_tokens"] != "4" {
		t.Fatalf("billing facts=%+v", observation.Facts)
	}
}

func TestOpenAIImagesDecodeObservationInjectsExpressionFacts(t *testing.T) {
	body := []byte(`{"data":[{"url":"https://images.example/result.png"}],"usage":{"input_tokens":3,"output_tokens":4}}`)
	observation, err := (OpenAIImages{}).DecodeObservation(body, false, "png")
	if err != nil {
		t.Fatal(err)
	}
	expression, err := billing.ParseExpression("count + input_tokens + output_tokens + success", observation.Facts.Expr.Declared)
	if err != nil {
		t.Fatal(err)
	}
	amount, err := expression.Evaluate(observation.Facts.Expr)
	if err != nil || amount.String() != "9" {
		t.Fatalf("amount=%s err=%v expr=%+v", amount.String(), err, observation.Facts.Expr)
	}
}

func TestOpenAIImagesDecodeObservationRejectsUnsafeURL(t *testing.T) {
	body := []byte(`{"data":[{"url":"https://images.example/result.png#fragment"}]}`)
	if _, err := (OpenAIImages{}).DecodeObservation(body, false, ""); err == nil {
		t.Fatal("accepted unsafe result URL")
	}
}

func TestOpenAIImagesRejectsEmbeddedBytesAndMalformedResults(t *testing.T) {
	invalidRequests := [][]byte{
		[]byte(`{"model":"public","prompt":"draw","n":1,"response_format":"url","image_urls":["data:image/png;base64,AAAA"]}`),
		[]byte(`{"model":"public","prompt":"draw","n":0,"response_format":"url"}`),
		[]byte(`{"model":"public","prompt":"draw","n":1,"response_format":"url","unknown":true}`),
	}
	for _, payload := range invalidRequests {
		if _, err := (OpenAIImages{}).Prepare(context.Background(), ImagesGenerate, http.MethodPost, "/v1/images/generations", "vendor", payload, nil); err == nil {
			t.Fatalf("accepted request %s", payload)
		}
	}
	invalidResponses := []string{
		`{"data":[]}`,
		`{"data":[{"url":"https://images.example/result.png","b64_json":"AAAA"}]}`,
		`{"data":[{"url":"data:image/png;base64,AAAA"}]}`,
		`{"data":[{"b64_json":"AAAA"}]}`,
		`{"data":[{"url":"https://images.example/result.png"}],"usage":{"input_tokens":1.5}}`,
	}
	for _, body := range invalidResponses {
		if _, err := (OpenAIImages{}).Decode([]byte(body), false, "png"); err == nil {
			t.Fatalf("accepted response %s", body)
		}
	}
}
