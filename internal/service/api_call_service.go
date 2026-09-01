package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAPICallNotFound          = errors.New("api call not found")
	ErrAPICallAttemptNotFound   = errors.New("api call attempt not found")
	ErrAPICallAccessDenied      = errors.New("api call access denied")
	ErrAPICallInvalidTransition = errors.New("invalid api call status transition")
	ErrAPICallInvalidInput      = errors.New("invalid api call input")
	ErrAPICallLeaseUnavailable  = errors.New("api call execution lease is unavailable")
)

// APICallService 维护统一调用台账。APICall 表示一次下游请求，
// APICallAttempt 表示该请求实际访问某个上游的单次尝试。
type APICallService struct{}

// APICallPayloadCapture buffers a bounded downstream stream payload only when
// payload retention is enabled for the owning call.
type APICallPayloadCapture struct {
	service       *APICallService
	payload       model.APICallPayload
	buffer        bytes.Buffer
	maxBytes      int
	originalBytes int64
}

func NewAPICallService() *APICallService {
	return &APICallService{}
}

func (s *APICallService) NewPayloadCapture(callID string, attemptID uint, kind, contentType string) (*APICallPayloadCapture, error) {
	callID = strings.TrimSpace(callID)
	kind = strings.TrimSpace(kind)
	if callID == "" || kind == "" {
		return nil, fmt.Errorf("%w: call id and payload kind are required", ErrAPICallInvalidInput)
	}

	// 锁住 Call 后递增 AttemptCount，使并发恢复任务不会生成相同的 attempt_no。
	var call model.APICall
	if err := model.DB().Select("id", "retain_payload").First(&call, "id = ?", callID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAPICallNotFound
		}
		return nil, err
	}
	if !call.RetainPayload {
		return nil, nil
	}
	if contentType == "" {
		contentType = "application/json"
	}
	return &APICallPayloadCapture{
		service: s,
		payload: model.APICallPayload{
			CallID: callID, AttemptID: attemptID, Kind: kind, ContentType: contentType,
		},
		maxBytes: apiCallPayloadMaxBytes(),
	}, nil
}

func (s *APICallService) NewPayloadCaptureBestEffort(callID string, attemptID uint, kind, contentType string) *APICallPayloadCapture {
	capture, err := s.NewPayloadCapture(callID, attemptID, kind, contentType)
	if err != nil {
		logger.Error("initialize API call payload capture",
			zap.String("call_id", callID),
			zap.Uint("attempt_id", attemptID),
			zap.String("kind", kind),
			zap.Error(err),
		)
	}
	return capture
}

func (c *APICallPayloadCapture) Write(data []byte) (int, error) {
	if c == nil || len(data) == 0 {
		return len(data), nil
	}
	c.originalBytes += int64(len(data))
	remaining := c.maxBytes - c.buffer.Len()
	if c.maxBytes <= 0 {
		remaining = len(data)
	}
	if remaining > len(data) {
		remaining = len(data)
	}
	if remaining > 0 {
		_, _ = c.buffer.Write(data[:remaining])
	}
	return len(data), nil
}

func (c *APICallPayloadCapture) Save() error {
	if c == nil || c.service == nil || c.originalBytes == 0 {
		return nil
	}
	c.payload.Data = append([]byte(nil), c.buffer.Bytes()...)
	c.payload.OriginalBytes = c.originalBytes
	c.payload.Truncated = c.originalBytes > int64(c.buffer.Len())
	return c.service.RecordPayload(&c.payload)
}

func (c *APICallPayloadCapture) SaveBestEffort() {
	if err := c.Save(); err != nil {
		logger.Error("save API call payload capture",
			zap.String("call_id", c.payload.CallID),
			zap.Uint("attempt_id", c.payload.AttemptID),
			zap.String("kind", c.payload.Kind),
			zap.Error(err),
		)
	}
}

func (s *APICallService) RecordPayloadBestEffort(payload *model.APICallPayload) {
	if err := s.RecordPayload(payload); err != nil {
		callID, kind := "", ""
		attemptID := uint(0)
		if payload != nil {
			callID, attemptID, kind = payload.CallID, payload.AttemptID, payload.Kind
		}
		logger.Error("save API call payload",
			zap.String("call_id", callID),
			zap.Uint("attempt_id", attemptID),
			zap.String("kind", kind),
			zap.Error(err),
		)
	}
}

func GenerateAPICallID() string {
	return "call_" + uuid.NewString()
}

func GenerateRequestID() string {
	return uuid.NewString()
}

type StartCallRequest struct {
	ID                  string
	RequestID           string
	UserID              uint
	TokenID             uint
	Endpoint            string
	Operation           string
	Model               string
	IsStream            bool
	Background          bool
	Store               bool
	RetainPayload       *bool
	PayloadExpiresAt    *time.Time
	ResourceType        string
	ResourceID          string
	ConversationID      uint
	ProjectConversation bool
	ReservedAmount      decimal.Decimal
}

type StartAttemptRequest struct {
	CallID      string
	RouteKind   string
	Stage       string
	AbilityID   uint
	ChannelID   uint
	KeyID       uint
	EndpointID  uint
	AccountID   uint
	Protocol    model.Protocol
	VendorModel string
	Transport   model.UpstreamTransport
	RequestPath string
}

type CompleteAttemptRequest struct {
	HTTPStatus            int
	RequestPath           string
	DurationMs            int64
	InputTokens           int
	OutputTokens          int
	TotalTokens           int
	CachedInputTokens     int
	ReasoningOutputTokens int
	UsageJSON             datatypes.JSON
	ProviderResponseID    string
}

type FailAttemptRequest struct {
	HTTPStatus            int
	RequestPath           string
	DurationMs            int64
	ErrorType             string
	ErrorCode             string
	ErrorMessage          string
	ErrorRetryable        bool
	InputTokens           int
	OutputTokens          int
	TotalTokens           int
	CachedInputTokens     int
	ReasoningOutputTokens int
	UsageJSON             datatypes.JSON
	ProviderResponseID    string
}

type CancelAttemptRequest struct {
	HTTPStatus     int
	ErrorType      string
	ErrorCode      string
	ErrorMessage   string
	ErrorRetryable bool
}

type CompleteCallRequest struct {
	LeaseOwner             string
	FinalAttemptID         uint
	InputTokens            int
	OutputTokens           int
	TotalTokens            int
	CachedInputTokens      int
	ReasoningOutputTokens  int
	UsageJSON              datatypes.JSON
	ProviderResponseID     string
	FinalCost              decimal.Decimal
	RefundedAmount         decimal.Decimal
	HTTPStatus             int
	ClientDisconnected     bool
	CompleteStartedAttempt bool
	ConversationProjection *ConversationProjectionOutputRequest
}

type FailCallRequest struct {
	LeaseOwner             string
	FinalAttemptID         uint
	HTTPStatus             int
	ErrorType              string
	ErrorCode              string
	ErrorMessage           string
	ErrorParam             datatypes.JSON
	ErrorRetryable         bool
	InputTokens            int
	OutputTokens           int
	TotalTokens            int
	CachedInputTokens      int
	ReasoningOutputTokens  int
	UsageJSON              datatypes.JSON
	FinalCost              decimal.Decimal
	RefundedAmount         decimal.Decimal
	ClientDisconnected     bool
	FailStartedAttempt     bool
	ConversationProjection *ConversationProjectionOutputRequest
}

type CancelCallRequest struct {
	LeaseOwner             string
	FinalAttemptID         uint
	HTTPStatus             int
	ErrorType              string
	ErrorCode              string
	ErrorMessage           string
	ErrorParam             datatypes.JSON
	ClientDisconnected     bool
	ConversationProjection *ConversationProjectionOutputRequest
}

