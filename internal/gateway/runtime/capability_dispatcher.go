package runtime

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/payloadview"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/pkg/safeurl"
)

const maxCapabilityResponseBytes = 128 << 20

type CapabilityDecode func([]byte) (AsyncObservation, error)

type CapabilityDispatchInput struct {
	CallID, AttemptID uint64
	AdapterKey        string
	Prepared          AsyncRequest
	Decode            CapabilityDecode
	ObserveResponse   func([]byte) error
}

// ProviderCapabilityError is returned after the corresponding failure fact
// has already been committed to the unified lifecycle.
type ProviderCapabilityError struct {
	HTTPStatus int
	Code       string
	Message    string
}

func (e *ProviderCapabilityError) Error() string {
	if e == nil || e.Code == "" {
		return "provider capability request failed"
	}
	return "provider capability request failed: " + e.Code
}

type CapabilityDispatcher struct {
	service *Service
	client  *http.Client
	keys    AsyncKeys
	network *AsyncDispatcher
}

func NewCapabilityDispatcher(service *Service, client *http.Client, keys AsyncKeys) (*CapabilityDispatcher, error) {
	if service == nil || service.Store == nil {
		return nil, repository.ErrInvalidInput
	}
	if err := validateAsyncKeys(keys); err != nil {
		return nil, err
	}
	var configured http.Client
	if client != nil {
		configured = *client
	} else {
		configured = *safeurl.NewClient(0)
	}
	configured.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	copied := AsyncKeys{
		CredentialKEK:  append([]byte(nil), keys.CredentialKEK...),
		CredentialHMAC: append([]byte(nil), keys.CredentialHMAC...),
		PayloadKEK:     append([]byte(nil), keys.PayloadKEK...),
		PayloadHMAC:    append([]byte(nil), keys.PayloadHMAC...),
	}
	network := &AsyncDispatcher{service: service, client: &configured, keys: copied}
	return &CapabilityDispatcher{service: service, client: &configured, keys: copied, network: network}, nil
}

func (d *CapabilityDispatcher) Close() {
	if d == nil {
		return
	}
	clear(d.keys.CredentialKEK)
	clear(d.keys.CredentialHMAC)
	clear(d.keys.PayloadKEK)
	clear(d.keys.PayloadHMAC)
}

