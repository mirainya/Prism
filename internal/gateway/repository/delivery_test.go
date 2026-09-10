package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/security"
)

func TestFailResultDeliveryTransitionAndRollback(t *testing.T) {
	eventErr := errors.New("event write failed")
	for _, test := range []struct {
		name       string
		state      string
		missing    bool
		updateRows int64
		eventErr   error
		wantErr    error
	}{
		{name: "pending", state: "pending", updateRows: 1},
		{name: "ready", state: "ready", wantErr: ErrConflict},
		{name: "already_failed", state: "delivery_failed", wantErr: ErrConflict},
		{name: "expired", state: "expired", wantErr: ErrConflict},
		{name: "missing", missing: true, wantErr: ErrNotFound},
		{name: "update_conflict", state: "pending", wantErr: ErrConflict},
		{name: "event_failure", state: "pending", updateRows: 1, eventErr: eventErr, wantErr: eventErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store, _ := New(db)
			mock.ExpectBegin()
			rows := sqlmock.NewRows([]string{"state", "state_version"})
			if !test.missing {
				rows.AddRow(test.state, 1)
			}
			mock.ExpectQuery("SELECT state,state_version FROM gw_result_deliveries.*FOR UPDATE").WithArgs(uint64(7)).WillReturnRows(rows)
			if test.state == "pending" {
				mock.ExpectExec("UPDATE gw_result_deliveries SET state='delivery_failed'.*WHERE id=\\? AND state='pending' AND state_version=\\?").
					WithArgs("source_expiry_unknown", sqlmock.AnyArg(), uint64(7), uint64(1)).WillReturnResult(sqlmock.NewResult(0, test.updateRows))
				if test.updateRows == 1 {
					event := mock.ExpectExec("INSERT INTO gw_state_transition_events.*'pending','delivery_failed'").
						WithArgs(uint64(7), uint64(2), "source_expiry_unknown", sqlmock.AnyArg())
					if test.eventErr != nil {
						event.WillReturnError(test.eventErr)
					} else {
						event.WillReturnResult(sqlmock.NewResult(8, 1))
					}
				}
			}
			if test.wantErr == nil {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
				return store.FailResultDelivery(context.Background(), tx, 7, "source_expiry_unknown")
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("err=%v, want %v", err, test.wantErr)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRefreshReferenceDeliveryRejectsInvalidInput(t *testing.T) {
	store := &Store{}
	key := make([]byte, security.KeySize)
	expires := time.Now().UTC().Add(time.Minute)
	for _, test := range []struct {
		tx   *sql.Tx
		id   uint64
		body string
		want error
	}{
		{nil, 1, "https://example.com/result.mp4", ErrInvalidInput},
		{new(sql.Tx), 0, "https://example.com/result.mp4", ErrInvalidInput},
		{new(sql.Tx), 1, "not-a-url", ErrInvalidInput},
	} {
		err := store.RefreshReferenceDelivery(context.Background(), test.tx, test.id, BlobInput{Plaintext: []byte(test.body), KEK: key, HMACKey: key}, 2, &expires)
		if !errors.Is(err, test.want) {
			t.Errorf("body=%q err=%v want=%v", test.body, err, test.want)
		}
	}
}

func TestFailResultDeliveryRejectsInvalidInput(t *testing.T) {
	store := &Store{}
	for _, test := range []struct {
		tx     *sql.Tx
		id     uint64
		reason string
	}{
		{nil, 1, "source_expiry_unknown"},
		{new(sql.Tx), 0, "source_expiry_unknown"},
		{new(sql.Tx), 1, ""},
		{new(sql.Tx), 1, strings.Repeat("a", 65)},
	} {
		if err := store.FailResultDelivery(context.Background(), test.tx, test.id, test.reason); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("invalid input returned %v", err)
		}
	}
}

func TestExpireResultDeliveryTransition(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT d.state,d.state_version,d.expires_at,d.delivery_mode,pt.source_url_policy FROM gw_result_deliveries.*FOR UPDATE").WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "state_version", "expires_at", "delivery_mode", "source_url_policy"}).AddRow("ready", 3, time.Now().UTC().Add(-time.Minute), "reference", "fixed"))
	mock.ExpectExec("UPDATE gw_result_deliveries SET state='expired'.*WHERE id=\\? AND state='ready' AND state_version=\\?").
		WithArgs(sqlmock.AnyArg(), uint64(9), uint64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_state_transition_events.*'ready','expired'").
		WithArgs(uint64(9), uint64(4), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error { return store.ExpireResultDelivery(context.Background(), tx, 9) })
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExpireReadyDeliveriesSkipsConcurrentRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectQuery("SELECT id FROM gw_result_deliveries WHERE state='ready'.*LIMIT \\?").WithArgs(100).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9).AddRow(10))
	// Row 9 was already handled by another worker.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT d.state,d.state_version,d.expires_at,d.delivery_mode,pt.source_url_policy FROM gw_result_deliveries.*FOR UPDATE").WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "state_version", "expires_at", "delivery_mode", "source_url_policy"}).AddRow("expired", 2, time.Now().UTC().Add(-time.Minute), "reference", "fixed"))
	mock.ExpectRollback()
	// Row 10 expires successfully.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT d.state,d.state_version,d.expires_at,d.delivery_mode,pt.source_url_policy FROM gw_result_deliveries.*FOR UPDATE").WithArgs(uint64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "state_version", "expires_at", "delivery_mode", "source_url_policy"}).AddRow("ready", 3, time.Now().UTC().Add(-time.Minute), "reference", "fixed"))
	mock.ExpectExec("UPDATE gw_result_deliveries SET state='expired'.*WHERE id=\\? AND state='ready' AND state_version=\\?").
		WithArgs(sqlmock.AnyArg(), uint64(10), uint64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_state_transition_events.*'ready','expired'").
		WithArgs(uint64(10), uint64(4), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	count, err := store.ExpireReadyDeliveries(context.Background(), 100)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
