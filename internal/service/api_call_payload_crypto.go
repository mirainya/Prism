package service

import (
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/config"
)

const apiCallPayloadCipherVersion byte = 1

var errAPICallPayloadDecrypt = errors.New("api call payload cannot be decrypted")

func encryptAPICallPayload(payload *model.APICallPayload) (bool, error) {
	key := configuredAPICallPayloadKey()
	if payload == nil || len(payload.Data) == 0 || len(key) == 0 || payload.Encrypted {
		return false, nil
	}
	gcm, err := newPayloadGCM(key)
	if err != nil {
		return false, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(cryptorand.Reader, nonce); err != nil {
		return false, fmt.Errorf("generate API call payload nonce: %w", err)
	}
	result := make([]byte, 1+len(nonce))
	result[0] = apiCallPayloadCipherVersion
	copy(result[1:], nonce)
	result = gcm.Seal(result, nonce, payload.Data, payloadAssociatedData(payload))
	payload.Data = result
	payload.Encrypted = true
	return true, nil
}

func decryptAPICallPayload(payload *model.APICallPayload) ([]byte, error) {
	if payload == nil || !payload.Encrypted {
		if payload == nil {
			return nil, nil
		}
		return append([]byte(nil), payload.Data...), nil
	}
	key := configuredAPICallPayloadKey()
	if len(key) == 0 {
		return nil, errAPICallPayloadDecrypt
	}
	gcm, err := newPayloadGCM(key)
	if err != nil {
		return nil, err
	}
	if len(payload.Data) < 1+gcm.NonceSize() || payload.Data[0] != apiCallPayloadCipherVersion {
		return nil, errAPICallPayloadDecrypt
	}
	nonce := payload.Data[1 : 1+gcm.NonceSize()]
	plaintext, err := gcm.Open(nil, nonce, payload.Data[1+gcm.NonceSize():], payloadAssociatedData(payload))
	if err != nil {
		return nil, errAPICallPayloadDecrypt
	}
	return plaintext, nil
}

func newPayloadGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func configuredAPICallPayloadKey() []byte {
	cfg := config.Get()
	if cfg == nil {
		return nil
	}
	secret := strings.TrimSpace(cfg.Observability.APICallPayloadEncryptionKey)
	if secret == "" {
		return nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(secret); err == nil && len(decoded) == 32 {
		return decoded
	}
	if decoded, err := hex.DecodeString(secret); err == nil && len(decoded) == 32 {
		return decoded
	}
	digest := sha256.Sum256([]byte(secret))
	return digest[:]
}

func payloadAssociatedData(payload *model.APICallPayload) []byte {
	return []byte(payload.CallID + "\x00" + strconv.FormatUint(uint64(payload.AttemptID), 10) + "\x00" + payload.Kind)
}
