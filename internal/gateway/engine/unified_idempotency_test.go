package engine

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func TestExecuteIdempotentReplayStopsBeforeUpstream(t *testing.T) {
	db := openUnifiedIdempotencyEngineDB(t)
	model.SetDB(db)
	key := make([]byte, security.KeySize)
	for index := range key {
		key[index] = byte(index + 1)
	}
	encodedKey := base64.StdEncoding.EncodeToString(key)
	t.Setenv("PRISM_GATEWAY_PAYLOAD_KEK_B64", encodedKey)
	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64", encodedKey)

	keyHMAC := strings.Repeat("a", 64)
	requestHMAC := strings.Repeat("b", 64)
	now := time.Now().UTC()
	for _, statement := range []string{
		`INSERT INTO gw_skus(id,release_id,idempotency_mode) VALUES (4,1,'optional')`,
		`INSERT INTO gw_api_calls(id,public_id,user_id,token_id) VALUES (21,'existing-call',7,9)`,
		`INSERT INTO gw_api_resources(id,public_id,resource_kind,call_id) VALUES (22,'resp_existing','response',21)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`INSERT INTO gw_api_call_idempotencies(id,token_id,operation_contract_id,call_id,key_hmac,hmac_key_version,request_hmac,status,created_at,updated_at) VALUES (23,9,2,21,?,1,?,'active',?,?)`, keyHMAC, requestHMAC, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO gw_api_call_idempotency_keys(idempotency_id,token_id,operation_contract_id,key_hmac,hmac_key_version,created_at) VALUES (23,9,2,?,1,?)`, keyHMAC, now).Error; err != nil {
		t.Fatal(err)
	}

	schedule := billing.RateSchedule{
		Currency: billing.Currency{Code: "USD", Version: 1, FractionDigits: 6, RoundingMode: "half_even", MaxAmount: "1000000"},
		Components: []billing.RateComponent{{
			ID: 1, Code: "request", Unit: "request", Source: billing.QuantityOne,
			Event: billing.ChargeSucceeded, UnitPrice: "1", QuantityStep: "0", MaxQuantity: "1",
		}},
	}
	item := &engineTestTransport{id: transport.OpenAIResponses}
	registry := transport.NewRegistry()
	if err := registry.Register(item); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	selector := &engineTestSelector{route: &routing.RouteResult{
		ReleaseID: 1, OperationContractID: 2, ModelOperationID: 3, SKUID: 4,
		RouteID: 5, OfferingID: 6, CostPlanID: 13, ProductTransportID: 7, CredentialPoolID: 8,
		CredentialID: 9, CredentialVersionID: 10, PurposeGrantID: 11,
		KeyID: 12, Transport: transport.OpenAIResponses, ModelName: "public", VendorModel: "vendor",
		Currency: "USD", CurrencyVersion: 1, DeliveryMode: "reference", SellSchedule: &schedule,
	}}
	executionEngine, err := New(selector, registry)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executionEngine.Execute(t.Context(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIResponses, Model: "public",
	}, ExecuteOptions{
		UserID: 7, TokenID: 9, CallID: "11111111-1111-1111-1111-111111111111",
		ResourceType: "response", ResourceID: "resp_new",
		Idempotency: &repository.IdempotencyInput{
			TokenID: 9, OperationContractID: 2, KeyHMAC: keyHMAC,
			HMACKeyVersion: 1, RequestHMAC: requestHMAC,
		},
		EnforceIdempotencyPolicy: true,
	})
	var replay *IdempotentReplayError
	if result != nil || !errors.As(err, &replay) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if replay.CallID != 21 || replay.ResourceID != 22 || replay.CallPublicID != "existing-call" || replay.ResourcePublicID != "resp_existing" {
		t.Fatalf("replay=%#v", replay)
	}
	if item.prepareCall != 0 {
		t.Fatalf("prepared %d upstream requests during replay", item.prepareCall)
	}
	var calls, resources int64
	if err := db.Table("gw_api_calls").Count(&calls).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("gw_api_resources").Count(&resources).Error; err != nil {
		t.Fatal(err)
	}
	if calls != 1 || resources != 1 {
		t.Fatalf("calls=%d resources=%d", calls, resources)
	}
}

func openUnifiedIdempotencyEngineDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:engine_idempotency?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, statement := range []string{
		`CREATE TABLE gw_skus(id INTEGER NOT NULL,release_id INTEGER NOT NULL,idempotency_mode TEXT NOT NULL,PRIMARY KEY(id,release_id))`,
		`CREATE TABLE gw_api_calls(id INTEGER PRIMARY KEY,public_id TEXT NOT NULL,user_id INTEGER NOT NULL,token_id INTEGER NOT NULL)`,
		`CREATE TABLE gw_api_resources(id INTEGER PRIMARY KEY,public_id TEXT NOT NULL,resource_kind TEXT NOT NULL,call_id INTEGER NOT NULL)`,
		`CREATE TABLE gw_api_call_idempotencies(id INTEGER PRIMARY KEY,token_id INTEGER NOT NULL,operation_contract_id INTEGER NOT NULL,call_id INTEGER,key_hmac TEXT NOT NULL,hmac_key_version INTEGER NOT NULL,request_hmac TEXT NOT NULL,status TEXT NOT NULL,replay_expires_at DATETIME,key_reuse_after DATETIME,created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL,UNIQUE(token_id,operation_contract_id,hmac_key_version,key_hmac))`,
		`CREATE TABLE gw_api_call_idempotency_keys(id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_id INTEGER NOT NULL,token_id INTEGER NOT NULL,operation_contract_id INTEGER NOT NULL,key_hmac TEXT NOT NULL,hmac_key_version INTEGER NOT NULL,created_at DATETIME NOT NULL,UNIQUE(idempotency_id,hmac_key_version),UNIQUE(token_id,operation_contract_id,hmac_key_version,key_hmac))`,
	} {
		if err := database.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return database
}
