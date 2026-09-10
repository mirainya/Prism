package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/credentials"
)

func validManagedCredential() ManagedCredentialInput {
	return ManagedCredentialInput{Code: "test-key", Weight: 1, Secret: []byte("test-secret"), Purposes: []credentials.Purpose{credentials.PurposeExecution}}
}

func TestManagedCredentialValidationAndRedaction(t *testing.T) {
	in := validManagedCredential()
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(in)
	if err != nil || strings.Contains(string(body), "test-secret") || strings.Contains(string(body), "Secret") {
		t.Fatalf("unsafe audit body: %s, %v", body, err)
	}
	for _, change := range []func(*ManagedCredentialInput){
		func(in *ManagedCredentialInput) { in.Code = "bad code" },
		func(in *ManagedCredentialInput) { in.Secret = nil },
		func(in *ManagedCredentialInput) { in.Secret = []byte("header\r\ninjection") },
		func(in *ManagedCredentialInput) { in.Secret = []byte(strings.Repeat("x", 8193)) },
		func(in *ManagedCredentialInput) { in.Weight = 0 },
		func(in *ManagedCredentialInput) { zero := uint64(0); in.TaskLimit = &zero },
		func(in *ManagedCredentialInput) { in.Purposes = nil },
		func(in *ManagedCredentialInput) { in.Purposes = []credentials.Purpose{"unknown"} },
		func(in *ManagedCredentialInput) { in.Purposes = append(in.Purposes, credentials.PurposeExecution) },
	} {
		in = validManagedCredential()
		change(&in)
		if in.Validate() == nil {
			t.Fatal("invalid credential accepted")
		}
	}
}

func TestManagedCredentialUpdateAtomicity(t *testing.T) {
	for _, stale := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		store, _ := New(db)
		mock.ExpectBegin()
		version := 2
		if stale {
			version = 3
		}
		mock.ExpectQuery("SELECT status,config_version FROM gw_credentials").WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"status", "version"}).AddRow("active", version))
		if !stale {
			mock.ExpectExec("UPDATE gw_credentials").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO audit_events").WillReturnError(errors.New("audit unavailable"))
		}
		mock.ExpectRollback()
		err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
			return store.UpdateManagedCredential(context.Background(), tx, 1, CredentialUpdate{Weight: 2, ExpectedVersion: 2}, 7)
		})
		if err == nil {
			t.Fatal("stale or unaudited change committed")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
}

func TestPoolUnicodeNameMatchesStorageLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	name := strings.Repeat("\u6d4b", 128)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO gw_credential_pools").WithArgs(1, "pool", name, nil, nil, sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := store.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := store.CreateCredentialPool(context.Background(), tx, PoolInput{ChannelID: 1, PoolCode: "pool", DisplayName: name})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialCannotDisableWithUnresolvedSlots(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status FROM gw_credentials").WithArgs(3).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("draining"))
	mock.ExpectQuery("SELECT COUNT.*FROM gw_credential_slots WHERE credential_id=\\? AND state IN \\('active','recovery_required'\\)").WithArgs(3).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.TransitionCredential(context.Background(), tx, 3, credentials.CredentialDraining, credentials.CredentialDisabled, "admin:1")
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("unresolved slot was ignored: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
