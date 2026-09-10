package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPurgeExpiredCallPayloadDetachesBeforeDeletingCiphertext(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	now := time.Date(2026, 9, 7, 6, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT payload.id FROM gw_api_call_payloads").WithArgs(now, 10).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT payload.encrypted_blob_id FROM gw_api_call_payloads").WithArgs(uint64(41), now).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_blob_id"}).AddRow(71))
	mock.ExpectExec("UPDATE gw_api_call_payloads SET encrypted_blob_id=NULL").WithArgs(now, uint64(41), uint64(71)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT purpose FROM encrypted_blobs").WithArgs(uint64(71)).
		WillReturnRows(sqlmock.NewRows([]string{"purpose"}).AddRow("gateway-payload"))
	mock.ExpectExec("DELETE FROM encrypted_blob_key_wraps").WithArgs(uint64(71)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("DELETE FROM encrypted_blobs").WithArgs(uint64(71), "gateway-payload").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	count, err := store.PurgeExpiredCallPayloads(context.Background(), now, 10)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeExpiredCallPayloadRechecksEligibilityUnderLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	now := time.Date(2026, 9, 7, 6, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT payload.id FROM gw_api_call_payloads").WithArgs(now, 10).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT payload.encrypted_blob_id FROM gw_api_call_payloads").WithArgs(uint64(41), now).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()

	count, err := store.PurgeExpiredCallPayloads(context.Background(), now, 10)
	if err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeExpiredRequestLogPayloadsRemovesBothBodies(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	now := time.Date(2026, 9, 7, 6, 0, 0, 0, time.UTC)
	cutoff := now.Add(-7 * 24 * time.Hour)

	mock.ExpectQuery("SELECT id FROM gw_channel_request_logs").WithArgs(cutoff, 10).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(51))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT request_payload_blob_id,response_payload_blob_id FROM gw_channel_request_logs").WithArgs(uint64(51), cutoff).
		WillReturnRows(sqlmock.NewRows([]string{"request_payload_blob_id", "response_payload_blob_id"}).AddRow(81, 82))
	mock.ExpectExec("UPDATE gw_channel_request_logs SET request_payload_blob_id=NULL").WithArgs(uint64(51)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	for _, payload := range []struct {
		id      uint64
		purpose string
	}{{81, "gateway-upstream-request"}, {82, "gateway-upstream-response"}} {
		mock.ExpectQuery("SELECT purpose FROM encrypted_blobs").WithArgs(payload.id).
			WillReturnRows(sqlmock.NewRows([]string{"purpose"}).AddRow(payload.purpose))
		mock.ExpectExec("DELETE FROM encrypted_blob_key_wraps").WithArgs(payload.id).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("DELETE FROM encrypted_blobs").WithArgs(payload.id, payload.purpose).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()

	count, err := store.PurgeExpiredRequestLogPayloads(context.Background(), cutoff, now, 10)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeRequestLogPayloadRejectsUnexpectedBlobPurpose(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	now := time.Date(2026, 9, 7, 6, 0, 0, 0, time.UTC)
	cutoff := now.Add(-time.Hour)

	mock.ExpectQuery("SELECT id FROM gw_channel_request_logs").WithArgs(cutoff, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(51))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT request_payload_blob_id,response_payload_blob_id FROM gw_channel_request_logs").WithArgs(uint64(51), cutoff).
		WillReturnRows(sqlmock.NewRows([]string{"request_payload_blob_id", "response_payload_blob_id"}).AddRow(81, nil))
	mock.ExpectExec("UPDATE gw_channel_request_logs SET request_payload_blob_id=NULL").WithArgs(uint64(51)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT purpose FROM encrypted_blobs").WithArgs(uint64(81)).
		WillReturnRows(sqlmock.NewRows([]string{"purpose"}).AddRow("credential"))
	mock.ExpectRollback()

	count, err := store.PurgeExpiredRequestLogPayloads(context.Background(), cutoff, now, 1)
	if err == nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSensitiveRetentionRejectsInvalidBatch(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	if _, err := store.PurgeExpiredCallPayloads(context.Background(), time.Now(), 0); err == nil {
		t.Fatal("zero batch accepted")
	}
	if _, err := store.PurgeExpiredRequestLogPayloads(context.Background(), time.Now(), time.Now().Add(-time.Hour), 1); err == nil {
		t.Fatal("cutoff after now accepted")
	}
}

func TestPurgeExpiredCallbackBindingsDetachesAliasBeforeDeletingBlob(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 6, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT id FROM gw_callback_binding_token_aliases").WithArgs(now, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(71))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status,encrypted_blob_id FROM gw_callback_binding_token_aliases").
		WithArgs(uint64(71), now).
		WillReturnRows(sqlmock.NewRows([]string{"status", "encrypted_blob_id"}).AddRow("active", uint64(101)))
	mock.ExpectExec("UPDATE gw_callback_binding_token_aliases SET status='expired',invalidated_at=.*encrypted_blob_id=NULL").
		WithArgs(sqlmock.AnyArg(), uint64(71), now, uint64(101)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM gw_callback_binding_token_aliases WHERE encrypted_blob_id=\\?").
		WithArgs(uint64(101)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(uint64(0)))
	mock.ExpectQuery("SELECT purpose FROM encrypted_blobs").
		WithArgs(uint64(101)).WillReturnRows(sqlmock.NewRows([]string{"purpose"}).AddRow("gateway-callback-binding-token"))
	mock.ExpectExec("DELETE FROM encrypted_blob_key_wraps").
		WithArgs(uint64(101)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM encrypted_blobs").
		WithArgs(uint64(101), "gateway-callback-binding-token").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	count, err := store.PurgeExpiredCallbackBindings(context.Background(), now, 1)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeExpiredCallbackReceiptDetachesAndDeletesBody(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 6, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)

	mock.ExpectQuery("SELECT id FROM gw_upstream_callback_receipts").WithArgs(now, 10).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status,encrypted_payload_blob_id,expires_at FROM gw_upstream_callback_receipts").
		WithArgs(uint64(41)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "encrypted_payload_blob_id", "expires_at"}).AddRow("processed", uint64(71), expired))
	mock.ExpectExec("UPDATE gw_upstream_callback_receipts SET encrypted_payload_blob_id=NULL").
		WithArgs(uint64(41), uint64(71)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM gw_upstream_callback_receipts").
		WithArgs(uint64(71)).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(0))
	mock.ExpectQuery("SELECT purpose FROM encrypted_blobs").WithArgs(uint64(71)).
		WillReturnRows(sqlmock.NewRows([]string{"purpose"}).AddRow("gateway-callback-payload"))
	mock.ExpectExec("DELETE FROM encrypted_blob_key_wraps").WithArgs(uint64(71)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM encrypted_blobs").WithArgs(uint64(71), "gateway-callback-payload").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	count, err := store.PurgeExpiredCallbackReceipts(context.Background(), now, 10)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeExpiredCallbackReceiptRejectsReceivedAndRecordsTransition(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 6, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)

	mock.ExpectQuery("SELECT id FROM gw_upstream_callback_receipts").WithArgs(now, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status,encrypted_payload_blob_id,expires_at FROM gw_upstream_callback_receipts").
		WithArgs(uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "encrypted_payload_blob_id", "expires_at"}).AddRow("received", uint64(72), expired))
	mock.ExpectExec("UPDATE gw_upstream_callback_receipts SET status='rejected'").
		WithArgs(uint64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT state_version FROM gw_upstream_callback_receipts").
		WithArgs(uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"state_version"}).AddRow(uint64(2)))
	mock.ExpectExec("INSERT INTO gw_state_transition_events").
		WithArgs(uint64(42), "received", "rejected", uint64(2), "callback_expired", now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_upstream_callback_receipts SET encrypted_payload_blob_id=NULL").
		WithArgs(uint64(42), uint64(72)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM gw_upstream_callback_receipts").
		WithArgs(uint64(72)).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(1))
	mock.ExpectCommit()

	count, err := store.PurgeExpiredCallbackReceipts(context.Background(), now, 1)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
