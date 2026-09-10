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
