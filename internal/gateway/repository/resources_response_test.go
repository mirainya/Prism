package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestUpdateAIResponsePreservesFullPayloadBoundary(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT resource_kind FROM gw_api_resources WHERE id=? FOR UPDATE")).
		WithArgs(uint64(4)).WillReturnRows(sqlmock.NewRows([]string{"resource_kind"}).AddRow("response"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status FROM gw_ai_responses WHERE resource_id=? FOR UPDATE")).
		WithArgs(uint64(4)).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("in_progress"))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_ai_responses SET status=?,result_summary=COALESCE(?,result_summary),updated_at=? WHERE resource_id=?")).
		WithArgs("completed", `{"output_items":1}`, sqlmock.AnyArg(), uint64(4)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.UpdateAIResponse(context.Background(), tx, 4, "completed", map[string]any{"output_items": 1})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateAIResponseRejectsTerminalRegression(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT resource_kind FROM gw_api_resources WHERE id=? FOR UPDATE")).
		WithArgs(uint64(4)).WillReturnRows(sqlmock.NewRows([]string{"resource_kind"}).AddRow("response"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status FROM gw_ai_responses WHERE resource_id=? FOR UPDATE")).
		WithArgs(uint64(4)).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("completed"))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.UpdateAIResponse(context.Background(), tx, 4, "in_progress", nil)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAIResponseRejectsCrossTokenPreviousResponse(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	previousID := uint64(3)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT resource_kind FROM gw_api_resources WHERE id=? FOR UPDATE")).
		WithArgs(uint64(4)).WillReturnRows(sqlmock.NewRows([]string{"resource_kind"}).AddRow("response"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT user_id,token_id FROM gw_api_resources WHERE id=? FOR SHARE")).
		WithArgs(uint64(4)).WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id"}).AddRow(8, 9))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT resource_kind,user_id,token_id FROM gw_api_resources WHERE id=? FOR SHARE")).
		WithArgs(previousID).WillReturnRows(sqlmock.NewRows([]string{"resource_kind", "user_id", "token_id"}).AddRow("response", 8, 10))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.CreateAIResponse(context.Background(), tx, AIResponseInput{ResourceID: 4, ResponseNo: "response-4", Status: "queued", PreviousResourceID: &previousID})
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
