package safeurl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrUnsafeURL = errors.New("URL resolves to a non-public address")

type Result struct {
	Data        []byte
	ContentType string
	FinalURL    string
}

func Download(ctx context.Context, rawURL string, maxBytes int64) (*Result, error) {
	if maxBytes <= 0 {
		return nil, errors.New("maxBytes must be positive")
	}
	parsed, err := parseAndValidate(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	client := NewClient(45 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxBytes)
	}
	contentType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return &Result{Data: data, ContentType: contentType, FinalURL: resp.Request.URL.String()}, nil
}

// Validate resolves the URL host and rejects non-HTTP schemes and any host
// that resolves to a non-public address.
func Validate(ctx context.Context, rawURL string) error {
	_, err := parseAndValidate(ctx, rawURL)
	return err
}

// NewClient returns an HTTP client that repeats the public-address check at
// connect time and for every redirect. Callers should still call Validate for
// the initial URL so malformed or unsafe input is rejected before execution.
func NewClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := resolvePublicAddresses(ctx, host)
		if err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return validateResolvedURL(req.Context(), req.URL)
	}
	return client
}

func parseAndValidate(ctx context.Context, rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if err := validateResolvedURL(ctx, parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func validateURL(value *url.URL) error {
	if value == nil || (value.Scheme != "http" && value.Scheme != "https") || value.Hostname() == "" {
		return ErrUnsafeURL
	}
	if ip := net.ParseIP(value.Hostname()); ip != nil && !isPublicIP(ip) {
		return ErrUnsafeURL
	}
	return nil
}

func validateResolvedURL(ctx context.Context, value *url.URL) error {
	if err := validateURL(value); err != nil {
		return err
	}
	_, err := resolvePublicAddresses(ctx, value.Hostname())
	return err
}

func resolvePublicAddresses(ctx context.Context, host string) ([]net.IPAddr, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return nil, ErrUnsafeURL
		}
		return []net.IPAddr{{IP: ip}}, nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, ErrUnsafeURL
	}
	for _, address := range addresses {
		if !isPublicIP(address.IP) {
			return nil, ErrUnsafeURL
		}
	}
	return addresses, nil
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 0 || ip4[0] == 10 || ip4[0] == 127 || (ip4[0] == 100 && ip4[1]&0xc0 == 64) || (ip4[0] == 169 && ip4[1] == 254) || (ip4[0] == 172 && ip4[1]&0xf0 == 16) || (ip4[0] == 192 && ip4[1] == 168) || (ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19)) || ip4[0] >= 224 {
			return false
		}
	}
	if _, network, err := net.ParseCIDR("2001:db8::/32"); err == nil && network.Contains(ip) {
		return false
	}
	return true
}
