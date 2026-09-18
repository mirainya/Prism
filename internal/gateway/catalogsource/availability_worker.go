package catalogsource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
)

const (
	// The provider aggregates over a two-hour sliding window, so polling much
	// faster than this returns a nearly identical figure at the cost of extra
	// upstream load. Polling much slower would let a collapse go unreported for
	// longer than a user's own retry loop.
	availabilityInterval = 4 * time.Minute
	// A first refresh runs on startup; this bounds one whole cycle including
	// login, so a wedged provider cannot hold the worker forever.
	availabilityCycleTimeout = 2 * time.Minute
	availabilityRowLimit     = "2000"
)

// The provider separates observations by product family. Prism exposes all
// executable catalog models through one model directory, so every family must
// be refreshed in the same cycle.
var availabilityCategories = []string{"language", "image", "video"}

// RunAvailability keeps the cached upstream success rates fresh. It is separate
// from the discovery loop on purpose: discovery produces reviewable evidence
// that may enter a catalog release, while this produces a volatile observation
// that must never enter one.
//
// A failure is reported and retried on the next tick rather than stopping the
// loop. Availability is advisory: losing it degrades the display, it must not
// take down the worker that also serves catalog discovery.
func (w *Worker) RunAvailability(ctx context.Context, report func(error)) error {
	if w == nil {
		return repository.ErrInvalidInput
	}
	ticker := time.NewTicker(availabilityInterval)
	defer ticker.Stop()
	for {
		if err := w.RefreshAvailability(ctx); err != nil && ctx.Err() == nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// RefreshAvailability polls every configured source once. Sources are
// independent, so one failing account does not suppress the others.
func (w *Worker) RefreshAvailability(ctx context.Context) error {
	if w == nil {
		return repository.ErrInvalidInput
	}
	sources, err := w.store.ListAvailabilitySources(ctx, AICostPricingV1)
	if err != nil || len(sources) == 0 {
		return err
	}
	var failures []error
	for _, source := range sources {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := w.refreshSource(ctx, source); err != nil {
			failures = append(failures, fmt.Errorf("catalog source %d: %w", source.SourceID, err))
		}
	}
	return errors.Join(failures...)
}

func (w *Worker) refreshSource(ctx context.Context, source repository.AvailabilitySource) error {
	secret, err := w.credentialSecret(ctx, source.CredentialID, source.CredentialSecret, source.CredentialBlobID)
	if err != nil {
		return err
	}
	defer clear(secret)
	account, err := ParseAICostAccountSecret(secret)
	if err != nil {
		return err
	}
	base, err := catalogBaseURL(source.BaseURL)
	if err != nil {
		return err
	}
	workCtx, cancel := context.WithTimeout(ctx, availabilityCycleTimeout)
	defer cancel()

	client := *w.client
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client.Jar = jar
	timeout := time.Duration(source.RequestTimeoutMS) * time.Millisecond

	body, err := account.LoginBody()
	if err != nil {
		return err
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	loginResponse, err := w.availabilityExchange(workCtx, &client, timeout, http.MethodPost,
		resolveCatalogPath(base, "/api/user/login"), body, header)
	if err != nil {
		return err
	}
	login, err := ParseAICostLogin(loginResponse)
	if err != nil {
		return err
	}
	header = http.Header{}
	header.Set("New-Api-User", strconv.FormatUint(login.UserID, 10))

	var failures []error
	itemsByModel := make(map[string]UpstreamAvailability)
	for _, category := range availabilityCategories {
		target := availabilityURL(base, category)
		response, err := w.availabilityExchange(workCtx, &client, timeout, http.MethodGet, target, nil, header)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", category, err))
			continue
		}
		items, err := ParseAICostAvailability(response, category)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", category, err))
			continue
		}
		for _, item := range items {
			key := strings.ToLower(strings.TrimSpace(item.ModelCode))
			current, exists := itemsByModel[key]
			if !exists || item.HasData && !current.HasData || item.HasData == current.HasData && item.ObservedAt.After(current.ObservedAt) {
				itemsByModel[key] = item
			}
		}
	}
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	rows := make([]repository.UpstreamAvailabilityRow, 0, len(itemsByModel))
	for _, item := range itemsByModel {
		if !item.HasData {
			continue
		}
		rows = append(rows, repository.UpstreamAvailabilityRow{
			ModelCode: item.ModelCode, Category: item.Category, SuccessRate: item.SuccessRate,
			AverageCompletionSeconds: item.AverageCompletionSeconds,
			WindowMinutes:            item.WindowMinutes, ObservedAt: item.ObservedAt,
		})
	}
	if err := w.store.ReplaceUpstreamAvailability(workCtx, source.SourceID, rows); err != nil {
		return err
	}
	return errors.Join(failures...)
}

func availabilityURL(base *url.URL, category string) string {
	target := *base
	target.Path, target.RawPath, target.Fragment = AICostAvailabilityPath, "", ""
	query := url.Values{}
	query.Set("category", category)
	query.Set("limit", availabilityRowLimit)
	target.RawQuery = query.Encode()
	return target.String()
}

// availabilityExchange performs one provider call. Unlike the discovery path it
// records no control-plane request log: there is no discovery run to attribute
// the call to, and a cache refresh produces no catalog evidence.
func (w *Worker) availabilityExchange(ctx context.Context, client *http.Client, timeout time.Duration,
	method, target string, body []byte, header http.Header) ([]byte, error) {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(requestCtx, method, target, reader)
	if err != nil {
		return nil, err
	}
	for key, values := range header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.Join(ErrProviderRequest, err)
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxDiscoveryResponse+1))
	if readErr != nil || len(data) > maxDiscoveryResponse {
		clear(data)
		return nil, ErrProviderResponse
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		clear(data)
		return nil, ErrProviderResponse
	}
	return data, nil
}
