package migrate

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/pkg/config"
)

func TestImportLegacyAIFilesRequiresDependencies(t *testing.T) {
	if _, err := ImportLegacyAIFiles(context.Background(), nil, ImportOptions{}); err == nil {
		t.Fatal("expected database and HMAC key validation")
	}
	previous := config.C
	config.C = &config.Config{}
	t.Cleanup(func() { config.C = previous })
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := ImportLegacyAIFiles(context.Background(), db, ImportOptions{HMACKey: make([]byte, security.KeySize)}); err == nil {
		t.Fatal("expected file storage validation")
	}
}

func TestImportLegacyAIFilesEmptySnapshot(t *testing.T) {
	previous := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{BaseURL: "http://storage.invalid", APIKey: "test"}}
	t.Cleanup(func() { config.C = previous })
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT id,user_id,token_id.*FROM ai_files").WillReturnRows(sqlmock.NewRows([]string{
		"id", "user_id", "token_id", "filename", "purpose", "bytes", "mime_type", "status", "created_at", "content_bytes", "digest",
	}))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,status FROM gw_migration_runs.*FOR UPDATE").
		WithArgs(legacyAIFileImportOperation, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}))
	mock.ExpectExec("INSERT INTO gw_migration_runs").
		WithArgs(legacyAIFileImportOperation, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectCommit()
	mock.ExpectExec("UPDATE gw_migration_runs SET status=").
		WithArgs("succeeded", sqlmock.AnyArg(), "", int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	report, err := ImportLegacyAIFiles(context.Background(), db, ImportOptions{HMACKey: make([]byte, security.KeySize)})
	if err != nil {
		t.Fatal(err)
	}
	if report.RunID != 9 || report.Imported != 0 || report.Skipped != 0 || report.Issues != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyAIFileBlobReaderReadsBoundedChunks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT SUBSTRING\\(content,\\?,\\?\\) FROM ai_files WHERE id=\\?").
		WithArgs(int64(1), int64(3), "file_test").
		WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow([]byte("abc")))
	mock.ExpectQuery("SELECT SUBSTRING\\(content,\\?,\\?\\) FROM ai_files WHERE id=\\?").
		WithArgs(int64(4), int64(2), "file_test").
		WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow([]byte("de")))
	mock.ExpectRollback()
	reader := &legacyAIFileBlobReader{ctx: context.Background(), tx: tx, fileID: "file_test", size: 5}
	buffer := make([]byte, 3)
	count, err := reader.Read(buffer)
	if err != nil || count != 3 || string(buffer) != "abc" {
		t.Fatalf("first read count=%d data=%q err=%v", count, buffer, err)
	}
	count, err = reader.Read(buffer)
	if err != nil || count != 2 || string(buffer[:count]) != "de" {
		t.Fatalf("second read count=%d data=%q err=%v", count, buffer[:count], err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
