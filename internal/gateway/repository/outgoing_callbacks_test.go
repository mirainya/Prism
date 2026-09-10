package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestClaimCallbackDeliveryCreatesFencedAttemptBeforeDispatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	replayUntil := time.Now().UTC().Add(time.Hour)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT d.id,d.call_id,d.callback_target_id,d.callback_event_seq,")).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "call_id", "callback_target_id", "callback_event_seq", "target_blob_id", "target_hmac", "algorithm", "policy_version",
			"payload_blob_id", "payload_hmac", "attempt_count", "max_attempts", "replay_expires_at", "state", "lease_expires_at", "public_id",
		}).AddRow(uint64(7), uint64(8), uint64(9), uint64(1), uint64(10), digestHex("a"), "http-post-json-v1", uint32(1), uint64(11), digestHex("b"), uint64(0), uint64(5), replayUntil, "pending", nil, "call-public"))
	mock.ExpectExec("UPDATE gw_callback_deliveries").
		WithArgs(uint64(1), "worker-1", sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7), "pending", uint64(0)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_callback_delivery_attempts").
		WithArgs(uint64(7), uint64(1), digestHex("b"), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(12, 1))
	mock.ExpectCommit()

	var claim ClaimedCallbackDelivery
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		var claimErr error
		claim, claimErr = store.ClaimCallbackDelivery(context.Background(), tx, "worker-1", time.Minute)
		return claimErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim.ID != 7 || claim.AttemptID != 12 || claim.AttemptNo != 1 || claim.MaxAttempts != 5 || claim.LeaseOwner != "worker-1" {
		t.Fatalf("claim = %+v", claim)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteCallbackDeliveryRecordsResponseAndRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	claim := ClaimedCallbackDelivery{ID: 7, AttemptID: 12, AttemptNo: 2, LeaseOwner: "worker-1"}
	retryAt := time.Now().UTC().Add(time.Minute)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE gw_callback_delivery_attempts SET state=\\?").
		WithArgs("failed", digestHex("c"), true, true, uint32(503), "callback_http_retryable", uint64(12)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_callback_deliveries").
		WithArgs("failed", retryAt, "callback_http_retryable", nil, sqlmock.AnyArg(), uint64(7), "worker-1", uint64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.CompleteCallbackDelivery(context.Background(), tx, claim, CallbackDeliveryCompletion{
			AttemptState: "failed", ResponseHMAC: digestHex("c"), ErrorCode: "callback_http_retryable",
			HTTPStatus: 503, RequestComplete: true, ResponseComplete: true, Outcome: "retry", RetryAt: retryAt,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClaimCallbackDeliveryRejectsExpiredSenderWithoutDispatchingAttempt(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT d.id,d.call_id,d.callback_target_id,d.callback_event_seq").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "call_id", "callback_target_id", "callback_event_seq", "target_blob_id", "target_hmac", "algorithm", "policy_version",
			"payload_blob_id", "payload_hmac", "attempt_count", "max_attempts", "replay_expires_at", "state", "lease_expires_at", "public_id",
		}).AddRow(uint64(7), uint64(8), uint64(9), uint64(1), uint64(10), digestHex("a"), "http-post-json-v1", uint32(1), uint64(11), digestHex("b"), uint64(1), uint64(5), now.Add(time.Hour), "sending", now.Add(-time.Minute), "call-public"))
	mock.ExpectExec("UPDATE gw_callback_delivery_attempts").
		WithArgs(uint64(7), uint64(1)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, claimErr := store.ClaimCallbackDelivery(context.Background(), tx, "worker-2", time.Minute)
		return claimErr
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeadLetterUndeliverableCallbacksClosesExpiredAttemptAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT id FROM gw_callback_deliveries").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), 10).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uint64(7)))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state,attempt_count,max_attempts,replay_expires_at,lease_expires_at").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "attempt_count", "max_attempts", "replay_expires_at", "lease_expires_at"}).
			AddRow("sending", uint64(5), uint64(5), now.Add(time.Hour), now.Add(-time.Minute)))
	mock.ExpectExec("UPDATE gw_callback_delivery_attempts").
		WithArgs(uint64(7), uint64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_callback_deliveries").
		WithArgs("callback_attempts_exhausted", sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7), "sending", uint64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	count, err := store.DeadLetterUndeliverableCallbacks(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count=%d, want 1", count)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func digestHex(character string) string {
	result := ""
	for range 64 {
		result += character
	}
	return result
}
