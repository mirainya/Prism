package catalogsource

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/pkg/safeurl"
)

const (
	maxDiscoveryResponse = 8 << 20
	discoveryLease       = 5 * time.Minute
	discoveryFinalize    = 5 * time.Second
)

type WorkerKeys struct {
	CredentialKEK  []byte
	CredentialHMAC []byte
	EvidenceHMAC   []byte
}

type Worker struct {
	store     *repository.Store
	runtime   *gatewayruntime.Service
	client    *http.Client
	keys      WorkerKeys
	providers ProviderRegistry
}

func NewWorker(store *repository.Store, client *http.Client, keys WorkerKeys) (*Worker, error) {
	providers, err := DefaultProviderRegistry()
	if err != nil {
		return nil, err
	}
	return NewWorkerWithRegistry(store, client, keys, providers)
}

func NewWorkerWithRegistry(store *repository.Store, client *http.Client, keys WorkerKeys, providers ProviderRegistry) (*Worker, error) {
	if store == nil || len(keys.EvidenceHMAC) != security.KeySize ||
		(len(keys.CredentialKEK) == 0) != (len(keys.CredentialHMAC) == 0) ||
		len(keys.CredentialKEK) != 0 && (len(keys.CredentialKEK) != security.KeySize || len(keys.CredentialHMAC) != security.KeySize) ||
		len(providers.byContract) == 0 {
		return nil, repository.ErrInvalidInput
	}
	service, err := gatewayruntime.New(store)
	if err != nil {
		return nil, err
	}
	var configured http.Client
	if client == nil {
		configured = *safeurl.NewClient(0)
	} else {
		configured = *client
	}
	configured.Timeout = 0
	configured.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Worker{store: store, runtime: service, client: &configured, keys: WorkerKeys{
		CredentialKEK: bytes.Clone(keys.CredentialKEK), CredentialHMAC: bytes.Clone(keys.CredentialHMAC), EvidenceHMAC: bytes.Clone(keys.EvidenceHMAC),
	}, providers: providers}, nil
}

func (w *Worker) Close() {
	if w == nil {
		return
	}
	clear(w.keys.CredentialKEK)
	clear(w.keys.CredentialHMAC)
	clear(w.keys.EvidenceHMAC)
}

func (w *Worker) Run(ctx context.Context, owner string, report func(error)) error {
	if w == nil || !validText(owner, 128) {
		return repository.ErrInvalidInput
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		worked, err := w.ProcessOne(ctx, owner)
		if err != nil && !errors.Is(err, context.Canceled) && report != nil {
			report(err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *Worker) ProcessOne(ctx context.Context, owner string) (bool, error) {
	if w == nil || !validText(owner, 128) {
		return false, repository.ErrInvalidInput
	}
	run, found, err := w.store.ClaimCatalogDiscoveryRun(ctx, owner, discoveryLease)
	if err != nil || !found {
		return found, err
	}
	if err := w.process(ctx, run); err != nil {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discoveryFinalize)
		finishErr := w.store.FinishCatalogDiscoveryRun(finishCtx, run, "failed", discoveryFailureCode(err))
		cancel()
		return true, errors.Join(err, finishErr)
	}
	return true, nil
}

func (w *Worker) process(ctx context.Context, run repository.CatalogDiscoveryRun) error {
	provider, requestCount, ok := w.providers.Provider(run.ContractCode)
	if !ok {
		return ErrProviderNotRegistered
	}
	secret, err := w.credentialSecret(ctx, run.CredentialID, run.CredentialSecret, run.CredentialBlobID)
	if err != nil {
		return err
	}
	defer clear(secret)
	timeout := time.Duration(run.RequestTimeoutMS) * time.Millisecond
	workCtx, cancel := context.WithTimeout(ctx, timeout*time.Duration(requestCount)+5*time.Second)
	defer cancel()
	client := *w.client
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client.Jar = jar
	base, err := catalogBaseURL(run.BaseURL)
	if err != nil {
		return err
	}
	sequence, err := w.nextSequence(workCtx, run.ID)
	if err != nil {
		return err
	}
	nextSequence := sequence
	providerExchange := limitProviderExchange(requestCount, func(exchangeCtx context.Context, method, target string, body []byte, header http.Header) ([]byte, error) {
		current := nextSequence
		nextSequence++
		return w.exchange(exchangeCtx, &client, run, current, method, target, body, header)
	})
	result, err := provider.Discover(workCtx, DiscoveryProviderRequest{
		Contract: run.ContractCode, ExternalGroup: run.ExternalGroup,
		Secret: secret, BaseURL: base, Exchange: providerExchange,
	})
	if err != nil {
		return err
	}
	if len(result.Response) == 0 {
		return ErrProviderResponse
	}
	defer clear(result.Response)
	digest := security.HMACSHA256(w.keys.EvidenceHMAC, result.Response)
	items := make([]repository.CatalogDiscoveryItem, 0, len(result.Models))
	for _, model := range result.Models {
		items = append(items, repository.CatalogDiscoveryItem{
			Code: model.Code, Description: model.Description, Tags: model.Tags, VendorID: model.VendorID,
			ProviderQuotaType: model.ProviderQuotaType, ModelPrice: model.ModelPrice, ModelRatio: model.ModelRatio,
			CompletionRatio: model.CompletionRatio, OwnerBy: model.OwnerBy, PricingVersion: model.PricingVersion,
			Groups: model.Groups, EndpointTypes: model.EndpointTypes,
			SelectedGroupEnabled: model.SelectedGroupEnabled, PriceCandidate: model.PriceCandidate,
		})
	}
	_, err = w.store.SaveCatalogDiscoverySnapshot(workCtx, repository.CatalogDiscoverySnapshotInput{
		Run: run, ResponseHMAC: hex.EncodeToString(digest[:]), ResultSchemaVersion: 1,
		ObservedAt: time.Now().UTC(), Items: items,
	})
	return err
}

// credentialSecret prefers the direct operator-managed value. Older rows keep
// only an encrypted blob, which remains readable during the migration.
func (w *Worker) credentialSecret(ctx context.Context, credentialID uint64, direct []byte, blobID uint64) ([]byte, error) {
	if len(direct) != 0 {
		return append([]byte(nil), direct...), nil
	}
	if blobID == 0 {
		return nil, repository.ErrNotFound
	}
	envelope, err := w.store.ReadEncryptedBlob(ctx, w.store.DB(), blobID)
	if err != nil {
		return nil, err
	}
	if envelope.Purpose != "credential" {
		return nil, repository.ErrConflict
	}
	return repository.OpenBlob(envelope, blobID, []byte(fmt.Sprintf("credential:%d", credentialID)), w.keys.CredentialKEK, w.keys.CredentialHMAC)
}

func (w *Worker) nextSequence(ctx context.Context, runID uint64) (uint64, error) {
	var sequence uint64
	if err := w.store.DB().QueryRowContext(ctx, `SELECT COALESCE(MAX(request_seq),0)+1 FROM gw_channel_request_logs WHERE control_plane_run_id=?`, runID).Scan(&sequence); err != nil {
		return 0, err
	}
	return sequence, nil
}

func (w *Worker) exchange(ctx context.Context, client *http.Client, run repository.CatalogDiscoveryRun, sequence uint64, method, target string, body []byte, header http.Header) ([]byte, error) {
	mapping := security.DomainDigest(w.keys.EvidenceHMAC, "catalog-request-mapping-v1", []byte(run.ContractCode), []byte(method), []byte(target))
	requestDigest := security.HMACSHA256(w.keys.EvidenceHMAC, body)
	requestID, err := w.runtime.BeginRequest(ctx, repository.RequestLogInput{
		ControlPlaneRunID: &run.ID, RequestSeq: sequence, Action: "catalog_discovery",
		MappingHMAC: hex.EncodeToString(mapping[:]), RequestBytesHMAC: hex.EncodeToString(requestDigest[:]),
	})
	if err != nil {
		return nil, err
	}
	started := time.Now()
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		finishErr := w.finishRequest(ctx, requestID, "not_sent", repository.RequestLogResult{ErrorCode: "invalid_request"})
		return nil, errors.Join(ErrProviderRequest, finishErr)
	}
	request.Header = header.Clone()
	response, err := client.Do(request)
	if err != nil {
		duration := uint64(time.Since(started).Milliseconds())
		code := "network_error"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "provider_timeout"
		}
		finishErr := w.finishRequest(ctx, requestID, "unknown", repository.RequestLogResult{DurationMS: &duration, ErrorCode: code})
		return nil, errors.Join(ErrProviderRequest, finishErr)
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxDiscoveryResponse+1))
	duration := uint64(time.Since(started).Milliseconds())
	status := uint16(response.StatusCode)
	result := catalogResponseLogResult(status, duration, data, readErr, w.keys.EvidenceHMAC)
	if finishErr := w.finishRequest(ctx, requestID, "response_recorded", result); finishErr != nil {
		clear(data)
		return nil, finishErr
	}
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

