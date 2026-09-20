package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/mirainya/Prism/pkg/filestorage"
	"github.com/mirainya/Prism/pkg/safeurl"
)

const maxAsyncInputAssetBytes = 64 << 20

type storedAsyncAssetLoader struct{}

func (storedAsyncAssetLoader) LoadAsyncAsset(ctx context.Context, location string) (AsyncAsset, error) {
	data, storageErr := filestorage.ReadURL(ctx, location, maxAsyncInputAssetBytes)
	if storageErr == nil {
		return AsyncAsset{Data: data, ContentType: http.DetectContentType(data)}, nil
	}
	downloaded, downloadErr := safeurl.Download(ctx, location, maxAsyncInputAssetBytes)
	if downloadErr != nil {
		return AsyncAsset{}, fmt.Errorf("%w: %v", ErrAsyncAssetUnavailable, errors.Join(storageErr, downloadErr))
	}
	return AsyncAsset{Data: downloaded.Data, ContentType: downloaded.ContentType}, nil
}
