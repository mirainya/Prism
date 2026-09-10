package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestResolveCurrentBillingContextUsesLatestEffectiveActivation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT id FROM billing_accounts").
		WithArgs(uint64(5), "CREDIT", uint32(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectQuery("SELECT a.id.*token_budget_policy_activations").
		WithArgs(uint64(7), uint64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(11))
	mock.ExpectQuery("SELECT id FROM token_budget_windows").
		WithArgs(uint64(7), uint64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(13))

	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	accountID, windowID, err := store.ResolveCurrentBillingContext(context.Background(), tx, 5, 7, "CREDIT", 1)
	if err != nil || accountID != 9 || windowID != 13 {
		t.Fatalf("account=%d window=%d err=%v", accountID, windowID, err)
	}
	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCurrentBillingContextRejectsOverlappingWindows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT id FROM billing_accounts").
		WithArgs(uint64(5), "CREDIT", uint32(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectQuery("SELECT a.id.*token_budget_policy_activations").
		WithArgs(uint64(7), uint64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(11))
	mock.ExpectQuery("SELECT id FROM token_budget_windows").
		WithArgs(uint64(7), uint64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(13).AddRow(14))

	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.ResolveCurrentBillingContext(context.Background(), tx, 5, 7, "CREDIT", 1)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v", err)
	}
	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
