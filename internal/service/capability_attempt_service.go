package service

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type CapabilityAttemptMetadata = provider.RequestMetadata

func StartCapabilityAttempt(
	task *model.Task,
	endpoint *model.Endpoint,
	stage string,
) (*model.APICallAttempt, error) {
	if task == nil || endpoint == nil {
		return nil, fmt.Errorf("%w: task and endpoint are required", ErrAPICallInvalidInput)
	}
	if task.CallID == "" {
		return nil, nil
	}
	requestPath := endpoint.RequestPath
	if stage == model.APICallStagePoll {
		requestPath = endpoint.PollPath
	}
	requestPath = sanitizeCapabilityRequestPath(requestPath)

	var attempt *model.APICallAttempt
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		var err error
		attempt, err = NewAPICallService().StartAttemptTx(tx, &StartAttemptRequest{
			CallID:      task.CallID,
			RouteKind:   model.APICallRouteCapability,
			Stage:       stage,
			EndpointID:  endpoint.ID,
			AccountID:   task.AccountID,
			VendorModel: endpoint.VendorModel,
			RequestPath: requestPath,
		})
		if err != nil {
			return err
		}
		if stage != model.APICallStageSubmit {
			return nil
		}
		return tx.Model(&model.BillingLog{}).
			Where("call_id = ? AND phase = ? AND attempt_id = 0", task.CallID, model.BillingPhaseReserve).
			Update("attempt_id", attempt.ID).Error
	})
	return attempt, err
}

func FinishCapabilityAttempt(
	task *model.Task,
	channel *model.Channel,
	endpoint *model.Endpoint,
	attempt *model.APICallAttempt,
	stage string,
	metadata CapabilityAttemptMetadata,
	requestErr error,
) error {
	if task == nil || channel == nil || endpoint == nil {
		return fmt.Errorf("%w: task, channel and endpoint are required", ErrAPICallInvalidInput)
	}
	statusCode := metadata.StatusCode
	if requestErr != nil {
		errorStatusCode := domain.UpstreamStatusCode(requestErr)
		if errorStatusCode >= 400 && errorStatusCode < 600 && (statusCode == 0 || statusCode < 400) {
			statusCode = errorStatusCode
		}
	}
	requestPath := sanitizeCapabilityRequestPath(metadata.RequestPath)
	if requestPath == "" {
		requestPath = endpoint.RequestPath
		if stage == model.APICallStagePoll {
			requestPath = endpoint.PollPath
		}
	}
	requestPath = sanitizeCapabilityRequestPath(requestPath)

	if attempt != nil {
		var err error
		if requestErr != nil {
			err = NewAPICallService().FailAttempt(attempt.ID, &FailAttemptRequest{
				HTTPStatus:     statusCode,
				RequestPath:    requestPath,
				DurationMs:     metadata.DurationMs,
				ErrorType:      "upstream_error",
				ErrorCode:      stage + "_request_failed",
				ErrorMessage:   requestErr.Error(),
				ErrorRetryable: isRetryableCapabilityAttempt(statusCode),
			})
		} else {
			err = NewAPICallService().CompleteAttempt(attempt.ID, &CompleteAttemptRequest{
				HTTPStatus:  statusCode,
				RequestPath: requestPath,
				DurationMs:  metadata.DurationMs,
			})
		}
		if err != nil {
			return err
		}
	}

	requestAt := metadata.RequestAt
	if requestAt.IsZero() {
		if attempt != nil {
			requestAt = attempt.StartedAt
		} else {
			requestAt = time.Now()
		}
	}
	durationMs := metadata.DurationMs
	if durationMs <= 0 && attempt != nil {
		durationMs = time.Since(attempt.StartedAt).Milliseconds()
	}
	method := strings.TrimSpace(metadata.Method)
	requestType := model.RequestTypeSubmit
	if stage == model.APICallStagePoll {
		requestType = model.RequestTypePoll
		if method == "" {
			method = endpoint.PollMethod
		}
	} else if method == "" {
		method = endpoint.RequestMethod
	}
	if method == "" {
		if stage == model.APICallStagePoll {
			method = http.MethodGet
		} else {
			method = http.MethodPost
		}
	}

	errorMessage := ""
	if requestErr != nil {
		errorMessage = SanitizeAPICallErrorMessage(requestErr.Error())
	}
	attemptID := uint(0)
	if attempt != nil {
		attemptID = attempt.ID
	}
	requestLog := &model.ChannelRequestLog{
		CallID:         task.CallID,
		AttemptID:      attemptID,
		TaskID:         task.ID,
		TaskNo:         task.TaskNo,
		ChannelID:      task.ChannelID,
		AccountID:      task.AccountID,
		CapabilityCode: task.ModelCode,
		RequestType:    requestType,
		ModelCode:      task.ModelCode,
		VendorModel:    endpoint.VendorModel,
		RequestPath:    requestPath,
		Method:         method,
		URL:            joinUpstreamURL(channel.BaseURL, requestPath),
		StatusCode:     statusCode,
		DurationMs:     durationMs,
		ErrorMessage:   errorMessage,
		RequestAt:      requestAt,
	}
	if err := NewRequestLogService().Create(requestLog); err != nil {
		logger.Error("save capability request log failed",
			zap.String("call_id", task.CallID), zap.Uint("attempt_id", attemptID), zap.Error(err))
	}
	return nil
}

