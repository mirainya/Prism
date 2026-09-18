package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/pkg/filestorage"
)

func TestPrepareManagedResultCopyAtPreservesOriginalOrdinal(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	expectManagedOwner(mock, 2, 10, 20)
	expectManagedAllocation(mock, 2, 1, 10, 20, "gateway-result:2:3", 31)
	expectManagedUploadRecord(mock, 31)
	restore := stubManagedStorage(t)
	defer restore()
	uploadManagedResult = func(_ context.Context, apiKey string, data []byte, contentType, path, filename string) (filestorage.UploadResult, error) {
		digest := sha256.Sum256(testPNG)
		if apiKey != testStorageAPIKey || string(data) != string(testPNG) || contentType != "image/png" || !strings.HasSuffix(path, "gateway-results/31/") || filename != hex.EncodeToString(digest[:])+".png" {
			t.Fatalf("upload = key:%q %x %q %q %q", apiKey, data, contentType, path, filename)
		}
		return filestorage.UploadResult{URL: "https://storage.example/result.png", RawURL: "private/result.png", ObjectID: "object-v1"}, nil
	}

	copy, err := service.prepareManagedResultCopyAt(context.Background(), 2, 3, delivery.RemoteResult{
		Role: "image", InlineData: append([]byte(nil), testPNG...), ContentType: "image/png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if copy.MediaAssetID != 31 || copy.ObjectKey != "gateway-result:2:3" || copy.StorageLocator != "private/result.png" || copy.ContentType != "image/png" || copy.ContentLength != uint64(len(testPNG)) {
		t.Fatalf("copy=%+v", copy)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareManagedResultCopyAtRejectsMissingCallStorageKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	mock.ExpectQuery("SELECT c.user_id,c.token_id,COALESCE\\(c.xfs_api_key,''\\)").WithArgs(uint64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id", "xfs_api_key"}).AddRow(10, 20, ""))
	_, err = service.prepareManagedResultCopyAt(context.Background(), 2, 0, delivery.RemoteResult{
		Role: "image", InlineData: append([]byte(nil), testPNG...), ContentType: "image/png",
	})
	var permanent *PermanentDispatchError
	if !errors.As(err, &permanent) || permanent.Code != "managed_copy_storage_unconfigured" {
		t.Fatalf("err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
