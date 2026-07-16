package responses

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	openairesponses "github.com/mirainya/Prism/internal/gateway/codec/openai_responses"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type responseConversationProjection struct {
	conversationID     uint
	previousResponseID string
	inputItems         []canonical.Item
	response           *canonical.Response
}

func newResponseConversationProjection(request *protocol.Request, conversationID uint) (*responseConversationProjection, error) {
	if request == nil {
		return nil, errors.New("Responses conversation projection request is required")
	}
	decoded, err := openairesponses.DecodeRequest(*cloneResponseRequest(request))
	if err != nil {
		return nil, err
	}
	items := canonical.CloneItems(decoded.Items)
	if strings.TrimSpace(decoded.Instructions) != "" {
		instruction := canonical.Item{
			Type: "message", Role: canonical.RoleSystem,
			Content: []canonical.Content{{Type: "input_text", Text: decoded.Instructions}},
		}
		items = append([]canonical.Item{instruction}, items...)
	}
	return &responseConversationProjection{
		conversationID:     conversationID,
		previousResponseID: strings.TrimSpace(request.PreviousResponseID),
		inputItems:         items,
	}, nil
}

func cloneResponseConversationProjection(source *responseConversationProjection) *responseConversationProjection {
	if source == nil {
		return nil
	}
	clone := *source
	clone.inputItems = canonical.CloneItems(source.inputItems)
	clone.response = cloneCanonicalResponse(source.response)
	return &clone
}

func cloneCanonicalResponse(source *canonical.Response) *canonical.Response {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Output = canonical.CloneItems(source.Output)
	if source.Error != nil {
		errorCopy := *source.Error
		errorCopy.Raw = append(json.RawMessage(nil), source.Error.Raw...)
		clone.Error = &errorCopy
	}
	return &clone
}

func (projection *responseConversationProjection) withResponse(response *canonical.Response) *responseConversationProjection {
	clone := cloneResponseConversationProjection(projection)
	if clone == nil {
		clone = &responseConversationProjection{}
	}
	clone.response = cloneCanonicalResponse(response)
	return clone
}

func responseProjectionFromStreamSummary(projection *responseConversationProjection, summary *V2StreamSummary) *responseConversationProjection {
	if summary == nil {
		return cloneResponseConversationProjection(projection)
	}
	response := cloneCanonicalResponse(summary.Response)
	if response == nil {
		response = &canonical.Response{}
	} else {
		response.Output = openairesponses.RestoreFunctionCallProofCarriers(response.Output)
	}
	if response.ProviderResponseID == "" {
		response.ProviderResponseID = summary.ProviderResponseID
	}
	if summary.Usage != nil {
		usage := *summary.Usage
		response.Usage = &usage
	}
	if summary.Error != nil {
		errorCopy := *summary.Error
		response.Error = &errorCopy
	}
	switch summary.Terminal {
	case canonical.EventCompleted:
		response.Status = "completed"
	case canonical.EventIncomplete:
		response.Status = "incomplete"
	case canonical.EventFailed, canonical.EventError:
		response.Status = "failed"
	}
	return projection.withResponse(response)
}

func projectResponseConversationBestEffort(record *model.AIResponse) {
	if _, err := projectResponseConversation(record); err != nil {
		logResponseConversationProjectionError("project Responses conversation", responseRecordCallID(record), err)
	}
}

func projectResponseConversation(record *model.AIResponse) (uint, error) {
	if record == nil || strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.CallID) == "" {
		return 0, errors.New("Responses conversation projection record is incomplete")
	}
	conversationID, err := service.ProjectPendingAPIConversation(record.CallID)
	if err != nil {
		return 0, err
	}
	if err := linkResponseReplayCalls(record.ID, conversationID); err != nil {
		logResponseConversationProjectionError("link Responses replay conversations", record.CallID, err)
	}
	return conversationID, nil
}

