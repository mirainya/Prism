package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

type outboxDispatcherFunc struct{}

func (outboxDispatcherFunc) Dispatch(context.Context, repository.OutboxItem) error { return nil }
func (outboxDispatcherFunc) Recover(context.Context, repository.OutboxItem) error  { return nil }

type attemptDispatcherFunc struct{}

func (attemptDispatcherFunc) DispatchAttempt(context.Context, repository.OutboxItem) error {
	return nil
}
func (attemptDispatcherFunc) RecoverAttempt(context.Context, repository.OutboxItem) error {
	return nil
}

func TestOutboxRetryDelayRemainsBoundedWithoutAbandoningWork(t *testing.T) {
	for _, tc := range []struct {
		attempt uint64
		want    time.Duration
	}{{1, time.Second}, {2, 2 * time.Second}, {9, 256 * time.Second}, {10, 5 * time.Minute}, {1000000, 5 * time.Minute}} {
		if got := outboxRetryDelay(time.Second, tc.attempt); got != tc.want {
			t.Errorf("attempt=%d delay=%s want=%s", tc.attempt, got, tc.want)
		}
	}
}

func TestDisabledReadinessPreventsEveryOutboxClaim(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	gate := NewReadinessGate(false)
	service, err := NewWithReadiness(store, gate.Require)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	assertRejected := func(name string, worked bool, err error) {
		t.Helper()
		if worked || !errors.Is(err, ErrNotReady) {
			t.Fatalf("%s: worked=%v err=%v, want false/ErrNotReady", name, worked, err)
		}
	}

	worked, err := service.ProcessOne(ctx, "async", time.Minute, time.Second, outboxDispatcherFunc{})
	assertRejected("async", worked, err)
	worked, err = service.ProcessAttemptOne(ctx, "response", attemptDispatcherFunc{})
	assertRejected("response", worked, err)
	worked, err = service.ProcessCallbackOne(ctx, "provider-callback", time.Minute)
	assertRejected("provider callback", worked, err)
	worked, err = service.ProcessDeliveryOne(ctx, "delivery", time.Minute, time.Second, deliveryReconcilerFunc(func(context.Context, repository.OutboxItem) error { return nil }))
	assertRejected("delivery", worked, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("a disabled worker reached its claim transaction: %v", err)
	}
}

func TestDeploymentPointerFencePreventsWorkerClaim(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	identity := repository.DeploymentIdentity{InstanceID: "test-instance", Role: "api-worker", AdapterDigest: strings.Repeat("a", 64)}
	service, err := NewWithDeploymentIdentity(store, func(context.Context) error { return nil }, identity)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT active_release_id,active_deployment_generation_id FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"active_release_id", "active_deployment_generation_id"}).AddRow(int64(7), nil))
	mock.ExpectRollback()
	worked, err := service.ProcessOne(context.Background(), "worker", time.Minute, time.Second, outboxDispatcherFunc{})
	if worked || !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("worked=%v err=%v, want false/ErrConflict", worked, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
