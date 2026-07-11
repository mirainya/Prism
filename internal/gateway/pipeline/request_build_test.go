package pipeline

import (
	"encoding/json"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	anthropictransport "github.com/mirainya/Prism/internal/gateway/transport/anthropic"
	openaitransport "github.com/mirainya/Prism/internal/gateway/transport/openai"
	volcenginetransport "github.com/mirainya/Prism/internal/gateway/transport/volcengine"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/internal/service"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestAdaptChatExtensionsNormalizesBeforePlanWithoutMutatingSource(t *testing.T) {
	raw := json.RawMessage(`{"reasoning_effort":"high","caching":{"type":"enabled"}}`)
	source := canonical.Request{ClientExtensions: map[string]json.RawMessage{"openai_chat.request_extras": raw}}
	converted := source
	if err := adaptChatExtensions(&converted, transport.VolcengineResponsesV3); err != nil {
		t.Fatal(err)
	}
	if converted.Reasoning == nil || converted.Reasoning.Effort != "high" {
		t.Fatalf("reasoning extension was not normalized: %#v", converted.Reasoning)
	}
	if converted.ProviderOptions.Volcengine == nil || string(converted.ProviderOptions.Volcengine.Caching) != `{"type":"enabled"}` {
		t.Fatalf("Volcengine extension was not normalized: %#v", converted.ProviderOptions.Volcengine)
	}
	if len(converted.ClientExtensions) != 0 {
		t.Fatalf("consumed extensions remain: %#v", converted.ClientExtensions)
	}
	if string(source.ClientExtensions["openai_chat.request_extras"]) != string(raw) || source.Reasoning != nil || source.ProviderOptions.Volcengine != nil {
		t.Fatalf("source request was mutated: %#v", source)
	}

	responses := source
	if err := adaptChatExtensions(&responses, transport.OpenAIResponses); err != nil {
		t.Fatal(err)
	}
	if responses.Reasoning == nil || responses.Reasoning.Effort != "high" {
		t.Fatalf("Responses reasoning extension was not normalized: %#v", responses.Reasoning)
	}
	if len(responses.ClientExtensions["openai_chat.request_extras"]) == 0 {
		t.Fatal("Responses conversion incorrectly consumed Volcengine-only caching")
	}
}

func TestChatReasoningEffortIsNormalizedBeforeResponsesPlan(t *testing.T) {
	effort := "high"
	request := &service.CompletionRequest{
		Model: "public", ReasoningEffort: &effort,
		Messages: []chat.ChatMessage{{Role: "user", Content: "hello"}},
	}
	canonicalRequest, err := canonicalChatRequest(request, request.Messages, request.Model)
	if err != nil {
		t.Fatal(err)
	}
	item := openaitransport.NewResponses(nil)
	if plan := item.Plan(transport.OperationChat, canonicalRequest, canonicalRequest.RequiredFeatures()); plan.Supported() {
		t.Fatalf("raw Chat extension unexpectedly bypassed normalization: %#v", plan)
	}
	options := (&Pipeline{}).v2Options(request)
	normalized, err := options.PrepareTransport(t.Context(), canonicalRequest, transport.OpenAIResponses)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Reasoning == nil || normalized.Reasoning.Effort != effort {
		t.Fatalf("normalized reasoning = %#v", normalized.Reasoning)
	}
	if plan := item.Plan(transport.OperationChat, normalized, normalized.RequiredFeatures()); !plan.Supported() {
		t.Fatalf("normalized Chat reasoning was rejected before routing: %#v", plan)
	}
}

func TestConfiguredVolcengineReasoningIsNormalizedBeforePlan(t *testing.T) {
	setupBuildRequestDB(t, "build_chat_request_volcengine_reasoning")
	if err := model.DB().Create(&model.GwModelMeta{
		ModelName:      "public-volcengine",
		ThinkingConfig: datatypes.JSON(`{"default":"high","options":[{"value":"high","body":{"reasoning":{"effort":"high"}}}]}`),
	}).Error; err != nil {
		t.Fatal(err)
	}
	effort := "high"
	request := &service.CompletionRequest{
		Model: "public-volcengine", ReasoningEffort: &effort,
		Messages: []chat.ChatMessage{{Role: "user", Content: "hello"}},
	}
	options := (&Pipeline{}).v2Options(request)
	route := &routing.RouteResult{ModelName: request.Model, VendorModel: "vendor", Protocol: model.ProtocolVolcengine, Transport: transport.VolcengineResponsesV3}
	routeRequest, err := options.PrepareRoute(t.Context(), canonical.Request{}, route)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := options.PrepareTransport(t.Context(), routeRequest, transport.VolcengineResponsesV3)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Reasoning == nil || string(normalized.Reasoning.Raw) != `{"effort":"high"}` || len(normalized.ClientExtensions) != 0 {
		t.Fatalf("configured reasoning was not normalized: %#v", normalized)
	}
	item := volcenginetransport.New(transport.HTTPClient{})
	if plan := item.Plan(transport.OperationChat, normalized, normalized.RequiredFeatures()); !plan.Supported() {
		t.Fatalf("normalized Volcengine reasoning was rejected: %#v", plan)
	}
}

