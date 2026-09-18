package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

// runtimeStateDB is the minimum an A-class runtime-state change touches: the
// offering it points at, the state row it locks, the event log it appends to and
// the audit trail every admin change writes.
func runtimeStateDB(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, query := range []string{
		`CREATE TABLE gw_offerings(id INTEGER PRIMARY KEY, release_id INTEGER)`,
		`CREATE TABLE gw_offering_runtime_state(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, offering_id INTEGER, state TEXT, state_version INTEGER, reason_code TEXT, updated_at TEXT)`,
		`CREATE TABLE gw_offering_state_events(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER, offering_id INTEGER, state_version INTEGER, old_state TEXT, new_state TEXT, reason_code TEXT, created_at TEXT)`,
		`CREATE TABLE audit_events(id INTEGER PRIMARY KEY AUTOINCREMENT, actor_type TEXT, actor_user_id INTEGER, action TEXT, resource_type TEXT, resource_id TEXT, outcome TEXT, http_status INTEGER, metadata TEXT, created_at TEXT)`,
		`INSERT INTO gw_offerings VALUES (1201,1),(1202,1)`,
		`INSERT INTO gw_offering_runtime_state(release_id,offering_id,state,state_version,reason_code,updated_at) VALUES (1,1201,'active',1,'admin_create','t')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("fixture %q: %v", query, err)
		}
	}
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// TestOfferingRuntimeStateChangeIsRecorded covers the A-class contract: the state
// moves in place, the version advances, and the transition is appended to the
// event log and the audit trail in the same transaction.
func TestOfferingRuntimeStateChangeIsRecorded(t *testing.T) {
	store := runtimeStateDB(t)
	ctx := context.Background()
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.SetOfferingRuntimeState(ctx, tx, 1201, OfferingRuntimeStateInput{
			State: "draining", ReasonCode: "operator_drain", ExpectedVersion: 1,
		}, 7)
	})
	if err != nil {
		t.Fatalf("draining an active offering failed: %v", err)
	}
	var state, reason string
	var version uint64
	if err := store.DB().QueryRow(`SELECT state,state_version,reason_code FROM gw_offering_runtime_state WHERE offering_id=1201`).Scan(&state, &version, &reason); err != nil {
		t.Fatal(err)
	}
	if state != "draining" || version != 2 || reason != "operator_drain" {
		t.Fatalf("runtime state=%s version=%d reason=%s", state, version, reason)
	}
	var old, next string
	if err := store.DB().QueryRow(`SELECT old_state,new_state FROM gw_offering_state_events WHERE offering_id=1201 AND state_version=2`).Scan(&old, &next); err != nil {
		t.Fatalf("the transition was not appended to the event log: %v", err)
	}
	if old != "active" || next != "draining" {
		t.Fatalf("event records %s -> %s", old, next)
	}
	var audits uint64
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='unified.offering.runtime_state' AND resource_id='1201'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("audit rows=%d, want 1", audits)
	}
}

// TestOfferingRuntimeStateRecovers is why this transition table is not one-way
// like credentials and pools: an offering disabled by mistake has to come back,
// and cancelling a drain is an ordinary operator action.
func TestOfferingRuntimeStateRecovers(t *testing.T) {
	store := runtimeStateDB(t)
	ctx := context.Background()
	for version, step := range []struct{ state, reason string }{
		{"disabled", "upstream_outage"},
		{"active", "upstream_recovered"},
		{"draining", "operator_drain"},
		{"active", "drain_cancelled"},
	} {
		err := store.WithTx(ctx, func(tx *sql.Tx) error {
			return store.SetOfferingRuntimeState(ctx, tx, 1201, OfferingRuntimeStateInput{
				State: step.state, ReasonCode: step.reason, ExpectedVersion: uint64(version) + 1,
			}, 7)
		})
		if err != nil {
			t.Fatalf("step %d (%s) failed: %v", version, step.state, err)
		}
	}
	var events uint64
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM gw_offering_state_events WHERE offering_id=1201`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	// One row per transition, none skipped and none written twice.
	if events != 4 {
		t.Fatalf("state events=%d, want 4", events)
	}
}

func TestOfferingRuntimeStateRejectsBadChanges(t *testing.T) {
	for _, test := range []struct {
		name     string
		offering uint64
		in       OfferingRuntimeStateInput
		want     error
	}{
		{"unknown_state", 1201, OfferingRuntimeStateInput{State: "paused", ReasonCode: "x", ExpectedVersion: 1}, ErrInvalidInput},
		{"no_reason", 1201, OfferingRuntimeStateInput{State: "disabled", ExpectedVersion: 1}, ErrInvalidInput},
		{"no_expected_version", 1201, OfferingRuntimeStateInput{State: "disabled", ReasonCode: "x"}, ErrInvalidInput},
		{"stale_version", 1201, OfferingRuntimeStateInput{State: "disabled", ReasonCode: "x", ExpectedVersion: 9}, ErrConflict},
		// Rewriting the current state would spend a state_version on a row that
		// records no change.
		{"same_state", 1201, OfferingRuntimeStateInput{State: "active", ReasonCode: "x", ExpectedVersion: 1}, ErrConflict},
		{"missing_offering", 4242, OfferingRuntimeStateInput{State: "disabled", ReasonCode: "x", ExpectedVersion: 1}, ErrNotFound},
		// The offering exists but has no runtime state row at all.
		{"no_runtime_row", 1202, OfferingRuntimeStateInput{State: "disabled", ReasonCode: "x", ExpectedVersion: 1}, ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := runtimeStateDB(t)
			ctx := context.Background()
			err := store.WithTx(ctx, func(tx *sql.Tx) error {
				return store.SetOfferingRuntimeState(ctx, tx, test.offering, test.in, 7)
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v, want %v", err, test.want)
			}
			// A rejected change must leave the offering exactly as it was.
			var state string
			var version uint64
			if err := store.DB().QueryRow(`SELECT state,state_version FROM gw_offering_runtime_state WHERE offering_id=1201`).Scan(&state, &version); err != nil {
				t.Fatal(err)
			}
			if state != "active" || version != 1 {
				t.Fatalf("a rejected change moved the offering to %s at version %d", state, version)
			}
		})
	}
}
