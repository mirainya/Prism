package runtime

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

func TestRequireConfiguredReadinessRejectsNilDatabase(t *testing.T) {
	if !errors.Is(RequireConfiguredReadiness(context.Background(), nil), ErrNotReady) {
		t.Fatal("nil database must not be considered ready")
	}
}

func TestRequireConfiguredReadinessDoesNotReadLegacyRoutingTables(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE gw_catalog_runtime_state(id INTEGER PRIMARY KEY,active_release_id INTEGER NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO gw_catalog_runtime_state(id,active_release_id) VALUES (1,NULL)`); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(RequireConfiguredReadiness(context.Background(), db), ErrNotReady) {
		t.Fatal("an inactive unified catalog must keep the data plane disabled")
	}
}

func TestReadinessGateWatchFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "proof expired", err: errors.New("catalog readiness proof expired")},
		{name: "generation retired", err: errors.New("deployment generation retired")},
		{name: "database unavailable", err: errors.New("database unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := NewReadinessGate(true)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := gate.Watch(ctx, time.Millisecond, func(context.Context) error { return tc.err })
			if !errors.Is(err, ErrNotReady) {
				t.Fatalf("Watch() error = %v, want ErrNotReady", err)
			}
			if gate.Ready() || !errors.Is(gate.Require(context.Background()), ErrNotReady) {
				t.Fatal("failed readiness probe did not permanently disable the process gate")
			}
		})
	}
}

func TestReadinessGateCancellationDoesNotChangeReadyState(t *testing.T) {
	gate := NewReadinessGate(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gate.Watch(ctx, time.Hour, func(context.Context) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Watch() error = %v, want context.Canceled", err)
	}
	if !gate.Ready() {
		t.Fatal("normal shutdown disabled a ready process gate")
	}
}
