package open

import (
	"bytes"
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestReadUnifiedImageBytesUsesCallStorageKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previousStore := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previousStore })

	mock.ExpectQuery("SELECT d.id,d.call_id,d.attempt_id,d.user_id,d.token_id").
		WithArgs(uint64(17), uint64(10), uint64(41), uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_id", "attempt_id", "user_id", "token_id", "delivery_mode", "source_kind", "state", "content_type", "reason_code", "expires_at", "source_url_policy", "source_seq", "encrypted_url_blob_id", "media_asset_ref_id", "media_asset_id", "object_key"}).
			AddRow(uint64(17), uint64(10), uint64(11), uint64(41), uint64(7), "managed_copy", "inline_response", "ready", "image/png", "managed_copy_available", nil, "fixed", nil, nil, uint64(20), uint64(21), "Prism/results/image.png"))
	mock.ExpectQuery("SELECT c.user_id,c.token_id,COALESCE\\(c.xfs_api_key,''\\)").
		WithArgs(uint64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id", "xfs_api_key"}).
			AddRow(uint64(41), uint64(7), testManagedResultAPIKey))

	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
	previousRead := readManagedResult
	readManagedResult = func(_ context.Context, apiKey, locator string, maxBytes int64) ([]byte, error) {
		if apiKey != testManagedResultAPIKey || locator != "Prism/results/image.png" || maxBytes != 64<<20 {
			t.Fatalf("read key=%q locator=%q maxBytes=%d", apiKey, locator, maxBytes)
		}
		return bytes.Clone(png), nil
	}
	t.Cleanup(func() { readManagedResult = previousRead })

	result, err := readUnifiedImageBytes(context.Background(), 17, 10, 41, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, png) {
		t.Fatalf("result=%x", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
