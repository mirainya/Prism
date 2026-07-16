package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	openai_chat "github.com/mirainya/Prism/internal/gateway/codec/openai_chat"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/internal/service"
)

func (p *Pipeline) completeV2(ctx context.Context, request *service.CompletionRequest) (*service.CompletionResponse, error) {
	canonicalRequest, err := CanonicalChatRequest(request, request.Messages, request.Model)
	if err != nil {
		return nil, err
	}
	result, err := p.v2.Execute(ctx, canonicalRequest, p.v2Options(request))
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("Gateway V2 returned no Chat response")
	}
	if result.Response == nil {
		responseErr := errors.New("Gateway V2 returned no Chat response")
		_, stageErr := service.StageAPIConversationProjectionOutputIfPresent(service.ConversationProjectionOutputRequest{
			CallID: result.CallID, OutputItems: []canonical.Item{}, RequestLogID: result.RequestLogID,
		})
		return nil, errors.Join(responseErr, stageErr, result.FailDelivery(responseErr, false))
	}
	providerResponseID := result.Response.ProviderResponseID
	if providerResponseID == "" {
		providerResponseID = result.Response.ID
	}
	result.Response.Model = request.Model
	if result.Response.CreatedAt == 0 {
		result.Response.CreatedAt = time.Now().Unix()
	}
	encoded, err := openai_chat.EncodeResponseJSON(*result.Response)
	if err != nil {
		_ = result.FailDelivery(err, false)
		return nil, err
	}
	var wire chat.ChatResponse
	if err := json.Unmarshal(encoded, &wire); err != nil {
		_ = result.FailDelivery(err, false)
		return nil, err
	}
	canonicalResponse := cloneCanonicalResponse(*result.Response)
	return &service.CompletionResponse{
		ID: wire.ID, Object: wire.Object, Created: wire.Created, Model: request.Model,
		Choices: wire.Choices, Usage: wire.Usage, SystemFingerprint: wire.SystemFingerprint,
		ServiceTier: wire.ServiceTier, ProviderResponseID: providerResponseID,
		RequestLogID: result.RequestLogID, CallID: result.CallID, AttemptID: result.AttemptID,
		ProviderKeyID: result.Route.KeyID, UpstreamTransport: result.Route.Transport,
		CanonicalResponse: &canonicalResponse,
		CompleteDelivery:  result.CompleteDelivery, FailDelivery: result.FailDelivery,
	}, nil
}

func (p *Pipeline) streamCompleteV2(ctx context.Context, request *service.CompletionRequest) (*StreamSession, error) {
	canonicalRequest, err := CanonicalChatRequest(request, request.Messages, request.Model)
	if err != nil {
		return nil, err
	}
	canonicalRequest.Stream = true
	options := p.v2Options(request)
	options.DeferCallCompletion = true
	result, err := p.v2.Execute(ctx, canonicalRequest, options)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Stream == nil || result.Route == nil {
		return nil, errors.New("Gateway V2 returned no Chat stream")
	}
	reader, writer := io.Pipe()
	session := &StreamSession{
		UpstreamResp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader},
		requestLogID: result.RequestLogID, callID: result.CallID, attemptID: result.AttemptID,
		keyID: result.Route.KeyID, transport: result.Route.Transport, stream: result.Stream,
		done: make(chan struct{}),
	}
	go session.proxyV2ChatStream(ctx, writer, result.Stream, request.Model)
	return session, nil
}

func (p *Pipeline) v2Options(request *service.CompletionRequest) engine.ExecuteOptions {
	deferCallCompletion := request.DownstreamEndpoint == "/v1/chat/completions"
	downstreamEndpoint := request.DownstreamEndpoint
	if downstreamEndpoint == "" {
		downstreamEndpoint = "/v1/chat/completions"
	}
	return engine.ExecuteOptions{
		UserID: request.UserID, TokenID: request.TokenID,
		CallID: request.CallID, RequestID: request.RequestID,
		DownstreamEndpoint: downstreamEndpoint, DownstreamRequest: request.DownstreamRequest,
		ConversationID:      request.ConversationRecordID,
		ProjectConversation: deferCallCompletion,
		DeferCallCompletion: deferCallCompletion,
		BillingKey:          uuid.NewString(), MaxAttempts: 3,
		PrepareTransport: func(_ context.Context, candidate canonical.Request, transportID transport.ID) (canonical.Request, error) {
			if err := adaptChatExtensions(&candidate, transportID); err != nil {
				return canonical.Request{}, err
			}
			return candidate, nil
		},
		PrepareRoute: func(_ context.Context, _ canonical.Request, route *routing.RouteResult) (canonical.Request, error) {
			messages := request.Messages
			previousResponseID := ""
			if request.PreviousResponseID != "" && request.ProviderKeyID == route.KeyID && request.UpstreamTransport == route.Transport {
				messages = request.NewMessages
				previousResponseID = request.PreviousResponseID
			}
			attemptRequest := *request
			attemptRequest.Messages = messages
			wire, err := p.buildChatRequest(&attemptRequest, route)
			if err != nil {
				return canonical.Request{}, err
			}
			wire.Model = request.Model
			wire.Stream = request.Stream
			decoded, err := openai_chat.DecodeRequest(*wire)
			if err != nil {
				return canonical.Request{}, err
			}
			decoded.Model = request.Model
			decoded.PreviousResponseID = previousResponseID
			if previousResponseID != "" {
				decoded.TransportHints = []string{string(route.Transport)}
			}
			return decoded, nil
		},
	}
}

