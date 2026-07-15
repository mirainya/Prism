package responses

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/httputil"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Pipeline struct {
	billing *service.BillingService
	calls   *service.APICallService
	v2      *V2Executor
	engine  *engine.Engine
}

var backgroundCancels sync.Map

func New(executionEngine *engine.Engine) *Pipeline {
	if executionEngine == nil {
		panic("Gateway V2 engine is required")
	}
	executor, err := NewV2Executor(executionEngine)
	if err != nil {
		panic(err)
	}
	return &Pipeline{
		billing: service.NewBillingService(), calls: service.NewAPICallService(),
		engine: executionEngine, v2: executor,
	}
}

type Result struct {
	Response                 *protocol.Response
	Record                   *model.AIResponse
	CallID                   string
	AttemptID                uint
	PublicPreviousResponseID string
	V2Stream                 *engine.StreamResult
	IdempotentReplay         bool
	idempotencyClaim         *responseIdempotencyClaim
	execution                *engine.Result
	conversation             *responseConversationProjection
}

func (r *Result) CompleteDelivery() error {
	if r == nil || strings.TrimSpace(r.CallID) == "" {
		return nil
	}
	var err error
	if r.execution != nil {
		err = r.execution.CompleteDelivery()
	} else {
		projection, projectionErr := terminalResponseConversationOutputRequest(r.Record, r.conversation, r.IdempotentReplay)
		if projectionErr != nil {
			return projectionErr
		}
		err = service.NewAPICallService().CompleteCall(r.CallID, &service.CompleteCallRequest{
			FinalAttemptID: r.AttemptID, HTTPStatus: http.StatusOK, ConversationProjection: projection,
		})
	}
	if err == nil {
		if r.IdempotentReplay {
			linkResponseReplayBestEffort(r.CallID, r.Record)
		} else {
			projectResponseConversationBestEffort(r.Record)
		}
	}
	return err
}

func (r *Result) FailDelivery(err error, clientDisconnected bool) error {
	if r == nil || strings.TrimSpace(r.CallID) == "" {
		return nil
	}
	if err == nil {
		err = errors.New("downstream response delivery failed")
	}
	var deliveryErr error
	if r.execution != nil {
		deliveryErr = r.execution.FailDelivery(err, clientDisconnected)
	} else {
		projection, projectionErr := terminalResponseConversationOutputRequest(r.Record, r.conversation, r.IdempotentReplay)
		if projectionErr != nil {
			return projectionErr
		}
		if clientDisconnected {
			deliveryErr = service.NewAPICallService().CancelCall(r.CallID, &service.CancelCallRequest{
				FinalAttemptID: r.AttemptID, ErrorType: "cancelled", ErrorCode: "client_disconnected",
				ErrorMessage: err.Error(), ClientDisconnected: true, ConversationProjection: projection,
			})
		} else {
			deliveryErr = service.NewAPICallService().FailCall(r.CallID, &service.FailCallRequest{
				FinalAttemptID: r.AttemptID, HTTPStatus: http.StatusBadGateway,
				ErrorType: "server_error", ErrorCode: "downstream_delivery_failed", ErrorMessage: err.Error(),
				ConversationProjection: projection,
			})
		}
	}
	if deliveryErr == nil {
		if r.IdempotentReplay {
			linkResponseReplayBestEffort(r.CallID, r.Record)
		} else {
			projectResponseConversationBestEffort(r.Record)
		}
	}
	return deliveryErr
}

func terminalResponseConversationOutputRequest(
	record *model.AIResponse,
	projection *responseConversationProjection,
	skip bool,
) (*service.ConversationProjectionOutputRequest, error) {
	if skip || record == nil || strings.TrimSpace(record.CallID) == "" {
		return nil, nil
	}
	request, err := responseConversationOutputRequest(record, projection)
	if err != nil {
		return nil, err
	}
	return &request, nil
}

type responseCallError struct {
	callID string
	err    error
}

func (e *responseCallError) Error() string { return e.err.Error() }
func (e *responseCallError) Unwrap() error { return e.err }

func withResponseCallError(callID string, err error) error {
	if callID == "" || err == nil {
		return err
	}
	var existing *responseCallError
	if errors.As(err, &existing) {
		return err
	}
	return &responseCallError{callID: callID, err: err}
}

func CallIDFromError(err error) string {
	var callErr *responseCallError
	if errors.As(err, &callErr) {
		return callErr.callID
	}
	return ""
}