// stageResponseConversationReadyBestEffort marks output ready immediately
// before a terminal transition. Empty fallback output never replaces a real
// or partial snapshot written by the engine.
func stageResponseConversationReadyBestEffort(record *model.AIResponse, projection *responseConversationProjection) {
	if projection == nil || projection.response == nil {
		stageResponseConversationOutputIfMissingBestEffort(record, projection)
		return
	}
	stageResponseConversationOutputBestEffort(record, projection)
}

func stageResponseConversationInputTx(tx *gorm.DB, record *model.AIResponse, source *responseConversationProjection) error {
	request, err := responseConversationInputRequest(record, source)
	if err != nil {
		return err
	}
	return service.StageAPIConversationProjectionInputTx(tx, request)
}

func responseConversationInputRequest(record *model.AIResponse, source *responseConversationProjection) (service.ConversationProjectionInputRequest, error) {
	if record == nil || strings.TrimSpace(record.CallID) == "" {
		return service.ConversationProjectionInputRequest{}, errors.New("Responses conversation projection record is incomplete")
	}
	projection := cloneResponseConversationProjection(source)
	if projection == nil {
		var request protocol.Request
		if len(bytes.TrimSpace(record.RequestJSON)) > 0 && !bytes.Equal(bytes.TrimSpace(record.RequestJSON), []byte("null")) {
			if err := json.Unmarshal(record.RequestJSON, &request); err != nil {
				return service.ConversationProjectionInputRequest{}, fmt.Errorf("decode stored Responses request: %w", err)
			}
		} else if len(bytes.TrimSpace(record.InputItems)) > 0 {
			request = protocol.Request{
				Model: record.Model, Input: append(json.RawMessage(nil), record.InputItems...),
				PreviousResponseID: record.PreviousResponseID,
			}
		} else {
			return service.ConversationProjectionInputRequest{}, errors.New("stored Responses conversation input is missing")
		}
		conversationID := uint(0)
		var call model.APICall
		if err := model.DB().Select("conversation_id").First(&call, "id = ?", record.CallID).Error; err == nil {
			conversationID = call.ConversationID
		}
		var err error
		projection, err = newResponseConversationProjection(&request, conversationID)
		if err != nil {
			return service.ConversationProjectionInputRequest{}, err
		}
	}
	if projection.previousResponseID == "" {
		projection.previousResponseID = record.PreviousResponseID
	}
	return service.ConversationProjectionInputRequest{
		CallID: record.CallID, ConversationID: projection.conversationID,
		PreviousResponseID: projection.previousResponseID,
		InputItems:         canonical.CloneItems(projection.inputItems),
	}, nil
}

func stageResponseConversationOutputBestEffort(record *model.AIResponse, projection *responseConversationProjection) {
	if err := stageResponseConversationOutput(record, projection); err != nil {
		logResponseConversationProjectionError("stage Responses conversation output", responseRecordCallID(record), err)
	}
}

func stageResponseConversationOutput(record *model.AIResponse, projection *responseConversationProjection) error {
	request, err := responseConversationOutputRequest(record, projection)
	if err != nil {
		return err
	}
	_, err = service.StageAPIConversationProjectionOutputIfPresent(request)
	return err
}

func stageResponseConversationOutputIfMissingBestEffort(record *model.AIResponse, projection *responseConversationProjection) {
	request, err := responseConversationOutputRequest(record, projection)
	if err == nil {
		_, err = service.StageAPIConversationProjectionOutputIfMissing(request)
	}
	if err != nil {
		logResponseConversationProjectionError("stage missing Responses conversation output", responseRecordCallID(record), err)
	}
}

