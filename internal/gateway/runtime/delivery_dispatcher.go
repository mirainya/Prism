package runtime

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

// ReconcileDelivery performs an authoritative provider query for one failed
// or expired delivery. It never changes the generation, billing or task state
// and never sends a submit request.
func (d *AsyncDispatcher) ReconcileDelivery(ctx context.Context, item repository.OutboxItem) error {
	if d == nil || d.service == nil || item.ResultDeliveryID == 0 || item.Action != "reconcile_delivery" || item.CallID != 0 || item.AttemptID != 0 || item.AsyncExecutionID != 0 || item.CallbackReceiptID != 0 {
		return &PermanentDispatchError{Code: "invalid_delivery_reconciliation"}
	}
	handled, err := d.service.PrepareDeliveryReconciliation(ctx, item)
	if err != nil || handled {
		return err
	}
	target, err := d.service.Store.ReadDeliveryRecoveryTarget(ctx, item.ResultDeliveryID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return &PermanentDispatchError{Code: "delivery_not_refreshable"}
		}
		return err
	}
	if target.StateVersion != item.StateVersion || target.ActionSeq != item.ActionSeq {
		return repository.ErrConflict
	}
	if target.Mode == "managed_copy" {
		return d.recoverManagedCopy(ctx, item, target)
	}
	fixed, err := d.service.Store.ReadAsyncDispatch(ctx, target.AsyncExecutionID)
	if err != nil {
		return err
	}
	if fixed.AttemptID != target.AttemptID || fixed.TaskIdentityBlobID == 0 || fixed.SourceURLPolicy != "refreshable" || fixed.DeliveryMode != "reference" {
		return &PermanentDispatchError{Code: "delivery_dispatch_mismatch"}
	}
	codec, ok := d.codecs[fmt.Sprintf("%s@%d", fixed.AdapterCode, fixed.AdapterVersion)]
	if !ok {
		return &PermanentDispatchError{Code: "unsupported_async_protocol"}
	}
	body, err := d.openBlob(ctx, fixed.RequestBlobID, fmt.Sprintf("call:%d:request", fixed.CallID), "gateway-payload", false)
	if err != nil {
		return err
	}
	defer clear(body)
	taskID, err := d.openBlob(ctx, fixed.TaskIdentityBlobID, fmt.Sprintf("async:%d:task-identity", target.AsyncExecutionID), "gateway-task-identity", false)
	if err != nil {
		return err
	}
	defer clear(taskID)
	prepared, err := codec.Prepare(ctx, "query", fixed, body, string(taskID))
	if err != nil {
		return &PermanentDispatchError{Code: "invalid_async_request"}
	}
	defer clear(prepared.Body)
	secret, err := d.openCredential(ctx, fixed.CredentialID, fixed.CredentialSecret, fixed.CredentialBlobID)
	if err != nil {
		return err
	}
	defer clear(secret)
	request, err := d.prepareHTTP(ctx, fixed, prepared, secret)
	if err != nil {
		return err
	}
	timeout := min(time.Duration(min(fixed.TransportTimeoutMS, uint64(120000)))*time.Millisecond, time.Until(item.LeaseExpiresAt)-5*time.Second)
	if timeout <= 0 {
		return context.DeadlineExceeded
	}
	workCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request = request.WithContext(workCtx)
	mapping := security.DomainDigest(d.keys.PayloadHMAC, "delivery-reconcile-request-v1", []byte(fixed.Protocol), []byte(prepared.Method), []byte(request.URL.String()), []byte(strconv.FormatUint(item.ResultDeliveryID, 10)), prepared.Body)
	requestID, err := d.service.BeginDeliveryReconcileRequest(ctx, item, hex.EncodeToString(mapping[:]), d.digest(prepared.Body))
	if err != nil {
		return err
	}
	responseBody, response := d.exchange(request)
	defer clear(responseBody)
	markCtx, markCancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
	defer markCancel()
	if response.ErrorCode != "" {
		return d.finishDeliveryExchangeFailure(markCtx, item, requestID, response)
	}
	observation, err := codec.Decode("query", body, responseBody)
	if err != nil {
		response.ErrorCode = "invalid_provider_response"
		if finishErr := d.service.FinishDeliveryReconcileRequest(markCtx, item, requestID, "response_recorded", response); finishErr != nil {
			return finishErr
		}
		return errors.New(response.ErrorCode)
	}
	if observation.TaskID != "" && observation.TaskID != string(taskID) {
		response.ErrorCode = "provider_task_identity_mismatch"
		if finishErr := d.service.FinishDeliveryReconcileRequest(markCtx, item, requestID, "response_recorded", response); finishErr != nil {
			return finishErr
		}
		return &PermanentDispatchError{Code: response.ErrorCode}
	}
	switch observation.State {
	case execution.AsyncAccepted, execution.AsyncRunning:
		if err := d.service.FinishDeliveryReconcileRequest(markCtx, item, requestID, "response_recorded", response); err != nil {
			return err
		}
		return errDeliveryRefreshPending
	case execution.AsyncFailed, execution.AsyncCancelled:
		if err := d.service.FinishDeliveryReconcileRequest(markCtx, item, requestID, "response_recorded", response); err != nil {
			return err
		}
		return &PermanentDispatchError{Code: "provider_terminal_without_result"}
	case execution.AsyncSucceeded:
	default:
		response.ErrorCode = "invalid_provider_state"
		if err := d.service.FinishDeliveryReconcileRequest(markCtx, item, requestID, "response_recorded", response); err != nil {
			return err
		}
		return errors.New(response.ErrorCode)
	}
	if err := delivery.ValidateSourcesForResource(target.ResourceKind, observation.Sources); err != nil || uint64(target.Ordinal) >= uint64(len(observation.Sources)) {
		response.ErrorCode = "invalid_provider_result_sources"
		if finishErr := d.service.FinishDeliveryReconcileRequest(markCtx, item, requestID, "response_recorded", response); finishErr != nil {
			return finishErr
		}
		return errors.New(response.ErrorCode)
	}
	if err := d.validateResultSources(markCtx, fixed, observation.Sources); err != nil {
		response.ErrorCode = "provider_result_host_not_allowed"
		if finishErr := d.service.FinishDeliveryReconcileRequest(markCtx, item, requestID, "response_recorded", response); finishErr != nil {
			return finishErr
		}
		return err
	}
	source := observation.Sources[target.Ordinal]
	if delivery.SourceKind(source) != target.SourceKind {
		response.ErrorCode = "provider_result_source_changed"
		if finishErr := d.service.FinishDeliveryReconcileRequest(markCtx, item, requestID, "response_recorded", response); finishErr != nil {
			return finishErr
		}
		return &PermanentDispatchError{Code: response.ErrorCode}
	}
	if source.ExpiresAt == nil {
		if err := d.service.FinishDeliveryReconcileRequest(markCtx, item, requestID, "response_recorded", response); err != nil {
			return err
		}
		return &PermanentDispatchError{Code: "source_expiry_unknown"}
	}
	if !source.ExpiresAt.After(time.Now().UTC()) {
		if err := d.service.FinishDeliveryReconcileRequest(markCtx, item, requestID, "response_recorded", response); err != nil {
			return err
		}
		return &PermanentDispatchError{Code: "source_expired"}
	}
	blob, err := d.payloadBlob(markCtx, []byte(source.URL))
	if err != nil {
		return err
	}
	return d.service.ApplyDeliveryRefresh(markCtx, item, requestID, response, blob, source.ExpiresAt.UTC())
}

func (d *AsyncDispatcher) finishDeliveryExchangeFailure(ctx context.Context, item repository.OutboxItem, requestID uint64, response repository.RequestLogResult) error {
	status := "unknown"
	if response.ResponseComplete {
		status = "response_recorded"
	}
	if err := d.service.FinishDeliveryReconcileRequest(ctx, item, requestID, status, response); err != nil {
		return err
	}
	return errors.New(response.ErrorCode)
}
