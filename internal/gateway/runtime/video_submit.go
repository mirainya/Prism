package runtime

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/pkg/config"
)

type VideoSubmitInput struct {
	PublicID, RequestID, Endpoint, Operation string
	CallbackURL                              string
	UserID, TokenID                          uint64
	ServiceTier                              string
	RequestPayload                           []byte
	PayloadKeyringID                         uint64
	PayloadKEKVersion                        uint32
	PayloadKEK, PayloadHMAC                  []byte
	MediaAssetIDs                            []uint64
	Route                                    VideoRoute
	Idempotency                              *repository.IdempotencyInput
}

// SubmitVideo creates the call, reservation, fixed attempt and async submit
// outbox in one transaction. No provider request is sent by this method.
func (s *Service) SubmitVideo(ctx context.Context, in VideoSubmitInput) (Submission, error) {
	if s == nil || len(in.RequestPayload) == 0 || len(in.PayloadKEK) != security.KeySize || len(in.PayloadHMAC) != security.KeySize || in.PayloadKeyringID == 0 || in.PayloadKEKVersion == 0 || in.UserID == 0 || in.TokenID == 0 || in.PublicID == "" {
		return Submission{}, repository.ErrInvalidInput
	}
	if err := in.Route.Schedule.Validate(); err != nil {
		return Submission{}, err
	}
	serviceTier := strings.TrimSpace(in.ServiceTier)
	if serviceTier == "" {
		serviceTier = "standard"
	}
	if len(in.Route.ServiceTiers) == 0 {
		return Submission{}, repository.ErrConflict
	}
	allowedTier := false
	for _, configured := range in.Route.ServiceTiers {
		if strings.TrimSpace(configured) == serviceTier {
			allowedTier = true
			break
		}
	}
	if !allowedTier {
		return Submission{}, repository.ErrInvalidInput
	}
	callbackURL, err := NormalizeCallbackURL(in.CallbackURL)
	if err != nil {
		return Submission{}, err
	}
	quote, err := in.Route.Schedule.Reserve()
	if err != nil {
		return Submission{}, err
	}
	summary, err := buildVideoResourceSummary(in.RequestPayload, in.PayloadHMAC, in.Route.PublicModel, in.Route.VendorModel, serviceTier)
	if err != nil {
		return Submission{}, err
	}
	callbackTarget, err := newCallbackTargetRegistration(callbackURL, in.PayloadKeyringID, in.PayloadKEKVersion, in.PayloadKEK, in.PayloadHMAC)
	if err != nil {
		return Submission{}, err
	}
	var idempotencyRequest []byte
	if callbackTarget != nil {
		idempotencyRequest, err = json.Marshal(struct {
			Request     json.RawMessage `json:"request"`
			CallbackURL string          `json:"callback_url"`
		}{Request: json.RawMessage(in.RequestPayload), CallbackURL: callbackURL})
		if err != nil {
			return Submission{}, err
		}
	}
	retentionUntil := time.Now().UTC().Add(config.APICallPayloadRetentionDuration())
	return s.Submit(ctx, SubmitInput{
		Call: repository.CreateCallInput{PublicID: in.PublicID, UserID: in.UserID, TokenID: in.TokenID,
			OperationContractID: in.Route.OperationContractID, CatalogReleaseID: in.Route.ReleaseID,
			ModelOperationID: in.Route.ModelOperationID, SKUID: in.Route.SKUID, Currency: in.Route.Currency,
			CurrencyVersion: in.Route.CurrencyVersion, DeliveryMode: in.Route.DeliveryMode, QuotedAmount: quote.Amount.String()},
		Reservation: repository.ReservationInput{TokenID: in.TokenID,
			Amount: quote.Amount.String(), Currency: in.Route.Currency, CurrencyVersion: in.Route.CurrencyVersion},
		Attempt: repository.BeginAttemptInput{CatalogReleaseID: in.Route.ReleaseID, SKUID: in.Route.SKUID, RouteID: in.Route.RouteID,
			OfferingID: in.Route.OfferingID, CostPlanID: in.Route.CostPlanID, ProductTransportID: in.Route.ProductTransportID, CredentialPoolID: in.Route.CredentialPoolID,
			CredentialID: in.Route.CredentialID, CredentialVersionID: in.Route.CredentialVersionID, PurposeGrantID: in.Route.PurposeGrantID},
		RequestPayload:     repository.BlobInput{KeyringID: in.PayloadKeyringID, KEKVersion: in.PayloadKEKVersion, Plaintext: in.RequestPayload, KEK: in.PayloadKEK, HMACKey: in.PayloadHMAC, RetentionUntil: &retentionUntil},
		IdempotencyRequest: idempotencyRequest, Idempotency: in.Idempotency,
		AsyncScopeKind: in.Route.UpstreamScopeKind, AsyncScopeKey: in.Route.UpstreamScopeKey, Asynchronous: true,
		ResourceKind: "video_task", ResourceSummary: summary, MediaAssetIDs: in.MediaAssetIDs,
		CallbackTarget: callbackTarget,
	})
}

// buildVideoResourceSummary retains only non-sensitive task metadata in the
// queryable projection. Prompt text and media bodies remain in the encrypted
// request payload; the prompt HMAC supports diagnostics without disclosure.
func buildVideoResourceSummary(payload, hmacKey []byte, publicModel, vendorModel, serviceTier string) (map[string]any, error) {
	var request VideoRequestPayload
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, repository.ErrInvalidInput
	}
	if len(hmacKey) != security.KeySize {
		return nil, repository.ErrInvalidInput
	}
	digest := security.DomainDigest(hmacKey, "video-resource-prompt-v1", []byte(request.Prompt))
	paramKeys := make([]string, 0, len(request.Params))
	for key := range request.Params {
		paramKeys = append(paramKeys, key)
	}
	sort.Strings(paramKeys)
	summary := map[string]any{
		"model":          publicModel,
		"vendor_model":   vendorModel,
		"task_mode":      request.TaskMode,
		"service_tier":   serviceTier,
		"resolution":     request.Resolution,
		"ratio":          request.Ratio,
		"duration":       request.Duration,
		"generate_audio": request.GenerateAudio,
		"prompt_length":  len([]byte(request.Prompt)),
		"prompt_hmac":    hex.EncodeToString(digest[:]),
		"param_keys":     paramKeys,
	}
	if request.Content != nil {
		switch content := request.Content.(type) {
		case []any:
			summary["content_count"] = len(content)
		case map[string]any:
			summary["content_count"] = 1
		}
	}
	return summary, nil
}

// VideoRequestPayload creates the canonical adapter payload from public video
// fields without retaining callback secrets or provider credentials.
type VideoRequestPayload struct {
	Model         string         `json:"model"`
	Prompt        string         `json:"prompt"`
	TaskMode      string         `json:"task_mode,omitempty"`
	Resolution    string         `json:"resolution"`
	Ratio         string         `json:"ratio"`
	Duration      int            `json:"duration"`
	GenerateAudio bool           `json:"generate_audio"`
	ServiceTier   string         `json:"service_tier,omitempty"`
	Content       any            `json:"content,omitempty"`
	Params        map[string]any `json:"params,omitempty"`
}
