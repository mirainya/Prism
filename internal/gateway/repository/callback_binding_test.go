package repository

import (
	"bytes"
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCreateCallbackBindingTokenWritesEveryReadableAlias(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	key1 := bytes.Repeat([]byte{0x11}, 32)
	key2 := bytes.Repeat([]byte{0x22}, 32)
	kek := bytes.Repeat([]byte{0x33}, 32)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT attempt_id FROM gw_async_executions WHERE id=? FOR UPDATE")).
		WithArgs(uint64(41)).WillReturnRows(sqlmock.NewRows([]string{"attempt_id"}).AddRow(uint64(9)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id,current_version FROM crypto_keyring_state WHERE id=? AND purpose='gateway-payload' AND current_version IS NOT NULL FOR SHARE")).
		WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "current_version"}).AddRow(uint64(7), uint32(2)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT key_version,status FROM crypto_key_versions WHERE keyring_id=? AND status IN ('current','readable') ORDER BY key_version")).
		WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"key_version", "status"}).AddRow(uint32(1), "readable").AddRow(uint32(2), "current"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT encrypted_blob_id FROM gw_callback_binding_token_aliases WHERE async_execution_id=? ORDER BY hmac_key_version LIMIT 1 FOR UPDATE")).
		WithArgs(uint64(41)).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO encrypted_blobs").WillReturnResult(sqlmock.NewResult(31, 1))
	mock.ExpectExec("UPDATE encrypted_blobs SET aad_hash").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO encrypted_blob_key_wraps").WillReturnResult(sqlmock.NewResult(32, 1))
	mock.ExpectExec("INSERT INTO gw_callback_binding_token_aliases").
		WithArgs(uint64(41), uint32(1), sqlmock.AnyArg(), uint64(31), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(33, 1))
	mock.ExpectExec("INSERT INTO gw_callback_binding_token_aliases").
		WithArgs(uint64(41), uint32(2), sqlmock.AnyArg(), uint64(31), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(34, 1))
	mock.ExpectCommit()

	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.CreateCallbackBindingToken(context.Background(), tx, 41, CallbackBindingInput{
			KeyringID: 7, KEKVersion: 2, KEK: kek, HMACKey: key2,
			HMACKeys: map[uint32][]byte{1: key1, 2: key2},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if expectationErr := mock.ExpectationsWereMet(); expectationErr != nil {
		t.Fatal(expectationErr)
	}
}

func TestCallbackBindingTokenHMACIsVersionedByKey(t *testing.T) {
	token := "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI"
	first := CallbackBindingTokenHMAC(bytes.Repeat([]byte{1}, 32), token)
	second := CallbackBindingTokenHMAC(bytes.Repeat([]byte{2}, 32), token)
	if !validHexDigest(first, 32) || !validHexDigest(second, 32) || first == second {
		t.Fatalf("callback aliases are invalid or not key-version specific: %q %q", first, second)
	}
}

func TestFindCallbackBindingReturnsBlobWithoutCallbackVerifyGrant(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	digest := CallbackBindingTokenHMAC(bytes.Repeat([]byte{3}, 32), "token")
	mock.ExpectQuery("SELECT a.async_execution_id,ca.credential_version_id,a.encrypted_blob_id").
		WithArgs(uint32(2), digest).
		WillReturnRows(sqlmock.NewRows([]string{"async_execution_id", "credential_version_id", "encrypted_blob_id"}).AddRow(uint64(8), uint64(12), uint64(21)))
	got, err := store.FindCallbackBinding(context.Background(), 2, digest)
	if err != nil {
		t.Fatal(err)
	}
	if got.AsyncExecutionID != 8 || got.CredentialVersionID != 12 || got.EncryptedBlobID != 21 {
		t.Fatalf("unexpected binding: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
