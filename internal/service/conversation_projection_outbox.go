package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	conversationProjectionBatchLimit = 100
	conversationProjectionMaxBatch   = 1000
	conversationProjectionRetryBase  = time.Minute
	conversationProjectionRetryMax   = time.Hour
)

var (
	ErrConversationProjectionNotFound = errors.New("conversation projection outbox entry not found")
	ErrConversationProjectionNotReady = errors.New("conversation projection is not ready")
	ErrConversationProjectionNotFinal = errors.New("conversation projection API call is not terminal")
	ErrConversationProjectionFailed   = errors.New("conversation projection failed")
)

// ConversationProjectionInputRequest stages the downstream canonical request
// before execution can reach an API call terminal state.
type ConversationProjectionInputRequest struct {
	CallID             string
	ConversationID     uint
	PreviousResponseID string
	InputItems         []canonical.Item
}

// ConversationProjectionOutputRequest stages the final or partial canonical
// output before the corresponding API call is made terminal.
type ConversationProjectionOutputRequest struct {
	CallID             string
	OutputItems        []canonical.Item
	RequestLogID       uint
	ProviderResponseID string
	FinishReason       string
}

type conversationProjectionFunc func(ConversationProjectionRequest) (uint, error)

// ConversationProjectionOutboxService persists projection intent separately
// from execution state so projection can be retried after process failure.
type ConversationProjectionOutboxService struct {
	database func() *gorm.DB
	project  conversationProjectionFunc
	now      func() time.Time
}

func NewConversationProjectionOutboxService() *ConversationProjectionOutboxService {
	return &ConversationProjectionOutboxService{
		database: model.DB,
		project:  ProjectAPIConversation,
		now:      time.Now,
	}
}

func StageAPIConversationProjectionInput(request ConversationProjectionInputRequest) error {
	return NewConversationProjectionOutboxService().StageInput(request)
}

func StageAPIConversationProjectionInputTx(tx *gorm.DB, request ConversationProjectionInputRequest) error {
	return NewConversationProjectionOutboxService().StageInputTx(tx, request)
}

func StageAPIConversationProjectionOutputIfPresent(request ConversationProjectionOutputRequest) (bool, error) {
	return NewConversationProjectionOutboxService().StageOutputIfPresent(request)
}

func StageAPIConversationProjectionOutputIfPresentTx(tx *gorm.DB, request ConversationProjectionOutputRequest) (bool, error) {
	return NewConversationProjectionOutboxService().StageOutputIfPresentTx(tx, request)
}

func StageAPIConversationProjectionOutputIfMissing(request ConversationProjectionOutputRequest) (bool, error) {
	return NewConversationProjectionOutboxService().StageOutputIfMissing(request)
}

func StageAPIConversationProjectionOutputIfMissingTx(tx *gorm.DB, request ConversationProjectionOutputRequest) (bool, error) {
	return NewConversationProjectionOutboxService().StageOutputIfMissingTx(tx, request)
}

func ProjectPendingAPIConversation(callID string) (uint, error) {
	return NewConversationProjectionOutboxService().Project(callID)
}

func ReconcilePendingAPIConversations(ctx context.Context, limit int) (int, error) {
	return NewConversationProjectionOutboxService().Reconcile(ctx, limit)
}

// StageInput upserts only request-side data and preserves any output already
// staged by a concurrent terminal path.
func (s *ConversationProjectionOutboxService) StageInput(request ConversationProjectionInputRequest) error {
	return s.database().Transaction(func(tx *gorm.DB) error {
		return s.StageInputTx(tx, request)
	})
}

