package runtime

import (
	"context"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/pkg/config"
)

// ResponseRoute is the immutable catalog selection used by a deferred
// Responses call. The worker later reconstructs this exact route from the
// persisted Attempt; it never runs routing a second time.
type ResponseRoute struct {
	ReleaseID, OperationContractID, ModelOperationID, SKUID uint64
	RouteID, OfferingID, CostPlanID, ProductTransportID     uint64
	CredentialPoolID, CredentialID, CredentialVersionID     uint64
	PurposeGrantID                                          uint64
	Currency                                                string
	CurrencyVersion                                         uint32
	DeliveryMode                                            string
	Schedule                                                billing.RateSchedule
}

type ResponseSubmitInput struct {
	PublicID                   string
	UserID, TokenID            uint64
	RequestPayload             []byte
	IdempotencyRequest         []byte
	MediaAssetIDs              []uint64
	PayloadKeyringID           uint64
	PayloadKEKVersion          uint32
	PayloadKEK, PayloadHMAC    []byte
	Route                      ResponseRoute
	Idempotency                *repository.IdempotencyInput
	PreviousResponseResourceID *uint64
	Summary                    any
}

// SubmitResponse commits the public resource and its executable intent before
// returning queued. No Redis task or in-memory state is required for recovery.
func (s *Service) SubmitResponse(ctx context.Context, in ResponseSubmitInput) (Submission, error) {
	if err := in.Route.Schedule.Validate(); err != nil {
		return Submission{}, err
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
			QuotedAmount: quote.Amount.String(), Currency: in.Route.Currency,
			CurrencyVersion: in.Route.CurrencyVersion, DeliveryMode: in.Route.DeliveryMode,
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
		IdempotencyRequest: in.IdempotencyRequest,
		Idempotency:        in.Idempotency, Background: true, ResourceKind: "response",
		ResourceSummary: in.Summary, PreviousResponseResourceID: in.PreviousResponseResourceID,
		MediaAssetIDs: in.MediaAssetIDs,
	})
}
