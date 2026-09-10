package admin

import (
	"context"
	"errors"
	"os"

	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

func readAdminGatewayPayload(ctx context.Context, store *repository.Store, blobID uint64, purpose, owner string) ([]byte, error) {
	envelope, err := store.ReadEncryptedBlob(ctx, store.DB(), blobID)
	if err != nil {
		return nil, err
	}
	if envelope.Purpose != purpose || envelope.SchemaVersion != 1 {
		return nil, security.ErrAuthentication
	}
	kek, err := adminGatewayKey("PRISM_GATEWAY_PAYLOAD_KEK_B64")
	if err != nil {
		return nil, err
	}
	defer clear(kek)
	hmacKey, err := adminGatewayKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		return nil, err
	}
	defer clear(hmacKey)
	return repository.OpenBlob(envelope, blobID, []byte(owner), kek, hmacKey)
}

func adminGatewayKey(name string) ([]byte, error) {
	decoded, err := security.DecodeBase64Key(os.Getenv(name))
	if err != nil {
		return nil, errors.New(name + " must be a base64 encoded 32 byte key")
	}
	return decoded, nil
}
