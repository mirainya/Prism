package payloadview

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

func TestReadCallPayloadDecryptsOnlyTheBoundPayload(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}

	const callID, payloadID, blobID = uint64(11), uint64(12), uint64(13)
	plain := []byte(`{"data":[{"url":"https://example.com/result.png"}]}`)
	kek := []byte("01234567890123456789012345678901")
	hmacKey := []byte("abcdef0123456789abcdef0123456789")
	t.Setenv("PRISM_GATEWAY_PAYLOAD_KEK_B64", base64.StdEncoding.EncodeToString(kek))
	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64", base64.StdEncoding.EncodeToString(hmacKey))
	owner := []byte("call:11:result")
	aad, err := security.CanonicalAAD(blobID, "gateway-payload", 1, owner)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := security.Seal(plain, aad, kek, 1)
	if err != nil {
		t.Fatal(err)
	}
	digest := security.HMACSHA256(hmacKey, plain)

	mock.ExpectQuery("SELECT encrypted_blob_id FROM gw_api_call_payloads").
		WithArgs(payloadID, callID, "result").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_blob_id"}).AddRow(blobID))
	mock.ExpectQuery("SELECT b.keyring_id,b.purpose,b.schema_version").
		WithArgs(blobID).
		WillReturnRows(sqlmock.NewRows([]string{"keyring_id", "purpose", "schema_version", "aad_hash", "nonce", "ciphertext", "content_hmac", "kek_version", "wrap_nonce", "wrapped_dek"}).
			AddRow(uint64(1), "gateway-payload", uint32(1), "", sealed.Nonce, sealed.Ciphertext, hex.EncodeToString(digest[:]), uint32(1), sealed.WrapNonce, sealed.WrappedDEK))

	got, err := ReadCallPayload(context.Background(), store, callID, payloadID, "result")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Fatalf("payload = %q, want %q", got, plain)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReadCallPayloadRejectsInvalidInput(t *testing.T) {
	if _, err := ReadCallPayload(context.Background(), nil, 1, 2, "response"); err != repository.ErrInvalidInput {
		t.Fatalf("error = %v, want %v", err, repository.ErrInvalidInput)
	}
}

func TestReadCallPayloadReportsPurgedPayloadAsNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("SELECT encrypted_blob_id FROM gw_api_call_payloads").
		WithArgs(uint64(12), uint64(11), "result").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_blob_id"}))
	if _, err := ReadCallPayload(context.Background(), store, 11, 12, "result"); err != repository.ErrNotFound {
		t.Fatalf("error = %v, want %v", err, repository.ErrNotFound)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
