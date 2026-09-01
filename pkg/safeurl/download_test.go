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

func TestTrustedHostAllowsPrivateProviderDNS(t *testing.T) {
	value, err := url.Parse("http://localhost/result.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateResolvedURLWithTrustedHosts(context.Background(), value, []string{"localhost"}); err != nil {
		t.Fatalf("trusted provider host error = %v", err)
	}
	if err := validateResolvedURLWithTrustedHosts(context.Background(), value, []string{"other.example"}); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("untrusted provider host error = %v, want ErrUnsafeURL", err)
	}
}

func TestTrustedHostWildcard(t *testing.T) {
	if !trustedHost("bucket.cos.example.com", []string{"*.cos.example.com"}) {
		t.Fatal("wildcard trusted host did not match subdomain")
	}
	if trustedHost("cos.example.com", []string{"*.cos.example.com"}) {
		t.Fatal("wildcard trusted host matched base domain")
	}
}
