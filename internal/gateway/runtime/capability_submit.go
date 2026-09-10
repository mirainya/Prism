package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/pkg/config"
)

// RouteSnapshot contains the catalog identities and commercial policy fixed
// at selection time. Every execution record points back to these immutable IDs.
type RouteSnapshot struct {
	PublicModel, VendorModel                                string
	ReleaseID, OperationContractID, ModelOperationID, SKUID uint64
	RouteID, OfferingID, CostPlanID, ProductTransportID     uint64
	CredentialPoolID, CredentialID, CredentialVersionID     uint64
	PurposeGrantID                                          uint64
	Currency                                                string
	CurrencyVersion                                         uint32
	Schedule                                                billing.RateSchedule
	DeliveryMode, IdempotencyMode                           string
	TaskScope, CancelMode, SourceURLPolicy                  string
	UpstreamScopeKind, UpstreamScopeKey                     string
	ServiceTiers                                            []string
}

// VideoRoute remains the domain-specific spelling used by video submission.
type VideoRoute = RouteSnapshot

type CapabilitySubmitInput struct {
	PublicID, RequestID, Endpoint, Operation string
	UserID, TokenID                          uint64
	ServiceTier                              string
	RequestPayload                           []byte
	PayloadKeyringID                         uint64
	PayloadKEKVersion                        uint32
	PayloadKEK, PayloadHMAC                  []byte
	MediaAssetIDs                            []uint64
	ResourceSummary                          any
	Route                                    RouteSnapshot
	Idempotency                              *repository.IdempotencyInput
}

// SubmitCapability commits a synchronous capability Call and its fixed
// Attempt before any provider request is authorized.
func (s *Service) SubmitCapability(ctx context.Context, in CapabilitySubmitInput) (Submission, error) {
	if s == nil || len(in.RequestPayload) == 0 || len(in.PayloadKEK) != security.KeySize ||
		len(in.PayloadHMAC) != security.KeySize || in.PayloadKeyringID == 0 ||
		in.PayloadKEKVersion == 0 || in.UserID == 0 || in.TokenID == 0 || in.PublicID == "" ||
		in.ResourceSummary == nil {
		return Submission{}, repository.ErrInvalidInput
	}
	if err := in.Route.Schedule.Validate(); err != nil {
		return Submission{}, err
	}
	if in.Route.DeliveryMode != "reference" && in.Route.DeliveryMode != "managed_copy" ||
		in.Route.TaskScope != "none" && in.Route.TaskScope != "request" ||
		in.Route.CancelMode != "none" ||
		in.Route.SourceURLPolicy != "fixed" && in.Route.SourceURLPolicy != "refreshable" {
		return Submission{}, repository.ErrConflict
	}
	serviceTier := strings.TrimSpace(in.ServiceTier)
	if serviceTier == "" {
		serviceTier = "standard"
	}
	if !routeAllowsTier(in.Route.ServiceTiers, serviceTier) {
		return Submission{}, repository.ErrInvalidInput
	}
	quote, err := in.Route.Schedule.Reserve()
	if err != nil {
		return Submission{}, err
	}
	retentionUntil := time.Now().UTC().Add(config.APICallPayloadRetentionDuration())
	return s.Submit(ctx, SubmitInput{
		Call: repository.CreateCallInput{
			PublicID: in.PublicID, UserID: in.UserID, TokenID: in.TokenID,
			OperationContractID: in.Route.OperationContractID, CatalogReleaseID: in.Route.ReleaseID,
			ModelOperationID: in.Route.ModelOperationID, SKUID: in.Route.SKUID,
			Currency: in.Route.Currency, CurrencyVersion: in.Route.CurrencyVersion,
			DeliveryMode: in.Route.DeliveryMode, QuotedAmount: quote.Amount.String(),
		},
		Reservation: repository.ReservationInput{
			TokenID: in.TokenID, Amount: quote.Amount.String(), Currency: in.Route.Currency,
			CurrencyVersion: in.Route.CurrencyVersion,
		},
		Attempt: repository.BeginAttemptInput{
			CatalogReleaseID: in.Route.ReleaseID, SKUID: in.Route.SKUID, RouteID: in.Route.RouteID,
			OfferingID: in.Route.OfferingID, CostPlanID: in.Route.CostPlanID, ProductTransportID: in.Route.ProductTransportID,
			CredentialPoolID: in.Route.CredentialPoolID, CredentialID: in.Route.CredentialID,
			CredentialVersionID: in.Route.CredentialVersionID, PurposeGrantID: in.Route.PurposeGrantID,
		},
		RequestPayload: repository.BlobInput{
			KeyringID: in.PayloadKeyringID, KEKVersion: in.PayloadKEKVersion,
			Plaintext: in.RequestPayload, KEK: in.PayloadKEK, HMACKey: in.PayloadHMAC,
			RetentionUntil: &retentionUntil,
		},
		Idempotency: in.Idempotency, ResourceKind: "capability_task",
		ResourceSummary: in.ResourceSummary, MediaAssetIDs: in.MediaAssetIDs,
	})
}

func routeAllowsTier(configured []string, requested string) bool {
	for _, tier := range configured {
		if strings.TrimSpace(tier) == requested {
			return true
		}
	}
	return false
}
