package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/security"
)

// PutCallPayload stores one immutable executable payload per call and kind.
// The call pointer, payload metadata, ciphertext and key wrap commit together.
func (s *Store) PutCallPayload(ctx context.Context, tx *sql.Tx, callID uint64, kind string, blob BlobInput) (uint64, error) {
	if tx == nil || callID == 0 || (kind != "request" && kind != "result") || len(blob.Plaintext) == 0 || len(blob.KEK) != security.KeySize || len(blob.HMACKey) != security.KeySize || blob.RetentionUntil == nil || blob.RetentionUntil.IsZero() {
		return 0, ErrInvalidInput
	}
	column := "request_payload_id"
	if kind == "result" {
		column = "result_payload_id"
	}
	var existing sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT "+column+" FROM gw_api_calls WHERE id=? FOR UPDATE", callID).Scan(&existing); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	hmac := security.HMACSHA256(blob.HMACKey, blob.Plaintext)
	contentHMAC := fmt.Sprintf("%x", hmac[:])
	if existing.Valid {
		var storedHMAC string
		var storedLength uint64
		var blobID sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT content_hmac,content_length,encrypted_blob_id FROM gw_api_call_payloads WHERE id=? AND call_id=? AND kind=?`, existing.Int64, callID, kind).Scan(&storedHMAC, &storedLength, &blobID); err != nil {
			return 0, err
		}
		if !blobID.Valid {
			return 0, fmt.Errorf("%w: call payload has expired", ErrNotFound)
		}
		if storedHMAC != contentHMAC || storedLength != uint64(len(blob.Plaintext)) {
			return 0, fmt.Errorf("%w: call payload is immutable", ErrConflict)
		}
		return uint64(existing.Int64), nil
	}
	blob.Purpose, blob.SchemaVersion = "gateway-payload", 1
	blob.Owner = []byte(fmt.Sprintf("call:%d:%s", callID, kind))
	blobID, err := s.PutEncryptedBlob(ctx, tx, blob)
	if err != nil {
		return 0, err
	}
	payloadID, err := s.CreateCallPayload(ctx, tx, CallPayloadInput{CallID: callID, Kind: kind, SchemaVersion: 1,
		EncryptedBlobID: &blobID, ContentHMAC: contentHMAC, ContentLength: uint64(len(blob.Plaintext)), RetentionUntil: blob.RetentionUntil})
	if err != nil {
		return 0, err
	}
	if err := requireOneRow(tx.ExecContext(ctx, "UPDATE gw_api_calls SET "+column+"=? WHERE id=? AND "+column+" IS NULL", payloadID, callID)); err != nil {
		return 0, err
	}
	return payloadID, nil
}
