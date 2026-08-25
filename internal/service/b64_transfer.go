package service

import (
	"context"
	"fmt"
	"time"

	"github.com/mirainya/Prism/pkg/filestorage"
)

const base64UploadTimeout = 120 * time.Second

// resolveB64ToURLs uploads base64 image data and returns public URLs.
func resolveB64ToURLs(ctx context.Context, b64List []string, capabilityCode string) ([]string, error) {
	urls := make([]string, 0, len(b64List))
	for _, b64 := range b64List {
		if b64 == "" {
			continue
		}
		uploadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), base64UploadTimeout)
		url, err := filestorage.TransferBase64(uploadCtx, b64, capabilityCode)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("transfer base64: %w", err)
		}
		urls = append(urls, url)
	}
	return urls, nil
}