func adaptChatExtensions(request *canonical.Request, transportID transport.ID) error {
	if request == nil || len(request.ClientExtensions["openai_chat.request_extras"]) == 0 || transportID == transport.OpenAIChat {
		return nil
	}
	request.ClientExtensions = cloneClientExtensions(request.ClientExtensions)
	var extras map[string]json.RawMessage
	if err := json.Unmarshal(request.ClientExtensions["openai_chat.request_extras"], &extras); err != nil {
		return err
	}
	if raw := extras["reasoning_effort"]; len(raw) > 0 {
		var effort string
		if err := json.Unmarshal(raw, &effort); err != nil {
			return err
		}
		switch transportID {
		case transport.OpenAIResponses, transport.VolcengineResponsesV3:
			reasoning := cloneReasoning(request.Reasoning)
			reasoning.Effort = effort
			request.Reasoning = reasoning
			delete(extras, "reasoning_effort")
		case transport.AnthropicMessages:
			if request.Reasoning != nil && !transport.HasJSONValue(request.Reasoning.Raw) {
				request.Reasoning = nil
			}
			delete(extras, "reasoning_effort")
		}
	}
	if transportID == transport.OpenAIResponses || transportID == transport.VolcengineResponsesV3 {
		if raw := extras["reasoning"]; len(raw) > 0 {
			if transport.HasJSONValue(raw) {
				var value map[string]json.RawMessage
				if err := json.Unmarshal(raw, &value); err != nil {
					return err
				}
				reasoning := cloneReasoning(request.Reasoning)
				reasoning.Raw = append(json.RawMessage(nil), raw...)
				request.Reasoning = reasoning
			}
			delete(extras, "reasoning")
		}
	}
	switch transportID {
	case transport.AnthropicMessages:
		if raw := extras["thinking"]; len(raw) > 0 {
			if transport.HasJSONValue(raw) {
				request.Reasoning = &canonical.Reasoning{Raw: append(json.RawMessage(nil), raw...)}
			}
			delete(extras, "thinking")
		}
	case transport.VolcengineResponsesV3:
		options := cloneVolcengineOptions(request.ProviderOptions.Volcengine)
		request.ProviderOptions.Volcengine = options
		for key, target := range map[string]*json.RawMessage{
			"thinking": &options.Thinking, "caching": &options.Caching,
			"session": &options.Session, "context_management": &options.ContextManagement,
		} {
			if raw := extras[key]; len(raw) > 0 {
				*target = append(json.RawMessage(nil), raw...)
				delete(extras, key)
			}
		}
		if raw := extras["expire_at"]; len(raw) > 0 {
			var value int64
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			options.ExpireAt = &value
			delete(extras, "expire_at")
		}
	}
	if len(extras) == 0 {
		delete(request.ClientExtensions, "openai_chat.request_extras")
		return nil
	}
	encoded, err := json.Marshal(extras)
	if err != nil {
		return err
	}
	request.ClientExtensions["openai_chat.request_extras"] = encoded
	return nil
}

func cloneClientExtensions(source map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(source))
	for key, raw := range source {
		result[key] = append(json.RawMessage(nil), raw...)
	}
	return result
}

func cloneReasoning(source *canonical.Reasoning) *canonical.Reasoning {
	if source == nil {
		return &canonical.Reasoning{}
	}
	result := *source
	result.Raw = append(json.RawMessage(nil), source.Raw...)
	return &result
}

