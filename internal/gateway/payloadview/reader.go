package payloadview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

// ReadCallPayload opens the retained downstream request or final result for a
// unified call. The row lookup binds the payload to both its call and kind so a
// caller cannot use a valid blob identifier outside its owning call.
func ReadCallPayload(ctx context.Context, store *repository.Store, callID, payloadID uint64, kind string) ([]byte, error) {
	if store == nil || callID == 0 || payloadID == 0 || (kind != "request" && kind != "result") {
		return nil, repository.ErrInvalidInput
	}
	var blobID sql.NullInt64
	if err := store.DB().QueryRowContext(ctx, `SELECT encrypted_blob_id FROM gw_api_call_payloads WHERE id=? AND call_id=? AND kind=? AND purged_at IS NULL`, payloadID, callID, kind).Scan(&blobID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, err
	}
	if !blobID.Valid || blobID.Int64 <= 0 {
		return nil, repository.ErrNotFound
	}
	envelope, err := store.ReadEncryptedBlob(ctx, store.DB(), uint64(blobID.Int64))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, err
	}
	if envelope.Purpose != "gateway-payload" || envelope.SchemaVersion != 1 {
		return nil, security.ErrAuthentication
	}
	kek, err := keyFromEnvironment("PRISM_GATEWAY_PAYLOAD_KEK_B64")
	if err != nil {
		return nil, err
	}
	defer clear(kek)
	hmacKey, err := keyFromEnvironment("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		return nil, err
	}
	defer clear(hmacKey)
	owner := []byte(fmt.Sprintf("call:%d:%s", callID, kind))
	return repository.OpenBlob(envelope, uint64(blobID.Int64), owner, kek, hmacKey)
}

// ReadRequestLogPayload opens an upstream request or response body using the
// immutable purpose and owner assigned when the request log was written.
func ReadRequestLogPayload(ctx context.Context, store *repository.Store, requestLogID, blobID uint64, kind string) ([]byte, error) {
	if store == nil || requestLogID == 0 || blobID == 0 || (kind != "request" && kind != "response") {
		return nil, repository.ErrInvalidInput
	}
	return readRequestLogBlob(ctx, store, requestLogID, blobID, "gateway-upstream-"+kind, kind)
}

func ReadRequestLogDiagnostic(ctx context.Context, store *repository.Store, requestLogID, blobID uint64) ([]byte, error) {
	if store == nil || requestLogID == 0 || blobID == 0 {
		return nil, repository.ErrInvalidInput
	}
	return readRequestLogBlob(ctx, store, requestLogID, blobID, "gateway-request-diagnostic", "diagnostic")
}

func readRequestLogBlob(ctx context.Context, store *repository.Store, requestLogID, blobID uint64, purpose, ownerKind string) ([]byte, error) {
	envelope, err := store.ReadEncryptedBlob(ctx, store.DB(), blobID)
	if err != nil {
		return nil, err
	}
	if envelope.Purpose != purpose || envelope.SchemaVersion != 1 {
		return nil, security.ErrAuthentication
	}
	kek, err := keyFromEnvironment("PRISM_GATEWAY_PAYLOAD_KEK_B64")
	if err != nil {
		return nil, err
	}
	defer clear(kek)
	hmacKey, err := keyFromEnvironment("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		return nil, err
	}
	defer clear(hmacKey)
	owner := []byte(fmt.Sprintf("request-log:%d:%s", requestLogID, ownerKind))
	return repository.OpenBlob(envelope, blobID, owner, kek, hmacKey)
}

func keyFromEnvironment(name string) ([]byte, error) {
	decoded, err := security.DecodeBase64Key(os.Getenv(name))
	if err != nil {
		return nil, errors.New(name + " must be a base64 encoded 32 byte key")
	}
	return decoded, nil
}
