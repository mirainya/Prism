package responses

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	openairesponses "github.com/mirainya/Prism/internal/gateway/codec/openai_responses"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/security"
	gatewaytransport "github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/filestorage"
)

type backgroundRequestEnvelope struct {
	Version   uint32            `json:"version"`
	Request   protocol.Request  `json:"request"`
	Canonical canonical.Request `json:"canonical"`
}

type BackgroundDispatcher struct {
	service *gatewayruntime.Service
	engine  *engine.Engine
	keys    gatewayruntime.AsyncKeys
}

func NewBackgroundDispatcher(service *gatewayruntime.Service, executionEngine *engine.Engine, keys gatewayruntime.AsyncKeys) (*BackgroundDispatcher, error) {
	if service == nil || executionEngine == nil {
		return nil, repository.ErrInvalidInput
	}
	if len(keys.PayloadKEK) != security.KeySize || len(keys.PayloadHMAC) != security.KeySize {
		return nil, repository.ErrInvalidInput
	}
	if (len(keys.CredentialKEK) == 0) != (len(keys.CredentialHMAC) == 0) || len(keys.CredentialKEK) != 0 && (len(keys.CredentialKEK) != security.KeySize || len(keys.CredentialHMAC) != security.KeySize) {
		return nil, repository.ErrInvalidInput
	}
	ownedKeys := gatewayruntime.AsyncKeys{
		CredentialKEK:  append([]byte(nil), keys.CredentialKEK...),
		CredentialHMAC: append([]byte(nil), keys.CredentialHMAC...),
		PayloadKEK:     append([]byte(nil), keys.PayloadKEK...),
		PayloadHMAC:    append([]byte(nil), keys.PayloadHMAC...),
	}
	return &BackgroundDispatcher{service: service, engine: executionEngine, keys: ownedKeys}, nil
}

func (d *BackgroundDispatcher) RecoverAttempt(ctx context.Context, item repository.OutboxItem) error {
	dispatch, err := d.service.Store.ReadAttemptDispatch(ctx, item.AttemptID)
	if err != nil {
		return err
	}
	if dispatch.ResultBlobID != 0 || dispatch.ResourceStatus == "completed" {
		// Completion, result persistence, settlement and Outbox acknowledgement
		// share one transaction. Seeing only part of that fact is corruption, not
		// permission to execute the provider request again.
		return repository.ErrConflict
	}
	request, err := d.service.Store.ReadLatestAttemptRequest(ctx, item.AttemptID, item.Action)
	if errors.Is(err, repository.ErrNotFound) || err == nil && request.Status == "not_sent" {
		return d.DispatchAttempt(ctx, item)
	}
	if err != nil {
		return err
	}
	return d.service.MarkBackgroundResponseIndeterminate(ctx, item, "background_submission_unverified")
}

