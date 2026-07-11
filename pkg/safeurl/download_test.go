package safeurl

import (
	"context"
	"errors"
	"testing"
)

func TestDownloadRejectsPrivateAddresses(t *testing.T) {
	for _, rawURL := range []string{"http://127.0.0.1/file", "http://10.0.0.1/file", "http://100.64.0.1/file", "http://198.18.0.1/file", "http://[::1]/file", "http://[2001:db8::1]/file", "file:///etc/passwd"} {
		if _, err := Download(context.Background(), rawURL, 1024); !errors.Is(err, ErrUnsafeURL) {
			t.Fatalf("Download(%q) error = %v, want ErrUnsafeURL", rawURL, err)
		}
	}
}