type ListCallsRequest struct {
	Page        int                     `form:"page"`
	PageSize    int                     `form:"page_size"`
	SnapshotAt  string                  `form:"snapshot_at"`
	ActorUserID uint                    `form:"-"`
	IsAdmin     bool                    `form:"-"`
	UserID      uint                    `form:"user_id"`
	TokenID     uint                    `form:"token_id"`
	Model       string                  `form:"model"`
	Endpoint    string                  `form:"endpoint"`
	Status      model.APICallStatus     `form:"status"`
	RouteKind   string                  `form:"route_kind"`
	ChannelID   uint                    `form:"channel_id"`
	Transport   model.UpstreamTransport `form:"transport"`
	StartDate   string                  `form:"start_date"`
	EndDate     string                  `form:"end_date"`
	RequestID   string                  `form:"request_id"`
	CallID      string                  `form:"call_id"`
}

type ListCallsResponse struct {
	Items      []model.APICall `json:"items"`
	Total      int64           `json:"total"`
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	SnapshotAt string          `json:"snapshot_at"`
}

type APICallPayloadDetail struct {
	model.APICallPayload
	Data string `json:"data"`
}

type APICallDetail struct {
	Call        model.APICall          `json:"call"`
	Attempts    []model.APICallAttempt `json:"attempts"`
	BillingLogs []model.BillingLog     `json:"billing_logs"`
	Payloads    []APICallPayloadDetail `json:"payloads"`
}

func (s *APICallService) StartCall(req *StartCallRequest) (*model.APICall, error) {
	return s.StartCallTx(model.DB(), req)
}

// StartCallTx creates a received call inside the caller's transaction.
func (s *APICallService) StartCallTx(db *gorm.DB, req *StartCallRequest) (*model.APICall, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database is nil", ErrAPICallInvalidInput)
	}
	if req == nil {
		return nil, fmt.Errorf("%w: request is nil", ErrAPICallInvalidInput)
	}

	callID := strings.TrimSpace(req.ID)
	if callID == "" {
		callID = GenerateAPICallID()
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = GenerateRequestID()
	}

	now := time.Now()
	retainPayload, payloadExpiresAt := resolvePayloadPolicy(req.RetainPayload, req.PayloadExpiresAt, now)
	call := &model.APICall{
		ID:                  callID,
		RequestID:           requestID,
		UserID:              req.UserID,
		TokenID:             req.TokenID,
		Endpoint:            strings.TrimSpace(req.Endpoint),
		Operation:           strings.TrimSpace(req.Operation),
		Model:               strings.TrimSpace(req.Model),
		Status:              model.APICallStatusReceived,
		IsStream:            req.IsStream,
		Background:          req.Background,
		Store:               req.Store,
		RetainPayload:       retainPayload,
		PayloadExpiresAt:    payloadExpiresAt,
		ResourceType:        strings.TrimSpace(req.ResourceType),
		ResourceID:          strings.TrimSpace(req.ResourceID),
		ConversationID:      req.ConversationID,
		ProjectConversation: req.ProjectConversation,
		ReservedAmount:      req.ReservedAmount,
		StartedAt:           now,
	}
	if err := db.Create(call).Error; err != nil {
		return nil, err
	}
	return call, nil
}

func (s *APICallService) MarkCallRunning(callID string) error {
	return s.MarkCallRunningTx(model.DB(), callID)
}

func (s *APICallService) MarkCallRunningTx(db *gorm.DB, callID string) error {
	if db == nil {
		return fmt.Errorf("%w: database is nil", ErrAPICallInvalidInput)
	}
	return transitionCallStatus(
		db,
		callID,
		model.APICallStatusInProgress,
		[]model.APICallStatus{model.APICallStatusReceived},
		nil,
	)
}

func (s *APICallService) StartAttempt(req *StartAttemptRequest) (*model.APICallAttempt, error) {
	var attempt *model.APICallAttempt
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		var err error
		attempt, err = s.StartAttemptTx(tx, req)
		return err
	})
	return attempt, err
}

func (s *APICallService) StartAttemptTx(tx *gorm.DB, req *StartAttemptRequest) (*model.APICallAttempt, error) {
	if tx == nil {
		return nil, fmt.Errorf("%w: database is nil", ErrAPICallInvalidInput)
	}
	if req == nil || strings.TrimSpace(req.CallID) == "" {
		return nil, fmt.Errorf("%w: call id is required", ErrAPICallInvalidInput)
	}

	var call model.APICall
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&call, "id = ?", req.CallID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAPICallNotFound
		}
		return nil, err
	}
	if call.Status != model.APICallStatusReceived && call.Status != model.APICallStatusInProgress {
		return nil, callTransitionError(call.ID, call.Status, model.APICallStatusInProgress)
	}

	now := time.Now()
	attemptNo := call.AttemptCount + 1
	updates := map[string]any{
		"status":        model.APICallStatusInProgress,
		"attempt_count": attemptNo,
	}
	result := tx.Model(&model.APICall{}).
		Where("id = ? AND status IN ?", call.ID, []model.APICallStatus{
			model.APICallStatusReceived,
			model.APICallStatusInProgress,
		}).
		Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, callTransitionError(call.ID, call.Status, model.APICallStatusInProgress)
	}

	attempt := &model.APICallAttempt{
		CallID:      call.ID,
		AttemptNo:   attemptNo,
		RouteKind:   strings.TrimSpace(req.RouteKind),
		Stage:       strings.TrimSpace(req.Stage),
		AbilityID:   req.AbilityID,
		ChannelID:   req.ChannelID,
		KeyID:       req.KeyID,
		EndpointID:  req.EndpointID,
		AccountID:   req.AccountID,
		Protocol:    req.Protocol,
		VendorModel: strings.TrimSpace(req.VendorModel),
		Transport:   req.Transport,
		RequestPath: strings.TrimSpace(req.RequestPath),
		Status:      model.APICallAttemptStatusStarted,
		StartedAt:   now,
	}
	if attempt.RouteKind == "" {
		attempt.RouteKind = model.APICallRouteGatewayV2
	}
	if err := tx.Create(attempt).Error; err != nil {
		return nil, err
	}
	return attempt, nil
}

func (s *APICallService) MarkAttemptFirstByte(attemptID uint) error {
	if attemptID == 0 {
		return fmt.Errorf("%w: attempt id is required", ErrAPICallInvalidInput)
	}

	return model.DB().Transaction(func(tx *gorm.DB) error {
		var attempt model.APICallAttempt
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&attempt, attemptID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAPICallAttemptNotFound
			}
			return err
		}

		firstByteAt := attempt.FirstByteAt
		if firstByteAt == nil {
			if attempt.Status != model.APICallAttemptStatusStarted {
				return attemptTransitionError(attempt.ID, attempt.Status, model.APICallAttemptStatusStarted)
			}
			now := time.Now()
			firstByteAt = &now
			result := tx.Model(&model.APICallAttempt{}).
				Where("id = ? AND status = ? AND first_byte_at IS NULL", attempt.ID, model.APICallAttemptStatusStarted).
				Updates(map[string]any{
					"first_byte_at": now,
					"ttft_ms":       elapsedMilliseconds(attempt.StartedAt, now),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return attemptTransitionError(attempt.ID, attempt.Status, model.APICallAttemptStatusStarted)
			}
		}

		var call model.APICall
		if err := tx.Select("id", "started_at", "first_byte_at").First(&call, "id = ?", attempt.CallID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAPICallNotFound
			}
			return err
		}
		if call.FirstByteAt != nil {
			return nil
		}
		return tx.Model(&model.APICall{}).
			Where("id = ? AND first_byte_at IS NULL", call.ID).
			Updates(map[string]any{
				"first_byte_at": *firstByteAt,
				"ttft_ms":       elapsedMilliseconds(call.StartedAt, *firstByteAt),
			}).Error
	})
}