func findIdempotentResponse(ctx context.Context, tokenID uint, idempotencyKey string, requestJSON []byte) (*Result, error) {
	requestHash := hashResponseRequest(requestJSON)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var existing model.AIResponse
		if err := model.DB().WithContext(ctx).Where("token_id = ? AND idempotency_key = ?", tokenID, idempotencyKey).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil
			}
			return nil, err
		}
		if existing.RequestHash != "" {
			if existing.RequestHash != requestHash {
				return nil, domain.ErrBadRequest("Idempotency-Key was already used with a different request")
			}
		} else if !bytes.Equal(bytes.TrimSpace(existing.RequestJSON), bytes.TrimSpace(requestJSON)) {
			return nil, domain.ErrBadRequest("Idempotency-Key was already used with a different request")
		}
		if storedResponseReplayReady(&existing) {
			if storedResponseTerminal(&existing) && !existing.CreatedAt.Add(responseIdempotencyResultTTL).After(time.Now()) {
				result := model.DB().WithContext(ctx).Model(&model.AIResponse{}).
					Where("id = ? AND token_id = ? AND idempotency_key = ?", existing.ID, tokenID, idempotencyKey).
					UpdateColumn("idempotency_key", "internal:"+existing.ID)
				if result.Error != nil {
					return nil, result.Error
				}
				return nil, nil
			}
			return &Result{Response: responseFromRecord(&existing), Record: &existing}, nil
		}

		timer := time.NewTimer(responseIdempotencyPollDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func storedResponseReplayReady(record *model.AIResponse) bool {
	if record == nil {
		return false
	}
	if record.Background && len(record.ResponseJSON) > 0 {
		return true
	}
	return storedResponseTerminal(record)
}

func storedResponseTerminal(record *model.AIResponse) bool {
	if record == nil {
		return false
	}
	switch record.Status {
	case "completed", "failed", "incomplete", "cancelled":
		return true
	default:
		return false
	}
}

func (p *Pipeline) recordIdempotentReplay(existing *Result, requestID string, requestJSON []byte) (*Result, error) {
	if existing == nil || existing.Record == nil {
		return existing, nil
	}
	conversationID := uint(0)
	if existing.Record.CallID != "" {
		var originalCall model.APICall
		if err := model.DB().Select("conversation_id").First(&originalCall, "id = ?", existing.Record.CallID).Error; err != nil {
			return nil, err
		}
		conversationID = originalCall.ConversationID
	}
	callID := service.GenerateAPICallID()
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		call, err := p.callService().StartCallTx(tx, &service.StartCallRequest{
			ID: callID, RequestID: requestID,
			UserID: existing.Record.UserID, TokenID: existing.Record.TokenID,
			Endpoint: "/v1/responses", Operation: "responses.replay", Model: existing.Record.Model,
			Background: existing.Record.Background, Store: existing.Record.Store,
			ResourceType: "response", ResourceID: existing.Record.ID,
			ConversationID: conversationID,
		})
		if err != nil {
			return err
		}
		return p.callService().MarkCallRunningTx(tx, call.ID)
	})
	if err != nil {
		return nil, err
	}
	existing.CallID = callID
	existing.IdempotentReplay = true
	linkResponseReplayBestEffort(callID, existing.Record)
	p.callService().RecordPayloadBestEffort(&model.APICallPayload{
		CallID: callID, Kind: model.APICallPayloadRequest,
		ContentType: "application/json", Data: requestJSON,
	})
	p.recordDownstreamResponse(callID, 0, existing.Response)
	return existing, nil
}

func hashResponseRequest(requestJSON []byte) string {
	sum := sha256.Sum256(bytes.TrimSpace(requestJSON))
	return fmt.Sprintf("%x", sum[:])
}

func responseIdempotencyRequestJSON(requestJSON []byte, conversationID uint) []byte {
	if conversationID == 0 {
		return requestJSON
	}
	encoded, err := json.Marshal(struct {
		Request        json.RawMessage `json:"request"`
		ConversationID uint            `json:"prism_conversation_id"`
	}{Request: requestJSON, ConversationID: conversationID})
	if err != nil {
		return requestJSON
	}
	return encoded
}

func cloneResponseRequest(req *protocol.Request) *protocol.Request {
	encoded, _ := json.Marshal(req)
	var cloned protocol.Request
	_ = json.Unmarshal(encoded, &cloned)
	return &cloned
}

type permanentBackgroundError struct{ err error }

func (e *permanentBackgroundError) Error() string { return e.err.Error() }
func (e *permanentBackgroundError) Unwrap() error { return e.err }

