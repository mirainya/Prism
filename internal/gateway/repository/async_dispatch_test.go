package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestReadAsyncDispatchIncludesCallbackBindingToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery(regexp.QuoteMeta("COALESCE(product.capability_constraints,'{}'),ct.timeout_ms,adapter.adapter_code")).
		WithArgs(uint64(19)).
		WillReturnRows(sqlmock.NewRows([]string{
			"call_id", "attempt_id", "credential_id", "credential_secret", "credential_blob_id", "request_blob_id", "task_identity_blob_id",
			"callback_binding_token_blob_id", "channel_transport_id", "release_id", "public_id", "protocol", "base_url",
			"method", "path", "auth_scheme", "vendor_model", "delivery_mode", "source_url_policy", "adapter_config",
			"timeout_ms", "adapter_code", "adapter_version",
		}).AddRow(
			1, 2, 3, nil, 4, 5, 6, 7, 8, 9, "call-1", "json_task_v1", "https://provider.example",
			"POST", "/tasks", "bearer", "vendor-model", "managed_copy", "fixed", []byte(`{"schema_version":1}`),
			120000, "generic", 1,
		))

	dispatch, err := store.ReadAsyncDispatch(context.Background(), 19)
	if err != nil {
		t.Fatal(err)
	}
	if dispatch.TaskIdentityBlobID != 6 || dispatch.CallbackBindingTokenBlobID != 7 || dispatch.ChannelTransportID != 8 || dispatch.TransportTimeoutMS != 120000 || dispatch.AdapterCode != "generic" {
		t.Fatalf("dispatch fields shifted or omitted: %+v", dispatch)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReadAsyncDispatchRejectsZeroID(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadAsyncDispatch(context.Background(), 0); err != ErrInvalidInput {
		t.Fatalf("ReadAsyncDispatch(0) error = %v, want %v", err, ErrInvalidInput)
	}
}