func (s *APICallService) CompleteAttempt(attemptID uint, req *CompleteAttemptRequest) error {
	if req == nil {
		req = &CompleteAttemptRequest{}
	}
	now := time.Now()
	attempt, err := getAttempt(attemptID)
	if err != nil {
		return err
	}
	durationMs := req.DurationMs
	if durationMs <= 0 {
		durationMs = elapsedMilliseconds(attempt.StartedAt, now)
	}
	updates := map[string]any{
		"status":                  model.APICallAttemptStatusCompleted,
		"http_status":             req.HTTPStatus,
		"input_tokens":            req.InputTokens,
		"output_tokens":           req.OutputTokens,
		"total_tokens":            req.TotalTokens,
		"cached_input_tokens":     req.CachedInputTokens,
		"reasoning_output_tokens": req.ReasoningOutputTokens,
		"provider_response_id":    req.ProviderResponseID,
		"completed_at":            now,
		"duration_ms":             durationMs,
		"error_type":              "",
		"error_code":              "",
		"error_message":           "",
		"error_retryable":         false,
	}
	if strings.TrimSpace(req.RequestPath) != "" {
		updates["request_path"] = strings.TrimSpace(req.RequestPath)
	}
	if len(req.UsageJSON) > 0 {
		updates["usage_json"] = req.UsageJSON
	}
	return transitionAttemptStatus(
		model.DB(),
		attemptID,
		model.APICallAttemptStatusCompleted,
		[]model.APICallAttemptStatus{model.APICallAttemptStatusStarted},
		updates,
	)
}

func (s *APICallService) FailAttempt(attemptID uint, req *FailAttemptRequest) error {
	if req == nil {
		req = &FailAttemptRequest{}
	}
	now := time.Now()
	attempt, err := getAttempt(attemptID)
	if err != nil {
		return err
	}
	durationMs := req.DurationMs
	if durationMs <= 0 {
		durationMs = elapsedMilliseconds(attempt.StartedAt, now)
	}
	updates := map[string]any{
		"status":                  model.APICallAttemptStatusFailed,
		"http_status":             req.HTTPStatus,
		"error_type":              req.ErrorType,
		"error_code":              req.ErrorCode,
		"error_message":           sanitizeStoredErrorMessage(req.ErrorMessage),
		"error_retryable":         req.ErrorRetryable,
		"input_tokens":            req.InputTokens,
		"output_tokens":           req.OutputTokens,
		"total_tokens":            req.TotalTokens,
		"cached_input_tokens":     req.CachedInputTokens,
		"reasoning_output_tokens": req.ReasoningOutputTokens,
		"provider_response_id":    req.ProviderResponseID,
		"completed_at":            now,
		"duration_ms":             durationMs,
	}
	if strings.TrimSpace(req.RequestPath) != "" {
		updates["request_path"] = strings.TrimSpace(req.RequestPath)
	}
	if len(req.UsageJSON) > 0 {
		updates["usage_json"] = req.UsageJSON
	}
	return transitionAttemptStatus(
		model.DB(),
		attemptID,
		model.APICallAttemptStatusFailed,
		[]model.APICallAttemptStatus{model.APICallAttemptStatusStarted},
		updates,
	)
}

func (s *APICallService) CancelAttempt(attemptID uint, req *CancelAttemptRequest) error {
	if req == nil {
		req = &CancelAttemptRequest{}
	}
	now := time.Now()
	attempt, err := getAttempt(attemptID)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"status":          model.APICallAttemptStatusCancelled,
		"http_status":     req.HTTPStatus,
		"error_type":      req.ErrorType,
		"error_code":      req.ErrorCode,
		"error_message":   sanitizeStoredErrorMessage(req.ErrorMessage),
		"error_retryable": req.ErrorRetryable,
		"completed_at":    now,
		"duration_ms":     elapsedMilliseconds(attempt.StartedAt, now),
	}
	return transitionAttemptStatus(
		model.DB(),
		attemptID,
		model.APICallAttemptStatusCancelled,
		[]model.APICallAttemptStatus{model.APICallAttemptStatusStarted},
		updates,
	)
}

func (s *APICallService) CompleteCall(callID string, req *CompleteCallRequest) error {
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		return s.CompleteCallTx(tx, callID, req)
	})
	if err == nil || req == nil || !req.CompleteStartedAttempt ||
		errors.Is(err, ErrAPICallLeaseUnavailable) || errors.Is(err, ErrAPICallInvalidTransition) || errors.Is(err, ErrAPICallNotFound) {
		return err
	}
	// 上游已成功但终态事务失败时保存完成意图，恢复任务可据此重放，避免成功调用长期停在进行中。
	if intentErr := persistCallCompletionIntent(callID, req, err); intentErr != nil {
		return errors.Join(err, fmt.Errorf("persist call completion intent: %w", intentErr))
	}
	return err
}

func (s *APICallService) CompleteCallTx(db *gorm.DB, callID string, req *CompleteCallRequest) error {
	if db == nil {
		return fmt.Errorf("%w: database is nil", ErrAPICallInvalidInput)
	}
	if req == nil {
		req = &CompleteCallRequest{}
	}
	call, err := getCallForUpdateWithDB(db, callID)
	if err != nil {
		return err
	}
	if call.Status == model.APICallStatusCompleted {
		if call.AttemptCount > 0 && call.FinalAttemptID == 0 {
			return fmt.Errorf("%w: completed call %s has no final attempt", ErrAPICallInvalidTransition, call.ID)
		}
		if call.FinalAttemptID > 0 {
			if _, err := getCompletedCallAttemptWithDB(db, callID, call.FinalAttemptID); err != nil {
				return err
			}
		}
		return nil
	}
	if err := validateCallLease(call, req.LeaseOwner); err != nil {
		return err
	}

	result := *req
	if result.FinalCost.IsZero() {
		result.FinalCost = call.FinalCost
	}
	if result.RefundedAmount.IsZero() {
		result.RefundedAmount = call.RefundedAmount
	}
	var finalAttempt *model.APICallAttempt
	if result.FinalAttemptID == 0 && call.AttemptCount > 0 {
		return fmt.Errorf("%w: call %s has attempts but no final attempt was selected", ErrAPICallInvalidTransition, call.ID)
	}
	if result.FinalAttemptID > 0 {
		if result.CompleteStartedAttempt {
			finalAttempt, err = completeStartedCallAttemptWithDB(db, callID, result.FinalAttemptID, &result)
		} else {
			finalAttempt, err = getCompletedCallAttemptWithDB(db, callID, result.FinalAttemptID)
		}
		if err != nil {
			return err
		}
		fillCallCompletionFromAttempt(&result, finalAttempt)
	}

	now := time.Now()
	// 成功路由确定后，其他仍为 started 的尝试都是被重试替代的历史尝试。
	if err := cancelSupersededStartedCallAttemptsWithDB(db, callID, result.FinalAttemptID, now); err != nil {
		return err
	}
	updates := map[string]any{
		"status":                  model.APICallStatusCompleted,
		"final_attempt_id":        result.FinalAttemptID,
		"input_tokens":            result.InputTokens,
		"output_tokens":           result.OutputTokens,
		"total_tokens":            result.TotalTokens,
		"cached_input_tokens":     result.CachedInputTokens,
		"reasoning_output_tokens": result.ReasoningOutputTokens,
		"final_cost":              result.FinalCost,
		"refunded_amount":         result.RefundedAmount,
		"http_status":             result.HTTPStatus,
		"client_disconnected":     result.ClientDisconnected,
		"completed_at":            now,
		"duration_ms":             elapsedMilliseconds(call.StartedAt, now),
		"lease_owner":             "",
		"lease_expires_at":        nil,
		"error_type":              "",
		"error_code":              "",
		"error_message":           "",
		"error_param":             nil,
		"error_retryable":         false,
	}
	if len(result.UsageJSON) > 0 {
		updates["usage_json"] = result.UsageJSON
	}
	if call.FirstByteAt == nil && finalAttempt != nil && finalAttempt.FirstByteAt != nil {
		updates["first_byte_at"] = *finalAttempt.FirstByteAt
		updates["ttft_ms"] = elapsedMilliseconds(call.StartedAt, *finalAttempt.FirstByteAt)
	}
	if err := stageTerminalConversationProjectionOutputTx(db, call, result.ConversationProjection); err != nil {
		return err
	}
	return transitionCallStatus(
		db,
		callID,
		model.APICallStatusCompleted,
		[]model.APICallStatus{model.APICallStatusReceived, model.APICallStatusInProgress},
		updates,
	)
}

