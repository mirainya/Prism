package service

import (
	"context"
	"fmt"

	"github.com/mirainya/Prism/pkg/filestorage"
)

// resolveB64ToURLs uploads base64 image data and returns public URLs.
func resolveB64ToURLs(ctx context.Context, b64List []string, capabilityCode string) ([]string, error) {
	urls := make([]string, 0, len(b64List))
	for _, b64 := range b64List {
		if b64 == "" {
			continue
		}
		url, err := filestorage.TransferBase64(ctx, b64, capabilityCode)
		if err != nil {
			return nil, fmt.Errorf("transfer base64: %w", err)
		}
		urls = append(urls, url)
	}
	return urls, nil
}
