package adapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/runtime"
)

func genericVideoConfig() []byte {
	return []byte(`{"task_types":["first_frame"],"adapter":{"profile":"json_task_v1","auth_location":"header","auth_key":"Authorization","auth_prefix":"","submit":{"enabled":true,"method":"POST","path":"/workflow/h3"},"poll":{"enabled":true,"method":"GET","path":"/workflow/result/{task_id}"},"request":{"fields":{"model":"workflow","prompt":"prompt","duration":"duration","task_mode":"mode"},"include_content":false,"content_projections":[{"source":"url","target":"ref_image_0","types":["image_url"],"index":0}],"params_mode":"merge_missing"},"response":{"task_id_paths":["data.task_id"],"status_paths":["data.status"],"video_url_paths":["data.results.0.url"],"thumbnail_url_paths":["data.thumbnail_url"],"duration_paths":["data.duration"],"status_map":{"queued":"submitted","running":"tracking","success":"completed","failed":"failed"},"submit_default_status":"submitted","poll_default_status":"tracking","unknown_status":"tracking"},"validation":{"models":{"minimax-h3":{"duration_min":1,"duration_max":15,"task_modes":["first_frame"],"max_images":9}}}}}`)
}

func TestValidateCatalogProductUsesRuntimeGenericValidation(t *testing.T) {
	if err := ValidateCatalogProduct("generic", 1, "https://autodl.example", "POST", "/workflow/h3", "minimax-h3", "task", genericVideoConfig()); err != nil {
		t.Fatalf("valid generic product rejected: %v", err)
	}
	if err := ValidateCatalogProduct("generic", 1, "https://autodl.example", "POST", "/workflow/h3", "minimax-h3", "task", []byte(`{}`)); err == nil {
		t.Fatal("empty generic mapping was accepted")
	}
}

func TestGenericVideoUsesPinnedModelAndCatalogMapping(t *testing.T) {
	fixed := repository.AsyncDispatch{
		AdapterCode: "generic", AdapterVersion: 1, BaseURL: "https://autodl.example",
		Method: "POST", Path: "/workflow/h3", VendorModel: "minimax-h3", PublicID: "call-1",
		AdapterConfig: genericVideoConfig(),
	}
	payload := []byte(`{"model":"public-h3","prompt":"animate","task_mode":"first_frame","duration":5,"content":[{"type":"image_url","role":"first_frame","url":"https://assets.example/frame.png"}]}`)
	prepared, err := (GenericVideo{}).Prepare(context.Background(), "submit", fixed, payload, "")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.CredentialHeader != "Authorization" || prepared.CredentialPrefix != "" || prepared.Path != "/workflow/h3" {
		t.Fatalf("prepared = %+v", prepared)
	}
	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["workflow"] != "minimax-h3" || body["mode"] != "first_frame" || body["ref_image_0"] != "https://assets.example/frame.png" {
		t.Fatalf("body = %#v", body)
	}
	query, err := (GenericVideo{}).Prepare(context.Background(), "query", fixed, payload, "task/1")
	if err != nil || query.Path != "/workflow/result/task%2F1" || query.Method != "GET" {
		t.Fatalf("query = %+v, err = %v", query, err)
	}
}

func TestGenericVideoDecodesTerminalResultAndExactBillingFacts(t *testing.T) {
	fixed := repository.AsyncDispatch{BaseURL: "https://autodl.example", AdapterConfig: genericVideoConfig()}
	request := []byte(`{"duration":5}`)
	response := []byte(`{"data":{"task_id":"task-1","status":"success","duration":6.123456789123456789,"results":[{"url":"https://media.example/result.mp4"}],"thumbnail_url":"https://media.example/thumb.jpg"}}`)
	out, err := (GenericVideo{}).DecodeWithDispatch("query", fixed, request, response)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != execution.AsyncSucceeded || out.TaskID != "task-1" || len(out.Sources) != 2 {
		t.Fatalf("observation = %+v", out)
	}
	if out.Facts.Quantities[billing.QuantityGeneratedSeconds] != "6.123456789123456789" || out.Facts.Quantities[billing.QuantityRequestedSeconds] != "5" || out.Facts.Quantities[billing.QuantityGeneratedVideos] != "1" {
		t.Fatalf("facts = %+v", out.Facts)
	}
}

func TestGenericVideoDecodeInjectsExpressionFacts(t *testing.T) {
	fixed := repository.AsyncDispatch{BaseURL: "https://autodl.example", AdapterConfig: genericVideoConfig()}
	request := []byte(`{"duration":5,"resolution":"720p","generate_audio":true,"task_mode":"first_frame","content":[{"type":"video_url"}]}`)
	response := []byte(`{"data":{"task_id":"task-1","status":"success","duration":6.5,"results":[{"url":"https://media.example/result.mp4"}]}}`)
	out, err := (GenericVideo{}).DecodeWithDispatch("query", fixed, request, response)
	if err != nil {
		t.Fatal(err)
	}
	expression, err := billing.ParseExpression("seconds + has_audio + has_video_ref + success", out.Facts.Expr.Declared)
	if err != nil {
		t.Fatal(err)
	}
	amount, err := expression.Evaluate(out.Facts.Expr)
	if err != nil || amount.String() != "9.5" {
		t.Fatalf("amount=%s err=%v expr=%+v", amount.String(), err, out.Facts.Expr)
	}
}

func TestAsyncCodecRegistryIncludesVideoAndImageProtocols(t *testing.T) {
	for _, item := range []struct {
		code    string
		version uint32
	}{
		{code: "seedance", version: 1},
		{code: "generic", version: 1},
		{code: "openai_images", version: 1},
	} {
		if _, ok := AsyncCodecFor(item.code, item.version); !ok {
			t.Fatalf("missing video codec %s@%d", item.code, item.version)
		}
	}
	if _, ok := AsyncCodecFor("generic", 2); ok {
		t.Fatal("unimplemented generic contract version was accepted")
	}
}

func TestGenericVideoDoesNotEnableUndeclaredCallbacks(t *testing.T) {
	var codec any = GenericVideo{}
	if _, ok := codec.(runtime.CallbackCodec); ok {
		t.Fatal("generic video unexpectedly implements CallbackCodec")
	}
	if _, ok := codec.(runtime.DispatchAwareCallbackCodec); ok {
		t.Fatal("generic video unexpectedly implements DispatchAwareCallbackCodec")
	}
	if _, ok := codec.(runtime.CallbackTokenCodec); ok {
		t.Fatal("generic video unexpectedly implements CallbackTokenCodec")
	}
}