func getCompletedCallAttemptWithDB(db *gorm.DB, callID string, attemptID uint) (*model.APICallAttempt, error) {
	attempt, err := getCallAttemptWithDB(db, callID, attemptID)
	if err != nil {
		return nil, err
	}
	if attempt.Status != model.APICallAttemptStatusCompleted {
		return nil, fmt.Errorf(
			"%w: final attempt %d is %s, expected %s",
			ErrAPICallInvalidTransition,
			attempt.ID,
			attempt.Status,
			model.APICallAttemptStatusCompleted,
		)
	}
	return attempt, nil
}

func completeStartedCallAttemptWithDB(
	db *gorm.DB,
	callID string,
	attemptID uint,
	req *CompleteCallRequest,
) (*model.APICallAttempt, error) {
	attempt, err := getCallAttemptWithDB(db, callID, attemptID)
	if err != nil {
		return nil, err
	}
	if attempt.Status == model.APICallAttemptStatusCompleted {
		return attempt, nil
	}
	if attempt.Status != model.APICallAttemptStatusStarted {
		return nil, fmt.Errorf(
			"%w: final attempt %d is %s, expected %s or %s",
			ErrAPICallInvalidTransition,
			attempt.ID,
			attempt.Status,
			model.APICallAttemptStatusStarted,
			model.APICallAttemptStatusCompleted,
		)
	}

	now := time.Now()
	httpStatus := req.HTTPStatus
	if httpStatus == 0 {
		httpStatus = http.StatusOK
	}
	updates := map[string]any{
		"status":                  model.APICallAttemptStatusCompleted,
		"http_status":             httpStatus,
		"input_tokens":            req.InputTokens,
		"output_tokens":           req.OutputTokens,
		"total_tokens":            req.TotalTokens,
		"cached_input_tokens":     req.CachedInputTokens,
		"reasoning_output_tokens": req.ReasoningOutputTokens,
		"provider_response_id":    req.ProviderResponseID,
		"completed_at":            now,
		"duration_ms":             elapsedMilliseconds(attempt.StartedAt, now),
		"error_type":              "",
		"error_code":              "",
		"error_message":           "",
		"error_retryable":         false,
	}
	if len(req.UsageJSON) > 0 {
		updates["usage_json"] = req.UsageJSON
	}
	if err := transitionAttemptStatus(
		db,
		attempt.ID,
		model.APICallAttemptStatusCompleted,
		[]model.APICallAttemptStatus{model.APICallAttemptStatusStarted},
		updates,
	); err != nil {
		return nil, err
	}
	return getCompletedCallAttemptWithDB(db, callID, attemptID)
}

func failStartedCallAttemptWithDB(
	db *gorm.DB,
	callID string,
	attemptID uint,
	req *FailCallRequest,
) (*model.APICallAttempt, error) {
	attempt, err := getCallAttemptWithDB(db, callID, attemptID)
	if err != nil {
		return nil, err
	}
	switch attempt.Status {
	case model.APICallAttemptStatusFailed, model.APICallAttemptStatusCompleted:
		return attempt, nil
	case model.APICallAttemptStatusStarted:
	default:
		return nil, fmt.Errorf(
			"%w: final attempt %d is %s, expected %s, %s, or %s",
			ErrAPICallInvalidTransition,
			attempt.ID,
			attempt.Status,
			model.APICallAttemptStatusStarted,
			model.APICallAttemptStatusFailed,
			model.APICallAttemptStatusCompleted,
		)
	}

	now := time.Now()
	updates := map[string]any{
		"status":                  model.APICallAttemptStatusFailed,
		"http_status":             req.HTTPStatus,
		"error_type":              req.ErrorType,
		"error_code":              req.ErrorCode,
		"error_message":           sanitizeStoredErrorMessage(req.ErrorMessage),
		"error_retryable":         req.ErrorRetryable,
		"input_tokens":            req.InputTokens,
		"output_tokens":           req.OutputTokens,
		"total_tokens":            req.TotalTokens,
		"cached_input_tokens":     req.CachedInputTokens,
		"reasoning_output_tokens": req.ReasoningOutputTokens,
		"completed_at":            now,
		"duration_ms":             elapsedMilliseconds(attempt.StartedAt, now),
	}
	if len(req.UsageJSON) > 0 {
		updates["usage_json"] = req.UsageJSON
	}
	if err := transitionAttemptStatus(
		db,
		attempt.ID,
		model.APICallAttemptStatusFailed,
		[]model.APICallAttemptStatus{model.APICallAttemptStatusStarted},
		updates,
	); err != nil {
		return nil, err
	}
	return getCallAttemptWithDB(db, callID, attemptID)
}

func cancelSupersededStartedCallAttemptsWithDB(db *gorm.DB, callID string, finalAttemptID uint, now time.Time) error {
	var attempts []model.APICallAttempt
	query := db.Where("call_id = ? AND status = ?", callID, model.APICallAttemptStatusStarted)
	if finalAttemptID > 0 {
		query = query.Where("id <> ?", finalAttemptID)
	}
	if err := query.Find(&attempts).Error; err != nil {
		return err
	}
	for i := range attempts {
		result := db.Model(&model.APICallAttempt{}).
			Where("id = ? AND status = ?", attempts[i].ID, model.APICallAttemptStatusStarted).
			Updates(map[string]any{
				"status":          model.APICallAttemptStatusCancelled,
				"error_type":      "cancelled",
				"error_code":      "attempt_superseded",
				"error_message":   "Attempt superseded by the completed attempt",
				"error_retryable": false,
				"completed_at":    now,
				"duration_ms":     elapsedMilliseconds(attempts[i].StartedAt, now),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return attemptTransitionError(attempts[i].ID, attempts[i].Status, model.APICallAttemptStatusCancelled)
		}
	}
	return nil
}

func failRemainingStartedCallAttemptsWithDB(db *gorm.DB, callID string, req *FailCallRequest, now time.Time) error {
	var attempts []model.APICallAttempt
	if err := db.Where("call_id = ? AND status = ?", callID, model.APICallAttemptStatusStarted).
		Find(&attempts).Error; err != nil {
		return err
	}
	for i := range attempts {
		updates := map[string]any{
			"status":          model.APICallAttemptStatusFailed,
			"http_status":     req.HTTPStatus,
			"error_type":      req.ErrorType,
			"error_code":      req.ErrorCode,
			"error_message":   sanitizeStoredErrorMessage(req.ErrorMessage),
			"error_retryable": req.ErrorRetryable,
			"completed_at":    now,
			"duration_ms":     elapsedMilliseconds(attempts[i].StartedAt, now),
		}
		if attempts[i].ID == req.FinalAttemptID {
			updates["input_tokens"] = req.InputTokens
			updates["output_tokens"] = req.OutputTokens
			updates["total_tokens"] = req.TotalTokens
			updates["cached_input_tokens"] = req.CachedInputTokens
			updates["reasoning_output_tokens"] = req.ReasoningOutputTokens
			if len(req.UsageJSON) > 0 {
				updates["usage_json"] = req.UsageJSON
			}
		}
		result := db.Model(&model.APICallAttempt{}).
			Where("id = ? AND status = ?", attempts[i].ID, model.APICallAttemptStatusStarted).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return attemptTransitionError(attempts[i].ID, attempts[i].Status, model.APICallAttemptStatusFailed)
		}
	}
	return nil
}