func IsPermanentBackgroundError(err error) bool {
	var permanent *permanentBackgroundError
	return errors.As(err, &permanent)
}

func claimResponseFinalization(record *model.AIResponse) (bool, error) {
	if record != nil && record.Status == "finalizing" {
		return true, nil
	}
	result := model.DB().Model(record).
		Where("status IN ?", []string{"in_progress", "result_ready", "finalizing"}).
		Update("status", "finalizing")
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected > 0 {
		record.Status = "finalizing"
		return true, nil
	}
	return false, nil
}

func isResponseCancelled(id string) bool {
	var status string
	if model.DB().Model(&model.AIResponse{}).Where("id = ?", id).Pluck("status", &status).Error != nil {
		return false
	}
	return status == "cancelling" || status == "cancelled" || status == "refund_pending_cancelled"
}

func createRecord(userID, tokenID uint, req *protocol.Request, requestJSON []byte, requestHash string, inputItems datatypes.JSON, publicPreviousResponseID, key, requestID string, route *routing.RouteResult, projection *responseConversationProjection, conversationIDs ...uint) (*model.AIResponse, error) {
	metadata, _ := json.Marshal(req.Metadata)
	responseID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	store := true
	if req.Store != nil {
		store = *req.Store
	}
	storedKey := "internal:" + responseID
	if store && key != "" {
		storedKey = key
	}
	if !store {
		metadata = nil
	}
	var storedRequest, storedInput datatypes.JSON
	if store {
		storedRequest = append(datatypes.JSON(nil), requestJSON...)
		storedInput = append(datatypes.JSON(nil), inputItems...)
	}
	record := &model.AIResponse{
		ID: responseID, UserID: userID, TokenID: tokenID,
		Model: req.Model, Status: "in_progress", Background: req.Background, Store: store,
		PreviousResponseID: publicPreviousResponseID,
		RequestJSON:        storedRequest, RequestHash: requestHash,
		InputItems: storedInput, Metadata: metadata,
		IdempotencyKey: storedKey, CreatedAt: time.Now(),
	}
	if route != nil {
		record.ChannelID = route.ChannelID
		record.KeyID = route.KeyID
		record.UpstreamTransport = route.Transport
	}
	if err := createResponseWithCall(record, requestID, req.Stream, projection, conversationIDs...); err != nil {
		return nil, err
	}
	return record, nil
}

func createResponseWithCall(record *model.AIResponse, requestID string, stream bool, projection *responseConversationProjection, conversationIDs ...uint) error {
	if record == nil {
		return errors.New("response record is required")
	}
	if record.CallID == "" {
		record.CallID = service.GenerateAPICallID()
	}
	store := record.Store
	return model.DB().Transaction(func(tx *gorm.DB) error {
		if _, err := service.NewAPICallService().StartCallTx(tx, responseCallRequest(record, requestID, stream, conversationIDs...)); err != nil {
			return err
		}
		if err := tx.Create(record).Error; err != nil {
			return err
		}
		if !store {
			if err := tx.Model(record).UpdateColumn("store", false).Error; err != nil {
				return err
			}
			record.Store = false
		}
		return stageResponseConversationInputTx(tx, record, projection)
	})
}

func responseCallRequest(record *model.AIResponse, requestID string, stream bool, conversationIDs ...uint) *service.StartCallRequest {
	request := &service.StartCallRequest{
		ID: record.CallID, RequestID: requestID, UserID: record.UserID, TokenID: record.TokenID,
		Endpoint: "/v1/responses", Operation: "responses", Model: record.Model,
		IsStream: stream, Background: record.Background, Store: record.Store,
		ResourceType: "response", ResourceID: record.ID,
		ProjectConversation: true,
	}
	if len(conversationIDs) > 0 {
		request.ConversationID = conversationIDs[0]
	}
	return request
}

