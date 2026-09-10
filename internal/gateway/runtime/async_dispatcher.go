package runtime

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/safeurl"
)

type AsyncRequest struct {
	Method, Path string
	Body         []byte
	Header       http.Header
	// CredentialHeader and CredentialPrefix describe a catalog-pinned header
	// mapping. The codec never receives the secret itself.
	CredentialHeader string
	CredentialPrefix string
}

type AsyncObservation struct {
	TaskID  string
	State   execution.AsyncState
	Result  []byte
	Facts   billing.Facts
	Sources []delivery.RemoteResult
}

// Codecs perform no network I/O. The dispatcher is the sole HTTP sender, so
// every exchange is authorized and logged before it can reach the provider.
type AsyncCodec interface {
	Prepare(context.Context, string, repository.AsyncDispatch, []byte, string) (AsyncRequest, error)
	Decode(string, []byte, []byte) (AsyncObservation, error)
}

// CallbackCodec optionally decodes a provider callback. A codec may reuse its
// query decoder when callback and polling payloads share the same contract.
type CallbackCodec interface {
	DecodeCallback([]byte, []byte) (AsyncObservation, error)
}

// DispatchAwareAsyncCodec is used by declaration-driven adapters whose
// response mapping is stored in the immutable catalog product.
type DispatchAwareAsyncCodec interface {
	DecodeWithDispatch(string, repository.AsyncDispatch, []byte, []byte) (AsyncObservation, error)
}

type DispatchAwareCallbackCodec interface {
	DecodeCallbackWithDispatch(repository.AsyncDispatch, []byte, []byte) (AsyncObservation, error)
}

// CallbackTokenCodec explicitly opts an adapter into receiving the
// execution-scoped callback token. Adapters that do not implement this
// interface never see the token, even when a binding row exists.
type CallbackTokenCodec interface {
	InjectCallbackToken(context.Context, repository.AsyncDispatch, AsyncRequest, string) (AsyncRequest, error)
}

type AsyncKeys struct {
	CredentialKEK, CredentialHMAC []byte
	PayloadKEK, PayloadHMAC       []byte
}

type AsyncDispatcher struct {
	service *Service
	client  *http.Client
	keys    AsyncKeys
	codecs  map[string]AsyncCodec
}

const maxCapturedExchangeBody = 4 << 20

// HandleCallback implements CallbackOutboxHandler. Authentication and payload
// persistence happened at ingress; this method only decrypts, decodes and
// applies the resulting provider facts.
func (d *AsyncDispatcher) HandleCallback(ctx context.Context, item repository.OutboxItem) error {
	if item.CallbackReceiptID == 0 {
		return repository.ErrInvalidInput
	}
	receipt, err := d.service.Store.ReadCallbackReceipt(ctx, d.service.Store.DB(), item.CallbackReceiptID)
	if err != nil {
		return err
	}
	if receipt.EncryptedPayloadBlobID == 0 {
		return ErrCallbackNeedsQuery
	}
	fixed, err := d.service.Store.ReadAsyncDispatch(ctx, receipt.AsyncExecutionID)
	if err != nil {
		return err
	}
	codec, ok := d.codecs[fmt.Sprintf("%s@%d", fixed.AdapterCode, fixed.AdapterVersion)]
	if !ok {
		return &PermanentDispatchError{Code: "unsupported_async_protocol"}
	}
	payload, err := d.openBlob(ctx, receipt.EncryptedPayloadBlobID, fmt.Sprintf("async:%d:callback:%s", receipt.AsyncExecutionID, receipt.EventHMAC), "gateway-callback-payload", false)
	if err != nil {
		return err
	}
	defer clear(payload)
	request, err := d.openBlob(ctx, fixed.RequestBlobID, fmt.Sprintf("call:%d:request", fixed.CallID), "gateway-payload", false)
	if err != nil {
		return err
	}
	defer clear(request)
	var observation AsyncObservation
	if callbackCodec, ok := codec.(DispatchAwareCallbackCodec); ok {
		observation, err = callbackCodec.DecodeCallbackWithDispatch(fixed, request, payload)
	} else if callbackCodec, ok := codec.(CallbackCodec); ok {
		observation, err = callbackCodec.DecodeCallback(request, payload)
	} else {
		return ErrCallbackNeedsQuery
	}
	if err != nil {
		return &PermanentDispatchError{Code: "invalid_callback_payload"}
	}
	if err := d.validateResultSources(ctx, fixed, observation.Sources); err != nil {
		return err
	}
	if observation.TaskID != "" && fixed.TaskIdentityBlobID != 0 {
		identity, err := d.openBlob(ctx, fixed.TaskIdentityBlobID, fmt.Sprintf("async:%d:task-identity", receipt.AsyncExecutionID), "gateway-task-identity", false)
		if err != nil {
			return err
		}
		defer clear(identity)
		if observation.TaskID != string(identity) {
			return &PermanentDispatchError{Code: "provider_task_identity_mismatch"}
		}
	}
	if observation.State == execution.AsyncAccepted || observation.State == execution.AsyncRunning {
		return ErrCallbackNeedsQuery
	}
	if observation.State != execution.AsyncSucceeded && observation.State != execution.AsyncFailed && observation.State != execution.AsyncCancelled {
		return &PermanentDispatchError{Code: "invalid_callback_state"}
	}
	keys := repository.BlobInput{Plaintext: observation.Result, KEK: d.keys.PayloadKEK, HMACKey: d.keys.PayloadHMAC}
	if err := d.service.Store.DB().QueryRowContext(ctx, `SELECT k.id,k.current_version FROM crypto_keyring_state k JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version AND v.status='current' WHERE k.purpose='gateway-payload'`).Scan(&keys.KeyringID, &keys.KEKVersion); err != nil {
		return err
	}
	return d.service.ApplyCallbackObservation(ctx, item, CallbackObservation{AsyncExecutionID: receipt.AsyncExecutionID, ReceiptID: receipt.ID, State: observation.State, TaskID: observation.TaskID, Result: observation.Result, Facts: observation.Facts, Sources: observation.Sources, SourceURLPolicy: fixed.SourceURLPolicy, PayloadHMAC: receipt.PayloadHMAC}, fixed, keys)
}

