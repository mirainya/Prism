package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/security"
)

func callPayloadFixture() BlobInput {
	retentionUntil := time.Now().UTC().Add(time.Hour)
	return BlobInput{KeyringID: 1, KEKVersion: 1, Plaintext: []byte(`{"model":"test","items":[]}`),
		KEK: []byte("01234567890123456789012345678901"), HMACKey: []byte("01234567890123456789012345678902"), RetentionUntil: &retentionUntil}
}

func TestCallPayloadRetryCannotReplaceOrRecreatePayload(t *testing.T) {
	for _, mode := range []string{"same", "different", "expired"} {
		t.Run(mode, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store, _ := New(db)
			blob := callPayloadFixture()
			digest := security.HMACSHA256(blob.HMACKey, blob.Plaintext)
			hmac := fmt.Sprintf("%x", digest[:])
			var blobID any = uint64(7)
			if mode == "different" {
				hmac = "different"
			}
			if mode == "expired" {
				blobID = nil
			}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT request_payload_id FROM gw_api_calls").WithArgs(uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"request_payload_id"}).AddRow(2))
			mock.ExpectQuery("SELECT content_hmac,content_length,encrypted_blob_id").WithArgs(int64(2), uint64(1), "request").WillReturnRows(sqlmock.NewRows([]string{"content_hmac", "content_length", "encrypted_blob_id"}).AddRow(hmac, len(blob.Plaintext), blobID))
			if mode == "same" {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
				id, err := store.PutCallPayload(context.Background(), tx, 1, "request", blob)
				if mode == "same" && id != 2 {
					t.Errorf("replayed payload id=%d", id)
				}
				return err
			})
			if (mode == "same") != (err == nil) {
				t.Fatalf("mode=%s err=%v", mode, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCallPayloadPointerFailureRollsBackCiphertext(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT request_payload_id FROM gw_api_calls").WillReturnRows(sqlmock.NewRows([]string{"request_payload_id"}).AddRow(nil))
	mock.ExpectExec("INSERT INTO encrypted_blobs").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectExec("UPDATE encrypted_blobs").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO encrypted_blob_key_wraps").WillReturnResult(sqlmock.NewResult(8, 1))
	mock.ExpectExec("INSERT INTO gw_api_call_payloads").WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectExec("UPDATE gw_api_calls SET request_payload_id").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := store.PutCallPayload(context.Background(), tx, 1, "request", callPayloadFixture())
		return err
	})
	if err == nil {
		t.Fatal("failed payload pointer update was committed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCallPayloadRequiresEncryptionKeys(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectRollback()
	blob := callPayloadFixture()
	blob.KEK = nil
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := store.PutCallPayload(context.Background(), tx, 1, "request", blob)
		return err
	})
	if err == nil {
		t.Fatal("plaintext payload accepted without an encryption key")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
