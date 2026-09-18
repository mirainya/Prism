package runtime

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestConfigureMediaDeliverySelectsManagedCopyWithCurrentKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service := &Service{Store: store}
	in := SubmitInput{
		Call:         repository.CreateCallInput{UserID: 11, TokenID: 13},
		ResourceKind: "video_task",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE\\(xfs_api_key,''\\) FROM tokens").
		WithArgs(uint64(13), uint64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"xfs_api_key"}).AddRow(testStorageAPIKey))
	mock.ExpectCommit()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return service.configureMediaDelivery(context.Background(), tx, &in)
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.Call.DeliveryMode != "managed_copy" || in.Call.XFSAPIKey != testStorageAPIKey {
		t.Fatalf("call=%+v", in.Call)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigureMediaDeliverySelectsReferenceWithoutCurrentKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service := &Service{Store: store}
	in := SubmitInput{
		Call:         repository.CreateCallInput{UserID: 11, TokenID: 13, DeliveryMode: "reference"},
		ResourceKind: "capability_task",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE\\(xfs_api_key,''\\) FROM tokens").
		WithArgs(uint64(13), uint64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"xfs_api_key"}).AddRow(""))
	mock.ExpectCommit()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return service.configureMediaDelivery(context.Background(), tx, &in)
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.Call.DeliveryMode != "reference" {
		t.Fatalf("call=%+v", in.Call)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigureMediaDeliveryOverridesManagedCopyWithoutKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service := &Service{Store: store}
	in := SubmitInput{
		Call:         repository.CreateCallInput{UserID: 11, TokenID: 13, DeliveryMode: "managed_copy"},
		ResourceKind: "video_task",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE\\(xfs_api_key,''\\) FROM tokens").
		WithArgs(uint64(13), uint64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"xfs_api_key"}).AddRow(""))
	mock.ExpectCommit()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return service.configureMediaDelivery(context.Background(), tx, &in)
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.Call.DeliveryMode != "reference" || in.Call.XFSAPIKey != "" {
		t.Fatalf("call=%+v", in.Call)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
