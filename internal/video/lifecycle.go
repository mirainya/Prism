package video

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/queue"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func StartCallAttempt(ctx context.Context, task *VideoTask, channel *VideoChannel, key *VideoChannelKey, adapter Adapter) (*model.APICallAttempt, error) {
	if task == nil || channel == nil || key == nil {
		return nil, errors.New("video call attempt context is incomplete")
	}
	if task.CallID == "" {
		return nil, nil
	}
	vendorModel := task.VendorModel
	if vendorModel == "" {
		vendorModel = task.Model
	}
	db := model.DB().WithContext(ctx)
	var existing model.APICallAttempt
	err := db.Where("call_id = ? AND status = ?", task.CallID, model.APICallAttemptStatusStarted).
		Order("id DESC").First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	requestPath := "/api/v3/contents/generations/tasks"
	if provider, ok := adapter.(RequestPathProvider); ok && provider.RequestPath() != "" {
		requestPath = provider.RequestPath()
	}
	return service.NewAPICallService().StartAttempt(&service.StartAttemptRequest{
		CallID: task.CallID, RouteKind: model.APICallRouteVideo, Stage: model.APICallStageSubmit,
		ChannelID: channel.ID, KeyID: key.ID, Protocol: model.ProtocolCustom,
		VendorModel: vendorModel, Transport: model.UpstreamTransportVideoGeneration,
		RequestPath: requestPath,
	})
}

func CompleteTask(ctx context.Context, taskID, providerTaskID string, result *GenerationResult, pollCount int) (bool, error) {
	return finishTask(ctx, taskID, VideoTaskStatusCompleted, providerTaskID, result, "", pollCount)
}

func FailTask(ctx context.Context, taskID, message string) (bool, error) {
	return finishTask(ctx, taskID, VideoTaskStatusFailed, "", nil, message, 0)
}

func CancelTask(ctx context.Context, taskID string) (bool, error) {
	return finishTask(ctx, taskID, VideoTaskStatusCancelled, "", nil, "video task cancelled", 0)
}

// CancelVideoTask performs the provider cancellation and the local terminal
// transition as one shared operation for HTTP and worker callers.
func (e *Engine) CancelVideoTask(ctx context.Context, task *VideoTask) (bool, error) {
	if e == nil || e.db == nil || e.registry == nil {
		return false, ErrEngineUnavailable
	}
	if task == nil || task.ID == "" {
		return false, ErrInvalidTaskRequest
	}
	if task.Status.IsTerminal() {
		return false, nil
	}

	if task.ProviderTaskID != "" {
		channel, key, _, err := LoadVideoTaskRoute(e.db.WithContext(ctx), task)
		if err != nil {
			return false, err
		}
		adapter := e.registry.Get(channel.AdapterType, channel, key)
		canceller, ok := adapter.(Canceller)
		if !ok {
			return false, ErrCancelNotSupported
		}
		if !canceller.CanCancel(task.Status) {
			return false, ErrCancelNotAllowed
		}
		cancelCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = canceller.Cancel(cancelCtx, task.ProviderTaskID)
		cancel()
		if err != nil {
			return false, err
		}
	} else {
		channel, key, _, err := LoadVideoTaskRoute(e.db.WithContext(ctx), task)
		if err != nil {
			return false, err
		}
		adapter := e.registry.Get(channel.AdapterType, channel, key)
		if policy, ok := adapter.(LocalCancellationPolicy); ok && !policy.CanCancelLocal(task) {
			return false, ErrCancelNotSupported
		}
	}

	cancelled, err := CancelTask(ctx, task.ID)
	if err != nil {
		return false, err
	}
	if !cancelled {
		return false, nil
	}
	e.router.ReleaseConcurrency(ctx, task.KeyID)
	if task.CallbackURL != "" {
		_ = queue.EnqueueVideoNotify(task.ID)
	}
	return true, nil
}