func responseConversationOutputRequest(record *model.AIResponse, projection *responseConversationProjection) (service.ConversationProjectionOutputRequest, error) {
	if record == nil || strings.TrimSpace(record.CallID) == "" {
		return service.ConversationProjectionOutputRequest{}, errors.New("Responses conversation projection record is incomplete")
	}
	response := (*canonical.Response)(nil)
	if projection != nil {
		response = cloneCanonicalResponse(projection.response)
	}
	if response == nil {
		var err error
		response, err = canonicalResponseFromRecord(record)
		if err != nil {
			return service.ConversationProjectionOutputRequest{}, err
		}
	}
	providerResponseID := record.ProviderResponseID
	if providerResponseID == "" && response != nil {
		providerResponseID = response.ProviderResponseID
	}
	output := []canonical.Item(nil)
	finishReason := ""
	if response != nil {
		output = canonical.CloneItems(response.Output)
		finishReason = response.FinishReason
	}
	return service.ConversationProjectionOutputRequest{
		CallID: record.CallID, OutputItems: output, RequestLogID: record.RequestLogID,
		ProviderResponseID: providerResponseID, FinishReason: finishReason,
	}, nil
}

func canonicalResponseFromRecord(record *model.AIResponse) (*canonical.Response, error) {
	if record == nil {
		return nil, nil
	}
	var response protocol.Response
	if len(bytes.TrimSpace(record.ResponseJSON)) > 0 && !bytes.Equal(bytes.TrimSpace(record.ResponseJSON), []byte("null")) {
		if err := json.Unmarshal(record.ResponseJSON, &response); err != nil {
			return nil, fmt.Errorf("decode stored Responses response: %w", err)
		}
	} else {
		response = protocol.Response{
			ID: record.ID, Model: record.Model, Status: record.Status,
			Output: append(json.RawMessage(nil), record.OutputItems...),
		}
	}
	result := &canonical.Response{
		ID: response.ID, Model: response.Model, Status: response.Status,
		CreatedAt: response.CreatedAt, ProviderResponseID: record.ProviderResponseID,
	}
	if len(bytes.TrimSpace(response.Output)) > 0 && !bytes.Equal(bytes.TrimSpace(response.Output), []byte("null")) {
		items, err := openairesponses.DecodeItems(response.Output)
		if err != nil {
			return nil, fmt.Errorf("decode stored Responses output: %w", err)
		}
		result.Output = items
	}
	if response.Error != nil {
		result.Error = &canonical.Error{
			Type: response.Error.Type, Code: response.Error.Code,
			Message: response.Error.Message, Param: response.Error.Param,
		}
	}
	if result.Status == "" {
		result.Status = record.Status
	}
	return result, nil
}

func linkResponseReplayCalls(responseID string, conversationID uint) error {
	if strings.TrimSpace(responseID) == "" || conversationID == 0 {
		return nil
	}
	return model.DB().Model(&model.APICall{}).
		Where("resource_type = ? AND resource_id = ? AND operation = ? AND conversation_id = 0", "response", responseID, "responses.replay").
		Update("conversation_id", conversationID).Error
}

func responseAPICallStatus(callID string) model.APICallStatus {
	if strings.TrimSpace(callID) == "" {
		return ""
	}
	var status model.APICallStatus
	_ = model.DB().Model(&model.APICall{}).Where("id = ?", callID).Pluck("status", &status).Error
	return status
}

func linkResponseReplayBestEffort(callID string, record *model.AIResponse) {
	if strings.TrimSpace(callID) == "" || record == nil {
		return
	}
	var original model.APICall
	err := model.DB().Select("conversation_id").First(&original, "id = ?", record.CallID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && original.ConversationID == 0) {
		return
	}
	if err != nil {
		logResponseConversationProjectionError("load original Responses conversation", callID, err)
		return
	}
	result := model.DB().Model(&model.APICall{}).
		Where("id = ? AND operation = ? AND conversation_id = 0", callID, "responses.replay").
		Update("conversation_id", original.ConversationID)
	if result.Error != nil {
		logResponseConversationProjectionError("link Responses replay conversation", callID, result.Error)
	}
}

func responseRecordCallID(record *model.AIResponse) string {
	if record == nil {
		return ""
	}
	return record.CallID
}

func logResponseConversationProjectionError(action, callID string, err error) {
	if err == nil || logger.L == nil {
		return
	}
	logger.Error(action, zap.String("call_id", callID), zap.Error(err))
}
