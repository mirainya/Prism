package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/runtime"
)

func TestSeedanceCatalogContractIsFixed(t *testing.T) {
	if err := ValidateSeedanceVideoCatalog("POST", SeedanceSubmitPath); err != nil {
		t.Fatal(err)
	}
	for _, value := range [][2]string{{"GET", SeedanceSubmitPath}, {"POST", "/tasks"}} {
		if err := ValidateSeedanceVideoCatalog(value[0], value[1]); !errors.Is(err, repository.ErrInvalidInput) {
			t.Fatalf("contract %q %q error = %v", value[0], value[1], err)
		}
	}
}

func TestSeedanceUsesPinnedModelAndEscapesTaskIdentity(t *testing.T) {
	codec := Seedance{}
	fixed := repository.AsyncDispatch{VendorModel: "provider-model", Method: "POST", Path: "/tasks", PublicID: "call-1"}
	prepared, err := codec.Prepare(context.Background(), "submit", fixed, []byte(`{"model":"public-model","prompt":"test","duration":5}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(prepared.Body, &payload); err != nil || payload["model"] != "provider-model" || prepared.Header.Get("X-Request-ID") != "call-1" {
		t.Fatalf("pinned request: %v %v", payload, err)
	}
	query, err := codec.Prepare(context.Background(), "query", fixed, nil, "id/with?query#fragment")
	if err != nil || query.Path != "/tasks/id%2Fwith%3Fquery%23fragment" || query.Method != "GET" {
		t.Fatalf("query=%+v err=%v", query, err)
	}
	for _, id := range []string{"", ".", ".."} {
		if _, err := codec.Prepare(context.Background(), "query", fixed, nil, id); err == nil {
			t.Errorf("invalid task identity %q accepted", id)
		}
	}
}

func TestSeedancePreservesExactDurationAndDoesNotInventUsage(t *testing.T) {
	for _, response := range []string{
		`{"status":"succeeded","duration":6.123456789123456789,"content":{"video_url":"https://example.invalid/video.mp4"}}`,
		`{"code":0,"data":{"status":"succeeded","result":{"duration":6.123456789123456789,"video_url":"https://example.invalid/video.mp4"}}}`,
	} {
		out, err := (Seedance{}).Decode("query", []byte(`{"duration":5}`), []byte(response))
		if err != nil || out.State != execution.AsyncSucceeded || out.Facts.Quantities[billing.QuantityGeneratedSeconds] != "6.123456789123456789" || out.Facts.Quantities[billing.QuantityRequestedSeconds] != "5" {
			t.Fatalf("observation=%+v err=%v", out, err)
		}
	}
	out, err := (Seedance{}).Decode("query", []byte(`{"duration":5}`), []byte(`{"status":"succeeded","content":{"video_url":"https://example.invalid/video.mp4"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := out.Facts.Quantities[billing.QuantityGeneratedSeconds]; exists {
		t.Fatal("missing duration was replaced with requested duration")
	}
	if _, exists := out.Facts.Events[billing.ChargeDelivered]; exists {
		t.Fatal("generation incorrectly proved delivery")
	}
}

func TestSeedanceDecodeInjectsExpressionFacts(t *testing.T) {
	request := []byte(`{"duration":5,"resolution":"720p","generate_audio":true,"task_mode":"references","content":[{"type":"video_url"}]}`)
	response := []byte(`{"status":"succeeded","duration":6.123456789123456789,"content":{"video_url":"https://example.invalid/video.mp4"}}`)
	out, err := (Seedance{}).Decode("query", request, response)
	if err != nil {
		t.Fatal(err)
	}
	expression, err := billing.ParseExpression("(resolution=='720p' ? seconds : 0) + has_audio + has_video_ref + success", out.Facts.Expr.Declared)
	if err != nil {
		t.Fatal(err)
	}
	amount, err := expression.Evaluate(out.Facts.Expr)
	if err != nil || amount.String() != "9.123456789123456789" {
		t.Fatalf("amount=%s err=%v expr=%+v", amount.String(), err, out.Facts.Expr)
	}
}

func TestSeedanceDecodeInjectsPriorityExpressionFact(t *testing.T) {
	request := []byte(`{"duration":5,"resolution":"720p","task_mode":"references","params":{"priority":4}}`)
	response := []byte(`{"status":"succeeded","duration":6,"content":{"video_url":"https://example.invalid/video.mp4"}}`)
	out, err := (Seedance{}).Decode("query", request, response)
	if err != nil {
		t.Fatal(err)
	}
	expression, err := billing.ParseExpression("seconds * (priority==4 ? 1.5 : 1)", out.Facts.Expr.Declared)
	if err != nil {
		t.Fatal(err)
	}
	amount, err := expression.Evaluate(out.Facts.Expr)
	if err != nil || amount.String() != "9" {
		t.Fatalf("amount=%s err=%v expr=%+v", amount.String(), err, out.Facts.Expr)
	}
}

func TestSeedanceDecodeRejectsFractionalPriorityFact(t *testing.T) {
	request := []byte(`{"duration":5,"params":{"priority":4.5}}`)
	response := []byte(`{"status":"succeeded","duration":6,"content":{"video_url":"https://example.invalid/video.mp4"}}`)
	if _, err := (Seedance{}).Decode("query", request, response); err == nil {
		t.Fatal("fractional priority was accepted")
	}
}

func TestSeedanceIsPollingOnly(t *testing.T) {
	var codec any = Seedance{}
	if _, ok := codec.(runtime.CallbackCodec); ok {
		t.Fatal("seedance unexpectedly implements CallbackCodec")
	}
	if _, ok := codec.(runtime.DispatchAwareCallbackCodec); ok {
		t.Fatal("seedance unexpectedly implements DispatchAwareCallbackCodec")
	}
	if _, ok := codec.(runtime.CallbackTokenCodec); ok {
		t.Fatal("seedance unexpectedly implements CallbackTokenCodec")
	}
}

func TestSeedanceRejectsInvalidProviderDuration(t *testing.T) {
	for _, duration := range []string{"-1", "1.1234567891234567899"} {
		response := []byte(`{"status":"succeeded","duration":` + duration + `,"content":{"video_url":"https://example.invalid/video.mp4"}}`)
		if _, err := (Seedance{}).Decode("query", []byte(`{"duration":5}`), response); err == nil {
			t.Fatalf("invalid duration %s was accepted", duration)
		}
	}
}
