package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestCompleteCapabilityRejectsIncompleteExchangeBeforeDatabaseWork(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	err = service.CompleteCapability(context.Background(), CapabilitySuccessInput{AttemptID: 1, RequestID: 2})
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFailCapabilityRejectsIncompleteDefinitiveFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	err = service.FailCapability(context.Background(), CapabilityFailureInput{AttemptID: 1, RequestID: 2, Reason: "provider_failed"})
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
