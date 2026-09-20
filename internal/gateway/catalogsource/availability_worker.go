package catalogsource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
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
)

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
	var failures []error
	for _, binding := range w.providers.AvailabilityBindings() {
		sources, err := w.store.ListAvailabilitySources(ctx, binding.Contract)
		if err != nil {
			failures = append(failures, fmt.Errorf("contract %s: %w", binding.Contract, err))
			continue
		}
		for _, source := range sources {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := w.refreshSource(ctx, binding.Contract, binding.MaxRequests, binding.Provider, source); err != nil {
				failures = append(failures, fmt.Errorf("catalog source %d: %w", source.SourceID, err))
			}
		}
	}
	return errors.Join(failures...)
}

func (w *Worker) refreshSource(ctx context.Context, contract string, maxRequests int, provider CatalogAvailabilityProvider, source repository.AvailabilitySource) error {
	if provider == nil || maxRequests < 1 {
		return ErrProviderNotRegistered
	}
	secret, err := w.credentialSecret(ctx, source.CredentialID, source.CredentialSecret, source.CredentialBlobID)
	if err != nil {
		return err
	}
	defer clear(secret)
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
	exchange := limitProviderExchange(maxRequests, func(exchangeCtx context.Context, method, target string, body []byte, header http.Header) ([]byte, error) {
		return w.availabilityExchange(exchangeCtx, &client, timeout, method, target, body, header)
	})

	observations, err := provider.RefreshAvailability(workCtx, AvailabilityProviderRequest{
		Contract: contract, Secret: secret, BaseURL: base, Exchange: exchange,
	})
	if err != nil {
		return err
	}
	rows := make([]repository.UpstreamAvailabilityRow, 0, len(observations))
	for _, observation := range observations {
		rows = append(rows, repository.UpstreamAvailabilityRow{
			ModelCode: observation.ModelCode, Category: observation.Category,
			SuccessRate: observation.SuccessRate, AverageCompletionSeconds: observation.AverageCompletionSeconds,
			WindowMinutes: observation.WindowMinutes, ObservedAt: observation.ObservedAt,
		})
	}
	return w.store.ReplaceUpstreamAvailability(workCtx, source.SourceID, rows)
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
