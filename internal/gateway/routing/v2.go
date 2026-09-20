package routing

import (
	"context"
	"encoding/json"

	"github.com/mirainya/Prism/internal/model"
)

type ExecutionMode string

const (
	ExecutionModeChat               ExecutionMode = "chat"
	ExecutionModeResponsesNative    ExecutionMode = "responses_native"
	ExecutionModeResponsesConverted ExecutionMode = "responses_converted"
)

// RouteOptions describes the public operation and endpoint dialects a request
// may use. Preferred transports win within the same route priority.
type RouteOptions struct {
	SelectionKey        string
	OperationMethod     string
	OperationPath       string
	RequiredTaskScope   string
	AllowedTransports   []model.UpstreamTransport
	PreferredTransports []model.UpstreamTransport
	ExcludeChannels     []uint
	ExcludeKeys         []uint
	ExcludeAttempts     []TransportAttempt
	ResponsesRequest    bool
}

// TransportAttempt identifies one concrete credential and endpoint dialect.
type TransportAttempt struct {
	KeyID     uint
	Transport model.UpstreamTransport
}

// SelectTransport resolves requests exclusively through the active catalog.
// Legacy routing tables are migration sources only.
func (r *Router) SelectTransport(ctx context.Context, modelName string, requirements RouteRequirements, options RouteOptions) (*RouteResult, error) {
	if ctx == nil || options.SelectionKey == "" {
		return nil, ErrInvalidSelectionKey
	}
	active, err := r.unified.active(ctx)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrNoRoute
	}
	return r.selectUnified(ctx, modelName, requirements, options)
}

func semanticCapabilities(raw []byte) map[Capability]bool {
	result := make(map[Capability]bool)
	if len(raw) == 0 {
		return result
	}
	var object map[string]bool
	if json.Unmarshal(raw, &object) == nil && object != nil {
		for name, enabled := range object {
			if enabled {
				result[Capability(name)] = true
			}
		}
		return result
	}
	var declared []string
	if json.Unmarshal(raw, &declared) == nil {
		for _, name := range declared {
			result[Capability(name)] = true
		}
	}
	return result
}

func supportsSemanticRequirements(capabilities map[Capability]bool, requirements RouteRequirements) bool {
	for capability, required := range requirements {
		if required && !capabilities[capability] {
			return false
		}
	}
	return true
}

func transportAllowed(transport model.UpstreamTransport, allowed []model.UpstreamTransport) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		if value == transport {
			return true
		}
	}
	return false
}

func taskScopeAllowed(taskScope string, required string) bool {
	return required == "" || taskScope == required
}

func transportRank(transport model.UpstreamTransport, preferred []model.UpstreamTransport) int {
	for index, value := range preferred {
		if value == transport {
			return index
		}
	}
	return len(preferred)
}

func executionMode(responsesRequest bool, transport model.UpstreamTransport) ExecutionMode {
	if !responsesRequest {
		return ExecutionModeChat
	}
	if transport == model.UpstreamTransportOpenAIResponses || transport == model.UpstreamTransportVolcengineV3 {
		return ExecutionModeResponsesNative
	}
	return ExecutionModeResponsesConverted
}
