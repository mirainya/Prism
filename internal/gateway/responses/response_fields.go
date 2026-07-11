package responses

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
)

func applyResponseRequestFields(response *protocol.Response, request *protocol.Request) {
	response.Instructions = request.Instructions
	if request.MaxOutputTokens > 0 {
		value := request.MaxOutputTokens
		response.MaxOutputTokens = &value
	}
	response.ParallelToolCalls = request.ParallelToolCalls == nil || *request.ParallelToolCalls
	response.Reasoning = append(json.RawMessage(nil), request.Reasoning...)
	response.Temperature = request.Temperature
	response.Text = append(json.RawMessage(nil), request.Text...)
	response.ToolChoice = append(json.RawMessage(nil), request.ToolChoice...)
	if len(request.Tools) > 0 {
		response.Tools = append(json.RawMessage(nil), request.Tools...)
	} else {
		response.Tools = json.RawMessage(`[]`)
	}
	response.TopP = request.TopP
	response.Truncation = request.Truncation
	if response.Truncation == "" {
		response.Truncation = "disabled"
	}
	response.User = request.User
	if request.Metadata != nil {
		response.Metadata = make(map[string]string, len(request.Metadata))
		for key, value := range request.Metadata {
			response.Metadata[key] = value
		}
	}
}

func compactID() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }
