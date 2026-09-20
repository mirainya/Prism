package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/security"
)

// PutRequestLogPayload attaches one immutable raw HTTP body to an outbound
// exchange. Request and response bodies use separate AAD owners and columns.
func (s *Store) PutRequestLogPayload(ctx context.Context, tx *sql.Tx, requestLogID uint64, kind string, blob BlobInput) (uint64, error) {
	if tx == nil || requestLogID == 0 || (kind != "request" && kind != "response") || len(blob.Plaintext) == 0 || len(blob.KEK) != security.KeySize || len(blob.HMACKey) != security.KeySize {
		return 0, ErrInvalidInput
	}
	column := "request_payload_blob_id"
	if kind == "response" {
		column = "response_payload_blob_id"
	}
	return s.putRequestLogBlob(ctx, tx, requestLogID, column, "gateway-upstream-"+kind, kind, blob)
}

// PutRequestLogDiagnostic stores a bounded, sanitized summary separately from
// the immutable raw provider response.
func (s *Store) PutRequestLogDiagnostic(ctx context.Context, tx *sql.Tx, requestLogID uint64, blob BlobInput) (uint64, error) {
	if tx == nil || requestLogID == 0 || len(blob.Plaintext) == 0 || len(blob.Plaintext) > 16<<10 || len(blob.KEK) != security.KeySize || len(blob.HMACKey) != security.KeySize {
		return 0, ErrInvalidInput
	}
	return s.putRequestLogBlob(ctx, tx, requestLogID, "diagnostic_blob_id", "gateway-request-diagnostic", "diagnostic", blob)
}

func (s *Store) putRequestLogBlob(ctx context.Context, tx *sql.Tx, requestLogID uint64, column, purpose, ownerKind string, blob BlobInput) (uint64, error) {
	var existing sql.NullInt64
	if err := tx.QueryRowContext(ctx, s.forUpdate("SELECT "+column+" FROM gw_channel_request_logs WHERE id=?"), requestLogID).Scan(&existing); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	digest := security.HMACSHA256(blob.HMACKey, blob.Plaintext)
	contentHMAC := fmt.Sprintf("%x", digest[:])
	if existing.Valid {
		var storedPurpose, storedHMAC string
		var storedLength uint64
		if err := tx.QueryRowContext(ctx, `SELECT purpose,content_hmac,content_length FROM encrypted_blobs WHERE id=? AND purged_at IS NULL`, existing.Int64).Scan(&storedPurpose, &storedHMAC, &storedLength); err != nil {
			return 0, err
		}
		if storedPurpose != purpose || storedHMAC != contentHMAC || storedLength != uint64(len(blob.Plaintext)) {
			return 0, fmt.Errorf("%w: request log payload is immutable", ErrConflict)
		}
		return uint64(existing.Int64), nil
	}
	blob.Purpose = purpose
	blob.SchemaVersion = 1
	blob.Owner = []byte(fmt.Sprintf("request-log:%d:%s", requestLogID, ownerKind))
	blobID, err := s.PutEncryptedBlob(ctx, tx, blob)
	if err != nil {
		return 0, err
	}
	if err := requireOneRow(tx.ExecContext(ctx, "UPDATE gw_channel_request_logs SET "+column+"=? WHERE id=? AND "+column+" IS NULL", blobID, requestLogID)); err != nil {
		return 0, err
	}
	return blobID, nil
}