func (d *BackgroundDispatcher) DispatchAttempt(ctx context.Context, item repository.OutboxItem) error {
	fixed, err := d.service.Store.ReadDeferredDispatch(ctx, item.AttemptID)
	if err != nil {
		return err
	}
	requestBody, err := d.openBlob(ctx, fixed.RequestBlobID, fmt.Sprintf("call:%d:request", fixed.CallID), "gateway-payload", d.keys.PayloadKEK, d.keys.PayloadHMAC)
	if err != nil {
		return d.service.FailBackgroundResponse(ctx, item, 0, "background_request_unreadable", repository.RequestLogResult{})
	}
	defer clear(requestBody)
	var envelope backgroundRequestEnvelope
	if err := json.Unmarshal(requestBody, &envelope); err != nil || envelope.Version != 1 || envelope.Canonical.Stream {
		return d.service.FailBackgroundResponse(ctx, item, 0, "background_request_invalid", repository.RequestLogResult{})
	}
	fixed.PublicModel = strings.TrimSpace(envelope.Request.Model)
	if fixed.PublicModel == "" {
		return d.service.FailBackgroundResponse(ctx, item, 0, "background_request_invalid", repository.RequestLogResult{})
	}
	secret, err := d.openCredential(ctx, fixed.CredentialID, fixed.CredentialSecret, fixed.CredentialBlobID)
	if err != nil {
		return d.service.FailBackgroundResponse(ctx, item, 0, "background_credential_unreadable", repository.RequestLogResult{})
	}
	defer clear(secret)
	route, err := deferredRoute(fixed, string(secret))
	if err != nil {
		return d.service.FailBackgroundResponse(ctx, item, 0, "background_route_invalid", repository.RequestLogResult{})
	}
	canonicalRequest, err := d.resolveInputAssets(ctx, fixed, envelope.Canonical)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) || errors.Is(err, repository.ErrNotFound) {
			return d.service.FailBackgroundResponse(ctx, item, 0, "background_input_unreadable", repository.RequestLogResult{})
		}
		return err
	}
	prepared, err := d.engine.PrepareFixed(ctx, route, canonicalRequest)
	if err != nil {
		return d.service.FailBackgroundResponse(ctx, item, 0, "background_prepare_failed", repository.RequestLogResult{})
	}
	wire := prepared.Prepared()
	mapping := security.DomainDigest(d.keys.PayloadHMAC, "attempt-request-mapping-v1", []byte(wire.Method), []byte(wire.URL), wire.Body)
	evidence := gatewayruntime.AttemptRequestEvidence{MappingHMAC: hex.EncodeToString(mapping[:]), RequestBytesHMAC: digestHex(d.keys.PayloadHMAC, wire.Body)}
	requestLogID, err := d.service.BeginAttemptOutboxRequest(ctx, item, evidence)
	if err != nil {
		return err
	}
	started := time.Now()
	result, executeErr := prepared.Execute(ctx)
	duration := uint64(time.Since(started).Milliseconds())
	exchange := repository.RequestLogResult{DurationMS: &duration, RequestComplete: true}
	if executeErr != nil {
		status := domain.UpstreamStatusCode(executeErr)
		if status == 0 {
			exchange.ErrorCode = "background_provider_exchange_unknown"
			return d.service.MarkBackgroundResponseIndeterminate(ctx, item, exchange.ErrorCode)
		}
		code := uint16(status)
		exchange.HTTPStatus, exchange.ResponseComplete, exchange.ErrorCode = &code, true, "background_provider_error"
		if details := gatewaytransport.DetailsFromError(executeErr); details != nil && len(details.Raw) > 0 {
			exchange.ResponseBytesHMAC = digestHex(d.keys.PayloadHMAC, details.Raw)
			if len(details.Raw) <= 1<<20 {
				if blob, blobErr := d.payloadBlob(ctx, details.Raw); blobErr == nil {
					exchange.ResponsePayload = &blob
				}
			}
		}
		return d.service.FailBackgroundResponse(ctx, item, requestLogID, exchange.ErrorCode, exchange)
	}
	result.ID = fixed.PublicID
	result.Model = fixed.PublicModel
	if result.CreatedAt == 0 {
		result.CreatedAt = time.Now().Unix()
	}
	encoded, err := openairesponses.EncodeResponseJSON(result)
	if err != nil {
		return d.service.MarkBackgroundResponseIndeterminate(ctx, item, "background_response_encode_failed")
	}
	var public protocol.Response
	if err := json.Unmarshal(encoded, &public); err != nil {
		return d.service.MarkBackgroundResponseIndeterminate(ctx, item, "background_response_encode_failed")
	}
	public.ID, public.Object, public.Model = fixed.PublicID, "response", fixed.PublicModel
	public.Background, public.Store = true, true
	public.Status = "completed"
	setPublicPreviousResponseID(&public, envelope.Request.PreviousResponseID)
	resultBytes, err := json.Marshal(public)
	if err != nil {
		return d.service.MarkBackgroundResponseIndeterminate(ctx, item, "background_response_encode_failed")
	}
	resultBlob, err := d.payloadBlob(ctx, resultBytes)
	if err != nil {
		return err
	}
	status := uint16(200)
	exchange.HTTPStatus, exchange.ResponseComplete = &status, true
	exchange.ResponseBytesHMAC = digestHex(d.keys.PayloadHMAC, resultBytes)
	facts, err := backgroundBillingFacts(result.Usage)
	if err != nil {
		return d.service.MarkBackgroundResponseIndeterminate(ctx, item, "background_usage_invalid")
	}
	summary := map[string]any{"model": fixed.PublicModel, "output_items": len(result.Output)}
	return d.service.CompleteBackgroundResponse(ctx, gatewayruntime.BackgroundResponseResult{
		Item: item, RequestID: requestLogID, Payload: resultBlob, Summary: summary, Facts: facts, Exchange: exchange,
	})
}

