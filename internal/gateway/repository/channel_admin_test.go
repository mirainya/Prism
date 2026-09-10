package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestChannelAndPoolValidation(t *testing.T) {
	for _, code := range []string{"vendor", "provider-1", "my.channel", "pool_key"} {
		if (ChannelInput{Code: code, Name: "Test"}).Validate() != nil {
			t.Fatal(code)
		}
	}
	for _, code := range []string{"", "UPPER", "a/b", " a", "a' OR 1=1", strings.Repeat("a", 129)} {
		if (ChannelInput{Code: code, Name: "Test"}).Validate() == nil {
			t.Fatal(code)
		}
	}
	for _, name := range []string{"", " leading", "trailing ", "has\nline", strings.Repeat("a", 129)} {
		if (ChannelInput{Code: "valid", Name: name}).Validate() == nil {
			t.Fatal(name)
		}
	}
	limit := uint64(1000001)
	if (PoolUpdate{Name: "Pool", ExpectedVersion: 1, RequestLimit: &limit}).Validate() == nil {
		t.Fatal("oversized limit accepted")
	}
	limit = 0
	if (PoolUpdate{Name: "Pool", ExpectedVersion: 1, RequestLimit: &limit}).Validate() == nil {
		t.Fatal("zero capacity violates the storage contract")
	}
}

func TestChannelCreateRollsBackWhenAuditFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO gateway_channels").WithArgs("provider", "Provider", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectExec("INSERT INTO audit_events").WillReturnError(errors.New("audit unavailable"))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := store.CreateChannel(context.Background(), tx, ChannelInput{Code: "provider", Name: "Provider"}, 7)
		return err
	})
	if err == nil {
		t.Fatal("audit failure must abort configuration")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChannelUpdateRejectsStaleSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT display_name,status FROM gateway_channels").WithArgs(3).WillReturnRows(sqlmock.NewRows([]string{"name", "status"}).AddRow("New name", "active"))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.UpdateChannel(context.Background(), tx, 3, ChannelUpdate{Name: "Overwrite", Status: "disabled", ExpectedName: "Old name", ExpectedStatus: "active"}, 7)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPoolUpdateChecksVersionAndRecordsAudit(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "stale"}[stale], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store, _ := New(db)
			mock.ExpectBegin()
			version := 1
			if stale {
				version = 2
			}
			mock.ExpectQuery("SELECT status,config_version FROM gw_credential_pools").WithArgs(4).WillReturnRows(sqlmock.NewRows([]string{"status", "version"}).AddRow("active", version))
			if stale {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec("UPDATE gw_credential_pools").WithArgs("Renamed", nil, 1, sqlmock.AnyArg(), 4).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("INSERT INTO audit_events").WillReturnResult(sqlmock.NewResult(6, 1))
				mock.ExpectCommit()
			}
			limit := uint64(1)
			err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
				return store.UpdateCredentialPool(context.Background(), tx, 4, PoolUpdate{Name: "Renamed", TaskLimit: &limit, ExpectedVersion: 1}, 7)
			})
			if stale && !errors.Is(err, ErrConflict) || !stale && err != nil {
				t.Fatalf("error=%v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
