package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestReadCapabilityDispatchReturnsImmutableSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	rows := sqlmock.NewRows([]string{
		"call_id", "attempt_id", "credential_id", "credential_secret", "credential_blob_id", "request_blob_id",
		"channel_transport_id", "release_id", "public_id", "protocol", "base_url", "method", "path",
		"auth_scheme", "vendor_model", "delivery_mode", "source_url_policy", "adapter_code",
		"adapter_version", "timeout_ms",
	}).AddRow(1, 2, 3, nil, 4, 5, 6, 7, "call-public", "openai_images", "https://provider.example", "POST", "/v1/images/generations", "bearer", "vendor-image", "managed_copy", "fixed", "openai_images", 1, 90000)
	mock.ExpectQuery("SELECT c.id,a.id,a.credential_id,credential.secret").WithArgs(uint64(2)).WillReturnRows(rows)

	got, err := store.ReadCapabilityDispatch(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.CallID != 1 || got.AttemptID != 2 || got.CredentialBlobID != 4 || got.RequestBlobID != 5 ||
		got.AdapterCode != "openai_images" || got.AdapterVersion != 1 || got.TimeoutMS != 90000 ||
		got.BaseURL != "https://provider.example" || got.Path != "/v1/images/generations" {
		t.Fatalf("dispatch = %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReadCapabilityDispatchHidesMissingAttempt(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectQuery("SELECT c.id,a.id,a.credential_id,credential.secret").WithArgs(uint64(9)).WillReturnError(sql.ErrNoRows)

	_, err = store.ReadCapabilityDispatch(context.Background(), 9)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
