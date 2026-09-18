package runtime

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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

func TestConfiguredReleaseDoesNotRequireDeploymentGeneration(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE gw_catalog_runtime_state(id INTEGER PRIMARY KEY,active_release_id INTEGER NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO gw_catalog_runtime_state(id,active_release_id) VALUES (1,7)`); err != nil {
		t.Fatal(err)
	}
	releaseID, err := configuredReleaseID(context.Background(), db)
	if err != nil || releaseID != 7 {
		t.Fatalf("release=%d error=%v", releaseID, err)
	}
}

func TestRuntimeKeyMaterialRequiresPayloadAndConditionalLegacyKeys(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(make([]byte, 32))
	for _, name := range []string{
		"PRISM_GATEWAY_PAYLOAD_KEK_B64",
		"PRISM_GATEWAY_PAYLOAD_HMAC_B64",
		"PRISM_GATEWAY_KEK_B64",
		"PRISM_GATEWAY_HMAC_B64",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("PRISM_GATEWAY_PAYLOAD_KEK_B64", encoded)
	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64", encoded)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"required"}).AddRow(false))
	if err := checkRuntimeKeyMaterial(context.Background(), db); err != nil {
		t.Fatalf("plaintext credentials unexpectedly required legacy keys: %v", err)
	}
	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"required"}).AddRow(true))
	if err := checkRuntimeKeyMaterial(context.Background(), db); err == nil {
		t.Fatal("legacy encrypted credentials were accepted without credential keys")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReadinessGateWatchFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "catalog invalid", err: errors.New("catalog validation failed")},
		{name: "payload key unavailable", err: errors.New("payload key unavailable")},
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