func catalogResponseLogResult(status uint16, duration uint64, data []byte, readErr error, hmacKey []byte) repository.RequestLogResult {
	result := repository.RequestLogResult{HTTPStatus: &status, DurationMS: &duration, RequestComplete: true}
	if readErr == nil && len(data) <= maxDiscoveryResponse {
		result.ResponseComplete = true
		digest := security.HMACSHA256(hmacKey, data)
		result.ResponseBytesHMAC = hex.EncodeToString(digest[:])
	} else if len(data) > maxDiscoveryResponse {
		result.ErrorCode = "response_too_large"
	} else {
		result.ErrorCode = "response_read_failed"
	}
	if status < 200 || status >= 300 {
		result.ErrorCode = "provider_http_error"
	}
	return result
}

func (w *Worker) finishRequest(parent context.Context, requestID uint64, status string, result repository.RequestLogResult) error {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), discoveryFinalize)
	defer cancel()
	return w.runtime.FinishRequest(finishCtx, requestID, status, result)
}

func catalogBaseURL(raw string) (*url.URL, error) {
	value, err := url.Parse(raw)
	if err != nil || value.Scheme != "https" && value.Scheme != "http" || value.Hostname() == "" || value.User != nil || value.RawQuery != "" || value.Fragment != "" || value.Path != "" && value.Path != "/" {
		return nil, repository.ErrInvalidInput
	}
	return value, nil
}

func resolveCatalogPath(base *url.URL, path string) string {
	copy := *base
	copy.Path, copy.RawPath, copy.RawQuery, copy.Fragment = path, "", "", ""
	return copy.String()
}

func limitProviderExchange(maxRequests int, exchange ProviderExchange) ProviderExchange {
	var mu sync.Mutex
	requestCount := 0
	return func(ctx context.Context, method, target string, body []byte, header http.Header) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if exchange == nil || requestCount >= maxRequests {
			return nil, ErrProviderRequestLimit
		}
		requestCount++
		return exchange(ctx, method, target, body, header)
	}
}

func discoveryFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrInvalidSnapshot), errors.Is(err, ErrProviderResponse):
		return "invalid_provider_response"
	case errors.Is(err, context.DeadlineExceeded):
		return "provider_timeout"
	case errors.Is(err, ErrProviderRequest):
		return "provider_request_failed"
	case errors.Is(err, ErrProviderRequestLimit):
		return "provider_request_limit"
	default:
		return "discovery_failed"
	}
}

func clear(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
