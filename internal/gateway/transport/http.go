package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultMaxResponseBytes int64 = 16 << 20

// HTTPClient is shared by concrete transports. It deliberately does not map
// provider errors because each transport owns its wire error format.
type HTTPClient struct {
	Client           *http.Client
	MaxResponseBytes int64
}

func (c HTTPClient) Do(ctx context.Context, prepared PreparedRequest) (*http.Response, error) {
	if prepared.Method == "" || prepared.URL == "" {
		return nil, fmt.Errorf("prepared request method and URL are required")
	}
	req, err := http.NewRequestWithContext(ctx, prepared.Method, prepared.URL, bytes.NewReader(prepared.Body))
	if err != nil {
		return nil, err
	}
	req.Header = prepared.Headers.Clone()
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return client.Do(req)
}

func (c HTTPClient) ReadBody(response *http.Response) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("upstream response body is required")
	}
	limit := c.MaxResponseBytes
	if limit <= 0 {
		limit = defaultMaxResponseBytes
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("upstream response exceeds %d bytes", limit)
	}
	return body, nil
}
