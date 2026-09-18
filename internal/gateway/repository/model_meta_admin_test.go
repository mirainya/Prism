package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

func modelMetaDB(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, query := range []string{
		`CREATE TABLE gw_model_meta(model_name TEXT PRIMARY KEY, display_name TEXT, group_name TEXT, sort INTEGER, status INTEGER, config_version INTEGER, updated_at TEXT)`,
		`CREATE TABLE audit_events(id INTEGER PRIMARY KEY AUTOINCREMENT, actor_type TEXT, actor_user_id INTEGER, action TEXT, resource_type TEXT, resource_id TEXT, outcome TEXT, http_status INTEGER, metadata TEXT, created_at TEXT)`,
		`INSERT INTO gw_model_meta VALUES ('doubao-seed-1.6','豆包 Seed 1.6','火山',10,1,3,'t')`,
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

func TestModelMetaUpdateRewritesPresentationAndAudits(t *testing.T) {
	store := modelMetaDB(t)
	ctx := context.Background()
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpdateModelMeta(ctx, tx, "doubao-seed-1.6", ModelMetaUpdate{
			DisplayName: "Doubao Seed 1.6", GroupName: "", Sort: 20, Status: 0, ExpectedVersion: 3,
		}, 7)
	})
	if err != nil {
		t.Fatalf("renaming a model failed: %v", err)
	}
	var name, group string
	var sortOrder, status int
	var version uint64
	if err := store.DB().QueryRow(`SELECT display_name,group_name,sort,status,config_version FROM gw_model_meta WHERE model_name='doubao-seed-1.6'`).Scan(&name, &group, &sortOrder, &status, &version); err != nil {
		t.Fatal(err)
	}
	// An empty group is a real value meaning "group by source channel", so it must
	// be written rather than treated as "leave alone".
	if name != "Doubao Seed 1.6" || group != "" || sortOrder != 20 || status != 0 || version != 4 {
		t.Fatalf("row=%q/%q sort=%d status=%d version=%d", name, group, sortOrder, status, version)
	}
	// The whole row is rewritten, so the audit entry has to carry what it replaced
	// or the change cannot be reviewed after the fact.
	var metadata string
	if err := store.DB().QueryRow(`SELECT metadata FROM audit_events WHERE action='unified.model_meta.update' AND resource_id='doubao-seed-1.6'`).Scan(&metadata); err != nil {
		t.Fatalf("the change was not audited: %v", err)
	}
	for _, fragment := range []string{`"before"`, `"豆包 Seed 1.6"`, `"火山"`, `"after"`, `"Doubao Seed 1.6"`} {
		if !strings.Contains(metadata, fragment) {
			t.Fatalf("audit metadata %s is missing %s", metadata, fragment)
		}
	}
}

// Saving the form unchanged is accepted, unlike a runtime-state transition: this
// write appends to no event log, so there is no version being spent on a row that
// records nothing.
func TestModelMetaUpdateAcceptsUnchangedRow(t *testing.T) {
	store := modelMetaDB(t)
	ctx := context.Background()
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpdateModelMeta(ctx, tx, "doubao-seed-1.6", ModelMetaUpdate{
			DisplayName: "豆包 Seed 1.6", GroupName: "火山", Sort: 10, Status: 1, ExpectedVersion: 3,
		}, 7)
	})
	if err != nil {
		t.Fatalf("an unchanged save was rejected: %v", err)
	}
	var version uint64
	if err := store.DB().QueryRow(`SELECT config_version FROM gw_model_meta WHERE model_name='doubao-seed-1.6'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	// The version still advances, so a concurrent editor's stale token is refused.
	if version != 4 {
		t.Fatalf("version=%d, want 4", version)
	}
}

func TestModelMetaUpdateRejectsBadInput(t *testing.T) {
	for _, test := range []struct {
		name  string
		model string
		in    ModelMetaUpdate
		want  error
	}{
		{"no_expected_version", "doubao-seed-1.6", ModelMetaUpdate{DisplayName: "X", Status: 1}, ErrInvalidInput},
		{"empty_display_name", "doubao-seed-1.6", ModelMetaUpdate{Status: 1, ExpectedVersion: 3}, ErrInvalidInput},
		{"padded_display_name", "doubao-seed-1.6", ModelMetaUpdate{DisplayName: " X ", Status: 1, ExpectedVersion: 3}, ErrInvalidInput},
		{"newline_display_name", "doubao-seed-1.6", ModelMetaUpdate{DisplayName: "X\nY", Status: 1, ExpectedVersion: 3}, ErrInvalidInput},
		{"oversized_display_name", "doubao-seed-1.6", ModelMetaUpdate{DisplayName: strings.Repeat("名", 101), Status: 1, ExpectedVersion: 3}, ErrInvalidInput},
		{"oversized_group", "doubao-seed-1.6", ModelMetaUpdate{DisplayName: "X", GroupName: strings.Repeat("组", 81), Status: 1, ExpectedVersion: 3}, ErrInvalidInput},
		{"padded_group", "doubao-seed-1.6", ModelMetaUpdate{DisplayName: "X", GroupName: " 火山", Status: 1, ExpectedVersion: 3}, ErrInvalidInput},
		{"unknown_status", "doubao-seed-1.6", ModelMetaUpdate{DisplayName: "X", Status: 2, ExpectedVersion: 3}, ErrInvalidInput},
		{"negative_sort", "doubao-seed-1.6", ModelMetaUpdate{DisplayName: "X", Sort: -1, Status: 1, ExpectedVersion: 3}, ErrInvalidInput},
		{"empty_model", "", ModelMetaUpdate{DisplayName: "X", Status: 1, ExpectedVersion: 3}, ErrInvalidInput},
		{"injected_model", "a' OR 1=1", ModelMetaUpdate{DisplayName: "X", Status: 1, ExpectedVersion: 3}, ErrInvalidInput},
		{"stale_version", "doubao-seed-1.6", ModelMetaUpdate{DisplayName: "X", Status: 1, ExpectedVersion: 1}, ErrConflict},
		{"missing_model", "gpt-5", ModelMetaUpdate{DisplayName: "X", Status: 1, ExpectedVersion: 3}, ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := modelMetaDB(t)
			ctx := context.Background()
			err := store.WithTx(ctx, func(tx *sql.Tx) error {
				return store.UpdateModelMeta(ctx, tx, test.model, test.in, 7)
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v, want %v", err, test.want)
			}
			var name string
			var version uint64
			if err := store.DB().QueryRow(`SELECT display_name,config_version FROM gw_model_meta WHERE model_name='doubao-seed-1.6'`).Scan(&name, &version); err != nil {
				t.Fatal(err)
			}
			if name != "豆包 Seed 1.6" || version != 3 {
				t.Fatalf("a rejected change left name=%q version=%d", name, version)
			}
		})
	}
}

func TestModelMetaUpdateRequiresActor(t *testing.T) {
	store := modelMetaDB(t)
	ctx := context.Background()
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpdateModelMeta(ctx, tx, "doubao-seed-1.6", ModelMetaUpdate{DisplayName: "X", Status: 1, ExpectedVersion: 3}, 0)
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("an unattributed change was accepted: %v", err)
	}
}
