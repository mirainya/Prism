package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestReadVideoRoutePolicyReturnsPinnedCatalogPolicy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(`SELECT s\.delivery_mode,s\.idempotency_mode,pt\.task_scope,pt\.cancel_mode,pt\.source_url_policy,pt\.upstream_scope_kind,pt\.upstream_scope_key,ai\.adapter_code,ai\.contract_version,COALESCE\(p\.capability_constraints,'\{\}'\),s\.service_tiers`).
		WithArgs(uint64(17), uint64(23), uint64(17), uint64(29)).
		WillReturnRows(sqlmock.NewRows([]string{
			"delivery_mode", "idempotency_mode", "task_scope", "cancel_mode", "source_url_policy",
			"upstream_scope_kind", "upstream_scope_key", "adapter_code", "contract_version",
			"capability_constraints", "service_tiers",
		}).AddRow(
			"reference", "optional", "task", "none", "fixed",
			"credential", "video-generation", "generic", 1,
			[]byte(`{"adapter":{"profile":"json_task_v1"}}`), []byte(`["standard","priority"]`),
		))

	policy, err := store.ReadVideoRoutePolicy(context.Background(), 17, 23, 29)
	if err != nil {
		t.Fatal(err)
	}
	if policy.AdapterCode != "generic" || policy.AdapterVersion != 1 || policy.DeliveryMode != "reference" ||
		policy.IdempotencyMode != "optional" || policy.TaskScope != "task" || policy.CancelMode != "none" ||
		policy.SourceURLPolicy != "fixed" || policy.UpstreamScopeKind != "credential" ||
		policy.UpstreamScopeKey != "video-generation" || len(policy.ServiceTiers) != 2 ||
		policy.ServiceTiers[0] != "standard" || policy.ServiceTiers[1] != "priority" {
		t.Fatalf("policy = %+v", policy)
	}
	if string(policy.AdapterConfig) != `{"adapter":{"profile":"json_task_v1"}}` {
		t.Fatalf("adapter config = %s", policy.AdapterConfig)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReadVideoRoutePolicyRejectsInvalidCatalogJSON(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(`SELECT s\.delivery_mode`).
		WithArgs(uint64(17), uint64(23), uint64(17), uint64(29)).
		WillReturnRows(sqlmock.NewRows([]string{
			"delivery_mode", "idempotency_mode", "task_scope", "cancel_mode", "source_url_policy",
			"upstream_scope_kind", "upstream_scope_key", "adapter_code", "contract_version",
			"capability_constraints", "service_tiers",
		}).AddRow(
			"reference", "optional", "task", "none", "fixed",
			"credential", "video-generation", "generic", 1, []byte(`{`), []byte(`["standard"]`),
		))

	_, err = store.ReadVideoRoutePolicy(context.Background(), 17, 23, 29)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