func (s *APICallService) FailCall(callID string, req *FailCallRequest) error {
	return model.DB().Transaction(func(tx *gorm.DB) error {
		return s.FailCallTx(tx, callID, req)
	})
}

func (s *APICallService) FailCallTx(db *gorm.DB, callID string, req *FailCallRequest) error {
	if db == nil {
		return fmt.Errorf("%w: database is nil", ErrAPICallInvalidInput)
	}
	if req == nil {
		req = &FailCallRequest{}
	}
	call, err := getCallForUpdateWithDB(db, callID)
	if err != nil {
		return err
	}
	if call.Status == model.APICallStatusFailed {
		return nil
	}
	if err := validateCallLease(call, req.LeaseOwner); err != nil {
		return err
	}

	result := *req
	if result.FinalCost.IsZero() {
		result.FinalCost = call.FinalCost
	}
	if result.RefundedAmount.IsZero() {
		result.RefundedAmount = call.RefundedAmount
	}
	var finalAttempt *model.APICallAttempt
	if result.FinalAttemptID > 0 {
		if result.FailStartedAttempt {
			finalAttempt, err = failStartedCallAttemptWithDB(db, callID, result.FinalAttemptID, &result)
		} else {
			finalAttempt, err = getCallAttemptWithDB(db, callID, result.FinalAttemptID)
		}
		if err != nil {
			return err
		}
	}

	now := time.Now()
	if err := failRemainingStartedCallAttemptsWithDB(db, callID, &result, now); err != nil {
		return err
	}
	if result.FinalAttemptID > 0 {
		finalAttempt, err = getCallAttemptWithDB(db, callID, result.FinalAttemptID)
		if err != nil {
			return err
		}
		fillCallFailureFromAttempt(&result, finalAttempt)
	}
	updates := map[string]any{
		"status":                  model.APICallStatusFailed,
		"final_attempt_id":        result.FinalAttemptID,
		"http_status":             result.HTTPStatus,
		"error_type":              result.ErrorType,
		"error_code":              result.ErrorCode,
		"error_message":           sanitizeStoredErrorMessage(result.ErrorMessage),
		"error_param":             nil,
		"error_retryable":         result.ErrorRetryable,
		"input_tokens":            result.InputTokens,
		"output_tokens":           result.OutputTokens,
		"total_tokens":            result.TotalTokens,
		"cached_input_tokens":     result.CachedInputTokens,
		"reasoning_output_tokens": result.ReasoningOutputTokens,
		"final_cost":              result.FinalCost,
		"refunded_amount":         result.RefundedAmount,
		"client_disconnected":     result.ClientDisconnected,
		"completed_at":            now,
		"duration_ms":             elapsedMilliseconds(call.StartedAt, now),
		"lease_owner":             "",
		"lease_expires_at":        nil,
	}
	if len(result.ErrorParam) > 0 {
		updates["error_param"] = result.ErrorParam
	}
	if len(result.UsageJSON) > 0 {
		updates["usage_json"] = result.UsageJSON
	}
	if call.FirstByteAt == nil && finalAttempt != nil && finalAttempt.FirstByteAt != nil {
		updates["first_byte_at"] = *finalAttempt.FirstByteAt
		updates["ttft_ms"] = elapsedMilliseconds(call.StartedAt, *finalAttempt.FirstByteAt)
	}
	if err := stageTerminalConversationProjectionOutputTx(db, call, result.ConversationProjection); err != nil {
		return err
	}
	return transitionCallStatus(
		db,
		callID,
		model.APICallStatusFailed,
		[]model.APICallStatus{model.APICallStatusReceived, model.APICallStatusInProgress},
		updates,
	)
}

func (s *APICallService) CancelCall(callID string, req *CancelCallRequest) error {
	return model.DB().Transaction(func(tx *gorm.DB) error {
		return s.CancelCallTx(tx, callID, req)
	})
}

func (s *APICallService) CancelCallTx(tx *gorm.DB, callID string, req *CancelCallRequest) error {
	if tx == nil {
		return fmt.Errorf("%w: database is nil", ErrAPICallInvalidInput)
	}
	if req == nil {
		req = &CancelCallRequest{}
	}
	var call model.APICall
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&call, "id = ?", callID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAPICallNotFound
		}
		return err
	}
	if call.Status == model.APICallStatusCancelled {
		return nil
	}
	if err := validateCallLease(&call, req.LeaseOwner); err != nil {
		return err
	}
	if call.Status != model.APICallStatusReceived && call.Status != model.APICallStatusInProgress {
		return callTransitionError(call.ID, call.Status, model.APICallStatusCancelled)
	}

	now := time.Now()
	updates := map[string]any{
		"status":              model.APICallStatusCancelled,
		"final_attempt_id":    req.FinalAttemptID,
		"http_status":         req.HTTPStatus,
		"error_type":          req.ErrorType,
		"error_code":          req.ErrorCode,
		"error_message":       sanitizeStoredErrorMessage(req.ErrorMessage),
		"error_retryable":     false,
		"client_disconnected": req.ClientDisconnected,
		"completed_at":        now,
		"duration_ms":         elapsedMilliseconds(call.StartedAt, now),
		"lease_owner":         "",
		"lease_expires_at":    nil,
	}
	if len(req.ErrorParam) > 0 {
		updates["error_param"] = req.ErrorParam
	}
	if err := stageTerminalConversationProjectionOutputTx(tx, &call, req.ConversationProjection); err != nil {
		return err
	}
	result := tx.Model(&model.APICall{}).
		Where("id = ? AND status IN ?", call.ID, []model.APICallStatus{
			model.APICallStatusReceived,
			model.APICallStatusInProgress,
		}).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return callTransitionError(call.ID, call.Status, model.APICallStatusCancelled)
	}

	var attempts []model.APICallAttempt
	if err := tx.Where("call_id = ? AND status = ?", call.ID, model.APICallAttemptStatusStarted).
		Find(&attempts).Error; err != nil {
		return err
	}
	for i := range attempts {
		if err := tx.Model(&model.APICallAttempt{}).
			Where("id = ? AND status = ?", attempts[i].ID, model.APICallAttemptStatusStarted).
			Updates(map[string]any{
				"status":       model.APICallAttemptStatusCancelled,
				"completed_at": now,
				"duration_ms":  elapsedMilliseconds(attempts[i].StartedAt, now),
			}).Error; err != nil {
			return err
		}
	}
	return nil
}