func (d *BackgroundDispatcher) resolveInputAssets(ctx context.Context, fixed repository.DeferredDispatch, request canonical.Request) (canonical.Request, error) {
	fileIDs := backgroundFileIDs(request)
	if len(fileIDs) == 0 {
		return request.Clone(), nil
	}
	assets, err := d.service.Store.ReadCallInputAssets(ctx, fixed.CallID, fixed.UserID, fixed.TokenID)
	if err != nil {
		return canonical.Request{}, err
	}
	if len(assets) != len(fileIDs) {
		return canonical.Request{}, repository.ErrConflict
	}
	byID := make(map[string]repository.CallInputAsset, len(fileIDs))
	for index, fileID := range fileIDs {
		byID[fileID] = assets[index]
	}
	resolved := request.Clone()
	var total uint64
	for itemIndex := range resolved.Items {
		for contentIndex := range resolved.Items[itemIndex].Content {
			content := &resolved.Items[itemIndex].Content[contentIndex]
			fileID := strings.TrimSpace(content.FileID)
			if fileID == "" {
				continue
			}
			asset, exists := byID[fileID]
			if !exists || asset.ContentLength > 64<<20 || total > (128<<20)-asset.ContentLength {
				return canonical.Request{}, repository.ErrConflict
			}
			total += asset.ContentLength
			body, err := filestorage.ReadURL(ctx, asset.ObjectLocator, int64(asset.ContentLength))
			if err != nil {
				return canonical.Request{}, err
			}
			if uint64(len(body)) != asset.ContentLength {
				clear(body)
				return canonical.Request{}, repository.ErrConflict
			}
			digest := sha256.Sum256(body)
			if !strings.EqualFold(hex.EncodeToString(digest[:]), asset.SHA256) {
				clear(body)
				return canonical.Request{}, repository.ErrConflict
			}
			dataURL := "data:" + asset.ContentType + ";base64," + base64.StdEncoding.EncodeToString(body)
			clear(body)
			switch content.Type {
			case "input_image", "image", "image_url", "input_audio", "audio", "input_video", "video":
				content.URL = dataURL
			default:
				content.Data = dataURL
			}
			content.FileID = ""
			if content.MediaType == "" {
				content.MediaType = asset.ContentType
			}
		}
	}
	return resolved, nil
}

func (d *BackgroundDispatcher) openBlob(ctx context.Context, id uint64, owner, purpose string, kek, hmacKey []byte) ([]byte, error) {
	envelope, err := d.service.Store.ReadEncryptedBlob(ctx, d.service.Store.DB(), id)
	if err != nil {
		return nil, err
	}
	if envelope.Purpose != purpose || envelope.SchemaVersion != 1 {
		return nil, security.ErrAuthentication
	}
	return repository.OpenBlob(envelope, id, []byte(owner), kek, hmacKey)
}

// openCredential prefers the direct secret on the credential row and falls
// back to the encrypted blob retained by older credentials.
func (d *BackgroundDispatcher) openCredential(ctx context.Context, credentialID uint64, direct []byte, blobID uint64) ([]byte, error) {
	if len(direct) != 0 {
		return append([]byte(nil), direct...), nil
	}
	if blobID == 0 {
		return nil, repository.ErrNotFound
	}
	return d.openBlob(ctx, blobID, fmt.Sprintf("credential:%d", credentialID), "credential", d.keys.CredentialKEK, d.keys.CredentialHMAC)
}