func NewAsyncDispatcher(service *Service, client *http.Client, keys AsyncKeys, codecs map[string]AsyncCodec) (*AsyncDispatcher, error) {
	if service == nil || service.Store == nil || len(codecs) == 0 {
		return nil, repository.ErrInvalidInput
	}
	for _, key := range [][]byte{keys.CredentialKEK, keys.CredentialHMAC, keys.PayloadKEK, keys.PayloadHMAC} {
		if len(key) != security.KeySize {
			return nil, repository.ErrInvalidInput
		}
	}
	registered := make(map[string]AsyncCodec, len(codecs))
	for name, codec := range codecs {
		if name == "" || codec == nil {
			return nil, repository.ErrInvalidInput
		}
		registered[name] = codec
	}
	var configured http.Client
	if client != nil {
		// A supplied client is an explicit in-process transport injection used
		// by isolated integration tests. Production passes nil and receives the
		// connection-time public-address policy below.
		configured = *client
	} else {
		configured = *safeurl.NewClient(0)
	}
	configured.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &AsyncDispatcher{service: service, client: &configured, keys: AsyncKeys{
		CredentialKEK: bytes.Clone(keys.CredentialKEK), CredentialHMAC: bytes.Clone(keys.CredentialHMAC),
		PayloadKEK: bytes.Clone(keys.PayloadKEK), PayloadHMAC: bytes.Clone(keys.PayloadHMAC),
	}, codecs: registered}, nil
}

func (d *AsyncDispatcher) Recover(ctx context.Context, item repository.OutboxItem) error {
	return d.Dispatch(ctx, item)
}

