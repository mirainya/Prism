package runtime

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/filestorage"
	"github.com/mirainya/Prism/pkg/safeurl"
)

var testPNG = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
var testMP4 = []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm'}

const testStorageAPIKey = "xfs_0123456789abcdef0123456789abcdef"

func TestPrepareManagedCopiesReservesVerifiesAndPublishesUploadMetadata(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	mock.ExpectQuery("SELECT c.id,a.id,a.credential_id").WithArgs(uint64(9)).WillReturnRows(sqlmock.NewRows([]string{
		"call_id", "attempt_id", "credential_id", "credential_secret", "credential_blob", "request_blob", "identity_blob", "callback_token_blob", "transport_id", "release_id", "public_id", "protocol", "base_url", "method", "path", "auth", "vendor", "delivery", "source_policy", "adapter_config", "timeout", "adapter", "version",
	}).AddRow(1, 2, 3, nil, 4, 5, nil, nil, 6, 7, "call", "http", "https://provider.example", "POST", "/tasks", "bearer", "seedance", "managed_copy", "fixed", []byte(`{}`), 1000, "seedance", 1))
	expectManagedOwner(mock, 2, 10, 20)
	expectManagedAllocation(mock, 2, 1, 10, 20, "gateway-result:2:0", 31)
	expectManagedUploadRecord(mock, 31)
	restore := stubManagedStorage(t)
	defer restore()
	downloadManagedResult = func(context.Context, string, int64) (*safeurl.Result, error) {
		return &safeurl.Result{Data: append([]byte(nil), testMP4...), ContentType: "video/mp4"}, nil
	}
	uploadManagedResult = func(_ context.Context, apiKey string, data []byte, contentType, path, filename string) (filestorage.UploadResult, error) {
		digest := sha256.Sum256(testMP4)
		if apiKey != testStorageAPIKey || string(data) != string(testMP4) || contentType != "video/mp4" || !strings.HasSuffix(path, "gateway-results/31/") || filename != hex.EncodeToString(digest[:])+".mp4" {
			t.Fatalf("upload = key:%q %x %q %q %q", apiKey, data, contentType, path, filename)
		}
		return filestorage.UploadResult{URL: "https://storage.example/result.mp4", ID: "object-v1"}, nil
	}
	copies, err := service.prepareManagedCopies(context.Background(), AsyncResultInput{
		Item: repository.OutboxItem{AsyncExecutionID: 9}, State: execution.AsyncSucceeded,
		Result:  &repository.BlobInput{Plaintext: []byte(`{"schema_version":1}`)},
		Sources: []delivery.RemoteResult{{Role: "video", URL: "https://provider.example/result.mp4"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(copies) != 1 || copies[0].MediaAssetID != 31 || copies[0].ObjectKey != "gateway-result:2:0" || copies[0].StorageLocator != "https://storage.example/result.mp4" || copies[0].ContentType != "video/mp4" || copies[0].ContentLength != uint64(len(testMP4)) {
		t.Fatalf("copies=%+v", copies)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareManagedCopiesUploadsInlineImageWithoutDownloading(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	expectManagedOwner(mock, 2, 10, 20)
	expectManagedAllocation(mock, 2, 1, 10, 20, "gateway-result:2:0", 31)
	expectManagedUploadRecord(mock, 31)
	inlinePNG := make([]byte, 1<<20)
	copy(inlinePNG, testPNG)
	restore := stubManagedStorage(t)
	defer restore()
	downloadManagedResult = func(context.Context, string, int64) (*safeurl.Result, error) {
		t.Fatal("inline image triggered a remote download")
		return nil, nil
	}
	uploadManagedResult = func(_ context.Context, apiKey string, data []byte, contentType, path, filename string) (filestorage.UploadResult, error) {
		digest := sha256.Sum256(inlinePNG)
		if apiKey != testStorageAPIKey || sha256.Sum256(data) != digest || contentType != "image/png" || !strings.HasSuffix(path, "gateway-results/31/") || filename != hex.EncodeToString(digest[:])+".png" {
			t.Fatalf("upload = key:%q bytes:%d %q %q %q", apiKey, len(data), contentType, path, filename)
		}
		return filestorage.UploadResult{URL: "https://storage.example/result.png", ObjectID: "object-v1"}, nil
	}
	sources := []delivery.RemoteResult{{Role: "image", InlineData: inlinePNG, ContentType: "image/png"}}
	copies, err := service.prepareManagedResultCopies(context.Background(), 2, "managed_copy", sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(copies) != 1 || copies[0].MediaAssetID != 31 || copies[0].StorageLocator != "https://storage.example/result.png" || copies[0].ContentType != "image/png" || copies[0].ContentLength != 1<<20 {
		t.Fatalf("copies = %+v", copies)
	}
	if len(sources[0].InlineData) != 1 || sources[0].InlineData[0] != 0 {
		t.Fatalf("retained inline bytes = %d, want one-byte marker", len(sources[0].InlineData))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareManagedCopiesRecordsContentFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	expectManagedOwner(mock, 2, 10, 20)

	previousConfig := config.C
	testConfig := config.Config{}
	if previousConfig != nil {
		testConfig = *previousConfig
	}
	testConfig.FileStorage.MaxFileSizeMB = 1
	config.C = &testConfig
	t.Cleanup(func() { config.C = previousConfig })

	restore := stubManagedStorage(t)
	defer restore()
	downloadManagedResult = func(_ context.Context, value string, _ int64) (*safeurl.Result, error) {
		if value != "https://provider.example/empty.png" {
			t.Fatalf("download URL = %q", value)
		}
		return &safeurl.Result{ContentType: "image/png"}, nil
	}
	uploadManagedResult = func(context.Context, string, []byte, string, string, string) (filestorage.UploadResult, error) {
		t.Fatal("invalid content reached upload")
		return filestorage.UploadResult{}, nil
	}

	oversized := make([]byte, (1<<20)+1)
	copy(oversized, testPNG)
	sources := []delivery.RemoteResult{
		{Role: "image", InlineData: oversized, ContentType: "image/png"},
		{Role: "image", URL: "https://provider.example/empty.png"},
		{Role: "image", InlineData: []byte("plain text"), ContentType: "image/png"},
	}
	prepared, err := service.prepareManagedResultCopies(context.Background(), 2, "managed_copy", sources)
	if err != nil {
		t.Fatalf("prepare managed copies: %v", err)
	}
	wantReasons := []string{"managed_copy_size_exceeded", "managed_copy_empty", "managed_copy_content_type_invalid"}
	if len(prepared) != len(wantReasons) {
		t.Fatalf("prepared count = %d, want %d", len(prepared), len(wantReasons))
	}
	for index, want := range wantReasons {
		if prepared[index].FailureReason != want {
			t.Errorf("prepared[%d].FailureReason = %q, want %q", index, prepared[index].FailureReason, want)
		}
	}
	for _, index := range []int{0, 2} {
		if len(sources[index].InlineData) != 1 || sources[index].InlineData[0] != 0 {
			t.Errorf("sources[%d] retained %d inline bytes, want one-byte marker", index, len(sources[index].InlineData))
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareManagedCopiesKeepsGenerationWhenOneUploadFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	expectManagedOwner(mock, 2, 10, 20)
	expectManagedAllocation(mock, 2, 1, 10, 20, "gateway-result:2:0", 31)
	expectManagedUploadRecord(mock, 31)
	expectManagedAllocation(mock, 2, 1, 10, 20, "gateway-result:2:1", 32)
	restore := stubManagedStorage(t)
	defer restore()
	uploads := 0
	uploadManagedResult = func(context.Context, string, []byte, string, string, string) (filestorage.UploadResult, error) {
		uploads++
		if uploads == 2 {
			return filestorage.UploadResult{}, errors.New("storage unavailable")
		}
		return filestorage.UploadResult{URL: "https://storage.example/first.png", ID: "first"}, nil
	}
	deleted := ""
	deleteManagedResult = func(_ context.Context, _ string, value string) error { deleted = value; return nil }
	prepared, err := service.prepareManagedResultCopies(context.Background(), 2, "managed_copy", []delivery.RemoteResult{
		{Role: "image", InlineData: append([]byte(nil), testPNG...), ContentType: "image/png"},
		{Role: "image", InlineData: append([]byte(nil), testPNG...), ContentType: "image/png"},
	})
	if err != nil || deleted != "" || len(prepared) != 2 || prepared[0].MediaAssetID != 31 || prepared[1].FailureReason != "managed_copy_upload_failed" {
		t.Fatalf("prepared = %+v, err = %v, deleted = %q", prepared, err, deleted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareManagedCopiesDeletesUploadWhenRecordFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	expectManagedOwner(mock, 2, 10, 20)
	expectManagedAllocation(mock, 2, 1, 10, 20, "gateway-result:2:0", 31)
	recordFailure := errors.New("database unavailable")
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state,storage_locator FROM gw_media_assets").WithArgs(uint64(31)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "storage_locator"}).AddRow("staging", nil))
	mock.ExpectExec("UPDATE gw_media_assets SET storage_locator").WillReturnError(recordFailure)
	mock.ExpectRollback()

	restore := stubManagedStorage(t)
	defer restore()
	deleted := ""
	deleteHasDeadline := false
	deleteManagedResult = func(ctx context.Context, _ string, value string) error {
		_, deleteHasDeadline = ctx.Deadline()
		deleted = value
		return nil
	}
	uploadedFilename := ""
	uploadManagedResult = func(_ context.Context, _ string, _ []byte, _, _, filename string) (filestorage.UploadResult, error) {
		uploadedFilename = filename
		return filestorage.UploadResult{URL: "https://storage.example/result.png", ID: "object-v1"}, nil
	}

	_, err = service.prepareManagedResultCopies(context.Background(), 2, "managed_copy", []delivery.RemoteResult{{
		Role: "image", InlineData: append([]byte(nil), testPNG...), ContentType: "image/png",
	}})
	digest := sha256.Sum256(testPNG)
	expectedFilename := hex.EncodeToString(digest[:]) + ".png"
	if !errors.Is(err, recordFailure) || deleted != "https://storage.example/result.png" || !deleteHasDeadline || uploadedFilename != expectedFilename {
		t.Fatalf("err=%v deleted=%q deleteDeadline=%v filename=%q want=%q", err, deleted, deleteHasDeadline, uploadedFilename, expectedFilename)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectManagedOwner(mock sqlmock.Sqlmock, attemptID, userID, tokenID uint64) {
	mock.ExpectQuery("SELECT c.user_id,c.token_id,COALESCE\\(c.xfs_api_key,''\\)").WithArgs(attemptID).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id", "xfs_api_key"}).
			AddRow(userID, tokenID, testStorageAPIKey))
}

func expectManagedAllocation(mock sqlmock.Sqlmock, attemptID, callID, userID, tokenID uint64, key string, assetID int64) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT c.id,c.user_id,c.token_id FROM gw_api_call_attempts").WithArgs(attemptID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "token_id"}).AddRow(callID, userID, tokenID))
	mock.ExpectQuery("SELECT id,user_id,token_id,COALESCE\\(attempt_id,0\\),object_key,storage_locator").WithArgs(key).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO gw_media_assets").WillReturnResult(sqlmock.NewResult(assetID, 1))
	mock.ExpectExec("INSERT INTO gw_media_asset_state_events").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
}

func expectManagedUploadRecord(mock sqlmock.Sqlmock, assetID uint64) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state,storage_locator FROM gw_media_assets").WithArgs(assetID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "storage_locator"}).AddRow("staging", nil))
	mock.ExpectExec("UPDATE gw_media_assets SET storage_locator").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func stubManagedStorage(t *testing.T) func() {
	t.Helper()
	oldDownload, oldUpload, oldVerify, oldDelete := downloadManagedResult, uploadManagedResult, verifyManagedResult, deleteManagedResult
	verifyManagedResult = func(context.Context, string, string, int64, string) error { return nil }
	deleteManagedResult = func(context.Context, string, string) error { return nil }
	return func() {
		downloadManagedResult, uploadManagedResult, verifyManagedResult, deleteManagedResult = oldDownload, oldUpload, oldVerify, oldDelete
	}
}
