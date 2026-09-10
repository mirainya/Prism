package runtime

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func testAttemptOutboxItem() repository.OutboxItem {
	expiresAt := time.Now().UTC().Add(time.Minute).Truncate(time.Millisecond)
	return repository.OutboxItem{
		ID: 31, AttemptID: 12, ActionSeq: 1, StateVersion: 1, Action: "submit",
		Attempts: 1, LeaseOwner: "worker-1", LeaseExpiresAt: expiresAt,
		CallLease: repository.CallLease{CallID: 9, AttemptID: 12, Owner: "worker-1", ExpiresAt: expiresAt},
	}
}

func TestBeginAttemptOutboxRequestRejectsStaleWorkerBeforeLogging(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	item := testAttemptOutboxItem()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM gw_api_calls")).
		WithArgs(uint64(9), uint64(12), "worker-1", item.LeaseExpiresAt).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	_, err = service.BeginAttemptOutboxRequest(context.Background(), item, AttemptRequestEvidence{MappingHMAC: string(make([]byte, 64))})
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBeginAttemptOutboxRequestRollsBackInvalidEvidence(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	item := testAttemptOutboxItem()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM gw_api_calls")).
		WithArgs(uint64(9), uint64(12), "worker-1", item.LeaseExpiresAt).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT call_id,state,state_version FROM gw_api_call_attempts WHERE id=? FOR UPDATE")).
		WithArgs(uint64(12)).WillReturnRows(sqlmock.NewRows([]string{"call_id", "state", "state_version"}).AddRow(9, "started", 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM gw_async_outbox")).
		WithArgs(uint64(31), "worker-1", uint64(1), uint64(1), uint64(1), "submit", nil, uint64(12), nil, nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id,status FROM gw_channel_request_logs WHERE attempt_id=? AND action='submit' ORDER BY request_seq DESC LIMIT 1 FOR UPDATE")).
		WithArgs(uint64(12)).WillReturnRows(sqlmock.NewRows([]string{"id", "status"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT request_seq FROM gw_channel_request_logs WHERE attempt_id=? ORDER BY request_seq DESC LIMIT 1 FOR UPDATE")).
		WithArgs(uint64(12)).WillReturnRows(sqlmock.NewRows([]string{"request_seq"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT credential_id,credential_pool_id FROM gw_api_call_attempts WHERE id=? FOR UPDATE")).
		WithArgs(uint64(12)).WillReturnRows(sqlmock.NewRows([]string{"credential_id", "credential_pool_id"}).AddRow(4, 5))
	mock.ExpectRollback()
	_, err = service.BeginAttemptOutboxRequest(context.Background(), item, AttemptRequestEvidence{})
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("err=%v, want invalid input", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
