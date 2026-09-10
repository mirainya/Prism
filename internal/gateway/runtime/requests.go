package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/repository"
)

type AttemptRequestEvidence struct {
	MappingHMAC, RequestBytesHMAC string
	Payload                       *repository.BlobInput
}

// BeginAttemptOutboxRequest is the only send authorization for a deferred
// synchronous Attempt. The request log and request-capacity slot commit only
// while the exact Outbox and Call leases are still valid.
func (s *Service) BeginAttemptOutboxRequest(ctx context.Context, item repository.OutboxItem, evidence AttemptRequestEvidence) (uint64, error) {
	if item.AttemptID == 0 || item.Action != "submit" && item.Action != "recover" {
		return 0, repository.ErrInvalidInput
	}
	var requestID uint64
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := s.Store.AssertAttemptOutboxLease(ctx, tx, item); err != nil {
			return err
		}
		if item.Action == "submit" {
			var priorID uint64
			var priorStatus string
			err := tx.QueryRowContext(ctx, `SELECT id,status FROM gw_channel_request_logs WHERE attempt_id=? AND action='submit' ORDER BY request_seq DESC LIMIT 1 FOR UPDATE`, item.AttemptID).Scan(&priorID, &priorStatus)
			if err == nil && priorStatus != "not_sent" {
				return ErrSubmissionUncertain
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var requestSeq uint64
		err := tx.QueryRowContext(ctx, `SELECT request_seq FROM gw_channel_request_logs WHERE attempt_id=? ORDER BY request_seq DESC LIMIT 1 FOR UPDATE`, item.AttemptID).Scan(&requestSeq)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		requestID, err = s.beginRequest(ctx, tx, repository.RequestLogInput{
			AttemptID:        &item.AttemptID,
			RequestSeq:       requestSeq + 1,
			Action:           item.Action,
			MappingHMAC:      evidence.MappingHMAC,
			RequestBytesHMAC: evidence.RequestBytesHMAC,
		})
		if err != nil {
			return err
		}
		if evidence.Payload != nil {
			if _, err := s.Store.PutRequestLogPayload(ctx, tx, requestID, "request", *evidence.Payload); err != nil {
				return err
			}
		}
		return s.Store.AssertAttemptOutboxLease(ctx, tx, item)
	})
	if err != nil {
		return 0, err
	}
	return requestID, nil
}

func (s *Service) FinishAttemptOutboxRequest(ctx context.Context, item repository.OutboxItem, requestID uint64, status string, result repository.RequestLogResult) error {
	if item.AttemptID == 0 || requestID == 0 || item.Action != "submit" && item.Action != "recover" || status != "not_sent" && status != "response_recorded" && status != "unknown" {
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := s.Store.AssertAttemptOutboxLease(ctx, tx, item); err != nil {
			return err
		}
		var attemptID uint64
		var action string
		if err := tx.QueryRowContext(ctx, `SELECT attempt_id,action FROM gw_channel_request_logs WHERE id=? FOR UPDATE`, requestID).Scan(&attemptID, &action); err == sql.ErrNoRows {
			return repository.ErrNotFound
		} else if err != nil {
			return err
		}
		if attemptID != item.AttemptID || action != item.Action {
			return repository.ErrConflict
		}
		if err := s.finishRequest(ctx, tx, requestID, status, result); err != nil {
			return err
		}
		return s.Store.AssertAttemptOutboxLease(ctx, tx, item)
	})
}

// BeginRequest commits the dispatch fence and capacity claim before any HTTP.
// A dispatching record left by a crash is unknown, never proof of non-submission.
func (s *Service) BeginRequest(ctx context.Context, in repository.RequestLogInput) (uint64, error) {
	return s.BeginRequestWithPayload(ctx, in, nil)
}

// BeginRequestWithPayload commits the dispatch authorization, request slot,
// and optional encrypted wire evidence in the same transaction.
func (s *Service) BeginRequestWithPayload(ctx context.Context, in repository.RequestLogInput, payload *repository.BlobInput) (uint64, error) {
	if (in.AttemptID == nil) == (in.ControlPlaneRunID == nil) {
		return 0, repository.ErrInvalidInput
	}
	var requestID uint64
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		requestID, err = s.beginRequest(ctx, tx, in)
		if err != nil || payload == nil {
			return err
		}
		_, err = s.Store.PutRequestLogPayload(ctx, tx, requestID, "request", *payload)
		return err
	})
	if err != nil {
		return 0, err
	}
	return requestID, nil
}

func (s *Service) beginRequest(ctx context.Context, tx *sql.Tx, in repository.RequestLogInput) (uint64, error) {
	var credentialID, poolID uint64
	if in.AttemptID != nil {
		if err := tx.QueryRowContext(ctx, `SELECT credential_id,credential_pool_id FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, *in.AttemptID).Scan(&credentialID, &poolID); err != nil {
			return 0, err
		}
	} else {
		if err := tx.QueryRowContext(ctx, `SELECT credential_id FROM gw_control_plane_runs WHERE id=? FOR UPDATE`, *in.ControlPlaneRunID).Scan(&credentialID); err != nil {
			return 0, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT credential_pool_id FROM gw_credentials WHERE id=?`, credentialID).Scan(&poolID); err != nil {
			return 0, err
		}
	}
	requestID, err := s.Store.CreateRequestLog(ctx, tx, in)
	if err != nil {
		return 0, fmt.Errorf("create request log: %w", err)
	}
	_, err = s.Store.AcquireCredentialSlot(ctx, tx, repository.SlotInput{CredentialID: credentialID, CredentialPoolID: poolID, Scope: "request", RequestLogID: &requestID})
	if err != nil {
		return 0, fmt.Errorf("acquire request slot: %w", err)
	}
	if err := s.Store.CompleteRequestLog(ctx, tx, requestID, "dispatching", repository.RequestLogResult{}); err != nil {
		return 0, fmt.Errorf("mark request dispatching: %w", err)
	}
	return requestID, nil
}

// FinishRequest ends a local HTTP exchange, not the provider task. Even an
// unknown response releases request capacity; task capacity is finalized later.
func (s *Service) FinishRequest(ctx context.Context, requestID uint64, status string, result repository.RequestLogResult) error {
	if requestID == 0 || (status != "not_sent" && status != "response_recorded" && status != "unknown") {
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		return s.finishRequest(ctx, tx, requestID, status, result)
	})
}

func (s *Service) finishRequest(ctx context.Context, tx *sql.Tx, requestID uint64, status string, result repository.RequestLogResult) error {
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_channel_request_logs WHERE id=? FOR UPDATE`, requestID).Scan(&current); err != nil {
		return err
	}
	if current != status {
		if status == "response_recorded" && current == "dispatching" {
			if err := s.Store.CompleteRequestLog(ctx, tx, requestID, "sent", result); err != nil {
				return err
			}
		}
		if err := s.Store.CompleteRequestLog(ctx, tx, requestID, status, result); err != nil {
			return err
		}
	}
	var slotID uint64
	var slotState string
	if err := tx.QueryRowContext(ctx, `SELECT id,state FROM gw_credential_slots WHERE request_log_id=? AND scope='request' ORDER BY id DESC LIMIT 1 FOR UPDATE`, requestID).Scan(&slotID, &slotState); err != nil {
		return err
	}
	if slotState == "released" {
		return nil
	}
	return s.Store.ReleaseCredentialSlot(ctx, tx, slotID, false)
}