func cloneVolcengineOptions(source *canonical.VolcengineOptions) *canonical.VolcengineOptions {
	if source == nil {
		return &canonical.VolcengineOptions{}
	}
	result := *source
	result.Thinking = append(json.RawMessage(nil), source.Thinking...)
	result.Caching = append(json.RawMessage(nil), source.Caching...)
	result.Session = append(json.RawMessage(nil), source.Session...)
	result.ContextManagement = append(json.RawMessage(nil), source.ContextManagement...)
	if source.ExpireAt != nil {
		value := *source.ExpireAt
		result.ExpireAt = &value
	}
	if source.Unknown != nil {
		result.Unknown = make(map[string]json.RawMessage, len(source.Unknown))
		for key, raw := range source.Unknown {
			result.Unknown[key] = append(json.RawMessage(nil), raw...)
		}
	}
	return &result
}

// CanonicalChatRequest returns the protocol-neutral request for the supplied
// downstream message set, before route-specific history reduction.
func CanonicalChatRequest(request *service.CompletionRequest, messages []chat.ChatMessage, model string) (canonical.Request, error) {
	wire := chat.ChatRequest{
		Model: model, Messages: messages, Temperature: request.Temperature, MaxTokens: request.MaxTokens,
		MaxCompletionTokens: request.MaxCompletionTokens, TopP: request.TopP,
		FrequencyPenalty: request.FrequencyPenalty, PresencePenalty: request.PresencePenalty,
		Stop: request.Stop, Stream: request.Stream, StreamOptions: request.StreamOptions,
		N: request.N, Logprobs: request.Logprobs, TopLogprobs: request.TopLogprobs,
		Tools: request.Tools, ToolChoice: request.ToolChoice, ParallelToolCalls: request.ParallelToolCalls,
		ResponseFormat: request.ResponseFormat, Seed: request.Seed, User: request.User,
		Modalities: request.Modalities, Audio: request.Audio, Prediction: request.Prediction,
		Store: request.Store, Metadata: request.Metadata, ServiceTier: request.ServiceTier,
	}
	if request.ReasoningEffort != nil {
		wire.ExtraBody = map[string]any{"reasoning_effort": *request.ReasoningEffort}
	}
	decoded, err := openai_chat.DecodeRequest(wire)
	if err != nil {
		return canonical.Request{}, err
	}
	// Keep the semantic capability visible before route-specific thinking is injected.
	if request.ReasoningEffort != nil {
		decoded.Reasoning = &canonical.Reasoning{Effort: *request.ReasoningEffort}
	}
	return decoded, nil
}

func canonicalChatRequest(request *service.CompletionRequest, messages []chat.ChatMessage, model string) (canonical.Request, error) {
	return CanonicalChatRequest(request, messages, model)
}

func cloneCanonicalResponse(source canonical.Response) canonical.Response {
	clone := source
	clone.Output = canonical.CloneItems(source.Output)
	return clone
}

func (s *StreamSession) proxyV2ChatStream(ctx context.Context, writer *io.PipeWriter, stream *engine.StreamResult, publicModel string) {
	defer close(s.done)
	defer stream.Close()
	defer writer.Close()
	sentDone := false
	for {
		event, err := stream.Next(ctx)
		if errors.Is(err, io.EOF) {
			if !sentDone {
				_, _ = writer.Write([]byte("data: [DONE]\n\n"))
			}
			return
		}
		if err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		s.captureV2ProviderID(event)
		if !normalizeV2ChatEvent(&event, publicModel, s.transport) {
			continue
		}
		frame, encodeErr := openai_chat.EncodeSSEFrame(event)
		if encodeErr != nil {
			_ = stream.Abort(encodeErr, false)
			_ = writer.CloseWithError(encodeErr)
			return
		}
		if len(frame) == 0 {
			continue
		}
		sentDone = sentDone || bytes.Contains(frame, []byte("[DONE]"))
		if _, err := writer.Write(frame); err != nil {
			_ = stream.Abort(err, true)
			return
		}
	}
}

func normalizeV2ChatEvent(event *canonical.Event, publicModel string, upstreamTransport transport.ID) bool {
	if event == nil {
		return false
	}
	if event.Type == canonical.EventProviderProof {
		return false
	}
	if event.Type == canonical.EventRaw {
		return upstreamTransport == transport.OpenAIChat
	}
	event.Raw = nil
	event.RawType = ""
	if event.Response != nil {
		event.Response.Model = publicModel
	}
	return true
}

func (s *StreamSession) captureV2ProviderID(event canonical.Event) {
	providerID := event.ProviderResponseID
	if event.Response != nil {
		if event.Response.ProviderResponseID != "" {
			providerID = event.Response.ProviderResponseID
		} else if event.Response.ID != "" {
			providerID = event.Response.ID
		}
	}
	if providerID == "" {
		return
	}
	s.mu.Lock()
	s.providerID = providerID
	s.mu.Unlock()
}
