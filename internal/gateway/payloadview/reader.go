package payloadview

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

// ReadRequestLogPayload opens an upstream request or response body using the
// immutable purpose and owner assigned when the request log was written.
func ReadRequestLogPayload(ctx context.Context, store *repository.Store, requestLogID, blobID uint64, kind string) ([]byte, error) {
	if store == nil || requestLogID == 0 || blobID == 0 || (kind != "request" && kind != "response") {
		return nil, repository.ErrInvalidInput
	}
	envelope, err := store.ReadEncryptedBlob(ctx, store.DB(), blobID)
	if err != nil {
		return nil, err
	}
	purpose := "gateway-upstream-" + kind
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
	owner := []byte(fmt.Sprintf("request-log:%d:%s", requestLogID, kind))
	return repository.OpenBlob(envelope, blobID, owner, kek, hmacKey)
}

func keyFromEnvironment(name string) ([]byte, error) {
	decoded, err := security.DecodeBase64Key(os.Getenv(name))
	if err != nil {
		return nil, errors.New(name + " must be a base64 encoded 32 byte key")
	}
	return decoded, nil
}