func finishTask(
	ctx context.Context,
	taskID string,
	status VideoTaskStatus,
	providerTaskID string,
	result *GenerationResult,
	message string,
	pollCount int,
) (bool, error) {
	terminalized := false
	err := model.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task VideoTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, "id = ?", taskID).Error; err != nil {
			return err
		}
		if task.Status.IsTerminal() {
			return nil
		}

		attemptID, err := latestAttemptIDTx(tx, task.CallID)
		if err != nil {
			return err
		}
		now := time.Now()
		updates := map[string]any{
			"status": status, "completed_at": &now,
			"error_message": message, "submit_checkpoint": nil,
		}
		if providerTaskID != "" {
			updates["provider_task_id"] = providerTaskID
		}
		if pollCount > 0 {
			updates["poll_count"] = pollCount
		}

		billing := service.NewBillingService()
		calls := service.NewAPICallService()
		switch status {
		case VideoTaskStatusCompleted:
			resultJSON, err := json.Marshal(result)
			if err != nil {
				return err
			}
			updates["result_json"] = resultJSON
			updates["progress"] = 100
			updates["final_cost"] = task.EstimatedCost
			updates["billing_status"] = "charged"
			if task.CallID != "" && task.EstimatedCost.IsPositive() {
				if err := billing.SettleReservationWithBillingContextTx(
					tx, task.TokenID, task.UserID, task.EstimatedCost, task.EstimatedCost,
					task.ID+":settle", service.BillingContext{
						CallID: task.CallID, AttemptID: attemptID, Phase: model.BillingPhaseSettle,
					},
				); err != nil {
					return err
				}
			}
			if task.CallID != "" {
				if err := calls.CompleteCallTx(tx, task.CallID, &service.CompleteCallRequest{
					FinalAttemptID: attemptID, HTTPStatus: http.StatusOK,
					ProviderResponseID: providerTaskID, CompleteStartedAttempt: attemptID > 0,
				}); err != nil {
					return err
				}
			}
		case VideoTaskStatusFailed:
			updates["final_cost"] = 0
			updates["billing_status"] = "refunded"
			if err := refundReservationTx(tx, &task, attemptID); err != nil {
				return err
			}
			if task.CallID != "" {
				if err := calls.FailCallTx(tx, task.CallID, &service.FailCallRequest{
					FinalAttemptID: attemptID, HTTPStatus: http.StatusBadGateway,
					ErrorType: "upstream_error", ErrorCode: "video_generation_failed",
					ErrorMessage: message, FailStartedAttempt: attemptID > 0,
				}); err != nil {
					return err
				}
			}
		case VideoTaskStatusCancelled:
			updates["final_cost"] = 0
			updates["billing_status"] = "refunded"
			if err := refundReservationTx(tx, &task, attemptID); err != nil {
				return err
			}
			if task.CallID != "" {
				if err := calls.CancelCallTx(tx, task.CallID, &service.CancelCallRequest{
					FinalAttemptID: attemptID, HTTPStatus: http.StatusOK,
					ErrorType: "cancelled", ErrorCode: "video_cancelled", ErrorMessage: message,
				}); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unsupported terminal video status %q", status)
		}

		result := tx.Model(&VideoTask{}).
			Where("id = ? AND status IN ?", task.ID, []VideoTaskStatus{
				VideoTaskStatusQueued, VideoTaskStatusSubmitted, VideoTaskStatusTracking,
			}).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		terminalized = result.RowsAffected == 1
		return nil
	})
	return terminalized, err
}

func latestAttemptIDTx(tx *gorm.DB, callID string) (uint, error) {
	if callID == "" {
		return 0, nil
	}
	var attempt model.APICallAttempt
	err := tx.Where("call_id = ?", callID).Order("id DESC").First(&attempt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return attempt.ID, err
}

func refundReservationTx(tx *gorm.DB, task *VideoTask, attemptID uint) error {
	if task.CallID == "" || task.EstimatedCost.IsZero() {
		return nil
	}
	return service.NewBillingService().RefundWithBillingContextTx(
		tx, task.TokenID, task.UserID, task.EstimatedCost, task.ID+":refund",
		service.BillingContext{CallID: task.CallID, AttemptID: attemptID, Phase: model.BillingPhaseRefund},
	)
}