// RecordPayload stores payload content only when retention is enabled for the call.
func (s *APICallService) RecordPayload(payload *model.APICallPayload) error {
	if payload == nil || strings.TrimSpace(payload.CallID) == "" || strings.TrimSpace(payload.Kind) == "" {
		return fmt.Errorf("%w: call id and payload kind are required", ErrAPICallInvalidInput)
	}

	var call model.APICall
	if err := model.DB().Select("id", "retain_payload", "payload_expires_at").First(&call, "id = ?", payload.CallID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAPICallNotFound
		}
		return err
	}
	if !call.RetainPayload {
		return nil
	}
	if payload.AttemptID > 0 {
		var count int64
		if err := model.DB().Model(&model.APICallAttempt{}).
			Where("id = ? AND call_id = ?", payload.AttemptID, payload.CallID).
			Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return ErrAPICallAttemptNotFound
		}
	}
	if payload.ContentType == "" {
		payload.ContentType = "application/json"
	}
	originalBytes := len(payload.Data)
	if payload.OriginalBytes == 0 {
		payload.OriginalBytes = int64(originalBytes)
	}
	// 正文保留是可选观测能力；写库前统一脱敏和限长，不能依赖各协议调用方自行处理。
	payload.Data = sanitizeAPICallPayload(payload.Data)
	maxBytes := apiCallPayloadMaxBytes()
	if maxBytes > 0 && len(payload.Data) > maxBytes {
		payload.Data = append([]byte(nil), payload.Data[:maxBytes]...)
		payload.Truncated = true
	}
	if _, err := encryptAPICallPayload(payload); err != nil {
		return err
	}
	if payload.ExpiresAt == nil {
		payload.ExpiresAt = call.PayloadExpiresAt
	}
	if payload.ExpiresAt == nil {
		_, payload.ExpiresAt = resolvePayloadPolicy(boolPointer(true), nil, time.Now())
	}
	return model.DB().Create(payload).Error
}

func (s *APICallService) DeleteCallPayloads(callID string) error {
	if strings.TrimSpace(callID) == "" {
		return fmt.Errorf("%w: call id is required", ErrAPICallInvalidInput)
	}
	return model.DB().Where("call_id = ?", callID).Delete(&model.APICallPayload{}).Error
}

func (s *APICallService) DeleteExpiredPayloads(now time.Time, limit int) (int64, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	legacyCutoff := now.Add(-time.Duration(config.DefaultAPICallPayloadRetentionHours) * time.Hour)
	var ids []uint
	err := model.DB().Model(&model.APICallPayload{}).
		Where("expires_at <= ? OR (expires_at IS NULL AND created_at <= ?)", now, legacyCutoff).
		Order("id ASC").Limit(limit).Pluck("id", &ids).Error
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	result := model.DB().Where("id IN ?", ids).Delete(&model.APICallPayload{})
	return result.RowsAffected, result.Error
}

func (s *APICallService) ListCalls(req *ListCallsRequest) (*ListCallsResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is nil", ErrAPICallInvalidInput)
	}
	page := req.Page
	if page <= 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	snapshot := time.Now().Truncate(time.Millisecond)
	if value := strings.TrimSpace(req.SnapshotAt); value != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid snapshot_at", ErrAPICallInvalidInput)
		}
		snapshot = parsed.In(time.Local).Truncate(time.Millisecond)
	}

	query := model.DB().Model(&model.APICall{}).Where("created_at < ?", snapshot)
	if req.IsAdmin {
		if req.UserID > 0 {
			query = query.Where("user_id = ?", req.UserID)
		}
	} else {
		if req.ActorUserID == 0 {
			return nil, ErrAPICallAccessDenied
		}
		query = query.Where("user_id = ?", req.ActorUserID)
	}
	if req.TokenID > 0 {
		query = query.Where("token_id = ?", req.TokenID)
	}
	if req.Model != "" {
		query = query.Where("model = ?", req.Model)
	}
	if req.Endpoint != "" {
		query = query.Where("endpoint = ?", req.Endpoint)
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}
	if req.RequestID != "" {
		query = query.Where("request_id = ?", req.RequestID)
	}
	if req.CallID != "" {
		query = query.Where("id = ?", req.CallID)
	}

	if req.IsAdmin && (req.RouteKind != "" || req.ChannelID > 0 || req.Transport != "") {
		attempts := model.DB().Model(&model.APICallAttempt{}).Select("call_id")
		if req.RouteKind != "" {
			attempts = attempts.Where("route_kind = ?", req.RouteKind)
		}
		if req.ChannelID > 0 {
			endpointIDs := model.DB().Model(&model.Endpoint{}).
				Select("id").Where("channel_id = ?", req.ChannelID)
			attempts = attempts.Where(
				"(route_kind = ? AND channel_id = ?) OR (route_kind = ? AND endpoint_id IN (?)) OR (route_kind = ? AND channel_id = ?)",
				model.APICallRouteGatewayV2,
				req.ChannelID,
				model.APICallRouteCapability,
				endpointIDs,
				model.APICallRouteVideo,
				req.ChannelID,
			)
		}
		if req.Transport != "" {
			attempts = attempts.Where("transport = ?", req.Transport)
		}
		query = query.Where("id IN (?)", attempts)
	}

	if req.StartDate != "" {
		start, err := parseCallDate(req.StartDate, false)
		if err != nil {
			return nil, err
		}
		query = query.Where("created_at >= ?", start)
	}
	if req.EndDate != "" {
		end, exclusive, err := parseCallEndDate(req.EndDate)
		if err != nil {
			return nil, err
		}
		if exclusive {
			query = query.Where("created_at < ?", end)
		} else {
			query = query.Where("created_at <= ?", end)
		}
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var items []model.APICall
	if err := query.Order("created_at DESC").Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&items).Error; err != nil {
		return nil, err
	}
	if !req.IsAdmin {
		for index := range items {
			sanitizeUserCall(&items[index])
		}
	}
	return &ListCallsResponse{
		Items:      items,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		SnapshotAt: snapshot.Format(time.RFC3339Nano),
	}, nil
}

func (s *APICallService) GetCallDetail(callID string, actorUserID uint, isAdmin bool) (*APICallDetail, error) {
	query := model.DB().Where("id = ?", callID)
	if !isAdmin {
		if actorUserID == 0 {
			return nil, ErrAPICallAccessDenied
		}
		query = query.Where("user_id = ?", actorUserID)
	}

	var call model.APICall
	if err := query.First(&call).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAPICallNotFound
		}
		return nil, err
	}

	detail := &APICallDetail{
		Call:        call,
		Attempts:    make([]model.APICallAttempt, 0),
		BillingLogs: make([]model.BillingLog, 0),
		Payloads:    make([]APICallPayloadDetail, 0),
	}
	if err := model.DB().Where("call_id = ?", call.ID).
		Order("attempt_no ASC").Order("id ASC").
		Find(&detail.Attempts).Error; err != nil {
		return nil, err
	}
	if err := model.DB().Where("call_id = ?", call.ID).
		Order("id ASC").Find(&detail.BillingLogs).Error; err != nil {
		return nil, err
	}

	if isAdmin {
		var payloads []model.APICallPayload
		if err := model.DB().Where("call_id = ? AND (expires_at IS NULL OR expires_at > ?)", call.ID, time.Now()).
			Order("id ASC").Find(&payloads).Error; err != nil {
			return nil, err
		}
		detail.Payloads = make([]APICallPayloadDetail, len(payloads))
		for i := range payloads {
			data, decryptErr := decryptAPICallPayload(&payloads[i])
			if decryptErr != nil {
				logger.Error("decrypt API call payload",
					zap.String("call_id", payloads[i].CallID), zap.Uint("payload_id", payloads[i].ID), zap.Error(decryptErr))
				data = []byte("[encrypted payload unavailable]")
			}
			detail.Payloads[i] = APICallPayloadDetail{
				APICallPayload: payloads[i],
				Data:           string(data),
			}
		}
	}
	if !isAdmin {
		sanitizeUserCallDetail(detail)
	}
	return detail, nil
}

