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

// TestPoolCostGroupRatioValidation mirrors ck_gw_credential_pools_cost_group_ratio.
// The ratio only scales assumed upstream cost, so a bad value never overcharges a
// user — but it silently corrupts every margin figure the console reports, which
// is why it is rejected at the boundary rather than clamped.
func TestPoolCostGroupRatioValidation(t *testing.T) {
	for _, ratio := range []string{"1", "0.5", "1.25", "12.34567890"} {
		if (PoolUpdate{Name: "Pool", ExpectedVersion: 1, CostGroupRatio: &ratio}).Validate() != nil {
			t.Fatalf("ratio %q rejected", ratio)
		}
	}
	// Zero is refused along with the malformed values: it is representable and
	// non-negative, so ParseAmount accepts it, and it would report every upstream
	// call as free.
	for _, ratio := range []string{"", "0", "0.00", "-1", "1e3", "abc", "1.234567890", " 1"} {
		if (PoolUpdate{Name: "Pool", ExpectedVersion: 1, CostGroupRatio: &ratio}).Validate() == nil {
			t.Fatalf("ratio %q accepted", ratio)
		}
	}
	// Omitting the field is how a caller that predates the column keeps working.
	if (PoolUpdate{Name: "Pool", ExpectedVersion: 1}).Validate() != nil {
		t.Fatal("an omitted ratio must leave the pool updatable")
	}
}

// TestPoolUpdateWritesCostGroupRatio checks the value reaches the statement as
// given, rather than being reformatted on the way through.
func TestPoolUpdateWritesCostGroupRatio(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status,config_version FROM gw_credential_pools").WithArgs(4).WillReturnRows(sqlmock.NewRows([]string{"status", "version"}).AddRow("active", 1))
	mock.ExpectExec("UPDATE gw_credential_pools").WithArgs("Pool", nil, nil, "0.85", sqlmock.AnyArg(), 4).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO audit_events").WillReturnResult(sqlmock.NewResult(6, 1))
	mock.ExpectCommit()
	ratio := "0.85"
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.UpdateCredentialPool(context.Background(), tx, 4, PoolUpdate{Name: "Pool", CostGroupRatio: &ratio, ExpectedVersion: 1}, 7)
	})
	if err != nil {
		t.Fatalf("error=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
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
				// The nil in fourth position is the omitted cost_group_ratio, which
				// COALESCE turns into "keep whatever is stored".
				mock.ExpectExec("UPDATE gw_credential_pools").WithArgs("Renamed", nil, 1, nil, sqlmock.AnyArg(), 4).WillReturnResult(sqlmock.NewResult(0, 1))
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
