package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSlotAttemptActions(t *testing.T) {
	for _, tc := range []struct {
		state, action string
		allowed       bool
	}{
		{"started", "", true}, {"started", "submit", true},
		{"started", "catalog_discovery", false}, {"started", "invalid", false},
		{"recovery_pending", "submit", false}, {"recovery_pending", "query", true},
		{"completed", "result_fetch", true}, {"completed", "reconcile_delivery", true},
		{"started", "reconcile_delivery", false}, {"terminated_unknown", "reconcile_delivery", false},
		{"completed", "submit", false},
		{"terminated_unknown", "query", true}, {"terminated_unknown", "recover", true},
		{"terminated_unknown", "submit", false}, {"terminated_unknown", "", false},
		{"failed", "submit", false}, {"cancelled", "query", false},
	} {
		if got := slotAttemptAllows(tc.state, tc.action); got != tc.allowed {
			t.Errorf("%s/%s allowed=%v", tc.state, tc.action, got)
		}
	}
}

func TestSlotAuthorizationRejectsRevokedExpiredAndWrongPurpose(t *testing.T) {
	for _, tc := range []struct {
		name, version, secret, grant, purpose string
		recovery, expired, allowed            bool
	}{
		{"active", "active", "active", "active", "execution", false, false, true},
		{"revoked_secret", "active", "security_revoked", "active", "execution", true, false, false},
		{"expired_version", "active", "active", "active", "execution", true, true, false},
		{"retired_version", "retired", "active", "active", "execution", true, false, false},
		{"superseded_submit", "superseded", "active", "active", "execution", false, false, false},
		{"superseded_recovery", "superseded", "active", "active", "execution", true, false, true},
		{"draining_submit", "active", "active", "draining", "execution", false, false, false},
		{"draining_recovery", "active", "active", "draining", "execution", true, false, true},
		{"revoked_grant", "active", "active", "revoked", "execution", true, false, false},
		{"wrong_purpose", "active", "active", "active", "catalog_discovery", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, _ := sqlmock.New()
			defer db.Close()
			store, _ := New(db)
			mock.ExpectBegin()
			var expires any
			if tc.expired {
				expires = time.Now().UTC().Add(-time.Minute)
			}
			mock.ExpectQuery("SELECT v.status,v.valid_until,i.status").WithArgs(uint64(2), uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"version", "expires", "secret"}).AddRow(tc.version, expires, tc.secret))
			mock.ExpectQuery("SELECT status,purpose FROM gw_credential_purpose_grants").WithArgs(uint64(3), uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"grant", "purpose"}).AddRow(tc.grant, tc.purpose))
			if tc.allowed {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err := store.WithTx(context.Background(), func(tx *sql.Tx) error {
				return (slotOwner{versionID: 2, grantID: 3, recovery: tc.recovery}).validateAuthorization(context.Background(), tx, SlotInput{CredentialID: 1, Scope: "task"})
			})
			if (err == nil) != tc.allowed || err != nil && !errors.Is(err, ErrConflict) {
				t.Fatalf("authorization error=%v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSlotLimitUsesCurrentReadAndPropagatesScanErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		fail  bool
		want  error
	}{
		{"available", 1, false, nil},
		{"full", 2, false, ErrConcurrencyLimit},
		{"scan_failed", 2, true, sql.ErrConnDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, _ := sqlmock.New()
			defer db.Close()
			store, _ := New(db)
			mock.ExpectBegin()
			rows := sqlmock.NewRows([]string{"id"})
			for i := 0; i < tc.count; i++ {
				rows.AddRow(i + 1)
			}
			if tc.fail {
				rows.RowError(1, sql.ErrConnDone)
			}
			mock.ExpectQuery(`SELECT id FROM gw_credential_slots WHERE credential_pool_id=\? AND scope=\? AND state IN \('active','recovery_required'\) LIMIT \? FOR UPDATE`).WithArgs(uint64(1), "task", int64(2)).WillReturnRows(rows).RowsWillBeClosed()
			if tc.want == nil {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err := store.WithTx(context.Background(), func(tx *sql.Tx) error {
				return checkSlotLimit(context.Background(), tx, "credential_pool_id", 1, "task", sql.NullInt64{Int64: 2, Valid: true})
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFinalizeUnknownSlotIsIdempotentAndNeverReleases(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state FROM gw_api_call_attempts").WithArgs(uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("terminated_unknown"))
	mock.ExpectQuery("SELECT id,state FROM gw_credential_slots WHERE active_attempt_id").WithArgs(uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "state"}).AddRow(3, "recovery_required"))
	mock.ExpectCommit()
	if err := store.WithTx(context.Background(), func(tx *sql.Tx) error { return store.FinalizeAttemptSlot(context.Background(), tx, 2) }); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
