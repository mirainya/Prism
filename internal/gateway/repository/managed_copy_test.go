package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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
	mock.ExpectQuery("SELECT id,user_id,token_id,object_key,storage_locator,object_version,content_type,content_length,sha256,state,state_version FROM gw_media_assets").
		WithArgs("gateway-result:7:0").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "token_id", "object_key", "storage_locator", "object_version", "content_type", "content_length", "sha256", "state", "state_version"}).
			AddRow(17, 11, 13, "gateway-result:7:0", "https://storage.example/result.mp4", "v1", "video/mp4", 128, digest, "staging", 1))
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
	if asset.ID != 17 || asset.StorageLocator != "https://storage.example/result.mp4" || asset.ObjectVersion != "v1" || asset.State != "staging" {
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
	mock.ExpectQuery("SELECT user_id,token_id,purpose,state,content_type,content_length,sha256,storage_locator FROM gw_media_assets").
		WithArgs(uint64(17)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id", "purpose", "state", "content_type", "content_length", "sha256", "storage_locator"}).
			AddRow(11, 13, "result", "staging", "video/mp4", 128, digest, "https://storage.example/result.mp4"))
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
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_result_deliveries SET media_asset_ref_id=?")).
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