// Dispatch authorizes exactly one synchronous provider exchange and commits
// its request evidence, deliveries, result payload, execution states and bill.
func (d *CapabilityDispatcher) Dispatch(ctx context.Context, in CapabilityDispatchInput) error {
	if d == nil || d.service == nil || in.CallID == 0 || in.AttemptID == 0 ||
		in.AdapterKey == "" || in.Decode == nil || in.Prepared.Method == "" ||
		in.Prepared.Path == "" || len(in.Prepared.Body) == 0 {
		return repository.ErrInvalidInput
	}
	fixed, err := d.service.Store.ReadCapabilityDispatch(ctx, in.AttemptID)
	if err != nil {
		return err
	}
	if fixed.CallID != in.CallID || fmt.Sprintf("%s@%d", fixed.AdapterCode, fixed.AdapterVersion) != in.AdapterKey ||
		fixed.Method != in.Prepared.Method || fixed.Path != in.Prepared.Path {
		return d.rejectBeforeDispatch(ctx, in.AttemptID, "prepared_request_mismatch", repository.ErrConflict)
	}
	asyncFixed := repository.AsyncDispatch{
		AdapterCode: fixed.AdapterCode, AdapterVersion: fixed.AdapterVersion,
		CallID: fixed.CallID, AttemptID: fixed.AttemptID, CredentialID: fixed.CredentialID,
		CredentialBlobID: fixed.CredentialBlobID, CredentialSecret: append([]byte(nil), fixed.CredentialSecret...), RequestBlobID: fixed.RequestBlobID,
		ChannelTransportID: fixed.ChannelTransportID, ReleaseID: fixed.ReleaseID,
		PublicID: fixed.PublicID, Protocol: fixed.Protocol, BaseURL: fixed.BaseURL,
		Method: fixed.Method, Path: fixed.Path, AuthScheme: fixed.AuthScheme,
		VendorModel: fixed.VendorModel, DeliveryMode: fixed.DeliveryMode,
		SourceURLPolicy: fixed.SourceURLPolicy, TimeoutMS: uint64(fixed.TimeoutMS),
	}
	secret, err := d.network.openCredential(ctx, fixed.CredentialID, fixed.CredentialSecret, fixed.CredentialBlobID)
	if err != nil {
		return d.rejectBeforeDispatch(ctx, in.AttemptID, "credential_unavailable", err)
	}
	defer clear(secret)
	if strings.ContainsAny(string(secret), "\r\n") {
		return d.rejectBeforeDispatch(ctx, in.AttemptID, "invalid_credential", repository.ErrInvalidInput)
	}
	timeout := time.Duration(fixed.TimeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > 5*time.Minute {
		timeout = 5 * time.Minute
	}
	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	request, err := d.network.prepareHTTP(workCtx, asyncFixed, in.Prepared, secret)
	if err != nil {
		return d.rejectBeforeDispatch(ctx, in.AttemptID, "invalid_provider_target", err)
	}
	mapping := security.DomainDigest(d.keys.PayloadHMAC, "capability-request-mapping-v1",
		[]byte(fixed.Protocol), []byte(in.Prepared.Method), []byte(request.URL.String()), in.Prepared.Body)
	evidence := AttemptRequestEvidence{
		MappingHMAC:      hex.EncodeToString(mapping[:]),
		RequestBytesHMAC: d.network.digest(in.Prepared.Body),
	}
	if captureCapabilityRequestPayload(in.Prepared.Header.Get("Content-Type"), in.Prepared.Body) {
		blob, blobErr := d.network.payloadBlob(ctx, in.Prepared.Body)
		if blobErr != nil {
			return d.rejectBeforeDispatch(ctx, in.AttemptID, "request_evidence_unavailable", blobErr)
		}
		evidence.Payload = &blob
	}
	requestID, err := d.service.BeginRequestWithPayload(ctx, repository.RequestLogInput{
		AttemptID: &in.AttemptID, RequestSeq: 1, Action: "submit",
		MappingHMAC: evidence.MappingHMAC, RequestBytesHMAC: evidence.RequestBytesHMAC,
	}, evidence.Payload)
	if err != nil {
		return d.rejectBeforeDispatch(ctx, in.AttemptID, "request_authorization_failed", err)
	}
	body, exchange := d.exchange(request, in.ObserveResponse)
	defer clear(body)
	if err := d.attachResponsePayload(context.WithoutCancel(ctx), body, &exchange); err != nil {
		return d.failAfterAuthorization(ctx, in.AttemptID, requestID, exchange, true, err)
	}
	if exchange.ErrorCode != "" {
		uncertain := exchange.HTTPStatus == nil || !exchange.RequestComplete || !exchange.ResponseComplete
		return d.failAfterAuthorization(ctx, in.AttemptID, requestID, exchange, uncertain,
			providerCapabilityError(exchange, body, exchange.ErrorCode))
	}
	observation, err := in.Decode(body)
	if err != nil || observation.State != execution.AsyncSucceeded || len(observation.Result) == 0 || len(observation.Sources) == 0 {
		return d.failAfterAuthorization(ctx, in.AttemptID, requestID, exchange, false,
			providerCapabilityError(exchange, body, "invalid_provider_response"))
	}
	// Adapter decoders do not know the exchange duration. Add it only for a
	// variable already declared by the published adapter manifest.
	observation.Facts = enrichExpressionFacts(observation.Facts, exchange.DurationMS)
	if err := d.network.validateResultSources(ctx, asyncFixed, observation.Sources); err != nil {
		return d.failAfterAuthorization(ctx, in.AttemptID, requestID, exchange, false, err)
	}
	resultBlob, err := d.network.payloadBlob(ctx, observation.Result)
	if err != nil {
		return d.failAfterAuthorization(ctx, in.AttemptID, requestID, exchange, true, err)
	}
	completion := CapabilitySuccessInput{
		AttemptID: in.AttemptID, RequestID: requestID, Exchange: exchange,
		Result: resultBlob, Sources: observation.Sources,
		SourceURLPolicy: fixed.SourceURLPolicy, Facts: observation.Facts,
	}
	if err := d.service.CompleteCapability(context.WithoutCancel(ctx), completion); err != nil {
		return d.failAfterAuthorization(ctx, in.AttemptID, requestID, exchange, true, err)
	}
	return nil
}

func (d *CapabilityDispatcher) attachResponsePayload(ctx context.Context, body []byte, result *repository.RequestLogResult) error {
	if len(body) == 0 {
		return nil
	}
	blob, err := d.network.payloadBlob(ctx, body)
	if err != nil {
		return err
	}
	result.ResponsePayload = &blob
	return nil
}

func providerCapabilityError(exchange repository.RequestLogResult, body []byte, code string) *ProviderCapabilityError {
	status := http.StatusBadGateway
	if exchange.HTTPStatus != nil {
		status = int(*exchange.HTTPStatus)
	}
	message := ""
	if exchange.ResponseComplete && len(body) <= maxCapturedExchangeBody {
		message = payloadview.ExtractFailureMessage(body)
	}
	return &ProviderCapabilityError{HTTPStatus: status, Code: code, Message: message}
}

func captureCapabilityRequestPayload(contentType string, body []byte) bool {
	mediaType := strings.TrimSpace(strings.SplitN(strings.ToLower(contentType), ";", 2)[0])
	return mediaType == "application/json" && len(body) <= maxCapturedExchangeBody
}

func (d *CapabilityDispatcher) exchange(request *http.Request, observe func([]byte) error) ([]byte, repository.RequestLogResult) {
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
	reader := io.Reader(response.Body)
	if observe != nil {
		reader = io.TeeReader(reader, responseObserverWriter(observe))
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, maxCapabilityResponseBytes+1))
	duration := uint64(time.Since(started).Milliseconds())
	result.DurationMS = &duration
	result.ResponseBytesHMAC = d.network.digest(body)
	if readErr != nil || len(body) > maxCapabilityResponseBytes {
		result.ErrorCode = "provider_response_incomplete"
		if len(body) > maxCapabilityResponseBytes {
			body = body[:maxCapabilityResponseBytes]
		}
		return body, result
	}
	result.ResponseComplete = true
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		result.ErrorCode = "provider_http_error"
	}
	return body, result
}