func ensureResponseCall(record *model.AIResponse) (string, error) {
	if record == nil {
		return "", errors.New("response record is required")
	}
	if record.CallID == "" {
		callID := service.GenerateAPICallID()
		result := model.DB().Model(&model.AIResponse{}).
			Where("id = ? AND call_id = ''", record.ID).
			Update("call_id", callID)
		if result.Error != nil {
			return "", result.Error
		}
		if result.RowsAffected > 0 {
			record.CallID = callID
		} else if err := model.DB().Select("call_id").First(record, "id = ?", record.ID).Error; err != nil {
			return "", err
		}
	}

	var call model.APICall
	err := model.DB().Select("id", "request_id", "status", "project_conversation").First(&call, "id = ?", record.CallID).Error
	if err == nil {
		if !call.ProjectConversation && (call.Status == model.APICallStatusReceived || call.Status == model.APICallStatusInProgress) {
			projectionInput, projectionErr := responseConversationInputRequest(record, nil)
			if projectionErr != nil {
				return "", fmt.Errorf("rebuild legacy Responses conversation input: %w", projectionErr)
			}
			projectionErr = model.DB().Transaction(func(tx *gorm.DB) error {
				updated := tx.Model(&model.APICall{}).
					Where("id = ? AND status IN ?", call.ID, []model.APICallStatus{
						model.APICallStatusReceived, model.APICallStatusInProgress,
					}).
					Update("project_conversation", true)
				if updated.Error != nil {
					return updated.Error
				}
				if updated.RowsAffected == 0 {
					var current model.APICall
					if err := tx.Select("status", "project_conversation").First(&current, "id = ?", call.ID).Error; err != nil {
						return err
					}
					if !current.ProjectConversation || (current.Status != model.APICallStatusReceived && current.Status != model.APICallStatusInProgress) {
						return errors.New("legacy Responses call is no longer active")
					}
				}
				projectionInput.CallID = call.ID
				return service.StageAPIConversationProjectionInputTx(tx, projectionInput)
			})
			if projectionErr != nil {
				return "", projectionErr
			}
		}
		return call.RequestID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	projectionInput, err := responseConversationInputRequest(record, nil)
	if err != nil {
		return "", fmt.Errorf("rebuild Responses conversation input: %w", err)
	}
	var created *model.APICall
	err = model.DB().Transaction(func(tx *gorm.DB) error {
		var createErr error
		created, createErr = service.NewAPICallService().StartCallTx(tx, responseCallRequest(record, "", false))
		if createErr != nil {
			return createErr
		}
		projectionInput.CallID = created.ID
		return service.StageAPIConversationProjectionInputTx(tx, projectionInput)
	})
	if err != nil {
		var existing model.APICall
		if lookupErr := model.DB().Select("request_id").First(&existing, "id = ?", record.CallID).Error; lookupErr != nil {
			return "", err
		}
		return existing.RequestID, nil
	}
	return created.RequestID, nil
}

func setPublicPreviousResponseID(response *protocol.Response, id string) {
	response.PreviousResponseID = nil
	if id != "" {
		value := id
		response.PreviousResponseID = &value
	}
}

func completeRecord(record *model.AIResponse, response *protocol.Response, idempotencyClaims ...*responseIdempotencyClaim) error {
	return updateCompletedRecord(record, response, nil, true, idempotencyClaims...)
}

func completeRecordWithProjection(
	record *model.AIResponse,
	response *protocol.Response,
	projection *responseConversationProjection,
	idempotencyClaims ...*responseIdempotencyClaim,
) error {
	return updateCompletedRecord(record, response, projection, true, idempotencyClaims...)
}

func persistCompletedRecord(record *model.AIResponse, response *protocol.Response, idempotencyClaims ...*responseIdempotencyClaim) error {
	return updateCompletedRecord(record, response, nil, false, idempotencyClaims...)
}

func updateCompletedRecord(
	record *model.AIResponse,
	response *protocol.Response,
	projection *responseConversationProjection,
	completeCall bool,
	idempotencyClaims ...*responseIdempotencyClaim,
) error {
	var idempotencyClaim *responseIdempotencyClaim
	if len(idempotencyClaims) > 0 {
		idempotencyClaim = idempotencyClaims[0]
	}
	if idempotencyClaim != nil {
		defer idempotencyClaim.stopRenewal()
	}
	if record == nil || response == nil {
		return errors.New("response record and body are required")
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return err
	}
	output := response.Output
	usage, err := json.Marshal(response.Usage)
	if err != nil {
		return err
	}
	now := time.Now()
	status := response.Status
	if status == "" {
		status = "completed"
		response.Status = status
		responseJSON, err = json.Marshal(response)
		if err != nil {
			return err
		}
	}
	updates := map[string]any{
		"status": status, "provider_response_id": record.ProviderResponseID,
		"usage_json": usage, "completed_at": &now,
		"lease_owner": "", "lease_expires_at": nil,
	}
	if record.Store {
		updates["response_json"] = responseJSON
		updates["output_items"] = output
	} else {
		updates["response_json"] = nil
		updates["output_items"] = nil
	}
	var terminalProjection *service.ConversationProjectionOutputRequest
	if completeCall && record.CallID != "" && (status == "completed" || status == "incomplete") {
		projectionRecord := *record
		projectionRecord.Status = status
		projectionRecord.ResponseJSON = datatypes.JSON(append([]byte(nil), responseJSON...))
		projectionRecord.OutputItems = datatypes.JSON(append([]byte(nil), output...))
		var err error
		terminalProjection, err = terminalResponseConversationOutputRequest(&projectionRecord, projection, false)
		if err != nil {
			return err
		}
	}
	return model.DB().Transaction(func(tx *gorm.DB) error {
		result := tx.Model(record).Where("status IN ?", []string{"queued", "in_progress", "result_ready", "finalizing"}).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("response is no longer active")
		}
		if idempotencyClaim != nil {
			if err := completeResponseIdempotencyTx(tx, idempotencyClaim, record.ID, responseJSON); err != nil {
				return err
			}
		}
		if !completeCall || record.CallID == "" || (status != "completed" && status != "incomplete") {
			return nil
		}
		attemptID := latestResponseAttemptIDTx(tx, record.CallID)
		return service.NewAPICallService().CompleteCallTx(tx, record.CallID, &service.CompleteCallRequest{
			FinalAttemptID: attemptID, HTTPStatus: http.StatusOK,
			ConversationProjection: terminalProjection,
		})
	})
}