func (d *AsyncDispatcher) Dispatch(ctx context.Context, item repository.OutboxItem) error {
	if item.AsyncExecutionID == 0 || item.CallID != 0 || item.AttemptID != 0 {
		return &PermanentDispatchError{Code: "unsupported_outbox_parent"}
	}
	if item.Action != "submit" && item.Action != "query" && item.Action != "recover" {
		return &PermanentDispatchError{Code: "unsupported_async_action"}
	}
	handled, err := d.service.PrepareAsyncDispatch(ctx, item)
	if err != nil || handled {
		return err
	}
	if item.Action == "recover" {
		// No configured provider lookup can prove a task without its ID. Do not
		// turn an X-Request-ID header into an invented idempotency guarantee.
		return d.service.RequireAsyncManualReview(ctx, item)
	}
	fixed, err := d.service.Store.ReadAsyncDispatch(ctx, item.AsyncExecutionID)
	if err != nil {
		return err
	}
	codec, ok := d.codecs[fmt.Sprintf("%s@%d", fixed.AdapterCode, fixed.AdapterVersion)]
	if !ok {
		return &PermanentDispatchError{Code: "unsupported_async_protocol"}
	}
	body, err := d.openBlob(ctx, fixed.RequestBlobID, fmt.Sprintf("call:%d:request", fixed.CallID), "gateway-payload", false)
	if err != nil {
		return err
	}
	defer clear(body)
	var taskID []byte
	if item.Action == "query" {
		if fixed.TaskIdentityBlobID == 0 {
			return &PermanentDispatchError{Code: "missing_upstream_task_identity"}
		}
		taskID, err = d.openBlob(ctx, fixed.TaskIdentityBlobID, fmt.Sprintf("async:%d:task-identity", item.AsyncExecutionID), "gateway-task-identity", false)
		if err != nil {
			return err
		}
		defer clear(taskID)
	}
	prepared, err := codec.Prepare(ctx, item.Action, fixed, body, string(taskID))
	if err != nil {
		return &PermanentDispatchError{Code: "invalid_async_request"}
	}
	if item.Action == "submit" && fixed.CallbackBindingTokenBlobID != 0 {
		tokenCodec, ok := codec.(CallbackTokenCodec)
		if !ok {
			// A persisted binding is an explicit promise that the provider
			// receives an execution-scoped token.  Silently omitting it would
			// send an unauthenticated callback configuration and leave the
			// execution impossible to reconcile.
			return &PermanentDispatchError{Code: "callback_token_unsupported"}
		}
		token, tokenErr := d.openBlob(ctx, fixed.CallbackBindingTokenBlobID, fmt.Sprintf("async:%d:callback-binding-token", item.AsyncExecutionID), "gateway-callback-binding-token", false)
		if tokenErr != nil {
			return tokenErr
		}
		prepared, err = tokenCodec.InjectCallbackToken(ctx, fixed, prepared, string(token))
		clear(token)
		if err != nil {
			return &PermanentDispatchError{Code: "invalid_callback_token_mapping"}
		}
	}
	defer clear(prepared.Body)
	if len(prepared.Body) > maxCapturedExchangeBody {
		return &PermanentDispatchError{Code: "provider_request_body_too_large"}
	}
	secret, err := d.openBlob(ctx, fixed.CredentialBlobID, fmt.Sprintf("credential:%d", fixed.CredentialID), "credential", true)
	if err != nil {
		return err
	}
	defer clear(secret)
	request, err := d.prepareHTTP(ctx, fixed, prepared, secret)
	if err != nil {
		return err
	}
	// Leave time to durably record the HTTP outcome before the lease expires.
	timeout := min(time.Duration(min(fixed.TimeoutMS, uint64(300000)))*time.Millisecond, time.Until(item.LeaseExpiresAt)-5*time.Second)
	if timeout <= 0 {
		return context.DeadlineExceeded
	}
	workCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request = request.WithContext(workCtx)
	mapping := security.DomainDigest(d.keys.PayloadHMAC, "async-request-mapping-v1", []byte(fixed.Protocol), []byte(prepared.Method), []byte(request.URL.String()), []byte(prepared.CredentialHeader), []byte(prepared.CredentialPrefix), prepared.Body)
	var requestPayload *repository.BlobInput
	if len(prepared.Body) != 0 {
		blob, blobErr := d.payloadBlob(ctx, prepared.Body)
		if blobErr != nil {
			return blobErr
		}
		requestPayload = &blob
	}
	requestID, err := d.service.BeginAsyncRequest(ctx, item, AsyncRequestEvidence{
		MappingHMAC: hex.EncodeToString(mapping[:]), RequestHMAC: d.digest(prepared.Body), Payload: requestPayload,
	})
	if err != nil {
		return err
	}
	responseBody, response := d.exchange(request)
	defer clear(responseBody)
	// Shutdown cancellation must not discard a received provider response.
	markCtx, markCancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
	defer markCancel()
	if len(responseBody) != 0 {
		blob, blobErr := d.payloadBlob(markCtx, responseBody)
		if blobErr != nil {
			return blobErr
		}
		response.ResponsePayload = &blob
	}
	if response.ErrorCode != "" {
		return d.exchangeFailure(markCtx, item, requestID, response)
	}
	var observation AsyncObservation
	if aware, ok := codec.(DispatchAwareAsyncCodec); ok {
		observation, err = aware.DecodeWithDispatch(item.Action, fixed, body, responseBody)
	} else {
		observation, err = codec.Decode(item.Action, body, responseBody)
	}
	if err != nil {
		response.ErrorCode = "invalid_provider_response"
		return d.exchangeFailure(markCtx, item, requestID, response)
	}
	if err := d.validateResultSources(markCtx, fixed, observation.Sources); err != nil {
		response.ErrorCode = "provider_result_host_not_allowed"
		if finishErr := d.service.FinishRequest(markCtx, requestID, "response_recorded", response); finishErr != nil {
			return finishErr
		}
		return err
	}
	if item.Action == "query" && observation.TaskID != "" && observation.TaskID != string(taskID) {
		response.ErrorCode = "provider_task_identity_mismatch"
		return d.exchangeFailure(markCtx, item, requestID, response)
	}
	if item.Action == "submit" {
		if strings.TrimSpace(observation.TaskID) == "" {
			response.ErrorCode = "missing_provider_task_id"
			return d.exchangeFailure(markCtx, item, requestID, response)
		}
		blob, err := d.payloadBlob(markCtx, []byte(observation.TaskID))
		if err != nil {
			return err
		}
		_, err = d.service.AcceptAsyncSubmission(markCtx, AcceptAsyncInput{Item: item, RequestID: requestID, TaskIdentity: blob,
			IdentityExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour), QueryAt: time.Now().UTC().Add(5 * time.Second), Response: response})
		return err
	}
	input := AsyncResultInput{Item: item, RequestID: requestID, Response: response, State: observation.State,
		Facts: observation.Facts, Sources: observation.Sources, SourceURLPolicy: fixed.SourceURLPolicy, NextQuery: time.Now().UTC().Add(5 * time.Second)}
	if observation.State == execution.AsyncSucceeded {
		blob, err := d.payloadBlob(markCtx, observation.Result)
		if err != nil {
			return err
		}
		input.Result = &blob
	}
	err = d.service.RecordAsyncResult(markCtx, input)
	return err
}

