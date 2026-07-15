package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"gorm.io/gorm"
)

const (
	prismConversationIDHeader         = "X-Prism-Conversation-ID"
	maxPublicConversationRequestBytes = 32 * 1024 * 1024
)

var errConversationIDConflict = errors.New("conversation_id conflicts with X-Prism-Conversation-ID")

func parsePrismConversationID(bodyValue, headerValue string) (uint, error) {
	bodyValue = strings.TrimSpace(bodyValue)
	headerValue = strings.TrimSpace(headerValue)
	bodyID, err := parsePositiveConversationID(bodyValue)
	if err != nil {
		return 0, fmt.Errorf("invalid conversation_id: %w", err)
	}
	headerID, err := parsePositiveConversationID(headerValue)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", prismConversationIDHeader, err)
	}
	if bodyID != 0 && headerID != 0 && bodyID != headerID {
		return 0, errConversationIDConflict
	}
	if headerID != 0 {
		return headerID, nil
	}
	return bodyID, nil
}

func parseJSONConversationID(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	var stringValue string
	if err := json.Unmarshal(raw, &stringValue); err == nil {
		return stringValue, nil
	}
	var numericValue uint64
	if err := json.Unmarshal(raw, &numericValue); err == nil {
		return strconv.FormatUint(numericValue, 10), nil
	}
	return "", errors.New("conversation_id must be a positive integer or a numeric string")
}

func parsePositiveConversationID(value string) (uint, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		return 0, errors.New("must be a positive integer")
	}
	return uint(parsed), nil
}

func requestJSONHasField(data []byte, field string) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(data, &object) == nil && object[field] != nil
}

func createAPIConversationCall(
	callRequest *service.StartCallRequest,
	projectionRequest service.ConversationProjectionInputRequest,
) error {
	if callRequest == nil {
		return errors.New("API call request is required")
	}
	callRequest.ProjectConversation = true
	return model.DB().Transaction(func(tx *gorm.DB) error {
		call, err := service.NewAPICallService().StartCallTx(tx, callRequest)
		if err != nil {
			return err
		}
		projectionRequest.CallID = call.ID
		return service.StageAPIConversationProjectionInputTx(tx, projectionRequest)
	})
}

func projectAPIConversationBestEffort(action string, request service.ConversationProjectionRequest) bool {
	outputRequest := service.ConversationProjectionOutputRequest{
		CallID: request.CallID, OutputItems: canonical.CloneItems(request.OutputItems),
		RequestLogID: request.RequestLogID, ProviderResponseID: request.ProviderResponseID,
		FinishReason: request.FinishReason,
	}
	var stageErr error
	if request.OutputItems != nil || request.RequestLogID > 0 || request.ProviderResponseID != "" || request.FinishReason != "" {
		_, stageErr = service.StageAPIConversationProjectionOutputIfPresent(outputRequest)
	} else {
		_, stageErr = service.StageAPIConversationProjectionOutputIfMissing(outputRequest)
	}
	if stageErr != nil {
		logDeliveryError("stage "+action, request.CallID, stageErr)
	}
	_, projectErr := service.ProjectPendingAPIConversation(request.CallID)
	if projectErr != nil {
		logDeliveryError(action, request.CallID, projectErr)
		return false
	}
	return stageErr == nil
}

func stageAPIConversationOutputBestEffort(action string, request service.ConversationProjectionOutputRequest) bool {
	_, err := service.StageAPIConversationProjectionOutputIfPresent(request)
	if err != nil {
		logDeliveryError(action, request.CallID, err)
		return false
	}
	return true
}

func canonicalProviderResponseID(response canonical.Response) string {
	if response.ProviderResponseID != "" {
		return response.ProviderResponseID
	}
	return response.ID
}

func projectCanonicalResponseBestEffort(
	action string,
	request service.ConversationProjectionRequest,
	response canonical.Response,
	requestLogID uint,
	providerResponseID string,
	keyID uint,
	upstreamTransport model.UpstreamTransport,
) bool {
	request.RequestLogID = requestLogID
	request.OutputItems = canonical.CloneItems(response.Output)
	request.FinishReason = response.FinishReason
	request.ProviderResponseID = providerResponseID
	if request.ProviderResponseID == "" {
		request.ProviderResponseID = canonicalProviderResponseID(response)
	}
	request.Provenance = service.ConversationProvenance{KeyID: keyID, Transport: upstreamTransport}
	return projectAPIConversationBestEffort(action, request)
}