// AcknowledgeCapabilitySubmitAttempt completes an in-flight submit attempt
// when an authenticated callback proves that the upstream accepted the task.
func AcknowledgeCapabilitySubmitAttempt(task *model.Task) error {
	if task == nil || task.CallID == "" {
		return nil
	}
	var attempt model.APICallAttempt
	err := model.DB().Where(
		"call_id = ? AND route_kind = ? AND stage = ?",
		task.CallID,
		model.APICallRouteCapability,
		model.APICallStageSubmit,
	).Order("attempt_no DESC").First(&attempt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if attempt.Status == model.APICallAttemptStatusCompleted {
		return nil
	}
	if attempt.Status != model.APICallAttemptStatusStarted {
		return fmt.Errorf("%w: callback acknowledged submit attempt %d in state %s",
			ErrAPICallInvalidTransition, attempt.ID, attempt.Status)
	}
	return NewAPICallService().CompleteAttempt(attempt.ID, &CompleteAttemptRequest{})
}

func isRetryableCapabilityAttempt(statusCode int) bool {
	return statusCode == 0 || statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooManyRequests || statusCode >= 500
}

// CapabilityRequestReceivedHTTPResponse reports whether the upstream request
// returned an HTTP response, even when parsing or response handling failed.
func CapabilityRequestReceivedHTTPResponse(metadata CapabilityAttemptMetadata, requestErr error) bool {
	return metadata.StatusCode > 0 || domain.UpstreamStatusCode(requestErr) > 0
}

func joinUpstreamURL(baseURL, requestPath string) string {
	if parsed, err := url.Parse(strings.TrimSpace(baseURL)); err == nil {
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		parsed.User = nil
		parsed.Path = ""
		parsed.RawPath = ""
		baseURL = parsed.String()
	}
	if requestPath == "" {
		return strings.TrimRight(baseURL, "/")
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(requestPath, "/")
}

func sanitizeCapabilityRequestPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		if index := strings.IndexAny(value, "?#"); index >= 0 {
			return value[:index]
		}
		return value
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.Scheme = ""
	parsed.Host = ""
	parsed.User = nil
	return parsed.String()
}

// SanitizeCapabilityRequestPath removes credentials and query data before a
// provider request path is persisted in an operational checkpoint.
func SanitizeCapabilityRequestPath(value string) string {
	return sanitizeCapabilityRequestPath(value)
}