type responseObserverWriter func([]byte) error

func (write responseObserverWriter) Write(data []byte) (int, error) {
	if write != nil && len(data) != 0 {
		if err := write(data); err != nil {
			return len(data), err
		}
	}
	return len(data), nil
}

func (d *CapabilityDispatcher) failAfterAuthorization(ctx context.Context, attemptID, requestID uint64, exchange repository.RequestLogResult, uncertain bool, returned error) error {
	reason := exchange.ErrorCode
	if reason == "" {
		reason = "invalid_provider_response"
	}
	facts := billing.Facts{}
	if exchange.HTTPStatus != nil && *exchange.HTTPStatus >= 200 && *exchange.HTTPStatus < 300 {
		facts.Events = map[billing.ChargeEvent]bool{billing.ChargeAccepted: true}
	}
	finishErr := d.service.FailCapability(context.WithoutCancel(ctx), CapabilityFailureInput{
		AttemptID: attemptID, RequestID: requestID, Exchange: exchange,
		Reason: reason, Facts: facts, Uncertain: uncertain,
	})
	if finishErr != nil {
		return errors.Join(returned, finishErr)
	}
	return returned
}

func (d *CapabilityDispatcher) rejectBeforeDispatch(ctx context.Context, attemptID uint64, reason string, returned error) error {
	if err := d.service.RejectCapability(context.WithoutCancel(ctx), attemptID, reason); err != nil {
		return errors.Join(returned, err)
	}
	return returned
}