// StageInputTx stages request data in the caller's API call transaction. For
// an explicit or uniquely resolved previous-response conversation it stores
// the input delta computed under the same conversation lock and marks it so
// terminal projection will not trim again.
func (s *ConversationProjectionOutboxService) StageInputTx(tx *gorm.DB, request ConversationProjectionInputRequest) error {
	if tx == nil {
		return fmt.Errorf("%w: database is nil", ErrAPICallInvalidInput)
	}
	callID, err := validateConversationProjectionCallID(request.CallID)
	if err != nil {
		return err
	}
	previousResponseID := strings.TrimSpace(request.PreviousResponseID)
	resolvePrevious := request.ConversationID == 0 && previousResponseID != ""
	call, conversation, err := lockConversationProjectionTarget(tx, callID, request.ConversationID, resolvePrevious)
	if err != nil {
		return err
	}
	if conversation == nil && resolvePrevious && call.ConversationID != 0 {
		conversation, err = loadOwnedConversationForUpdateTx(tx, call.ConversationID, call.UserID, call.TokenID)
		if err != nil {
			return fmt.Errorf("load previously resolved conversation: %w", err)
		}
		request.ConversationID = call.ConversationID
	}
	if conversation == nil && resolvePrevious {
		resolved, found, resolveErr := resolvePreviousResponseConversationTx(
			tx, previousResponseID, call.UserID, call.TokenID,
		)
		if resolveErr != nil && !errors.Is(resolveErr, ErrConversationProjectionDependencyPending) {
			return fmt.Errorf("resolve previous response conversation: %w", resolveErr)
		}
		// A public previous response can become visible before its own durable
		// conversation projection. Keep this input implicit so terminal
		// projection can retry after the dependency is written.
		if resolveErr == nil && found {
			if call.ConversationID != 0 && call.ConversationID != resolved.ID {
				return fmt.Errorf(
					"%w: call %s conversation id %d does not match previous response conversation id %d",
					ErrAPICallInvalidInput, callID, call.ConversationID, resolved.ID,
				)
			}
			if err := linkConversationProjectionCallTx(tx, callID, resolved.ID); err != nil {
				return fmt.Errorf("link previous response conversation: %w", err)
			}
			request.ConversationID = resolved.ID
			conversation = resolved
		}
	}
	input := canonical.CloneItems(request.InputItems)
	inputPrepared := false
	contextMode := model.ConversationTurnContextMode("")
	if conversation != nil {
		var matched bool
		input, matched, err = prepareCanonicalConversationInputTx(tx, conversation, input)
		if err != nil {
			return fmt.Errorf("prepare canonical conversation input: %w", err)
		}
		inputPrepared = true
		contextMode = explicitConversationContextMode(matched, input)
	}
	encoded, err := marshalConversationProjectionItems(input)
	if err != nil {
		return fmt.Errorf("encode canonical conversation input: %w", err)
	}
	now := s.now()
	entry := model.ConversationProjectionOutbox{
		CallID: callID, ConversationID: request.ConversationID,
		PreviousResponseID: previousResponseID,
		CanonicalInput:     encoded, InputReady: true, InputPrepared: inputPrepared, ContextMode: contextMode,
		CreatedAt: now, UpdatedAt: now,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "call_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"conversation_id":      entry.ConversationID,
			"previous_response_id": entry.PreviousResponseID,
			"input_json":           entry.CanonicalInput,
			"input_ready":          true,
			"input_prepared":       entry.InputPrepared,
			"context_mode":         entry.ContextMode,
			"next_attempt_at":      nil,
			"updated_at":           now,
		}),
	}).Create(&entry).Error
}

// StageOutputIfPresent updates an input-ready intent without creating an
// output-only outbox entry. It is safe for shared engine terminal paths.
func (s *ConversationProjectionOutboxService) StageOutputIfPresent(request ConversationProjectionOutputRequest) (bool, error) {
	return s.StageOutputIfPresentTx(s.database(), request)
}

func (s *ConversationProjectionOutboxService) StageOutputIfPresentTx(tx *gorm.DB, request ConversationProjectionOutputRequest) (bool, error) {
	if tx == nil {
		return false, fmt.Errorf("%w: database is nil", ErrAPICallInvalidInput)
	}
	callID, err := validateConversationProjectionCallID(request.CallID)
	if err != nil {
		return false, err
	}
	encoded, err := marshalConversationProjectionItems(request.OutputItems)
	if err != nil {
		return false, fmt.Errorf("encode canonical conversation output: %w", err)
	}
	now := s.now()
	result := tx.Model(&model.ConversationProjectionOutbox{}).
		Where("call_id = ? AND input_ready = ?", callID, true).
		Updates(map[string]any{
			"output_json":          encoded,
			"output_ready":         true,
			"request_log_id":       request.RequestLogID,
			"provider_response_id": strings.TrimSpace(request.ProviderResponseID),
			"finish_reason":        strings.TrimSpace(request.FinishReason),
			"next_attempt_at":      nil,
			"updated_at":           now,
		})
	return result.RowsAffected > 0, result.Error
}

