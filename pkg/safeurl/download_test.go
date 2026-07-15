package safeurl

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestDownloadRejectsPrivateAddresses(t *testing.T) {
	for _, rawURL := range []string{"http://127.0.0.1/file", "http://10.0.0.1/file", "http://100.64.0.1/file", "http://198.18.0.1/file", "http://[::1]/file", "http://[2001:db8::1]/file", "file:///etc/passwd"} {
		if _, err := Download(context.Background(), rawURL, 1024); !errors.Is(err, ErrUnsafeURL) {
			t.Fatalf("Download(%q) error = %v, want ErrUnsafeURL", rawURL, err)
		}
	}
}

func TestValidateRejectsPrivateDNSAndAllowsPublicLiteral(t *testing.T) {
	for _, rawURL := range []string{
		"http://localhost/callback",
		"https://127.0.0.1/callback",
		"ftp://1.1.1.1/callback",
	} {
		if err := Validate(context.Background(), rawURL); !errors.Is(err, ErrUnsafeURL) {
			t.Fatalf("Validate(%q) error = %v, want ErrUnsafeURL", rawURL, err)
		}
	}
	if err := Validate(context.Background(), "https://1.1.1.1/callback"); err != nil {
		t.Fatalf("Validate(public URL) error = %v", err)
	}
}

func TestClientRejectsPrivateRedirect(t *testing.T) {
	client := NewClient(time.Second)
	redirectURL, err := url.Parse("http://127.0.0.1/callback")
	if err != nil {
		t.Fatal(err)
	}
	request := &http.Request{URL: redirectURL}
	via := []*http.Request{{URL: &url.URL{Scheme: "https", Host: "1.1.1.1"}}}
	if err := client.CheckRedirect(request, via); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("redirect error = %v, want ErrUnsafeURL", err)
	}
}