func (d *BackgroundDispatcher) payloadBlob(ctx context.Context, body []byte) (repository.BlobInput, error) {
	retention := time.Now().UTC().Add(configuredResponseRetention())
	input := repository.BlobInput{Plaintext: body, KEK: d.keys.PayloadKEK, HMACKey: d.keys.PayloadHMAC, RetentionUntil: &retention}
	err := d.service.Store.DB().QueryRowContext(ctx, `SELECT k.id,k.current_version FROM crypto_keyring_state k JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version AND v.status='current' WHERE k.purpose='gateway-payload'`).Scan(&input.KeyringID, &input.KEKVersion)
	return input, err
}

func deferredRoute(fixed repository.DeferredDispatch, apiKey string) (*routing.RouteResult, error) {
	capabilities := make(map[routing.Capability]bool)
	var tags []string
	if err := json.Unmarshal(fixed.Capabilities, &tags); err == nil {
		for _, tag := range tags {
			capabilities[routing.Capability(tag)] = true
		}
	} else {
		var flags map[string]bool
		if err := json.Unmarshal(fixed.Capabilities, &flags); err != nil {
			return nil, err
		}
		for tag, enabled := range flags {
			if enabled {
				capabilities[routing.Capability(tag)] = true
			}
		}
	}
	var upstream model.UpstreamTransport
	switch strings.ToLower(fixed.Protocol) {
	case "openai", "openai_chat":
		upstream = model.UpstreamTransportOpenAIChat
	case "openai_responses":
		upstream = model.UpstreamTransportOpenAIResponses
	case "anthropic", "anthropic_messages":
		upstream = model.UpstreamTransportAnthropic
	case "google", "google_generate_content":
		upstream = model.UpstreamTransportGoogle
	case "volcengine", "volcengine_responses", "volcengine_responses_v3":
		upstream = model.UpstreamTransportVolcengineV3
	default:
		return nil, repository.ErrConflict
	}
	return &routing.RouteResult{
		ReleaseID: uint(fixed.ReleaseID), OperationContractID: uint(fixed.OperationContractID),
		ModelOperationID: uint(fixed.ModelOperationID), SKUID: uint(fixed.SKUID), RouteID: uint(fixed.RouteID),
		OfferingID: uint(fixed.OfferingID), ProductTransportID: uint(fixed.ProductTransportID),
		CredentialPoolID: uint(fixed.CredentialPoolID), CredentialID: uint(fixed.CredentialID),
		CredentialVersionID: uint(fixed.CredentialVersionID), PurposeGrantID: uint(fixed.PurposeGrantID),
		KeyID: uint(fixed.CredentialID), ChannelID: uint(fixed.ChannelID), Protocol: model.Protocol(fixed.Protocol), BaseURL: fixed.BaseURL,
		APIKey: apiKey, VendorModel: fixed.VendorModel, ModelName: fixed.PublicModel,
		Capabilities: capabilities, Transport: upstream, TransportConfig: map[string]any{"request_path": fixed.RequestPath},
		DeliveryMode: fixed.DeliveryMode,
	}, nil
}

func backgroundBillingFacts(usage *canonical.Usage) (billing.Facts, error) {
	return engine.CanonicalBillingFacts(usage)
}

func digestHex(key, body []byte) string {
	digest := security.HMACSHA256(key, body)
	return hex.EncodeToString(digest[:])
}

func configuredResponseRetention() time.Duration { return config.ResourceHistoryRetentionDuration() }

func loadPayloadKeys() (gatewayruntime.AsyncKeys, error) {
	var keys gatewayruntime.AsyncKeys
	for _, item := range []struct {
		name string
		out  *[]byte
	}{
		{"PRISM_GATEWAY_PAYLOAD_KEK_B64", &keys.PayloadKEK},
		{"PRISM_GATEWAY_PAYLOAD_HMAC_B64", &keys.PayloadHMAC},
	} {
		decoded, err := security.DecodeBase64Key(os.Getenv(item.name))
		if err != nil {
			return keys, fmt.Errorf("%s must be configured as base64-encoded 32 bytes", item.name)
		}
		*item.out = decoded
	}
	return keys, nil
}

var _ gatewayruntime.AttemptOutboxDispatcher = (*BackgroundDispatcher)(nil)
