package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

func TestFindIdempotencyBackfillsCurrentVersionAlias(t *testing.T) {
	db, store := openIdempotencyTestStore(t)
	oldRequestHMAC := strings.Repeat("c", 64)
	currentRequestHMAC := strings.Repeat("d", 64)
	oldKeyHMAC := strings.Repeat("a", 64)
	currentKeyHMAC := strings.Repeat("b", 64)
	insertIdempotencyFact(t, db, 1, oldRequestHMAC, oldKeyHMAC, 1)
	if _, err := db.Exec(`INSERT INTO gw_api_call_idempotency_keys(idempotency_id,token_id,operation_contract_id,key_hmac,hmac_key_version,created_at) VALUES (1,7,9,?,1,?)`, oldKeyHMAC, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	result, err := store.FindIdempotency(context.Background(), IdempotencyInput{
		TokenID: 7, OperationContractID: 9,
		KeyHMAC: currentKeyHMAC, HMACKeyVersion: 2, RequestHMAC: currentRequestHMAC,
		KeyAliases: []IdempotencyKeyAlias{{KeyHMAC: oldKeyHMAC, RequestHMAC: oldRequestHMAC, HMACKeyVersion: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reused || result.ID != 1 {
		t.Fatalf("reservation=%#v", result)
	}
	var factID uint64
	if err := db.QueryRow(`SELECT idempotency_id FROM gw_api_call_idempotency_keys WHERE token_id=7 AND operation_contract_id=9 AND hmac_key_version=2 AND key_hmac=?`, currentKeyHMAC).Scan(&factID); err != nil {
		t.Fatal(err)
	}
	if factID != 1 {
		t.Fatalf("current alias fact=%d, want 1", factID)
	}
}

func TestFindIdempotencyRejectsAliasesForDifferentFacts(t *testing.T) {
	db, store := openIdempotencyTestStore(t)
	oldRequestHMAC := strings.Repeat("c", 64)
	currentRequestHMAC := strings.Repeat("d", 64)
	oldKeyHMAC := strings.Repeat("a", 64)
	currentKeyHMAC := strings.Repeat("b", 64)
	insertIdempotencyFact(t, db, 1, oldRequestHMAC, oldKeyHMAC, 1)
	insertIdempotencyFact(t, db, 2, currentRequestHMAC, currentKeyHMAC, 2)
	for _, row := range []struct {
		id      uint64
		key     string
		version uint32
	}{{1, oldKeyHMAC, 1}, {2, currentKeyHMAC, 2}} {
		if _, err := db.Exec(`INSERT INTO gw_api_call_idempotency_keys(idempotency_id,token_id,operation_contract_id,key_hmac,hmac_key_version,created_at) VALUES (?,7,9,?,?,?)`, row.id, row.key, row.version, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}

	_, err := store.FindIdempotency(context.Background(), IdempotencyInput{
		TokenID: 7, OperationContractID: 9,
		KeyHMAC: currentKeyHMAC, HMACKeyVersion: 2, RequestHMAC: currentRequestHMAC,
		KeyAliases: []IdempotencyKeyAlias{{KeyHMAC: oldKeyHMAC, RequestHMAC: oldRequestHMAC, HMACKeyVersion: 1}},
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("error=%v, want idempotency conflict", err)
	}
}

func TestReserveIdempotencyCreatesFactAndAllAliasesAtomically(t *testing.T) {
	db, store := openIdempotencyTestStore(t)
	oldRequestHMAC := strings.Repeat("c", 64)
	currentRequestHMAC := strings.Repeat("d", 64)
	oldKeyHMAC := strings.Repeat("a", 64)
	currentKeyHMAC := strings.Repeat("b", 64)
	var reserved IdempotencyReservation
	err := store.WithTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		reserved, err = store.ReserveIdempotency(context.Background(), tx, IdempotencyInput{
			TokenID: 7, OperationContractID: 9,
			KeyHMAC: currentKeyHMAC, HMACKeyVersion: 2, RequestHMAC: currentRequestHMAC,
			KeyAliases: []IdempotencyKeyAlias{{KeyHMAC: oldKeyHMAC, RequestHMAC: oldRequestHMAC, HMACKeyVersion: 1}},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if reserved.ID == 0 || reserved.Reused {
		t.Fatalf("reservation=%#v", reserved)
	}
	var factCount, aliasCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_api_call_idempotencies WHERE id=?`, reserved.ID).Scan(&factCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_api_call_idempotency_keys WHERE idempotency_id=?`, reserved.ID).Scan(&aliasCount); err != nil {
		t.Fatal(err)
	}
	if factCount != 1 || aliasCount != 2 {
		t.Fatalf("facts=%d aliases=%d", factCount, aliasCount)
	}
}

func openIdempotencyTestStore(t *testing.T) (*sql.DB, *Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE gw_api_call_idempotencies (
			id INTEGER PRIMARY KEY AUTOINCREMENT, token_id INTEGER NOT NULL, operation_contract_id INTEGER NOT NULL,
			call_id INTEGER, key_hmac TEXT NOT NULL, hmac_key_version INTEGER NOT NULL, request_hmac TEXT NOT NULL,
			status TEXT NOT NULL, replay_expires_at DATETIME, key_reuse_after DATETIME, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			UNIQUE(token_id,operation_contract_id,hmac_key_version,key_hmac))`,
		`CREATE TABLE gw_api_call_idempotency_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT, idempotency_id INTEGER NOT NULL, token_id INTEGER NOT NULL,
			operation_contract_id INTEGER NOT NULL, key_hmac TEXT NOT NULL, hmac_key_version INTEGER NOT NULL, created_at DATETIME NOT NULL,
			UNIQUE(idempotency_id,hmac_key_version), UNIQUE(token_id,operation_contract_id,hmac_key_version,key_hmac))`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, store
}

func insertIdempotencyFact(t *testing.T, db *sql.DB, id uint64, requestHMAC, keyHMAC string, version uint32) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO gw_api_call_idempotencies(id,token_id,operation_contract_id,key_hmac,hmac_key_version,request_hmac,status,created_at,updated_at) VALUES (?,7,9,?,?,?,'active',?,?)`, id, keyHMAC, version, requestHMAC, now, now); err != nil {
		t.Fatal(err)
	}
}
