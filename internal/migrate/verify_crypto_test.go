package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	"github.com/mirainya/Prism/internal/gateway/security"
)

func TestVerifyEncryptedCredentialsChecksMissingCurrentWrap(t *testing.T) {
	for _, test := range []struct {
		name, change, want string
	}{
		{"valid", "", ""},
		{"missing_wrap", `DELETE FROM encrypted_blob_key_wraps WHERE encrypted_blob_id=2 AND kek_version=2`, "credential version 2 is missing"},
		{"missing_blob", `DELETE FROM encrypted_blobs WHERE id=2`, "credential version 2 is missing"},
		{"missing_keyring", `DELETE FROM crypto_keyring_state`, "credential version 1 is missing"},
		{"wrong_owner", `UPDATE gw_credential_versions SET credential_id=3 WHERE id=2`, "credential version 2 crypto verification failed"},
		{"corrupt_ciphertext", `UPDATE encrypted_blobs SET ciphertext=X'00' WHERE id=2`, "credential version 2 crypto verification failed"},
		{"empty_database", `DELETE FROM gw_credential_versions`, "no encrypted credential versions found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			t.Cleanup(func() { db.Close() })
			for _, query := range []string{
				`CREATE TABLE gw_credential_versions(id INTEGER, credential_id INTEGER, encrypted_blob_id INTEGER)`,
				`CREATE TABLE encrypted_blobs(id INTEGER, keyring_id INTEGER, nonce BLOB, ciphertext BLOB)`,
				`CREATE TABLE crypto_keyring_state(id INTEGER, current_version INTEGER)`,
				`CREATE TABLE encrypted_blob_key_wraps(encrypted_blob_id INTEGER, keyring_id INTEGER, kek_version INTEGER, wrap_nonce BLOB, wrapped_dek BLOB)`,
				`INSERT INTO crypto_keyring_state VALUES (1,2)`,
			} {
				if _, err := db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			kek := bytes.Repeat([]byte{7}, security.KeySize)
			for id := uint64(1); id <= 2; id++ {
				aad, err := security.CanonicalAAD(id, "credential", 1, []byte(fmt.Sprintf("credential:%d", id)))
				if err != nil {
					t.Fatal(err)
				}
				envelope, err := security.Seal([]byte("test-secret"), aad, kek, 2)
				if err != nil {
					t.Fatal(err)
				}
				for _, statement := range []struct {
					query string
					args  []any
				}{
					{`INSERT INTO gw_credential_versions VALUES (?,?,?)`, []any{id, id, id}},
					{`INSERT INTO encrypted_blobs VALUES (?,1,?,?)`, []any{id, envelope.Nonce, envelope.Ciphertext}},
					{`INSERT INTO encrypted_blob_key_wraps VALUES (?,1,2,?,?)`, []any{id, envelope.WrapNonce, envelope.WrappedDEK}},
					{`INSERT INTO encrypted_blob_key_wraps VALUES (?,1,1,X'00',X'00')`, []any{id}},
				} {
					if _, err := db.Exec(statement.query, statement.args...); err != nil {
						t.Fatal(err)
					}
				}
			}
			if test.change != "" {
				if _, err := db.Exec(test.change); err != nil {
					t.Fatal(err)
				}
			}
			err = VerifyEncryptedCredentials(context.Background(), db, kek)
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
}