// StageOutputIfMissing marks an input-ready intent with an empty or fallback
// output without replacing a real or partial output written concurrently.
func (s *ConversationProjectionOutboxService) StageOutputIfMissing(request ConversationProjectionOutputRequest) (bool, error) {
	return s.StageOutputIfMissingTx(s.database(), request)
}

func (s *ConversationProjectionOutboxService) StageOutputIfMissingTx(tx *gorm.DB, request ConversationProjectionOutputRequest) (bool, error) {
	if tx == nil {
		return false, fmt.Errorf("%w: database is nil", ErrAPICallInvalidInput)
	}
	callID, err := validateConversationProjectionCallID(request.CallID)
	if err != nil {
		return false, err
	}
	encoded, err := marshalConversationProjectionItems(request.OutputItems)
	if err != nil {
		return false, fmt.Errorf("encode canonical conversation output: %w", err)
	}
	now := s.now()
	result := tx.Model(&model.ConversationProjectionOutbox{}).
		Where("call_id = ? AND input_ready = ? AND output_ready = ?", callID, true, false).
		Updates(map[string]any{
			"output_json":          encoded,
			"output_ready":         true,
			"request_log_id":       request.RequestLogID,
			"provider_response_id": strings.TrimSpace(request.ProviderResponseID),
			"finish_reason":        strings.TrimSpace(request.FinishReason),
			"next_attempt_at":      nil,
			"updated_at":           now,
		})
	return result.RowsAffected > 0, result.Error
}

// Project writes one terminal call into conversation history. The outbox row
// is deleted only after the idempotent conversation transaction succeeds.
func (s *ConversationProjectionOutboxService) Project(callID string) (uint, error) {
	callID, err := validateConversationProjectionCallID(callID)
	if err != nil {
		return 0, err
	}
	var entry model.ConversationProjectionOutbox
	err = s.database().First(&entry, "call_id = ?", callID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if conversationID, found, lookupErr := projectedConversationID(callID, s.database()); lookupErr != nil {
			return 0, lookupErr
		} else if found {
			return conversationID, nil
		}
		return 0, fmt.Errorf("%w: %s", ErrConversationProjectionNotFound, callID)
	}
	if err != nil {
		return 0, err
	}

	var call model.APICall
	if err := s.database().First(&call, "id = ?", callID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = fmt.Errorf("%w: %s", ErrAPICallNotFound, callID)
		}
		return 0, s.recordFailure(&entry, err)
	}
	status, err := conversationTurnStatusForAPICall(&call)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", ErrConversationProjectionNotFinal, call.Status)
	}
	if !entry.InputReady {
		return 0, s.recordFailure(&entry, fmt.Errorf("%w: canonical input is missing", ErrConversationProjectionNotReady))
	}
	if !entry.OutputReady {
		return 0, s.recordFailure(&entry, fmt.Errorf("%w: canonical output is missing", ErrConversationProjectionNotReady))
	}

	input, err := unmarshalConversationProjectionItems(entry.CanonicalInput, "input")
	if err != nil {
		return 0, s.recordFailure(&entry, err)
	}
	output, err := unmarshalConversationProjectionItems(entry.CanonicalOutput, "output")
	if err != nil {
		return 0, s.recordFailure(&entry, err)
	}

	conversationID, err := s.project(ConversationProjectionRequest{
		UserID: call.UserID, TokenID: call.TokenID, Model: call.Model,
		CallID: call.ID, ConversationID: entry.ConversationID,
		PreviousResponseID: entry.PreviousResponseID,
		InputItems:         input, OutputItems: output, InputPrepared: entry.InputPrepared, Status: status,
		ContextMode:        entry.ContextMode,
		RequestLogID:       entry.RequestLogID,
		ProviderResponseID: entry.ProviderResponseID,
		FinishReason:       entry.FinishReason,
		ErrorType:          call.ErrorType, ErrorCode: call.ErrorCode,
		ErrorMessage: call.ErrorMessage,
	})
	if err != nil {
		return 0, s.recordFailure(&entry, err)
	}
	deleted := s.database().Where("call_id = ?", callID).Delete(&model.ConversationProjectionOutbox{})
	if deleted.Error != nil {
		return conversationID, s.recordFailure(&entry, deleted.Error)
	}
	return conversationID, nil
}