func TestConfiguredAnthropicThinkingSurvivesInitialAndRoutePlanning(t *testing.T) {
	setupBuildRequestDB(t, "build_chat_request_anthropic_thinking")
	if err := model.DB().Create(&model.GwModelMeta{
		ModelName:      "public-anthropic",
		ThinkingConfig: datatypes.JSON(`{"default":"high","options":[{"value":"high","body":{"thinking":{"type":"enabled","budget_tokens":4096}}}]}`),
	}).Error; err != nil {
		t.Fatal(err)
	}
	effort := "high"
	request := &service.CompletionRequest{
		Model: "public-anthropic", ReasoningEffort: &effort,
		Messages: []chat.ChatMessage{{Role: "user", Content: "hello"}},
	}
	initial, err := canonicalChatRequest(request, request.Messages, request.Model)
	if err != nil {
		t.Fatal(err)
	}
	if !initial.RequiredFeatures().Has(canonical.FeatureReasoning) {
		t.Fatal("reasoning capability is missing before route selection")
	}
	options := (&Pipeline{}).v2Options(request)
	planningRequest, err := options.PrepareTransport(t.Context(), initial, transport.AnthropicMessages)
	if err != nil {
		t.Fatal(err)
	}
	item := anthropictransport.New(nil)
	if plan := item.Plan(transport.OperationChat, planningRequest, initial.RequiredFeatures()); !plan.Supported() {
		t.Fatalf("Anthropic was rejected before route thinking injection: %#v", plan)
	}

	route := &routing.RouteResult{ModelName: request.Model, VendorModel: "vendor", Protocol: model.ProtocolAnthropic, Transport: transport.AnthropicMessages}
	routeRequest, err := options.PrepareRoute(t.Context(), initial, route)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := options.PrepareTransport(t.Context(), routeRequest, transport.AnthropicMessages)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Reasoning == nil || string(normalized.Reasoning.Raw) != `{"budget_tokens":4096,"type":"enabled"}` || len(normalized.ClientExtensions) != 0 {
		t.Fatalf("configured Anthropic thinking was not normalized: %#v", normalized)
	}
	if plan := item.Plan(transport.OperationChat, normalized, initial.RequiredFeatures()); !plan.Supported() {
		t.Fatalf("normalized Anthropic thinking was rejected: %#v", plan)
	}
}

func TestBuildChatRequestPreservesSamplingAndNativeReasoning(t *testing.T) {
	setupBuildRequestDB(t, "build_chat_request_native")
	temperature, topP := 0.7, 0.8
	effort := "high"
	req := &service.CompletionRequest{Model: "public", Temperature: &temperature, TopP: &topP, ReasoningEffort: &effort}
	route := &routing.RouteResult{ModelName: "public", VendorModel: "vendor", Protocol: model.ProtocolOpenAI}

	converted, err := (&Pipeline{}).buildChatRequest(req, route)
	if err != nil {
		t.Fatal(err)
	}
	if converted.Temperature == nil || converted.TopP == nil {
		t.Fatalf("sampling parameters were dropped: %#v", converted)
	}
	if converted.ExtraBody["reasoning_effort"] != effort {
		t.Fatalf("reasoning_effort was dropped: %#v", converted.ExtraBody)
	}
}

func setupBuildRequestDB(t *testing.T, name string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.GwModelMeta{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
}

func TestBuildChatRequestRejectsUnconfiguredConvertedReasoning(t *testing.T) {
	setupBuildRequestDB(t, "build_chat_request_converted")
	effort := "high"
	req := &service.CompletionRequest{Model: "public", ReasoningEffort: &effort}
	route := &routing.RouteResult{ModelName: "public", VendorModel: "vendor", Protocol: model.ProtocolAnthropic}
	if _, err := (&Pipeline{}).buildChatRequest(req, route); err == nil {
		t.Fatal("unconfigured reasoning translation was accepted")
	}
}
