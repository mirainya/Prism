package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestUnifiedPayloadFailsWhenKeysAreUnavailable(t *testing.T) {
	t.Setenv("PRISM_GATEWAY_PAYLOAD_KEK_B64", "")
	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64", "")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	l := &unifiedLifecycle{store: store, callID: 1}
	if err := l.recordPayload(context.Background(), "request", []byte(`{"items":[]}`)); err == nil {
		t.Fatal("missing keys silently skipped the executable payload")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUnifiedPayloadReturnsKeyringLookupFailure(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	t.Setenv("PRISM_GATEWAY_PAYLOAD_KEK_B64", key)
	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64", key)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	l := &unifiedLifecycle{store: store, callID: 1}
	want := errors.New("keyring lookup failed")
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT k.id,k.current_version").WillReturnError(want)
	mock.ExpectRollback()
	if err := l.recordPayload(context.Background(), "request", []byte(`{"items":[]}`)); !errors.Is(err, want) {
		t.Fatalf("keyring error not propagated: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
