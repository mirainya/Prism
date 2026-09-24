package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/execution"
)

func TestScheduleAsyncQueryRejectsLateNonTerminalObservation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	err = store.ScheduleAsyncQuery(
		context.Background(),
		tx,
		41,
		execution.AsyncTerminatedUnknown,
		execution.AsyncRunning,
		7,
		time.Now().UTC().Add(time.Second),
	)
	if err != ErrConflict {
		t.Fatalf("err=%v, want %v", err, ErrConflict)
	}
	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
