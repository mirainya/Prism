package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCallLeaseUsesAttemptAndExpiryAsFence(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_api_calls\nSET lease_owner=?,lease_expires_at=?,next_action_at=NULL,updated_at=?")).
		WithArgs("worker-1", sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7), uint64(11)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	lease, err := store.ClaimCallLease(context.Background(), tx, 7, 11, "worker-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease.CallID != 7 || lease.AttemptID != 11 || lease.Owner != "worker-1" || lease.ExpiresAt.IsZero() {
		t.Fatalf("lease=%+v", lease)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM gw_api_calls")).
		WithArgs(uint64(7), uint64(11), "worker-1", lease.ExpiresAt).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	if err := store.AssertCallLease(context.Background(), tx, lease); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_api_calls SET lease_owner='',lease_expires_at=NULL,updated_at=?")).
		WithArgs(sqlmock.AnyArg(), uint64(7), "worker-1", lease.ExpiresAt, uint64(11)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.ReleaseCallLease(context.Background(), tx, lease); err != nil {
		t.Fatal(err)
	}
	mock.ExpectCommit()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAssertCallLeaseRejectsReclaimedGeneration(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	lease := CallLease{CallID: 7, AttemptID: 11, Owner: "worker-1", ExpiresAt: time.Now().UTC().Add(time.Minute).Truncate(time.Millisecond)}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM gw_api_calls")).
		WithArgs(uint64(7), uint64(11), "worker-1", lease.ExpiresAt).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if err := store.AssertCallLease(context.Background(), tx, lease); !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}
	mock.ExpectRollback()
	_ = tx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
