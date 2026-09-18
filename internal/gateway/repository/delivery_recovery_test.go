package repository

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/security"
)

func TestReadDeliveryRecoveryTargetRequiresLiveTaskIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectQuery("JOIN gw_upstream_task_identities i ON i.async_execution_id=x.id AND i.status='bound' AND i.expires_at>CURRENT_TIMESTAMP\\(3\\)").
		WithArgs(uint64(7)).WillReturnError(sql.ErrNoRows)
	if _, err := store.ReadDeliveryRecoveryTarget(context.Background(), 7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired or inactive task identity remained refreshable: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleDeliveryReconciliationIsRevisionIdempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT d.attempt_id,d.state,d.state_version,d.action_seq,d.delivery_mode,d.source_kind,pt.source_url_policy,d.reason_code,(s.id IS NOT NULL) FROM gw_result_deliveries")).
		WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"attempt_id", "state", "state_version", "action_seq", "delivery_mode", "source_kind", "source_url_policy", "reason_code", "source_available"}).AddRow(3, "expired", 4, 1, "reference", "remote_url", "refreshable", "source_expired", false))
	mock.ExpectQuery("SELECT id FROM gw_async_outbox WHERE result_delivery_id=\\? AND state_version=\\?").
		WithArgs(uint64(7), uint64(4)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec("UPDATE gw_result_deliveries SET action_seq=action_seq\\+1,retry_at=\\?").
		WithArgs(at, sqlmock.AnyArg(), uint64(7), "expired", uint64(4), uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_async_outbox\\(result_delivery_id,action_seq,action,status,state_version").
		WithArgs(uint64(7), uint64(2), uint64(4), at, sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(19, 1))
	mock.ExpectCommit()
	var id uint64
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = store.ScheduleDeliveryReconciliation(ctx, tx, 7, at)
		return err
	})
	if err != nil || id != 19 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExpireRefreshableDeliverySchedulesReconciliationAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	ctx := context.Background()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT d.state,d.state_version,d.expires_at,d.delivery_mode,pt.source_url_policy FROM gw_result_deliveries").
		WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"state", "state_version", "expires_at", "delivery_mode", "source_url_policy"}).AddRow("ready", 3, time.Now().UTC().Add(-time.Minute), "reference", "refreshable"))
	mock.ExpectExec("UPDATE gw_result_deliveries SET state='expired'").
		WithArgs(sqlmock.AnyArg(), uint64(7), uint64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_state_transition_events.*'ready','expired'").
		WithArgs(uint64(7), uint64(4), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT d.attempt_id,d.state,d.state_version,d.action_seq,d.delivery_mode,d.source_kind,pt.source_url_policy,d.reason_code,\\(s.id IS NOT NULL\\) FROM gw_result_deliveries").
		WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"attempt_id", "state", "state_version", "action_seq", "delivery_mode", "source_kind", "source_url_policy", "reason_code", "source_available"}).AddRow(3, "expired", 4, 0, "reference", "remote_url", "refreshable", "source_expired", false))
	mock.ExpectQuery("SELECT id FROM gw_async_outbox WHERE result_delivery_id=\\? AND state_version=\\?").
		WithArgs(uint64(7), uint64(4)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec("UPDATE gw_result_deliveries SET action_seq=action_seq\\+1,retry_at=\\?").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7), "expired", uint64(4), uint64(0)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_async_outbox\\(result_delivery_id,action_seq,action,status,state_version").
		WithArgs(uint64(7), uint64(1), uint64(4), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(13, 1))
	mock.ExpectCommit()
	if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.ExpireResultDelivery(ctx, tx, 7) }); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReadDeliveryRecoveryTargetSupportsManagedCopy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectQuery("SELECT d.id,d.attempt_id,COALESCE\\(x.id,0\\),d.result_ordinal,d.state,d.state_version,d.action_seq,d.delivery_mode,d.source_kind,r.resource_kind,COALESCE\\(s.encrypted_url_blob_id,0\\),COALESCE\\(s.source_seq,0\\)").
		WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{
		"delivery_id", "attempt_id", "async_id", "ordinal", "state", "state_version", "action_seq", "delivery_mode", "source_kind", "resource_kind", "source_blob_id", "source_sequence",
	}).AddRow(7, 3, 0, 1, "delivery_failed", 2, 1, "managed_copy", "remote_url", "video_task", 50, 1))
	target, err := store.ReadDeliveryRecoveryTarget(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if target.DeliveryID != 7 || target.AttemptID != 3 || target.AsyncExecutionID != 0 || target.Ordinal != 1 || target.Mode != "managed_copy" || target.SourceKind != "remote_url" || target.ResourceKind != "video_task" || target.SourceBlobID != 50 || target.SourceSequence != 1 {
		t.Fatalf("target=%+v", target)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleDueDeliveryReconciliationsScansManagedCopyFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectQuery("d.delivery_mode='managed_copy'.*d.reason_code IN.*managed_copy_download_failed").
		WithArgs(100).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	count, err := store.ScheduleDueDeliveryReconciliations(context.Background(), 100)
	if err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshReferenceDeliverySupersedesExpiredActiveSource(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	ctx := context.Background()
	expires := time.Now().UTC().Add(time.Hour)
	key := bytes.Repeat([]byte{9}, security.KeySize)
	url := "https://provider.example/refreshed.mp4"
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT call_id,attempt_id,state,delivery_mode,state_version,current_source_id FROM gw_result_deliveries").
		WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"call_id", "attempt_id", "state", "delivery_mode", "state_version", "current_source_id"}).AddRow(2, 3, "expired", "reference", 5, 41))
	mock.ExpectQuery("SELECT pt.source_url_policy FROM gw_result_deliveries").
		WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"source_url_policy"}).AddRow("refreshable"))
	mock.ExpectQuery("SELECT id FROM gw_channel_request_logs WHERE id=\\? AND attempt_id=\\? AND result_delivery_id=\\?").
		WithArgs(uint64(99), uint64(3), uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(99))
	mock.ExpectQuery("SELECT MAX\\(source_seq\\) FROM gw_result_delivery_sources").
		WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(1))
	mock.ExpectExec("INSERT INTO encrypted_blobs").
		WithArgs(uint64(8), "gateway-result-source", uint32(1), "", sqlmock.AnyArg(), sqlmock.AnyArg(), "", len(url), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(50, 1))
	mock.ExpectExec("UPDATE encrypted_blobs SET aad_hash=\\?,nonce=\\?,ciphertext=\\?,content_hmac=\\? WHERE id=\\?").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(50)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO encrypted_blob_key_wraps").
		WithArgs(uint64(50), uint64(8), uint32(2), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(51, 1))
	mock.ExpectQuery("SELECT l.id FROM gw_channel_request_logs l JOIN gw_result_deliveries d").
		WithArgs(uint64(7), uint64(99)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(99))
	mock.ExpectExec("INSERT INTO gw_result_delivery_sources").
		WithArgs(uint64(7), uint64(2), uint64(50), sqlmock.AnyArg(), nil, sqlmock.AnyArg(), &expires, uint64(99)).WillReturnResult(sqlmock.NewResult(52, 1))
	mock.ExpectExec("UPDATE gw_result_delivery_sources SET state='superseded' WHERE id=\\?").
		WithArgs(uint64(41), uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_result_delivery_sources SET state='active'").
		WithArgs(uint64(7), uint64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_result_deliveries SET current_source_id=").
		WithArgs(uint64(7), uint64(2), expires, sqlmock.AnyArg(), uint64(7), "expired", uint64(5)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_state_transition_events").
		WithArgs(uint64(7), "expired", "ready", uint64(6), "reference_refreshed", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(60, 1))
	mock.ExpectCommit()
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.RefreshReferenceDelivery(ctx, tx, 7, BlobInput{KeyringID: 8, KEKVersion: 2, Plaintext: []byte(url), KEK: key, HMACKey: key}, 99, &expires)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClaimDeliveryOutboxCarriesTypedParentAndLeaseFence(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	ctx := context.Background()
	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	available := time.Now().UTC()
	mock.ExpectQuery("SELECT id,result_delivery_id,action_seq,action,state_version,attempt_count,available_at,status,lease_expires_at FROM gw_async_outbox").
		WillReturnRows(sqlmock.NewRows([]string{"id", "result_delivery_id", "action_seq", "action", "state_version", "attempt_count", "available_at", "status", "lease_expires_at"}).AddRow(11, 7, 2, "reconcile_delivery", 4, 1, available, "dispatching", available.Add(-time.Second)))
	mock.ExpectExec("UPDATE gw_async_outbox SET status='dispatching'").
		WithArgs("delivery-worker", sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(11), uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	item, err := store.ClaimDeliveryOutbox(ctx, tx, "delivery-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if item.ResultDeliveryID != 7 || item.Attempts != 2 || !item.MayHaveDispatched || item.Action != "reconcile_delivery" {
		t.Fatalf("item=%+v", item)
	}
	mock.ExpectQuery("SELECT 1 FROM gw_async_outbox").
		WithArgs(uint64(11), "delivery-worker", uint64(2), uint64(2), uint64(4), "reconcile_delivery", nil, nil, nil, nil, uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	if err := store.AssertDeliveryOutboxLease(ctx, tx, item); err != nil {
		t.Fatal(err)
	}
	mock.ExpectRollback()
	_ = tx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
