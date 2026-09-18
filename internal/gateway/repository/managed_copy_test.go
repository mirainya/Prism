package repository

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/security"
)

func TestReserveManagedCopyAssetReusesMatchingStagingIntent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	digest := strings.Repeat("a", 64)
	retention := time.Now().UTC().Add(time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT c.id,c.user_id,c.token_id FROM gw_api_call_attempts").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"call_id", "user_id", "token_id"}).AddRow(3, 11, 13))
	mock.ExpectQuery("SELECT id,user_id,token_id,COALESCE\\(attempt_id,0\\),object_key,storage_locator,object_version,content_type,content_length,sha256,state,state_version FROM gw_media_assets").
		WithArgs("gateway-result:7:0").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "token_id", "attempt_id", "object_key", "storage_locator", "object_version", "content_type", "content_length", "sha256", "state", "state_version"}).
			AddRow(17, 11, 13, 7, "gateway-result:7:0", "https://storage.example/result.mp4", "v1", "video/mp4", 128, digest, "staging", 1))
	mock.ExpectCommit()

	var asset ManagedCopyAssetRecord
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		var reserveErr error
		asset, reserveErr = store.ReserveManagedCopyAsset(context.Background(), tx, 7, MediaAssetInput{
			UserID: 11, TokenID: 13, Purpose: "result", ObjectKey: "gateway-result:7:0",
			ContentType: "video/mp4", ContentLength: 128, SHA256: digest, RetentionUntil: &retention,
		})
		return reserveErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if asset.ID != 17 || asset.AttemptID != 7 || asset.StorageLocator != "https://storage.example/result.mp4" || asset.ObjectVersion != "v1" || asset.State != "staging" {
		t.Fatalf("asset=%+v", asset)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReserveManagedCopyAssetRejectsCrossOwnerReuse(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	digest := strings.Repeat("b", 64)
	retention := time.Now().UTC().Add(time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT c.id,c.user_id,c.token_id FROM gw_api_call_attempts").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"call_id", "user_id", "token_id"}).AddRow(3, 11, 13))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, reserveErr := store.ReserveManagedCopyAsset(context.Background(), tx, 7, MediaAssetInput{
			UserID: 99, TokenID: 13, Purpose: "result", ObjectKey: "gateway-result:7:0",
			ContentType: "video/mp4", ContentLength: 128, SHA256: digest, RetentionUntil: &retention,
		})
		return reserveErr
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordManagedCopyUploadIsIdempotentForSameLocator(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state,storage_locator FROM gw_media_assets").
		WithArgs(uint64(17)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "storage_locator"}).AddRow("staging", "https://storage.example/result.mp4"))
	mock.ExpectCommit()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.RecordManagedCopyUpload(context.Background(), tx, 17, "https://storage.example/result.mp4", "v2")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateFailedManagedCopyDeliveryPersistsSourceAndRecoveryAction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	key := bytes.Repeat([]byte{7}, security.KeySize)
	url := "https://provider.example/result.png"

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id,token_id FROM gw_api_calls").WithArgs(uint64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id"}).AddRow(11, 13))
	mock.ExpectQuery("SELECT call_id FROM gw_api_call_attempts").WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"call_id"}).AddRow(3))
	mock.ExpectExec("INSERT INTO gw_result_deliveries").WillReturnResult(sqlmock.NewResult(19, 1))
	mock.ExpectExec("INSERT INTO gw_state_transition_events").WillReturnResult(sqlmock.NewResult(20, 1))
	mock.ExpectExec("INSERT INTO encrypted_blobs").WillReturnResult(sqlmock.NewResult(31, 1))
	mock.ExpectExec("UPDATE encrypted_blobs SET aad_hash=").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO encrypted_blob_key_wraps").WillReturnResult(sqlmock.NewResult(32, 1))
	mock.ExpectQuery("SELECT l.id FROM gw_channel_request_logs l JOIN gw_result_deliveries d").
		WithArgs(uint64(19), uint64(99)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(99))
	mock.ExpectExec("INSERT INTO gw_result_delivery_sources").WillReturnResult(sqlmock.NewResult(41, 1))
	mock.ExpectExec("UPDATE gw_result_delivery_sources SET state='active'").
		WithArgs(uint64(41), uint64(19)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_result_deliveries SET current_source_id=\\?,updated_at=\\?").
		WithArgs(uint64(41), sqlmock.AnyArg(), uint64(19)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT state,state_version,delivery_mode,source_kind,current_source_id FROM gw_result_deliveries").
		WithArgs(uint64(19)).WillReturnRows(sqlmock.NewRows([]string{"state", "state_version", "delivery_mode", "source_kind", "current_source_id"}).
		AddRow("pending", 1, "managed_copy", "remote_url", 41))
	mock.ExpectExec("UPDATE gw_result_deliveries SET state='delivery_failed'").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_state_transition_events").WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectQuery("SELECT d.attempt_id,d.state,d.state_version,d.action_seq,d.delivery_mode,d.source_kind,pt.source_url_policy,d.reason_code,\\(s.id IS NOT NULL\\) FROM gw_result_deliveries").
		WithArgs(uint64(19)).WillReturnRows(sqlmock.NewRows([]string{"attempt_id", "state", "state_version", "action_seq", "delivery_mode", "source_kind", "source_url_policy", "reason_code", "source_available"}).
		AddRow(7, "delivery_failed", 2, 0, "managed_copy", "remote_url", "fixed", "managed_copy_upload_failed", true))
	mock.ExpectQuery("SELECT id FROM gw_async_outbox").WithArgs(uint64(19), uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec("UPDATE gw_result_deliveries SET action_seq=action_seq\\+1,retry_at=\\?").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_async_outbox").WillReturnResult(sqlmock.NewResult(51, 1))
	mock.ExpectCommit()

	var deliveryID uint64
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		var createErr error
		deliveryID, createErr = store.CreateFailedManagedCopyDelivery(context.Background(), tx, ResultDeliveryInput{
			CallID: 3, AttemptID: 7, Ordinal: 0, UserID: 11, TokenID: 13, Mode: "managed_copy", SourceKind: "remote_url",
		}, BlobInput{KeyringID: 8, KEKVersion: 2, Plaintext: []byte(url), KEK: key, HMACKey: key}, 99, nil, "managed_copy_upload_failed")
		return createErr
	})
	if err != nil || deliveryID != 19 {
		t.Fatalf("delivery=%d err=%v", deliveryID, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateManagedCopyDeliveryRollsBackPublicationFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	digest := strings.Repeat("c", 64)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id,token_id FROM gw_api_calls").WithArgs(uint64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id"}).AddRow(11, 13))
	mock.ExpectQuery("SELECT call_id FROM gw_api_call_attempts").WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"call_id"}).AddRow(3))
	mock.ExpectExec("INSERT INTO gw_result_deliveries").
		WillReturnResult(sqlmock.NewResult(19, 1))
	mock.ExpectExec("INSERT INTO gw_state_transition_events").
		WillReturnResult(sqlmock.NewResult(21, 1))
	mock.ExpectQuery("SELECT attempt_id,user_id,token_id,result_ordinal,delivery_mode,state,state_version,current_source_id FROM gw_result_deliveries").
		WithArgs(uint64(19)).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_id", "user_id", "token_id", "result_ordinal", "delivery_mode", "state", "state_version", "current_source_id"}).
			AddRow(7, 11, 13, 0, "managed_copy", "pending", 1, nil))
	mock.ExpectQuery("SELECT user_id,token_id,COALESCE\\(attempt_id,0\\),purpose,state,content_type,content_length,sha256,storage_locator FROM gw_media_assets").
		WithArgs(uint64(17)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id", "attempt_id", "purpose", "state", "content_type", "content_length", "sha256", "storage_locator"}).
			AddRow(11, 13, 7, "result", "staging", "video/mp4", 128, digest, "https://storage.example/result.mp4"))
	mock.ExpectQuery("SELECT state,state_version FROM gw_media_assets").WithArgs(uint64(17)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "state_version"}).AddRow("staging", 1))
	mock.ExpectExec("UPDATE gw_media_assets SET state=\\?,state_version=state_version\\+1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_media_asset_state_events").
		WillReturnResult(sqlmock.NewResult(22, 1))
	mock.ExpectQuery("SELECT user_id,token_id,purpose,state,retention_until FROM gw_media_assets").WithArgs(uint64(17)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id", "purpose", "state", "retention_until"}).AddRow(11, 13, "result", "active", nil))
	mock.ExpectQuery("SELECT user_id,token_id FROM gw_result_deliveries").WithArgs(uint64(19)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id"}).AddRow(11, 13))
	mock.ExpectExec("INSERT INTO gw_media_asset_refs").
		WillReturnResult(sqlmock.NewResult(23, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_result_deliveries SET current_source_id=?")).
		WillReturnError(errors.New("write failed"))
	mock.ExpectRollback()

	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, publishErr := store.CreateManagedCopyDelivery(context.Background(), tx, ManagedCopyDeliveryInput{
			ResultDeliveryInput: ResultDeliveryInput{CallID: 3, AttemptID: 7, Ordinal: 0, UserID: 11, TokenID: 13, Mode: "managed_copy", SourceKind: "remote_url"},
			MediaAssetID:        17, ContentType: "video/mp4", ContentLength: 128, SHA256: digest,
		})
		return publishErr
	})
	if err == nil || err.Error() != "write failed" {
		t.Fatalf("err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverManagedCopyDeliveryPublishesOriginalFailedDelivery(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	digest := strings.Repeat("d", 64)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT attempt_id,user_id,token_id,result_ordinal,delivery_mode,state,state_version,current_source_id FROM gw_result_deliveries").
		WithArgs(uint64(19)).WillReturnRows(sqlmock.NewRows([]string{"attempt_id", "user_id", "token_id", "result_ordinal", "delivery_mode", "state", "state_version", "current_source_id"}).
		AddRow(7, 11, 13, 2, "managed_copy", "delivery_failed", 4, 41))
	mock.ExpectQuery("SELECT user_id,token_id,COALESCE\\(attempt_id,0\\),purpose,state,content_type,content_length,sha256,storage_locator FROM gw_media_assets").
		WithArgs(uint64(17)).WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id", "attempt_id", "purpose", "state", "content_type", "content_length", "sha256", "storage_locator"}).
		AddRow(11, 13, 7, "result", "staging", "video/mp4", 128, digest, "raw://managed/result"))
	mock.ExpectQuery("SELECT state,state_version FROM gw_media_assets").WithArgs(uint64(17)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "state_version"}).AddRow("staging", 1))
	mock.ExpectExec("UPDATE gw_media_assets SET state=\\?,state_version=state_version\\+1").
		WithArgs("active", sqlmock.AnyArg(), uint64(17), "staging", uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_media_asset_state_events").
		WithArgs(uint64(17), "staging", "active", uint64(2), "managed_copy_published", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT user_id,token_id,purpose,state,retention_until FROM gw_media_assets").WithArgs(uint64(17)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id", "purpose", "state", "retention_until"}).AddRow(11, 13, "result", "active", nil))
	mock.ExpectQuery("SELECT user_id,token_id FROM gw_result_deliveries").WithArgs(uint64(19)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id"}).AddRow(11, 13))
	mock.ExpectExec("INSERT INTO gw_media_asset_refs").
		WithArgs(uint64(17), uint64(11), uint64(13), "result", uint32(2), nil, uint64(19), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(23, 1))
	mock.ExpectExec("UPDATE gw_result_delivery_sources SET state='consumed'").
		WithArgs(uint64(41), uint64(19)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_result_deliveries SET current_source_id=\\?").
		WithArgs(nil, uint64(23), digest, uint64(128), "video/mp4", "managed_copy_recovered", sqlmock.AnyArg(), uint64(19), "delivery_failed", uint64(4)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_state_transition_events").
		WithArgs(uint64(19), "delivery_failed", uint64(5), "managed_copy_recovered", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(24, 1))
	mock.ExpectCommit()

	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.RecoverManagedCopyDelivery(context.Background(), tx, 19, ManagedCopyRecoveryInput{
			MediaAssetID: 17, ContentType: "video/mp4", ContentLength: 128, SHA256: digest,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
