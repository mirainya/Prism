package responses

import (
	"bytes"
	"context"
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
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Pipeline struct {
	billing *service.BillingService
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
	return &Pipeline{billing: service.NewBillingService(), engine: executionEngine, v2: executor}
}

type Result struct {
	Response                 *protocol.Response
	Record                   *model.AIResponse
	PublicPreviousResponseID string
	V2Stream                 *engine.StreamResult
}

func findIdempotentResponse(tokenID uint, idempotencyKey string, requestJSON []byte) (*Result, error) {
	var existing model.AIResponse
	if err := model.DB().Where("token_id = ? AND idempotency_key = ?", tokenID, idempotencyKey).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if !bytes.Equal(bytes.TrimSpace(existing.RequestJSON), bytes.TrimSpace(requestJSON)) {
		return nil, domain.ErrBadRequest("Idempotency-Key was already used with a different request")
	}
	return &Result{Response: responseFromRecord(&existing), Record: &existing}, nil
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
	result := model.DB().Model(record).Where("status = ?", "in_progress").Update("status", "finalizing")
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
	return status == "cancelled"
}

func createRecord(userID, tokenID uint, req *protocol.Request, requestJSON []byte, inputItems datatypes.JSON, publicPreviousResponseID, key string, route *routing.RouteResult) (*model.AIResponse, error) {
	metadata, _ := json.Marshal(req.Metadata)
	responseID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	storedKey := key
	if storedKey == "" {
		storedKey = "internal:" + responseID
	}
	store := true
	if req.Store != nil {
		store = *req.Store
	}
	record := &model.AIResponse{
		ID: responseID, UserID: userID, TokenID: tokenID,
		Model: req.Model, Status: "in_progress", Background: req.Background, Store: store,
		PreviousResponseID: publicPreviousResponseID,
		RequestJSON:        requestJSON, InputItems: inputItems, Metadata: metadata,
		IdempotencyKey: storedKey, CreatedAt: time.Now(),
	}
	if route != nil {
		record.ChannelID = route.ChannelID
		record.KeyID = route.KeyID
		record.UpstreamTransport = route.Transport
	}
	if err := model.DB().Create(record).Error; err != nil {
		return nil, err
	}
	return record, nil
}

func setPublicPreviousResponseID(response *protocol.Response, id string) {
	response.PreviousResponseID = nil
	if id != "" {
		value := id
		response.PreviousResponseID = &value
	}
}

func completeRecord(record *model.AIResponse, response *protocol.Response) error {
	responseJSON, _ := json.Marshal(response)
	output := response.Output
	usage, _ := json.Marshal(response.Usage)
	now := time.Now()
	status := response.Status
	if status == "" {
		status = "completed"
		response.Status = status
		responseJSON, _ = json.Marshal(response)
	}
	result := model.DB().Model(record).Where("status IN ?", []string{"queued", "in_progress", "finalizing"}).Updates(map[string]any{
		"status": status, "provider_response_id": record.ProviderResponseID, "response_json": responseJSON,
		"output_items": output, "usage_json": usage, "completed_at": &now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("response is no longer active")
	}
	return nil
}

func markFailed(record *model.AIResponse, err error) {
	errorJSON, _ := json.Marshal(responseErrorFromError(err))
	now := time.Now()
	_ = model.DB().Model(record).Where("status IN ?", []string{"queued", "in_progress", "finalizing"}).Updates(map[string]any{"status": "failed", "error_json": errorJSON, "completed_at": &now}).Error
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

func refundResponseReservation(record *model.AIResponse, reservation *responseReservation, cause error, terminalStatus string) error {
	if reservation != nil {
		if err := reservation.cancel(); err != nil {
			return markRefundPending(record, errors.Join(cause, fmt.Errorf("refund response reservation: %w", err)), terminalStatus)
		}
	}
	if terminalStatus == "cancelled" {
		now := time.Now()
		result := model.DB().Model(record).Where("status IN ?", []string{"queued", "in_progress", "finalizing", "cancelling"}).Updates(map[string]any{"status": "cancelled", "completed_at": &now})
		return result.Error
	}
	markFailed(record, cause)
	return nil
}

func markRefundPending(record *model.AIResponse, err error, terminalStatus string) error {
	status := "refund_pending_failed"
	if terminalStatus == "cancelled" {
		status = "refund_pending_cancelled"
	}
	errorJSON, _ := json.Marshal(protocol.Error{Code: "billing_settlement_pending", Message: "billing settlement is pending"})
	result := model.DB().Model(record).Where("status IN ?", []string{"queued", "in_progress", "finalizing", "cancelling", "cancelled"}).Updates(map[string]any{"status": status, "error_json": errorJSON, "completed_at": nil})
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

type responseReservation struct {
	billing         *service.BillingService
	tokenID, userID uint
	reserved        decimal.Decimal
	key             string
}

func loadResponseReservation(billing *service.BillingService, record *model.AIResponse) *responseReservation {
	var entry model.BillingLog
	if err := model.DB().Where("idempotent_key = ? AND type = ?", record.ID+":reserve", model.BillingTypeDeduct).First(&entry).Error; err != nil {
		return nil
	}
	return &responseReservation{billing: billing, tokenID: record.TokenID, userID: record.UserID, reserved: entry.Amount, key: record.ID + ":settle"}
}
func (r *responseReservation) cancel() error {
	if r == nil || !r.reserved.IsPositive() {
		return nil
	}
	return r.billing.SettleReservation(r.tokenID, r.userID, r.reserved, decimal.Zero, r.key)
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
		if response.Status == "finalizing" || response.Status == "cancelling" || strings.HasPrefix(response.Status, "refund_pending_") {
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
	if response.Status == "finalizing" || response.Status == "cancelling" || strings.HasPrefix(response.Status, "refund_pending_") {
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
	result := model.DB().Where("id = ? AND token_id = ? AND store = 1 AND status NOT IN ?", id, tokenID, []string{"queued", "in_progress", "finalizing", "cancelling", "refund_pending_failed", "refund_pending_cancelled"}).Delete(&model.AIResponse{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return routing.ErrModelNotFound
	}
	return nil
}

func (p *Pipeline) Cancel(tokenID uint, id string) (*protocol.Response, error) {
	var record model.AIResponse
	if err := model.DB().Where("id = ? AND token_id = ?", id, tokenID).First(&record).Error; err != nil {
		return nil, err
	}
	if !record.Background {
		return nil, domain.ErrBadRequest("only background responses can be cancelled")
	}
	if record.Status == "in_progress" || record.Status == "queued" {
		result := model.DB().Model(&record).Where("status IN ?", []string{"queued", "in_progress"}).Updates(map[string]any{"status": "cancelling", "completed_at": nil})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected > 0 {
			if cancel, ok := backgroundCancels.Load(id); ok {
				cancel.(context.CancelFunc)()
			}
			record.Status = "cancelling"
			if err := refundResponseReservation(&record, loadResponseReservation(p.billing, &record), nil, "cancelled"); err != nil {
				return nil, err
			}
			record.Status = "cancelled"
		} else if err := model.DB().Where("id = ? AND token_id = ?", id, tokenID).First(&record).Error; err != nil {
			return nil, err
		}
	}
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
