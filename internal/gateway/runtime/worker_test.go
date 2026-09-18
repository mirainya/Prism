package runtime

import (
	"context"
	"database/sql"
	"errors"
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

func TestReadyWorkerClaimDoesNotReadDeploymentPointer(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewWithReadiness(store, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,call_id,attempt_id,async_execution_id").WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	worked, err := service.ProcessOne(context.Background(), "worker", time.Minute, time.Second, outboxDispatcherFunc{})
	if err != nil || worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
