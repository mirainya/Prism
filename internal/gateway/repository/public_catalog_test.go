package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPublicCatalogReleaseIDPreservesDatabaseErrors(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("catalog query failed")
	mock.ExpectQuery(`SELECT rel\.id FROM gw_catalog_runtime_state`).WillReturnError(want)

	_, err = store.PublicCatalogReleaseID(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want database error", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPublicCatalogReleaseIDHandlesEmptyAndPublishedCatalog(t *testing.T) {
	for _, test := range []struct {
		name string
		rows *sqlmock.Rows
		want uint64
	}{
		{name: "empty", rows: sqlmock.NewRows([]string{"id"}), want: 0},
		{name: "published", rows: sqlmock.NewRows([]string{"id"}).AddRow(7), want: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			store, err := New(db)
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectQuery(`SELECT rel\.id FROM gw_catalog_runtime_state`).WillReturnRows(test.rows)

			got, err := store.PublicCatalogReleaseID(context.Background())
			if err != nil || got != test.want {
				t.Fatalf("release = %d, error = %v, want %d", got, err, test.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