func (p *Pipeline) callService() *service.APICallService {
	if p != nil && p.calls != nil {
		return p.calls
	}
	return service.NewAPICallService()
}

func latestResponseAttemptIDTx(db *gorm.DB, callID string) uint {
	if strings.TrimSpace(callID) == "" {
		return 0
	}
	var attempt model.APICallAttempt
	if err := db.Select("id").Where("call_id = ?", callID).Order("attempt_no DESC").First(&attempt).Error; err != nil {
		return 0
	}
	return attempt.ID
}

func (p *Pipeline) recordResponseFailure(record *model.AIResponse, cause error, retryable bool, projections ...*responseConversationProjection) error {
	if record == nil {
		return nil
	}
	var projection *responseConversationProjection
	if len(projections) > 0 {
		projection = projections[0]
	}
	terminalProjection, err := terminalResponseConversationOutputRequest(record, projection, false)
	if err != nil {
		return err
	}
	errorJSON, _ := json.Marshal(responseErrorFromError(cause))
	now := time.Now()
	err = model.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(record).
			Where("status IN ?", []string{"queued", "in_progress", "result_ready", "finalizing", "failed"}).
			Updates(map[string]any{
				"status": "failed", "error_json": errorJSON, "completed_at": &now,
				"lease_owner": "", "lease_expires_at": nil,
			}).Error; err != nil {
			return err
		}
		if record.CallID == "" {
			return nil
		}
		request := responseFailCallRequest(tx, record, cause, retryable)
		request.ConversationProjection = terminalProjection
		err := p.callService().FailCallTx(tx, record.CallID, request)
		if errors.Is(err, service.ErrAPICallInvalidTransition) {
			return nil
		}
		return err
	})
	if err == nil {
		projectResponseConversationBestEffort(record)
	}
	return err
}

func responseFailCallRequest(db *gorm.DB, record *model.AIResponse, err error, retryable bool) *service.FailCallRequest {
	detail := responseErrorFromError(err)
	status := domain.UpstreamStatusCode(err)
	if status == 0 {
		status = http.StatusBadGateway
	}
	param, _ := json.Marshal(detail.Param)
	if detail.Param == nil {
		param = nil
	}
	return &service.FailCallRequest{
		FinalAttemptID: latestResponseAttemptIDTx(db, record.CallID), HTTPStatus: status,
		ErrorType: detail.Type, ErrorCode: detail.Code, ErrorMessage: detail.Message,
		ErrorParam: datatypes.JSON(param), ErrorRetryable: retryable,
	}
}

func (p *Pipeline) recordResponseCancellation(record *model.AIResponse, projections ...*responseConversationProjection) error {
	if record == nil {
		return nil
	}
	var projection *responseConversationProjection
	if len(projections) > 0 {
		projection = projections[0]
	}
	terminalProjection, err := terminalResponseConversationOutputRequest(record, projection, false)
	if err != nil {
		return err
	}
	now := time.Now()
	err = model.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(record).
			Where("status IN ?", []string{"queued", "in_progress", "result_ready", "finalizing", "cancelling", "cancelled"}).
			Updates(map[string]any{
				"status": "cancelled", "completed_at": &now,
				"lease_owner": "", "lease_expires_at": nil,
			}).Error; err != nil {
			return err
		}
		if record.CallID == "" {
			return nil
		}
		return p.callService().CancelCallTx(tx, record.CallID, &service.CancelCallRequest{
			FinalAttemptID: latestResponseAttemptIDTx(tx, record.CallID),
			ErrorType:      "cancelled_error", ErrorCode: "response_cancelled", ErrorMessage: "Response was cancelled",
			ConversationProjection: terminalProjection,
		})
	})
	if err == nil {
		projectResponseConversationBestEffort(record)
	}
	return err
}

