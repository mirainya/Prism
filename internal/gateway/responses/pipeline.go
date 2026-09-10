package responses

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/httputil"
	"gorm.io/datatypes"
)

// Pipeline 在公开 Responses 资源语义与 Gateway Engine 执行语义之间编排持久化、
// 幂等、后台任务、会话投影和下游交付。
type Pipeline struct {
	v2     *V2Executor
	engine *engine.Engine
}

func New(executionEngine *engine.Engine) *Pipeline {
	if executionEngine == nil {
		panic("Gateway V2 engine is required")
	}
	executor, err := NewV2Executor(executionEngine)
	if err != nil {
		panic(err)
	}
	return &Pipeline{engine: executionEngine, v2: executor}
}

type Result struct {
	Response                 *protocol.Response
	Record                   *model.AIResponse
	CallID                   string
	AttemptID                uint
	PublicPreviousResponseID string
	V2Stream                 *engine.StreamResult
	IdempotentReplay         bool
	execution                *engine.Result
	conversation             *responseConversationProjection
	unifiedResource          bool
}

func (r *Result) CompleteDelivery() error {
	if r == nil || strings.TrimSpace(r.CallID) == "" {
		return nil
	}
	if r.IdempotentReplay || r.execution == nil {
		return nil
	}
	err := r.execution.CompleteDelivery()
	if err == nil {
		projectResponseConversationBestEffort(r.Record)
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
	if r.IdempotentReplay || r.execution == nil {
		return nil
	}
	deliveryErr := r.execution.FailDelivery(err, clientDisconnected)
	if deliveryErr == nil {
		projectResponseConversationBestEffort(r.Record)
	}
	return deliveryErr
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

func newResponseRecord(userID, tokenID uint, req *protocol.Request, requestJSON []byte, requestHash string, inputItems datatypes.JSON, publicPreviousResponseID string) *model.AIResponse {
	metadata, _ := json.Marshal(req.Metadata)
	responseID := newResponseID()
	store := true
	if req.Store != nil {
		store = *req.Store
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
		CreatedAt: time.Now(),
	}
	return record
}

func setPublicPreviousResponseID(response *protocol.Response, id string) {
	response.PreviousResponseID = nil
	if id != "" {
		value := id
		response.PreviousResponseID = &value
	}
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
		parent, _, err := loadPreviousResponse(context.Background(), previous.UserID, previous.TokenID, cursor.PreviousResponseID)
		if err != nil || parent == nil {
			return domain.ErrBadRequest("previous_response_id history was not found")
		}
		seen[parent.ID] = true
		chain = append(chain, parent)
		cursor = parent
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

func resolveInputFiles(ctx context.Context, tokenID uint, req *protocol.Request) error {
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
					loaded, err := service.LoadOwnedAIFile(ctx, tokenID, id, true)
					if err != nil {
						return domain.ErrBadRequest("file_id was not found")
					}
					file = *loaded
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

func (p *Pipeline) Get(userID, tokenID uint, id string) (*protocol.Response, error) {
	response, err := getUnifiedResponse(context.Background(), userID, tokenID, id)
	if errors.Is(err, repository.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return nil, routing.ErrModelNotFound
	}
	return response, err
}

func (p *Pipeline) Delete(userID, tokenID uint, id string) error {
	runtime, err := unifiedResponseRuntime()
	if err != nil {
		return err
	}
	err = runtime.DeleteResponse(context.Background(), uint64(userID), uint64(tokenID), id)
	if errors.Is(err, repository.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return routing.ErrModelNotFound
	}
	if errors.Is(err, repository.ErrConflict) {
		return domain.ErrBadRequest("response can only be deleted after it reaches a terminal state")
	}
	return err
}

func (p *Pipeline) Cancel(userID, tokenID uint, id string) (*protocol.Response, error) {
	runtime, err := unifiedResponseRuntime()
	if err != nil {
		return nil, err
	}
	if err := runtime.CancelQueuedResponse(context.Background(), uint64(userID), uint64(tokenID), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return nil, routing.ErrModelNotFound
		}
		if errors.Is(err, repository.ErrConflict) {
			return nil, domain.ErrBadRequest("response can no longer be cancelled because dispatch may have started")
		}
		return nil, err
	}
	return getUnifiedResponse(context.Background(), userID, tokenID, id)
}

type InputItemsOptions struct {
	Limit int
	Order string
	After string
}

func (p *Pipeline) InputItems(userID, tokenID uint, id string, options ...InputItemsOptions) (*protocol.List, error) {
	input, err := unifiedResponseInput(context.Background(), userID, tokenID, id)
	if errors.Is(err, repository.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return nil, routing.ErrModelNotFound
	}
	if err != nil {
		return nil, err
	}
	return paginateResponseInput(id, input, options...)
}

func paginateResponseInput(responseID string, items []json.RawMessage, options ...InputItemsOptions) (*protocol.List, error) {
	for index := range items {
		items[index] = ensureInputItemID(responseID, index, items[index])
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
