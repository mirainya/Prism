package responses

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/model"
)

func TestPrepareResponseIdempotencyUsesStableOperationAndHMACOnly(t *testing.T) {
	db := openResponsesTestDB(t)
	model.SetDB(db)
	for _, statement := range []string{
		`CREATE TABLE gw_operation_routes (operation_contract_id INTEGER NOT NULL, http_method TEXT NOT NULL, route_template TEXT NOT NULL)`,
		`CREATE TABLE crypto_keyring_state (id INTEGER PRIMARY KEY, purpose TEXT NOT NULL, current_version INTEGER NOT NULL)`,
		`CREATE TABLE crypto_key_versions (keyring_id INTEGER NOT NULL, key_version INTEGER NOT NULL, status TEXT NOT NULL)`,
		`INSERT INTO gw_operation_routes VALUES (17,'POST','/v1/responses')`,
		`INSERT INTO crypto_keyring_state VALUES (3,'gateway-payload',4)`,
		`INSERT INTO crypto_key_versions VALUES (3,4,'current')`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	key := make([]byte, security.KeySize)
	for index := range key {
		key[index] = byte(index + 1)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	for _, name := range []string{"PRISM_GATEWAY_KEK_B64", "PRISM_GATEWAY_HMAC_B64", "PRISM_GATEWAY_PAYLOAD_KEK_B64", "PRISM_GATEWAY_PAYLOAD_HMAC_B64"} {
		t.Setenv(name, encoded)
	}
	intent := []byte(`{"model":"stable","input":"hello"}`)
	input, err := prepareResponseIdempotency(context.Background(), 9, "secret-request-key", intent)
	if err != nil {
		t.Fatal(err)
	}
	if input.TokenID != 9 || input.OperationContractID != 17 || input.HMACKeyVersion != 4 {
		t.Fatalf("idempotency identity=%#v", input)
	}
	wantKey := security.HMACSHA256(key, []byte("gateway-idempotency-v1:secret-request-key"))
	wantRequest := security.HMACSHA256(key, intent)
	if input.KeyHMAC != hex.EncodeToString(wantKey[:]) || input.RequestHMAC != hex.EncodeToString(wantRequest[:]) {
		t.Fatalf("unexpected HMACs: %#v", input)
	}
	if input.KeyHMAC == "secret-request-key" || input.ReplayExpiresAt == nil || input.KeyReuseAfter == nil || !input.KeyReuseAfter.After(*input.ReplayExpiresAt) {
		t.Fatalf("unsafe or incomplete idempotency input=%#v", input)
	}
}

func TestResponseIdempotencyErrorsAreStableClientErrors(t *testing.T) {
	for _, source := range []error{repository.ErrIdempotencyConflict, repository.ErrIdempotencyExpired} {
		err := responseIdempotencyError(source)
		if err == nil || errors.Is(err, source) {
			t.Fatalf("repository error leaked: %v", err)
		}
	}
}

func TestUnifiedResponseReplayWaitHonorsContext(t *testing.T) {
	db := openResponsesTestDB(t)
	model.SetDB(db)
	for _, statement := range []string{
		`CREATE TABLE gw_api_calls (id INTEGER PRIMARY KEY, public_id TEXT, user_id INTEGER, token_id INTEGER, request_payload_id INTEGER, result_payload_id INTEGER, status TEXT, final_attempt_id INTEGER, current_attempt_id INTEGER, created_at DATETIME)`,
		`CREATE TABLE gw_api_resources (id INTEGER PRIMARY KEY, public_id TEXT, resource_kind TEXT, call_id INTEGER, user_id INTEGER, token_id INTEGER, deleted_at DATETIME)`,
		`CREATE TABLE gw_ai_responses (resource_id INTEGER PRIMARY KEY, status TEXT, result_summary BLOB)`,
		`CREATE TABLE gw_api_call_attempts (id INTEGER PRIMARY KEY, credential_id INTEGER, product_transport_id INTEGER, catalog_release_id INTEGER)`,
		`CREATE TABLE gw_product_transports (id INTEGER, release_id INTEGER, product_id INTEGER, channel_transport_id INTEGER)`,
		`CREATE TABLE gw_products (id INTEGER, release_id INTEGER, channel_id INTEGER)`,
		`CREATE TABLE gw_channel_transports (id INTEGER, release_id INTEGER, protocol TEXT)`,
		`INSERT INTO gw_api_calls VALUES (1,'call-public',7,9,11,NULL,'in_progress',NULL,NULL,CURRENT_TIMESTAMP)`,
		`INSERT INTO gw_api_resources VALUES (2,'resp-public','response',1,7,9,NULL)`,
		`INSERT INTO gw_ai_responses VALUES (2,'in_progress','{}')`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := waitResponseIdempotentReplay(ctx, 7, 9, 1)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error=%v", err)
	}
}
