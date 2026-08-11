// Package taskhttp contains the provider-neutral HTTP execution primitive used
// by asynchronous video adapters. It deliberately knows nothing about any
// provider's request or response schema.
package taskhttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mirainya/Prism/internal/video"
)

const DefaultMaxResponseBytes = 4 << 20

type Operation struct {
	Method string
	Path   string
}

type Config struct {
	BaseURL          string
	APIKey           string
	AuthHeader       string
	AuthPrefix       string
	HTTPClient       *http.Client
	MaxResponseBytes int
}

type Client struct {
	baseURL          string
	apiKey           string
	authHeader       string
	authPrefix       string
	httpClient       *http.Client
	maxResponseBytes int
}

func NewClient(config Config) *Client {
	authHeader := strings.TrimSpace(config.AuthHeader)
	if authHeader == "" {
		authHeader = "Authorization"
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:          strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"),
		apiKey:           config.APIKey,
		authHeader:       authHeader,
		authPrefix:       config.AuthPrefix,
		httpClient:       httpClient,
		maxResponseBytes: maxResponseBytes,
	}
}

func (c *Client) Ready() error {
	if c == nil {
		return fmt.Errorf("task HTTP client is nil")
	}
	if c.baseURL == "" || c.httpClient == nil || strings.TrimSpace(c.apiKey) == "" {
		return fmt.Errorf("task HTTP client is not initialized")
	}
	return nil
}

func (c *Client) Do(ctx context.Context, operation string, config Operation, body []byte, headers map[string]string, taskID ...string) ([]byte, error) {
	if err := c.Ready(); err != nil {
		return nil, err
	}
	method := strings.ToUpper(strings.TrimSpace(config.Method))
	if method == "" {
		return nil, fmt.Errorf("%s request method is required", operation)
	}
	path := config.Path
	if len(taskID) > 0 {
		path = strings.ReplaceAll(path, "{task_id}", url.PathEscape(taskID[0]))
	}
	requestURL := c.baseURL + path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return nil, fmt.Errorf("create %s request: %w", operation, err)
	}
	request.Header.Set(c.authHeader, c.authPrefix+c.apiKey)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if operation == "submit" {
			return nil, video.NewAmbiguousProviderError(operation+" request", err)
		}
		return nil, video.NewRetryableProviderError(operation+" request", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, int64(c.maxResponseBytes)+1))
	if err != nil {
		if operation == "submit" {
			return nil, video.NewAmbiguousProviderError("read "+operation+" response", err)
		}
		return nil, video.NewRetryableProviderError("read "+operation+" response", err)
	}
	if len(responseBody) > c.maxResponseBytes {
		return nil, fmt.Errorf("%s response exceeds %d bytes", operation, c.maxResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseErr := fmt.Errorf("upstream HTTP %d: %s", response.StatusCode, truncate(responseBody, 512))
		if response.StatusCode == http.StatusRequestTimeout || response.StatusCode >= 500 {
			if operation == "submit" {
				return nil, video.NewAmbiguousProviderError(operation+" request", responseErr)
			}
			return nil, video.NewRetryableProviderError(operation+" request", responseErr)
		}
		if response.StatusCode == http.StatusTooEarly || response.StatusCode == http.StatusTooManyRequests {
			return nil, video.NewRetryableProviderError(operation+" request", responseErr)
		}
		return nil, responseErr
	}
	return responseBody, nil
}

func truncate(value []byte, max int) string {
	if len(value) <= max {
		return string(value)
	}
	return string(value[:max]) + "..."
}