func (d *AsyncDispatcher) exchange(request *http.Request) ([]byte, repository.RequestLogResult) {
	started := time.Now()
	var result repository.RequestLogResult
	response, err := d.client.Do(request)
	if err != nil {
		duration := uint64(time.Since(started).Milliseconds())
		result.DurationMS, result.ErrorCode = &duration, "provider_exchange_unknown"
		return nil, result
	}
	defer response.Body.Close()
	status := uint16(response.StatusCode)
	result.HTTPStatus, result.RequestComplete = &status, true
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCapturedExchangeBody+1))
	duration := uint64(time.Since(started).Milliseconds())
	result.DurationMS = &duration
	result.ResponseBytesHMAC = d.digest(body)
	if err != nil || len(body) > maxCapturedExchangeBody {
		result.ErrorCode = "provider_response_incomplete"
		if len(body) > maxCapturedExchangeBody {
			body = body[:maxCapturedExchangeBody]
		}
		return body, result
	}
	result.ResponseComplete = true
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.ErrorCode = "provider_http_error"
	}
	return body, result
}

func (d *AsyncDispatcher) exchangeFailure(ctx context.Context, item repository.OutboxItem, requestID uint64, response repository.RequestLogResult) error {
	if item.Action == "submit" {
		if definitiveSubmissionRejection(response) {
			return d.service.RecordAsyncSubmissionNotCreated(ctx, item, requestID, response)
		}
		return d.service.RecordAsyncSubmissionUnknown(ctx, item, requestID, response)
	}
	status := "unknown"
	if response.ResponseComplete {
		status = "response_recorded"
	}
	if err := d.service.FinishRequest(ctx, requestID, status, response); err != nil {
		return err
	}
	return errors.New(response.ErrorCode)
}

func definitiveSubmissionRejection(response repository.RequestLogResult) bool {
	return response.HTTPStatus != nil && (*response.HTTPStatus < http.StatusOK || *response.HTTPStatus >= http.StatusMultipleChoices) && response.RequestComplete && response.ResponseComplete
}

func (d *AsyncDispatcher) openBlob(ctx context.Context, id uint64, owner, purpose string, credential bool) ([]byte, error) {
	envelope, err := d.service.Store.ReadEncryptedBlob(ctx, d.service.Store.DB(), id)
	if err != nil {
		return nil, err
	}
	if envelope.Purpose != purpose || envelope.SchemaVersion != 1 {
		return nil, security.ErrAuthentication
	}
	kek, hmac := d.keys.PayloadKEK, d.keys.PayloadHMAC
	if credential {
		kek, hmac = d.keys.CredentialKEK, d.keys.CredentialHMAC
	}
	return repository.OpenBlob(envelope, id, []byte(owner), kek, hmac)
}

