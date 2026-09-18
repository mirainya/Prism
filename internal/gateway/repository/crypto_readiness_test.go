package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

// Only the database clock expression differs between SQLite and MySQL.
type readinessTestDB struct{ *sql.DB }

func (db readinessTestDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.DB.QueryRowContext(ctx, strings.ReplaceAll(query, "CURRENT_TIMESTAMP(3)", "'2099-01-01 00:00:00'"), args...)
}

func cryptoFixture(t *testing.T) readinessTestDB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, query := range []string{
		`CREATE TABLE gw_credentials(id INTEGER, status TEXT, secret TEXT)`,
		`CREATE TABLE gw_credential_versions(credential_id INTEGER, encrypted_blob_id INTEGER)`,
		`CREATE TABLE crypto_keyring_state(id INTEGER, purpose TEXT, current_version INTEGER)`,
		`CREATE TABLE crypto_key_versions(keyring_id INTEGER, key_version INTEGER, status TEXT)`,
		`CREATE TABLE gw_deployment_members(id INTEGER, deployment_generation_id INTEGER, instance_id TEXT, role TEXT)`,
		`CREATE TABLE crypto_key_readiness(deployment_generation_id INTEGER, deployment_member_id INTEGER, keyring_id INTEGER, key_version INTEGER, operation TEXT, status TEXT, expires_at TEXT)`,
		`INSERT INTO crypto_keyring_state VALUES (1,'gateway-credential',2),(2,'gateway-payload',2)`,
		`INSERT INTO crypto_key_versions VALUES (1,2,'current'),(2,2,'current')`,
		`INSERT INTO gw_credentials VALUES (20,'active',NULL)`,
		`INSERT INTO gw_credential_versions VALUES (20,30)`,
		`INSERT INTO gw_deployment_members VALUES (10,1,'instance-a','api-worker'),(11,1,'instance-b','api-worker')`,
		`INSERT INTO crypto_key_readiness SELECT m.deployment_generation_id,m.id,k.id,k.current_version,o.operation,'ready','2099-01-02 00:00:00' FROM gw_deployment_members m CROSS JOIN crypto_keyring_state k CROSS JOIN (SELECT 'mac' AS operation UNION ALL SELECT 'wrap' UNION ALL SELECT 'unwrap' UNION ALL SELECT 'encrypt' UNION ALL SELECT 'decrypt') o`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return readinessTestDB{db}
}

func TestCryptoReadinessChecksEveryMemberAndKey(t *testing.T) {
	for _, test := range []struct {
		name, change string
		ready        bool
	}{
		{"complete", "", true},
		{"missing_payload_keyring", `DELETE FROM crypto_keyring_state WHERE id=2`, false},
		{"missing_current_key", `DELETE FROM crypto_key_versions WHERE keyring_id=2`, false},
		{"revoked_current_key", `UPDATE crypto_key_versions SET status='security_revoked' WHERE keyring_id=2`, false},
		{"operations_split_across_versions", `UPDATE crypto_key_readiness SET key_version=1 WHERE operation='decrypt'`, false},
		{"operations_split_across_keyrings", `UPDATE crypto_key_readiness SET keyring_id=3 WHERE operation='decrypt'`, false},
		{"missing_member_proof", `DELETE FROM crypto_key_readiness WHERE deployment_member_id=11`, false},
		{"different_generation", `UPDATE crypto_key_readiness SET deployment_generation_id=2`, false},
		{"expired_proof", `UPDATE crypto_key_readiness SET expires_at='2098-12-31 00:00:00' WHERE operation='mac'`, false},
		{"failed_proof", `UPDATE crypto_key_readiness SET status='failed' WHERE operation='mac'`, false},
		{"historical_key_unproved", `INSERT INTO crypto_key_versions VALUES (1,1,'readable')`, false},
		{"retired_key_not_required", `INSERT INTO crypto_key_versions VALUES (1,1,'retired')`, true},
		{"no_members", `DELETE FROM gw_deployment_members`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := cryptoFixture(t)
			if test.change != "" {
				if _, err := db.Exec(test.change); err != nil {
					t.Fatal(err)
				}
			}
			err := CheckCryptoReadiness(context.Background(), db, 1)
			if test.ready && err != nil || !test.ready && !errors.Is(err, ErrConflict) {
				t.Fatalf("ready=%v error=%v", test.ready, err)
			}
		})
	}
}

func TestCryptoReadinessAcceptsHistoricalReadOperations(t *testing.T) {
	db := cryptoFixture(t)
	for _, query := range []string{
		`INSERT INTO crypto_key_versions VALUES (1,1,'readable')`,
		`INSERT INTO crypto_key_readiness SELECT deployment_generation_id,deployment_member_id,keyring_id,1,operation,status,expires_at FROM crypto_key_readiness WHERE keyring_id=1 AND operation IN ('mac','unwrap','decrypt')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckCryptoReadiness(context.Background(), db, 1); err != nil {
		t.Fatal(err)
	}
}

func TestCryptoReadinessPlaintextCredentialsOnlyRequirePayloadKeys(t *testing.T) {
	db := cryptoFixture(t)
	for _, query := range []string{
		`UPDATE gw_credentials SET secret='plain-api-key' WHERE id=20`,
		`DELETE FROM crypto_keyring_state WHERE purpose='gateway-credential'`,
		`DELETE FROM crypto_key_readiness WHERE keyring_id=1`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	required, err := LegacyCredentialCryptoRequired(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if required {
		t.Fatal("credential crypto was required after a direct secret became available")
	}
	if err := CheckCryptoReadiness(context.Background(), db, 1); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeKeyReadinessDoesNotRequireDeploymentProofs(t *testing.T) {
	db := cryptoFixture(t)
	for _, query := range []string{
		`UPDATE gw_credentials SET secret='plain-api-key' WHERE id=20`,
		`DELETE FROM crypto_keyring_state WHERE purpose='gateway-credential'`,
		`DELETE FROM crypto_key_readiness`,
		`DELETE FROM gw_deployment_members`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckRuntimeKeyReadiness(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM crypto_keyring_state WHERE purpose='gateway-payload'`); err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeKeyReadiness(context.Background(), db); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing payload keyring error=%v, want ErrConflict", err)
	}
}

func TestLegacyCredentialCryptoRequiredByCredentialState(t *testing.T) {
	for _, test := range []struct {
		status   string
		required bool
	}{
		{status: "active", required: true},
		{status: "draining", required: true},
		{status: "disabled", required: false},
	} {
		t.Run(test.status, func(t *testing.T) {
			db := cryptoFixture(t)
			if _, err := db.Exec(`UPDATE gw_credentials SET status=? WHERE id=20`, test.status); err != nil {
				t.Fatal(err)
			}
			required, err := LegacyCredentialCryptoRequired(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			if required != test.required {
				t.Fatalf("required=%v, want %v", required, test.required)
			}
		})
	}
}