// Reconcile retries eligible outbox rows whose API calls are terminal. It
// keeps processing the batch after individual failures and returns all errors.
func (s *ConversationProjectionOutboxService) Reconcile(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = conversationProjectionBatchLimit
	}
	if limit > conversationProjectionMaxBatch {
		limit = conversationProjectionMaxBatch
	}
	now := s.now()
	var callIDs []string
	err := s.database().WithContext(ctx).
		Table("conversation_projection_outbox AS projection").
		Select("projection.call_id").
		Joins("JOIN api_calls AS api_call ON api_call.id = projection.call_id").
		Where("api_call.status IN ?", []model.APICallStatus{
			model.APICallStatusCompleted,
			model.APICallStatusFailed,
			model.APICallStatusCancelled,
		}).
		Where("NOT (api_call.status = ? AND api_call.error_code = ?)", model.APICallStatusFailed, staleCallPendingCode).
		Where("projection.next_attempt_at IS NULL OR projection.next_attempt_at <= ?", now).
		Order("projection.updated_at ASC, projection.call_id ASC").
		Limit(limit).
		Pluck("projection.call_id", &callIDs).Error
	if err != nil {
		return 0, err
	}

	var reconcileErr error
	attempted := 0
	for _, callID := range callIDs {
		if err := ctx.Err(); err != nil {
			return attempted, errors.Join(reconcileErr, err)
		}
		attempted++
		if _, err := s.Project(callID); err != nil {
			reconcileErr = errors.Join(reconcileErr, err)
		}
	}
	return attempted, reconcileErr
}

func (s *ConversationProjectionOutboxService) recordFailure(entry *model.ConversationProjectionOutbox, cause error) error {
	message := "Unknown conversation projection error"
	if cause != nil {
		message = SanitizeAPICallErrorMessage(cause.Error())
		if message == "" {
			message = "Unknown conversation projection error"
		}
	}
	now := s.now()
	retryCount := entry.RetryCount + 1
	nextAttempt := now.Add(conversationProjectionRetryDelay(retryCount))
	updateErr := s.database().Model(&model.ConversationProjectionOutbox{}).
		Where("call_id = ?", entry.CallID).
		Updates(map[string]any{
			"retry_count":     gorm.Expr("retry_count + 1"),
			"last_error":      message,
			"last_attempt_at": &now,
			"next_attempt_at": &nextAttempt,
			"updated_at":      now,
		}).Error
	projectionErr := fmt.Errorf("%w for call %s: %s", ErrConversationProjectionFailed, entry.CallID, message)
	if errors.Is(cause, ErrConversationProjectionDependencyPending) {
		projectionErr = errors.Join(projectionErr, ErrConversationProjectionDependencyPending)
	}
	if updateErr != nil {
		return errors.Join(projectionErr, fmt.Errorf("save projection retry metadata: %w", updateErr))
	}
	return projectionErr
}

func validateConversationProjectionCallID(callID string) (string, error) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return "", fmt.Errorf("%w: call_id is required", ErrAPICallInvalidInput)
	}
	if len(callID) > 64 {
		return "", fmt.Errorf("%w: call_id exceeds 64 bytes", ErrAPICallInvalidInput)
	}
	return callID, nil
}

func marshalConversationProjectionItems(items []canonical.Item) ([]byte, error) {
	if items == nil {
		items = []canonical.Item{}
	}
	return json.Marshal(normalizeConversationProjectionItemsForStorage(items))
}