func responseErrorFromError(err error) protocol.Error {
	result := protocol.Error{Code: "upstream_error", Message: "upstream request failed", Type: "server_error"}
	if appErr, ok := domain.IsAppError(err); ok {
		result.Code = appErr.Code
		result.Message = appErr.Message
		result.Type = "invalid_request_error"
		return result
	}
	var upstreamErr *httputil.HTTPError
	if errors.As(err, &upstreamErr) {
		result.Message = upstreamErr.Message
		result.Type = upstreamErr.Type
		if result.Type == "" {
			switch upstreamErr.Status {
			case http.StatusUnauthorized:
				result.Type = "authentication_error"
			case http.StatusForbidden:
				result.Type = "permission_error"
			case http.StatusTooManyRequests:
				result.Type = "rate_limit_error"
			default:
				result.Type = "server_error"
				if upstreamErr.Status < 500 {
					result.Type = "invalid_request_error"
				}
			}
		}
		if upstreamErr.Code != nil {
			result.Code = fmt.Sprint(upstreamErr.Code)
		}
		if upstreamErr.Param != nil {
			result.Param = *upstreamErr.Param
		}
	}
	return result
}

func markRefundPending(record *model.AIResponse, err error, terminalStatus string) error {
	status := "refund_pending_failed"
	if terminalStatus == "cancelled" {
		status = "refund_pending_cancelled"
	}
	errorJSON, _ := json.Marshal(protocol.Error{Code: "billing_settlement_pending", Message: "billing settlement is pending"})
	result := model.DB().Model(record).Where("status IN ?", []string{"queued", "in_progress", "result_ready", "finalizing", "cancelling", "cancelled"}).Updates(map[string]any{
		"status": status, "error_json": errorJSON, "completed_at": nil,
		"lease_owner": "", "lease_expires_at": nil,
	})
	if result.Error != nil {
		return errors.Join(err, result.Error)
	}
	return err
}

func prepareContinuation(req *protocol.Request, previous *model.AIResponse, route *routing.RouteResult) error {
	if previous == nil {
		return nil
	}
	nativeState := route.Transport == model.UpstreamTransportOpenAIResponses || route.Transport == model.UpstreamTransportVolcengineV3
	sameProviderState := nativeState && previous.KeyID == route.KeyID && previous.UpstreamTransport == route.Transport
	if route.Transport == "" && previous.UpstreamTransport == "" {
		legacyNative := route.Protocol == model.ProtocolOpenAI || route.Protocol == model.ProtocolCustom || route.Protocol == model.ProtocolVolcengine
		sameProviderState = legacyNative && previous.ChannelID == route.ChannelID && previous.KeyID == route.KeyID
	}
	if sameProviderState && previous.ProviderResponseID != "" {
		req.PreviousResponseID = previous.ProviderResponseID
		return nil
	}
	current, err := decodeInputItems(req.Input)
	if err != nil {
		return err
	}

	chain := []*model.AIResponse{previous}
	seen := map[string]bool{previous.ID: true}
	for cursor := previous; cursor.PreviousResponseID != ""; {
		if len(chain) >= 100 || seen[cursor.PreviousResponseID] {
			return domain.ErrBadRequest("previous_response_id history is invalid or too deep")
		}
		var parent model.AIResponse
		if err := model.DB().Where("id = ? AND token_id = ? AND store = 1", cursor.PreviousResponseID, previous.TokenID).First(&parent).Error; err != nil {
			return domain.ErrBadRequest("previous_response_id history was not found")
		}
		seen[parent.ID] = true
		chain = append(chain, &parent)
		cursor = &parent
	}

	combined := make([]json.RawMessage, 0, len(current)+len(chain)*2)
	for i := len(chain) - 1; i >= 0; i-- {
		priorInput, err := decodeInputItems(chain[i].InputItems)
		if err != nil {
			return fmt.Errorf("decode stored response input: %w", err)
		}
		combined = append(combined, priorInput...)
		var priorOutput []json.RawMessage
		if len(bytes.TrimSpace(chain[i].OutputItems)) > 0 && !bytes.Equal(bytes.TrimSpace(chain[i].OutputItems), []byte("null")) {
			if err := json.Unmarshal(chain[i].OutputItems, &priorOutput); err != nil {
				return fmt.Errorf("decode stored response output: %w", err)
			}
		}
		combined = append(combined, priorOutput...)
	}
	combined = append(combined, current...)
	encoded, err := json.Marshal(combined)
	if err != nil {
		return err
	}
	req.Input = encoded
	req.PreviousResponseID = ""
	return nil
}