func sanitizeUserCallDetail(detail *APICallDetail) {
	if detail == nil {
		return
	}
	sanitizeUserCall(&detail.Call)
	for index := range detail.Attempts {
		attempt := &detail.Attempts[index]
		attempt.AbilityID = 0
		attempt.ChannelID = 0
		attempt.KeyID = 0
		attempt.EndpointID = 0
		attempt.AccountID = 0
		attempt.Protocol = ""
		attempt.Transport = ""
		attempt.VendorModel = ""
		attempt.RequestPath = ""
		attempt.ProviderResponseID = ""
		attempt.ErrorMessage = ""
	}
	for index := range detail.BillingLogs {
		entry := &detail.BillingLogs[index]
		entry.IdempotentKey = ""
		entry.TokenID = 0
		entry.UserID = 0
		entry.PricingSnapshot = nil
	}
	detail.Payloads = make([]APICallPayloadDetail, 0)
}

func sanitizeUserCall(call *model.APICall) {
	if call == nil {
		return
	}
	call.UserID = 0
	call.TokenID = 0
	call.ErrorMessage = ""
	call.ErrorParam = nil
}

func getCallForUpdateWithDB(db *gorm.DB, callID string) (*model.APICall, error) {
	return loadCallWithDB(db, callID, true)
}

func loadCallWithDB(db *gorm.DB, callID string, lock bool) (*model.APICall, error) {
	if strings.TrimSpace(callID) == "" {
		return nil, fmt.Errorf("%w: call id is required", ErrAPICallInvalidInput)
	}
	var call model.APICall
	query := db
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.First(&call, "id = ?", callID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAPICallNotFound
		}
		return nil, err
	}
	return &call, nil
}

func validateCallLease(call *model.APICall, owner string) error {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil
	}
	if call == nil || call.LeaseOwner != owner || call.LeaseExpiresAt == nil || !call.LeaseExpiresAt.After(time.Now()) {
		return ErrAPICallLeaseUnavailable
	}
	return nil
}

func stageTerminalConversationProjectionOutputTx(
	tx *gorm.DB,
	call *model.APICall,
	projection *ConversationProjectionOutputRequest,
) error {
	// 会话输出与 Call 终态共用事务，防止进程在两次写入之间退出而留下不完整历史。
	if call == nil {
		return fmt.Errorf("%w: API call is nil", ErrAPICallInvalidInput)
	}
	if !call.ProjectConversation {
		if projection != nil {
			return fmt.Errorf("%w: call %s is not configured for conversation projection", ErrAPICallInvalidInput, call.ID)
		}
		return nil
	}
	callID := strings.TrimSpace(call.ID)
	request := ConversationProjectionOutputRequest{CallID: callID}
	if projection != nil {
		request = *projection
	}
	if projectionCallID := strings.TrimSpace(request.CallID); projectionCallID != "" && projectionCallID != callID {
		return fmt.Errorf(
			"%w: conversation projection call id %s does not match terminal call %s",
			ErrAPICallInvalidInput,
			projectionCallID,
			callID,
		)
	}
	request.CallID = callID
	var updated bool
	var err error
	if projection == nil {
		updated, err = StageAPIConversationProjectionOutputIfMissingTx(tx, request)
	} else {
		updated, err = StageAPIConversationProjectionOutputIfPresentTx(tx, request)
	}
	if err != nil {
		return fmt.Errorf("stage terminal conversation projection output: %w", err)
	}
	if updated {
		return nil
	}

	// Some databases report zero affected rows when the same output was
	// already staged. Confirm readiness without weakening the invariant.
	var entry model.ConversationProjectionOutbox
	if err := tx.Select("output_ready").
		Where("call_id = ? AND input_ready = ?", callID, true).
		First(&entry).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: input-ready entry for call %s", ErrConversationProjectionNotFound, callID)
		}
		return fmt.Errorf("verify terminal conversation projection output: %w", err)
	}
	if !entry.OutputReady {
		return fmt.Errorf("%w: output for call %s", ErrConversationProjectionNotReady, callID)
	}
	return nil
}

func getAttempt(attemptID uint) (*model.APICallAttempt, error) {
	if attemptID == 0 {
		return nil, fmt.Errorf("%w: attempt id is required", ErrAPICallInvalidInput)
	}
	var attempt model.APICallAttempt
	if err := model.DB().First(&attempt, attemptID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAPICallAttemptNotFound
		}
		return nil, err
	}
	return &attempt, nil
}

func getCallAttemptWithDB(db *gorm.DB, callID string, attemptID uint) (*model.APICallAttempt, error) {
	var attempt model.APICallAttempt
	if err := db.Where("id = ? AND call_id = ?", attemptID, callID).First(&attempt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAPICallAttemptNotFound
		}
		return nil, err
	}
	return &attempt, nil
}

func transitionCallStatus(
	db *gorm.DB,
	callID string,
	target model.APICallStatus,
	allowed []model.APICallStatus,
	updates map[string]any,
) error {
	if strings.TrimSpace(callID) == "" {
		return fmt.Errorf("%w: call id is required", ErrAPICallInvalidInput)
	}
	if updates == nil {
		updates = make(map[string]any)
	}
	updates["status"] = target
	result := db.Model(&model.APICall{}).
		Where("id = ? AND status IN ?", callID, allowed).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	var current model.APICall
	if err := db.Select("id", "status").First(&current, "id = ?", callID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAPICallNotFound
		}
		return err
	}
	if current.Status == target {
		return nil
	}
	return callTransitionError(callID, current.Status, target)
}

func transitionAttemptStatus(
	db *gorm.DB,
	attemptID uint,
	target model.APICallAttemptStatus,
	allowed []model.APICallAttemptStatus,
	updates map[string]any,
) error {
	if attemptID == 0 {
		return fmt.Errorf("%w: attempt id is required", ErrAPICallInvalidInput)
	}
	if updates == nil {
		updates = make(map[string]any)
	}
	updates["status"] = target
	result := db.Model(&model.APICallAttempt{}).
		Where("id = ? AND status IN ?", attemptID, allowed).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	var current model.APICallAttempt
	if err := db.Select("id", "status").First(&current, attemptID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAPICallAttemptNotFound
		}
		return err
	}
	if current.Status == target {
		return nil
	}
	return attemptTransitionError(attemptID, current.Status, target)
}

func callTransitionError(callID string, current, target model.APICallStatus) error {
	return fmt.Errorf("%w: call %s is %s, cannot become %s", ErrAPICallInvalidTransition, callID, current, target)
}

func attemptTransitionError(attemptID uint, current, target model.APICallAttemptStatus) error {
	return fmt.Errorf("%w: attempt %d is %s, cannot become %s", ErrAPICallInvalidTransition, attemptID, current, target)
}

func fillCallCompletionFromAttempt(result *CompleteCallRequest, attempt *model.APICallAttempt) {
	if result.HTTPStatus == 0 {
		result.HTTPStatus = attempt.HTTPStatus
	}
	fillUsageFromAttempt(
		&result.InputTokens,
		&result.OutputTokens,
		&result.TotalTokens,
		&result.CachedInputTokens,
		&result.ReasoningOutputTokens,
		&result.UsageJSON,
		attempt,
	)
}

