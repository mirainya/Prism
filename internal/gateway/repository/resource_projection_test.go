package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestUpdateAsyncResourceProjectionVideo(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT r.id,r.call_id,r.user_id,r.token_id,r.public_id,r.resource_kind").
		WithArgs(uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_id", "user_id", "token_id", "public_id", "resource_kind"}).AddRow(7, 8, 9, 10, "public-video", "video_task"))
	mock.ExpectQuery("SELECT status,progress FROM gw_video_tasks").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "progress"}).AddRow("running", 25))
	mock.ExpectExec("UPDATE gw_video_tasks SET status=.*WHERE resource_id=\\?").
		WithArgs("succeeded", uint8(100), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.UpdateAsyncResourceProjection(context.Background(), tx, 42, "succeeded", 100, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateAsyncResourceProjectionRejectsRegression(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT r.id,r.call_id,r.user_id,r.token_id,r.public_id,r.resource_kind").
		WithArgs(uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_id", "user_id", "token_id", "public_id", "resource_kind"}).AddRow(7, 8, 9, 10, "public-video", "video_task"))
	mock.ExpectQuery("SELECT status,progress FROM gw_video_tasks").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "progress"}).AddRow("succeeded", 100))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.UpdateAsyncResourceProjection(context.Background(), tx, 42, "running", 50, nil)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateAsyncResourceProjectionResolvesTerminatedUnknown(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT r.id,r.call_id,r.user_id,r.token_id,r.public_id,r.resource_kind").
		WithArgs(uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_id", "user_id", "token_id", "public_id", "resource_kind"}).AddRow(7, 8, 9, 10, "public-video", "video_task"))
	mock.ExpectQuery("SELECT status,progress FROM gw_video_tasks").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "progress"}).AddRow("terminated_unknown", 0))
	mock.ExpectExec("UPDATE gw_video_tasks SET status=.*WHERE resource_id=\\?").
		WithArgs("completed", uint8(100), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.UpdateAsyncResourceProjection(context.Background(), tx, 42, "completed", 100, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateAsyncResourceProjectionDoesNotReopenTerminatedUnknown(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT r.id,r.call_id,r.user_id,r.token_id,r.public_id,r.resource_kind").
		WithArgs(uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_id", "user_id", "token_id", "public_id", "resource_kind"}).AddRow(7, 8, 9, 10, "public-video", "video_task"))
	mock.ExpectQuery("SELECT status,progress FROM gw_video_tasks").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "progress"}).AddRow("terminated_unknown", 0))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.UpdateAsyncResourceProjection(context.Background(), tx, 42, "tracking", 0, nil)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateAsyncResourceProjectionResponseIsNoop(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT r.id,r.call_id,r.user_id,r.token_id,r.public_id,r.resource_kind").
		WithArgs(uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_id", "user_id", "token_id", "public_id", "resource_kind"}).AddRow(7, 8, 9, 10, "public-response", "response"))
	mock.ExpectCommit()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.UpdateAsyncResourceProjection(context.Background(), tx, 42, "succeeded", 100, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
