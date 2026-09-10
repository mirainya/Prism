//go:build integration

package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/gateway/credentials"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func verifyCredentialAdministration(t *testing.T, db *sql.DB, store *repository.Store, poolID uint64) {
	t.Helper()
	ctx := context.Background()
	kek, hmacKey := bytes.Repeat([]byte{71}, 32), bytes.Repeat([]byte{72}, 32)
	err := store.WithTx(ctx, func(tx *sql.Tx) error { _, _, err := ensureKeyring(ctx, tx, time.Now().UTC()); return err })
	if err != nil {
		t.Fatal(err)
	}
	in := repository.ManagedCredentialInput{Code: "qa-key", Weight: 3, Secret: []byte("qa-secret-never-in-audit"), Purposes: []credentials.Purpose{credentials.PurposeExecution, credentials.PurposeCatalogDiscovery}}
	var id uint64
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = store.CreateManagedCredential(ctx, tx, poolID, in, kek, hmacKey, 1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var blobID, version, grants uint64
	if err := db.QueryRow(`SELECT v.encrypted_blob_id,c.config_version,(SELECT COUNT(*) FROM gw_credential_purpose_grants g WHERE g.credential_id=c.id AND g.status='active') FROM gw_credentials c JOIN gw_credential_versions v ON v.id=c.current_version_id WHERE c.id=?`, id).Scan(&blobID, &version, &grants); err != nil || version != 2 || grants != 2 {
		t.Fatalf("version=%d grants=%d error=%v", version, grants, err)
	}
	envelope, err := store.ReadEncryptedBlob(ctx, db, blobID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := repository.OpenBlob(envelope, blobID, []byte(fmt.Sprintf("credential:%d", id)), kek, hmacKey)
	if err != nil || !bytes.Equal(plain, in.Secret) || bytes.Contains(envelope.Ciphertext, in.Secret) {
		t.Fatal("encrypted credential round trip failed", err)
	}
	clear(plain)
	var audit string
	if err := db.QueryRow(`SELECT metadata FROM audit_events WHERE action='unified.credential.create' AND resource_id=?`, fmt.Sprint(id)).Scan(&audit); err != nil || strings.Contains(audit, string(in.Secret)) {
		t.Fatal("unsafe audit", err)
	}
	in.Code = "qa-duplicate"
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := store.CreateManagedCredential(ctx, tx, poolID, in, kek, hmacKey, 1)
		return err
	})
	if !errors.Is(err, repository.ErrDuplicateCredentialSecret) {
		t.Fatalf("duplicate key not rejected: %v", err)
	}
	in.Secret = []byte("qa-second-secret")
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := store.CreateManagedCredential(ctx, tx, poolID, in, bytes.Repeat([]byte{73}, 32), hmacKey, 1)
		return err
	})
	if !errors.Is(err, repository.ErrCredentialEncryptionUnavailable) {
		t.Fatalf("incorrect encryption key accepted: %v", err)
	}
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := store.CreateManagedCredential(ctx, tx, poolID, in, kek, bytes.Repeat([]byte{74}, 32), 1)
		return err
	})
	if !errors.Is(err, repository.ErrCredentialEncryptionUnavailable) {
		t.Fatalf("incorrect fingerprint key accepted: %v", err)
	}
	limit := uint64(2)
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpdateManagedCredential(ctx, tx, id, repository.CredentialUpdate{Weight: 5, RequestLimit: &limit, ExpectedVersion: 2}, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpdateManagedCredential(ctx, tx, id, repository.CredentialUpdate{Weight: 1, ExpectedVersion: 2}, 1)
	})
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("stale version accepted: %v", err)
	}
	for _, states := range [][2]credentials.CredentialState{{credentials.CredentialActive, credentials.CredentialDraining}, {credentials.CredentialDraining, credentials.CredentialDisabled}} {
		err = store.WithTx(ctx, func(tx *sql.Tx) error {
			return store.TransitionCredential(ctx, tx, id, states[0], states[1], "admin:1")
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpdateManagedCredential(ctx, tx, id, repository.CredentialUpdate{Weight: 2, ExpectedVersion: 5}, 1)
	})
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("disabled credential editable: %v", err)
	}
	var count uint64
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_credentials WHERE credential_pool_id=?`, poolID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed creates leaked rows: count=%d err=%v", count, err)
	}
}