func fillCallFailureFromAttempt(result *FailCallRequest, attempt *model.APICallAttempt) {
	if result.HTTPStatus == 0 {
		result.HTTPStatus = attempt.HTTPStatus
	}
	if result.ErrorType == "" {
		result.ErrorType = attempt.ErrorType
	}
	if result.ErrorCode == "" {
		result.ErrorCode = attempt.ErrorCode
	}
	if result.ErrorMessage == "" {
		result.ErrorMessage = attempt.ErrorMessage
	}
	if !result.ErrorRetryable {
		result.ErrorRetryable = attempt.ErrorRetryable
	}
	fillUsageFromAttempt(
		&result.InputTokens,
		&result.OutputTokens,
		&result.TotalTokens,
		&result.CachedInputTokens,
		&result.ReasoningOutputTokens,
		&result.UsageJSON,
		attempt,
	)
}

func fillUsageFromAttempt(
	inputTokens *int,
	outputTokens *int,
	totalTokens *int,
	cachedInputTokens *int,
	reasoningOutputTokens *int,
	usageJSON *datatypes.JSON,
	attempt *model.APICallAttempt,
) {
	if *inputTokens == 0 {
		*inputTokens = attempt.InputTokens
	}
	if *outputTokens == 0 {
		*outputTokens = attempt.OutputTokens
	}
	if *totalTokens == 0 {
		*totalTokens = attempt.TotalTokens
	}
	if *cachedInputTokens == 0 {
		*cachedInputTokens = attempt.CachedInputTokens
	}
	if *reasoningOutputTokens == 0 {
		*reasoningOutputTokens = attempt.ReasoningOutputTokens
	}
	if len(*usageJSON) == 0 && len(attempt.UsageJSON) > 0 {
		*usageJSON = attempt.UsageJSON
	}
}

func elapsedMilliseconds(start, end time.Time) int64 {
	if start.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func resolvePayloadPolicy(override *bool, expiresAt *time.Time, now time.Time) (bool, *time.Time) {
	enabled := false
	retentionHours := config.DefaultAPICallPayloadRetentionHours
	if cfg := config.Get(); cfg != nil {
		enabled = cfg.Observability.RetainAPICallPayloads
		if cfg.Observability.APICallPayloadRetentionHours > 0 {
			retentionHours = cfg.Observability.APICallPayloadRetentionHours
		}
	}
	if override != nil {
		enabled = *override
	}
	if !enabled {
		return false, nil
	}
	if expiresAt != nil {
		value := *expiresAt
		return true, &value
	}
	value := now.Add(time.Duration(retentionHours) * time.Hour)
	return true, &value
}

func apiCallPayloadMaxBytes() int {
	if cfg := config.Get(); cfg != nil && cfg.Observability.APICallPayloadMaxBytes > 0 {
		return cfg.Observability.APICallPayloadMaxBytes
	}
	return config.DefaultAPICallPayloadMaxBytes
}

func boolPointer(value bool) *bool { return &value }

func sanitizeAPICallPayload(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return sanitizeTextAPICallPayload(data)
	}
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return append([]byte(nil), data...)
	}
	redactAPICallPayloadValue(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return append([]byte(nil), data...)
	}
	return encoded
}

func redactAPICallPayloadValue(value any) {
	switch current := value.(type) {
	case []any:
		for _, item := range current {
			redactAPICallPayloadValue(item)
		}
	case map[string]any:
		for key, child := range current {
			if sensitivePayloadName(key) {
				current[key] = "[REDACTED]"
				continue
			}
			if text, ok := child.(string); ok {
				if strings.HasPrefix(text, "data:") && len(text) > 1024 {
					current[key] = "[OMITTED]"
					continue
				}
				current[key] = string(sanitizeTextAPICallPayload([]byte(text)))
				continue
			}
			redactAPICallPayloadValue(child)
		}
	}
}

func redactSignedURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return value
	}
	changed := parsed.User != nil
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		if sensitivePayloadName(key) {
			query.Set(key, "[REDACTED]")
			changed = true
		}
	}
	if !changed {
		return value
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func sensitivePayloadName(name string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(strings.TrimSpace(name)))
	compact := strings.ReplaceAll(normalized, "_", "")
	switch normalized {
	case "authorization", "proxy_authorization", "api_key", "x_api_key", "key", "sig", "token", "access_token", "refresh_token", "cookie", "set_cookie":
		return true
	}
	switch compact {
	case "authorization", "proxyauthorization", "apikey", "xapikey", "clientkey", "privatekey", "secretkey", "signingkey", "encryptionkey", "decryptionkey", "token", "accesstoken", "refreshtoken", "idtoken", "sessiontoken", "cookie", "setcookie", "awsaccesskeyid":
		return true
	}
	return strings.Contains(normalized, "secret") || strings.Contains(normalized, "password") ||
		strings.HasSuffix(normalized, "_token") || strings.Contains(normalized, "signature") ||
		strings.Contains(normalized, "credential") || strings.Contains(compact, "accesskey") ||
		strings.HasSuffix(compact, "token")
}

var (
	bearerCredentialPattern = regexp.MustCompile(`(?i)\bBearer\s+[^\s"']+`)
	secretAssignmentPattern = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|password|secret|signature|credential)(\s*[:=]\s*["']?)[^&\s,"'}]+`)
	signedURLPattern        = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

func sanitizeTextAPICallPayload(data []byte) []byte {
	text := bearerCredentialPattern.ReplaceAllString(string(data), "Bearer [REDACTED]")
	text = secretAssignmentPattern.ReplaceAllString(text, "${1}${2}[REDACTED]")
	text = signedURLPattern.ReplaceAllStringFunc(text, func(candidate string) string {
		suffixStart := len(candidate)
		for suffixStart > 0 && strings.ContainsRune(",;)]}", rune(candidate[suffixStart-1])) {
			suffixStart--
		}
		return redactSignedURL(candidate[:suffixStart]) + candidate[suffixStart:]
	})
	return []byte(text)
}

func sanitizeStoredErrorMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	if json.Valid([]byte(message)) {
		message = string(sanitizeAPICallPayload([]byte(message)))
	}
	message = bearerCredentialPattern.ReplaceAllString(message, "Bearer [REDACTED]")
	message = secretAssignmentPattern.ReplaceAllString(message, "${1}${2}[REDACTED]")
	parts := strings.Fields(message)
	for index, part := range parts {
		urlIndex := strings.Index(part, "http://")
		if urlIndex < 0 {
			urlIndex = strings.Index(part, "https://")
		}
		if urlIndex < 0 {
			continue
		}
		suffixStart := len(part)
		for suffixStart > urlIndex && strings.ContainsRune(",;)]}\"'", rune(part[suffixStart-1])) {
			suffixStart--
		}
		target := part[urlIndex:suffixStart]
		parts[index] = part[:urlIndex] + redactSignedURL(target) + part[suffixStart:]
	}
	message = strings.Join(parts, " ")
	runes := []rune(message)
	if len(runes) > 4096 {
		message = string(runes[:4096])
	}
	return message
}

func SanitizeAPICallErrorMessage(message string) string {
	return sanitizeStoredErrorMessage(message)
}

func parseCallDate(value string, endOfDay bool) (time.Time, error) {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid date %q", ErrAPICallInvalidInput, value)
	}
	if endOfDay {
		return parsed.AddDate(0, 0, 1), nil
	}
	return parsed, nil
}

func parseCallEndDate(value string) (time.Time, bool, error) {
	trimmed := strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed, false, nil
	}
	parsed, err := parseCallDate(trimmed, true)
	return parsed, true, err
}
