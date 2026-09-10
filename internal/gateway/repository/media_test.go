package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAddMediaAssetRefRejectsNonInputAssetForCall(t *testing.T) {
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
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	callID := uint64(41)
	mock.ExpectQuery("SELECT user_id,token_id FROM gw_api_calls").WithArgs(callID).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id"}).AddRow(2, 7))
	mock.ExpectQuery("SELECT user_id,token_id,purpose,state,retention_until FROM gw_media_assets").WithArgs(uint64(19)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id", "purpose", "state", "retention_until"}).AddRow(2, 7, "result", "active", nil))

	_, err = store.AddMediaAssetRef(context.Background(), tx, MediaAssetRefInput{
		MediaAssetID: 19, UserID: 2, TokenID: 7, Role: "input", CallID: &callID,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v", err)
	}
	mock.ExpectRollback()
	if rollbackErr := tx.Rollback(); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
