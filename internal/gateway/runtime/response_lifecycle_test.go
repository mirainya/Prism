package runtime

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestCancelQueuedResponseRejectsClaimedDispatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT c.id,a.id")).
		WithArgs("resp_1", uint64(6), uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"call_id", "attempt_id"}).AddRow(9, 12))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM billing_reservations WHERE call_id=?")).
		WithArgs(uint64(9)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status,state_version,current_attempt_id FROM gw_api_calls WHERE id=? FOR UPDATE")).
		WithArgs(uint64(9)).WillReturnRows(sqlmock.NewRows([]string{"status", "state_version", "current_attempt_id"}).AddRow("in_progress", 2, 12))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state,state_version FROM gw_api_call_attempts WHERE id=? AND call_id=? FOR UPDATE")).
		WithArgs(uint64(12), uint64(9)).WillReturnRows(sqlmock.NewRows([]string{"state", "state_version"}).AddRow("started", 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT r.id,response.status FROM gw_api_resources r JOIN gw_ai_responses response ON response.resource_id=r.id WHERE r.call_id=? AND r.public_id=? AND r.user_id=? AND r.token_id=? AND r.deleted_at IS NULL FOR UPDATE")).
		WithArgs(uint64(9), "resp_1", uint64(6), uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(15, "queued"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id,status,attempt_count,lease_owner FROM gw_async_outbox WHERE attempt_id=? AND action='submit' ORDER BY action_seq DESC LIMIT 1 FOR UPDATE")).
		WithArgs(uint64(12)).WillReturnRows(sqlmock.NewRows([]string{"id", "status", "attempt_count", "lease_owner"}).AddRow(31, "dispatching", 1, "worker-1"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM gw_channel_request_logs WHERE attempt_id=? ORDER BY request_seq DESC LIMIT 1 FOR UPDATE")).
		WithArgs(uint64(12)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	err = service.CancelQueuedResponse(context.Background(), 6, 7, "resp_1")
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteResponseRejectsActiveResource(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT r.id,response.status FROM gw_api_resources r JOIN gw_ai_responses response ON response.resource_id=r.id WHERE r.public_id=? AND r.resource_kind='response' AND r.user_id=? AND r.token_id=? AND r.deleted_at IS NULL FOR UPDATE")).
		WithArgs("resp_1", uint64(6), uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(15, "queued"))
	mock.ExpectRollback()
	err := service.DeleteResponse(context.Background(), 6, 7, "resp_1")
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteResponseHidesTerminalResource(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT r.id,response.status FROM gw_api_resources r JOIN gw_ai_responses response ON response.resource_id=r.id WHERE r.public_id=? AND r.resource_kind='response' AND r.user_id=? AND r.token_id=? AND r.deleted_at IS NULL FOR UPDATE")).
		WithArgs("resp_1", uint64(6), uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(15, "completed"))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_api_resources SET deleted_at=? WHERE id=? AND deleted_at IS NULL")).
		WithArgs(sqlmock.AnyArg(), uint64(15)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := service.DeleteResponse(context.Background(), 6, 7, "resp_1"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
