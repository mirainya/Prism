package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestValidCryptoReadinessOperation(t *testing.T) {
	for _, value := range []string{"mac", "wrap", "unwrap", "encrypt", "decrypt"} {
		if !validCryptoReadinessOperation(value) {
			t.Fatalf("operation %q should be accepted", value)
		}
	}
	for _, value := range []string{"", "sign", "MAC", "encrypt_decrypt"} {
		if validCryptoReadinessOperation(value) {
			t.Fatalf("operation %q should be rejected", value)
		}
	}
}

func TestActivateDeploymentAtomicallyReplacesActiveGeneration(t *testing.T) {
	for _, success := range []bool{true, false} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		store, err := New(db)
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(12))
		mock.ExpectQuery(`SELECT status FROM gw_deployment_generations WHERE id=\? FOR UPDATE`).WithArgs(2).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("preparing"))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM gw_deployment_members`).WithArgs(2).WillReturnRows(sqlmock.NewRows([]string{"members"}).AddRow(2))
		mock.ExpectQuery(`SELECT COUNT\(DISTINCT m.id\)`).WithArgs(2, 12, testDeploymentIdentity().AdapterDigest).WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(2))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM gw_deployment_members m`).WithArgs(2, "instance-a", "api-worker", 12, testDeploymentIdentity().AdapterDigest).WillReturnRows(sqlmock.NewRows([]string{"current"}).AddRow(1))
		mock.ExpectQuery(`SELECT EXISTS \(`).WillReturnRows(sqlmock.NewRows([]string{"required"}).AddRow(true))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM crypto_keyring_state`).WithArgs(true).WillReturnRows(sqlmock.NewRows([]string{"rings"}).AddRow(2))
		mock.ExpectQuery(`SELECT COUNT\(\*\), COALESCE\(SUM`).WithArgs(true, 2).WillReturnRows(sqlmock.NewRows([]string{"members", "ready"}).AddRow(2, 2))
		mock.ExpectExec(`UPDATE gw_deployment_generations SET status='retired'`).WithArgs(2).WillReturnResult(sqlmock.NewResult(0, 1))
		var affected int64
		if success {
			affected = 1
		}
		mock.ExpectExec(`UPDATE gw_deployment_generations SET status='active'`).WithArgs(sqlmock.AnyArg(), 2).WillReturnResult(sqlmock.NewResult(0, affected))
		if success {
			mock.ExpectExec(`UPDATE gw_catalog_runtime_state SET active_deployment_generation_id=\?,state_version=state_version\+1,updated_at=\? WHERE id=1`).WithArgs(uint64(2), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
		}
		if success {
			mock.ExpectCommit()
		} else {
			mock.ExpectRollback()
		}
		err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
			return store.ActivateDeploymentGeneration(context.Background(), tx, 2, testDeploymentIdentity())
		})
		if success && err != nil || !success && !errors.Is(err, ErrConflict) {
			t.Fatalf("success=%v error=%v", success, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
}

func TestActivateCatalogRejectsPreparingGeneration(t *testing.T) {
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
	mock.ExpectQuery(`SELECT state_version FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"state_version"}).AddRow(1))
	mock.ExpectQuery(`SELECT status FROM gw_deployment_generations`).WithArgs(2).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("preparing"))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.ActivateReleaseWhenReady(context.Background(), tx, 12, 1, 2, testDeploymentIdentity())
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActivateCatalogFromRejectsChangedSource(t *testing.T) {
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
	mock.ExpectQuery(`SELECT active_release_id,state_version FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"active_release_id", "state_version"}).AddRow(13, 2))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.ActivateReleaseWhenReadyFrom(context.Background(), tx, 14, 12, 2, 2, testDeploymentIdentity())
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v, want source conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
