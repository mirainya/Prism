//go:build integration

package migrate

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/credentials"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func verifyChannelAdministration(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	var channelID, poolID uint64
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		channelID, err = store.CreateChannel(ctx, tx, repository.ChannelInput{Code: "qa-provider", Name: "QA Provider"}, 1)
		if err != nil {
			return err
		}
		poolID, err = store.CreateManagedCredentialPool(ctx, tx, repository.PoolInput{ChannelID: channelID, PoolCode: "qa-pool", DisplayName: "QA Pool"}, 1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		limit := uint64(1)
		return store.UpdateCredentialPool(ctx, tx, poolID, repository.PoolUpdate{Name: "Pool updated", TaskLimit: &limit, ExpectedVersion: 1}, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpdateCredentialPool(ctx, tx, poolID, repository.PoolUpdate{Name: "Stale", ExpectedVersion: 1}, 1)
	})
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	verifyCredentialAdministration(t, db, store, poolID)
	for _, transition := range [][2]credentials.PoolState{{credentials.PoolActive, credentials.PoolDraining}, {credentials.PoolDraining, credentials.PoolDisabled}} {
		err = store.WithTx(ctx, func(tx *sql.Tx) error {
			return store.TransitionCredentialPool(ctx, tx, poolID, transition[0], transition[1], "qa-admin")
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpdateChannel(ctx, tx, channelID, repository.ChannelUpdate{Name: "QA renamed", Status: "disabled", ExpectedName: "QA Provider", ExpectedStatus: "active"}, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := store.CreateManagedCredentialPool(ctx, tx, repository.PoolInput{ChannelID: channelID, PoolCode: "denied", DisplayName: "Denied"}, 1)
		return err
	})
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("disabled channel accepts a new pool: %v", err)
	}
	var auditCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action LIKE 'unified.%'`).Scan(&auditCount); err != nil || auditCount != 6 {
		t.Fatalf("audit=%d error=%v", auditCount, err)
	}
}