// normalizeConversationProjectionItemsForStorage keeps partial stream output
// durable by representing malformed RawMessage values as JSON strings. The
// canonical fingerprint normalizer gives both forms the same string value.
func normalizeConversationProjectionItemsForStorage(items []canonical.Item) []canonical.Item {
	items = canonical.CloneItems(items)
	for itemIndex := range items {
		item := &items[itemIndex]
		item.Arguments = normalizeConversationProjectionRaw(item.Arguments, true)
		item.Output = normalizeConversationProjectionRaw(item.Output, true)
		normalizeConversationProjectionRawMap(item.Extra)
		for contentIndex := range item.Content {
			normalizeConversationProjectionRawMap(item.Content[contentIndex].Extra)
		}
	}
	return items
}

func normalizeConversationProjectionRaw(raw json.RawMessage, preserveEmpty bool) json.RawMessage {
	if (preserveEmpty && len(raw) == 0) || json.Valid(raw) {
		return raw
	}
	encoded, _ := json.Marshal(string(raw))
	return encoded
}

func normalizeConversationProjectionRawMap(values map[string]json.RawMessage) {
	for key, raw := range values {
		values[key] = normalizeConversationProjectionRaw(raw, false)
	}
}

func unmarshalConversationProjectionItems(encoded []byte, direction string) ([]canonical.Item, error) {
	var items []canonical.Item
	if err := json.Unmarshal(encoded, &items); err != nil {
		return nil, fmt.Errorf("decode canonical conversation %s: %w", direction, err)
	}
	if items == nil {
		items = []canonical.Item{}
	}
	return items, nil
}

func lockConversationProjectionTarget(
	tx *gorm.DB,
	callID string,
	conversationID uint,
	resolvePrevious bool,
) (*model.APICall, *model.Conversation, error) {
	var call model.APICall
	if err := tx.Select("user_id", "token_id", "conversation_id", "project_conversation").First(&call, "id = ?", callID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, fmt.Errorf("%w: %s", ErrAPICallNotFound, callID)
		}
		return nil, nil, err
	}
	if !call.ProjectConversation {
		return nil, nil, fmt.Errorf("%w: call %s is not configured for conversation projection", ErrAPICallInvalidInput, callID)
	}
	if call.ConversationID != conversationID && !resolvePrevious {
		return nil, nil, fmt.Errorf(
			"%w: call %s conversation id %d does not match projection conversation id %d",
			ErrAPICallInvalidInput,
			callID,
			call.ConversationID,
			conversationID,
		)
	}
	if conversationID == 0 {
		return &call, nil, nil
	}
	conversation, err := loadOwnedConversationForUpdateTx(tx, conversationID, call.UserID, call.TokenID)
	if err != nil {
		return nil, nil, err
	}
	return &call, conversation, nil
}

func linkConversationProjectionCallTx(tx *gorm.DB, callID string, conversationID uint) error {
	result := tx.Model(&model.APICall{}).
		Where("id = ? AND conversation_id = 0 AND project_conversation = ?", callID, true).
		Update("conversation_id", conversationID)
	if result.Error != nil || result.RowsAffected > 0 {
		return result.Error
	}
	var call model.APICall
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("conversation_id", "project_conversation").
		First(&call, "id = ?", callID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: %s", ErrAPICallNotFound, callID)
		}
		return err
	}
	if !call.ProjectConversation || call.ConversationID != conversationID {
		return fmt.Errorf(
			"%w: call %s conversation id %d does not match resolved conversation id %d",
			ErrAPICallInvalidInput, callID, call.ConversationID, conversationID,
		)
	}
	return nil
}

func projectedConversationID(callID string, db *gorm.DB) (uint, bool, error) {
	var turn model.ConversationTurn
	err := db.Select("conversation_id").First(&turn, "call_id = ?", callID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return turn.ConversationID, true, nil
}

func conversationProjectionRetryDelay(retryCount int) time.Duration {
	if retryCount < 1 {
		retryCount = 1
	}
	delay := conversationProjectionRetryBase
	for attempt := 1; attempt < retryCount && delay < conversationProjectionRetryMax; attempt++ {
		delay *= 2
		if delay > conversationProjectionRetryMax {
			delay = conversationProjectionRetryMax
		}
	}
	return delay
}