func (d *AsyncDispatcher) payloadBlob(ctx context.Context, body []byte) (repository.BlobInput, error) {
	retentionUntil := time.Now().UTC().Add(config.APICallPayloadRetentionDuration())
	blob := repository.BlobInput{Plaintext: body, KEK: d.keys.PayloadKEK, HMACKey: d.keys.PayloadHMAC, RetentionUntil: &retentionUntil}
	err := d.service.Store.DB().QueryRowContext(ctx, `SELECT k.id,k.current_version FROM crypto_keyring_state k JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version AND v.status='current' WHERE k.purpose='gateway-payload'`).Scan(&blob.KeyringID, &blob.KEKVersion)
	return blob, err
}

func (d *AsyncDispatcher) digest(body []byte) string {
	digest := security.HMACSHA256(d.keys.PayloadHMAC, body)
	return hex.EncodeToString(digest[:])
}

func (d *AsyncDispatcher) validateResultSources(ctx context.Context, fixed repository.AsyncDispatch, sources []delivery.RemoteResult) error {
	if len(sources) == 0 {
		return nil
	}
	var resourceKind string
	if err := d.service.Store.DB().QueryRowContext(ctx, `SELECT resource_kind FROM gw_api_resources WHERE call_id=?`, fixed.CallID).Scan(&resourceKind); err != nil {
		return err
	}
	if err := delivery.ValidateSourcesForResource(resourceKind, sources); err != nil {
		return &PermanentDispatchError{Code: "invalid_provider_result"}
	}
	for _, source := range sources {
		if source.URL == "" {
			continue
		}
		parsed, err := url.Parse(source.URL)
		if err != nil {
			return &PermanentDispatchError{Code: "invalid_provider_result"}
		}
		port := parsed.Port()
		if port == "" {
			port = "443"
			if parsed.Scheme == "http" {
				port = "80"
			}
		}
		host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		var allowed int
		if err := d.service.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_transport_allowed_hosts WHERE release_id=? AND channel_transport_id=? AND protocol=? AND host_pattern=? AND port=?`, fixed.ReleaseID, fixed.ChannelTransportID, parsed.Scheme, host, port).Scan(&allowed); err != nil {
			return err
		}
		if allowed == 0 {
			return &PermanentDispatchError{Code: "provider_result_host_not_allowed"}
		}
	}
	return nil
}

func (d *AsyncDispatcher) prepareHTTP(ctx context.Context, fixed repository.AsyncDispatch, prepared AsyncRequest, secret []byte) (*http.Request, error) {
	base, err := url.Parse(fixed.BaseURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Scheme != "https" && base.Scheme != "http" {
		return nil, &PermanentDispatchError{Code: "invalid_async_origin"}
	}
	path, err := url.Parse(prepared.Path)
	if err != nil || !strings.HasPrefix(prepared.Path, "/") || path.IsAbs() || path.Host != "" || path.Fragment != "" {
		return nil, &PermanentDispatchError{Code: "invalid_async_path"}
	}
	target, err := url.Parse(strings.TrimRight(base.String(), "/") + prepared.Path)
	if err != nil || target.Host != base.Host || target.Scheme != base.Scheme {
		return nil, &PermanentDispatchError{Code: "invalid_async_target"}
	}
	port := target.Port()
	if port == "" {
		port = "443"
		if target.Scheme == "http" {
			port = "80"
		}
	}
	var allowed int
	if err := d.service.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_transport_allowed_hosts WHERE release_id=? AND channel_transport_id=? AND protocol=? AND host_pattern=? AND port=?`, fixed.ReleaseID, fixed.ChannelTransportID, target.Scheme, target.Hostname(), port).Scan(&allowed); err != nil {
		return nil, err
	}
	if allowed == 0 {
		return nil, &PermanentDispatchError{Code: "async_host_not_allowed"}
	}
	request, err := http.NewRequestWithContext(ctx, prepared.Method, target.String(), bytes.NewReader(prepared.Body))
	if err != nil {
		return nil, &PermanentDispatchError{Code: "invalid_async_http_request"}
	}
	request.Header = prepared.Header.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	switch fixed.AuthScheme {
	case "bearer":
		header, prefix := "Authorization", "Bearer "
		if prepared.CredentialHeader != "" {
			header, prefix = prepared.CredentialHeader, prepared.CredentialPrefix
		}
		if !validCredentialHeader(header) || len(prefix) > 256 || strings.ContainsAny(prefix, "\r\n") || request.Header.Get(header) != "" {
			return nil, &PermanentDispatchError{Code: "invalid_async_auth_mapping"}
		}
		request.Header.Set(header, prefix+string(secret))
	default:
		return nil, &PermanentDispatchError{Code: "unsupported_async_auth"}
	}
	return request, nil
}

func validCredentialHeader(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", character) {
			continue
		}
		return false
	}
	return true
}