func decodeInputItems(raw []byte) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, err
		}
		item, _ := json.Marshal(map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": text}}})
		return []json.RawMessage{item}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func resolveInputFiles(tokenID uint, req *protocol.Request) error {
	var input any
	if json.Unmarshal(req.Input, &input) != nil {
		return domain.ErrBadRequest("invalid input")
	}
	files := make(map[string]model.AIFile)
	var walk func(any) error
	walk = func(value any) error {
		switch current := value.(type) {
		case []any:
			for _, item := range current {
				if err := walk(item); err != nil {
					return err
				}
			}
		case map[string]any:
			if id, ok := current["file_id"].(string); ok && id != "" {
				file, exists := files[id]
				if !exists {
					if err := model.DB().Where("id = ? AND token_id = ?", id, tokenID).First(&file).Error; err != nil {
						return domain.ErrBadRequest("file_id was not found")
					}
					files[id] = file
				}
				dataURL := "data:" + file.MimeType + ";base64," + base64.StdEncoding.EncodeToString(file.Content)
				switch current["type"] {
				case "input_image":
					current["image_url"] = dataURL
				case "input_video":
					current["video_url"] = dataURL
				case "input_audio":
					current["audio_url"] = dataURL
				default:
					current["file_data"] = dataURL
					current["filename"] = file.Filename
				}
				delete(current, "file_id")
			}
			for _, child := range current {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(input); err != nil {
		return err
	}
	encoded, err := json.Marshal(input)
	req.Input = encoded
	return err
}

func validateInputFiles(tokenID uint, raw json.RawMessage) error {
	var input any
	if json.Unmarshal(raw, &input) != nil {
		return domain.ErrBadRequest("invalid input")
	}
	checked := make(map[string]bool)
	var walk func(any) error
	walk = func(value any) error {
		switch current := value.(type) {
		case []any:
			for _, item := range current {
				if err := walk(item); err != nil {
					return err
				}
			}
		case map[string]any:
			if id, ok := current["file_id"].(string); ok && id != "" && !checked[id] {
				var count int64
				if err := model.DB().Model(&model.AIFile{}).Where("id = ? AND token_id = ?", id, tokenID).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					return domain.ErrBadRequest("file_id was not found")
				}
				checked[id] = true
			}
			for _, child := range current {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(input)
}

func mustJSON(value any) datatypes.JSON { encoded, _ := json.Marshal(value); return encoded }

func (p *Pipeline) Get(tokenID uint, id string) (*protocol.Response, error) {
	var record model.AIResponse
	if err := model.DB().Where("id = ? AND token_id = ? AND store = 1", id, tokenID).First(&record).Error; err != nil {
		return nil, err
	}
	return responseFromRecord(&record), nil
}

func responseFromRecord(record *model.AIResponse) *protocol.Response {
	var response protocol.Response
	if len(record.ResponseJSON) > 0 && json.Unmarshal(record.ResponseJSON, &response) == nil {
		response.Status = record.Status
		if response.Status == "result_ready" || response.Status == "finalizing" || response.Status == "cancelling" || strings.HasPrefix(response.Status, "refund_pending_") {
			response.Status = "in_progress"
		}
		if len(record.ErrorJSON) > 0 {
			var responseError protocol.Error
			if json.Unmarshal(record.ErrorJSON, &responseError) == nil {
				response.Error = &responseError
			}
		}
		return &response
	}
	response = protocol.Response{ID: record.ID, Object: "response", CreatedAt: record.CreatedAt.Unix(), Status: record.Status, Model: record.Model, Store: record.Store, Background: record.Background, Output: json.RawMessage(`[]`)}
	if response.Status == "result_ready" || response.Status == "finalizing" || response.Status == "cancelling" || strings.HasPrefix(response.Status, "refund_pending_") {
		response.Status = "in_progress"
	}
	if len(record.ErrorJSON) > 0 {
		var responseError protocol.Error
		if json.Unmarshal(record.ErrorJSON, &responseError) == nil {
			response.Error = &responseError
		}
	}
	setPublicPreviousResponseID(&response, record.PreviousResponseID)
	return &response
}

func (p *Pipeline) Delete(tokenID uint, id string) error {
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		var record model.AIResponse
		if err := tx.Where("id = ? AND token_id = ? AND store = 1 AND status NOT IN ?", id, tokenID, []string{"queued", "in_progress", "result_ready", "finalizing", "cancelling", "refund_pending_failed", "refund_pending_cancelled"}).First(&record).Error; err != nil {
			return err
		}
		if record.CallID != "" {
			if err := tx.Where("call_id = ?", record.CallID).Delete(&model.APICallPayload{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("token_id = ? AND response_id = ?", tokenID, record.ID).
			Delete(&model.AIResponseIdempotencyCache{}).Error; err != nil {
			return err
		}
		return tx.Delete(&record).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return routing.ErrModelNotFound
	}
	return err
}

func (p *Pipeline) Cancel(tokenID uint, id string) (*protocol.Response, error) {
	var record model.AIResponse
	if err := model.DB().Where("id = ? AND token_id = ?", id, tokenID).First(&record).Error; err != nil {
		return nil, err
	}
	if !record.Background {
		return nil, domain.ErrBadRequest("only background responses can be cancelled")
	}
	if _, err := ensureResponseCall(&record); err != nil {
		return nil, err
	}
	if record.Status == "in_progress" || record.Status == "queued" || record.Status == "result_ready" || record.Status == "finalizing" {
		result := model.DB().Model(&record).
			Where("status IN ?", []string{"queued", "in_progress", "result_ready", "finalizing"}).
			Updates(map[string]any{"status": "cancelling", "completed_at": nil})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			if err := model.DB().Where("id = ? AND token_id = ?", id, tokenID).First(&record).Error; err != nil {
				return nil, err
			}
		} else {
			record.Status = "cancelling"
		}
	}
	if record.Status != "cancelling" {
		return responseFromRecord(&record), nil
	}
	if cancel, ok := backgroundCancels.Load(id); ok {
		cancel.(context.CancelFunc)()
	}
	if err := p.reconcileV2BackgroundReservations(&record); err != nil {
		return nil, markRefundPending(&record, fmt.Errorf("refund response reservation: %w", err), "cancelled")
	}
	if err := p.recordResponseCancellation(&record); err != nil {
		if reloadErr := model.DB().Where("id = ? AND token_id = ?", id, tokenID).First(&record).Error; reloadErr != nil {
			return nil, errors.Join(err, reloadErr)
		}
		if record.Status != "cancelled" {
			return nil, err
		}
	}
	record.Status = "cancelled"
	return responseFromRecord(&record), nil
}

type InputItemsOptions struct {
	Limit int
	Order string
	After string
}

func (p *Pipeline) InputItems(tokenID uint, id string, options ...InputItemsOptions) (*protocol.List, error) {
	var record model.AIResponse
	if err := model.DB().Where("id = ? AND token_id = ? AND store = 1", id, tokenID).First(&record).Error; err != nil {
		return nil, err
	}
	items, err := decodeInputItems(record.InputItems)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index] = ensureInputItemID(record.ID, index, items[index])
	}
	opts := InputItemsOptions{Limit: 20, Order: "desc"}
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.Limit <= 0 || opts.Limit > 100 {
		opts.Limit = 20
	}
	if opts.Order != "asc" {
		opts.Order = "desc"
	}
	if opts.Order == "desc" {
		for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
			items[left], items[right] = items[right], items[left]
		}
	}
	if opts.After != "" {
		found := -1
		for index := range items {
			if itemID(items[index]) == opts.After {
				found = index
				break
			}
		}
		if found < 0 {
			return nil, domain.ErrBadRequest("after is not a valid input item cursor")
		}
		items = items[found+1:]
	}
	hasMore := len(items) > opts.Limit
	if hasMore {
		items = items[:opts.Limit]
	}
	list := &protocol.List{Object: "list", Data: items, HasMore: hasMore}
	if len(items) > 0 {
		list.FirstID = itemID(items[0])
		list.LastID = itemID(items[len(items)-1])
	}
	return list, nil
}

func ensureInputItemID(responseID string, index int, raw json.RawMessage) json.RawMessage {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	if id, _ := value["id"].(string); id == "" {
		value["id"] = fmt.Sprintf("item_%s_%d", strings.TrimPrefix(responseID, "resp_"), index)
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

func itemID(raw json.RawMessage) string {
	var value struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.ID
}
