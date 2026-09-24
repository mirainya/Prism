package open

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/internal/video/canonical"
	"github.com/mirainya/Prism/internal/video/codec/prismv1"
	perrors "github.com/mirainya/Prism/pkg/errors"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/safeurl"
)

var unifiedVideoStore *repository.Store
var unifiedVideoRouter *routing.Router

const maxVideoRequestBytes = 4 << 20

func InitUnifiedVideoGateway(db *sql.DB) {
	if db == nil {
		unifiedVideoStore, unifiedVideoRouter = nil, nil
		return
	}
	store, err := repository.New(db)
	if err != nil {
		return
	}
	unifiedVideoStore, unifiedVideoRouter = store, routing.NewRouter()
}

type GenerationResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	ServiceTier string `json:"service_tier,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	// CallbackSigningSecret is returned once, in hex, when the request
	// registered a callback URL. Clients must persist it and verify the
	// X-Prism-Signature header on inbound callback deliveries. It is omitted
	// on idempotent replays and on requests without callback_url.
	CallbackSigningSecret string `json:"callback_signing_secret,omitempty"`
}

type videoEstimateResponse struct {
	EstimatedCost string  `json:"estimated_cost"`
	BaseCost      string  `json:"base_cost"`
	MarkupRatio   string  `json:"markup_ratio"`
	PricingMode   string  `json:"pricing_mode"`
	ServiceTier   string  `json:"service_tier,omitempty"`
	UnitCost      float64 `json:"unit_cost,omitempty"`
	Units         float64 `json:"units,omitempty"`
	BillingMode   string  `json:"billing_mode,omitempty"`
	BillingTier   string  `json:"billing_tier,omitempty"`
	PricingSource string  `json:"pricing_source,omitempty"`
	Currency      string  `json:"currency,omitempty"`
}

func EstimateVideoGeneration(c *gin.Context) {
	spec, err := decodeVideoRequest(c)
	if err != nil {
		writeVideoDecodeError(c, err)
		return
	}
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	req, err := prismv1.ToTaskRequest(spec, token.UserID, token.ID)
	if err != nil {
		setVideoAccessErrorDetail(c, err)
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
		return
	}
	response, _, estimateErr := estimateUnifiedVideoGeneration(c.Request.Context(), req)
	if estimateErr != nil {
		writeVideoEstimateError(c, estimateErr)
		return
	}
	resp.Success(c, response)
}

func writeVideoEstimateError(c *gin.Context, err error) {
	setVideoAccessErrorDetail(c, err)
	switch {
	case errors.Is(err, video.ErrInvalidTaskRequest), errors.Is(err, video.ErrInvalidAsset), errors.Is(err, video.ErrAssetNotReady):
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
	case errors.Is(err, video.ErrAssetNotFound):
		resp.ErrorMsg(c, http.StatusNotFound, 404, "video asset not found")
	case errors.Is(err, gatewayruntime.ErrNotReady), errors.Is(err, routing.ErrNoRoute), errors.Is(err, routing.ErrNoCompatibleTransport), errors.Is(err, routing.ErrCapabilityUnavailable):
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, "video generation is unavailable")
	default:
		resp.ErrorMsg(c, http.StatusBadGateway, 502, "video estimate failed")
	}
}

func CreateVideoGeneration(c *gin.Context) {
	spec, err := decodeVideoRequest(c)
	if err != nil {
		writeVideoDecodeError(c, err)
		return
	}

	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}

	req, buildErr := prismv1.ToTaskRequest(spec, token.UserID, token.ID)
	if buildErr != nil {
		setVideoAccessErrorDetail(c, buildErr)
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, buildErr.Error()))
		return
	}
	// Unified public resource identifiers are stored in CHAR(36). The legacy
	// call_ UUID form is 41 bytes and cannot be persisted by this schema.
	req.CallID = newUnifiedVideoPublicID()
	req.RequestID = middleware.GetRequestID(c.Request.Context())
	req.Endpoint = c.FullPath()
	req.Operation = "videos.generate"
	signingSecret, _, err := createUnifiedVideoGeneration(c.Request.Context(), req, c.GetHeader("Idempotency-Key"))
	if err == nil {
		// An idempotent replay replaces CallID with the original public resource ID.
		// Do not emit a call ID for validation failures that never create a call.
		c.Header(prismCallIDHeader, req.CallID)
		resp.Success(c, GenerationResponse{ID: req.CallID, Status: "queued", ServiceTier: req.ServiceTier, CallbackSigningSecret: signingSecret})
		return
	}

	status, _, _ := classifyVideoCreateError(err)
	logger.Error("create video generation: " + err.Error())
	writeVideoCreateError(c, status, err)
}

func writeVideoCreateError(c *gin.Context, status int, err error) {
	setVideoAccessErrorDetail(c, err)
	switch status {
	case http.StatusBadRequest:
		if errors.Is(err, service.ErrInsufficientTokenBalance) || errors.Is(err, service.ErrInsufficientUserBalance) || errors.Is(err, repository.ErrInsufficient) {
			resp.BadRequest(c, perrors.ErrInsufficientQuota)
		} else {
			resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
		}
	case http.StatusNotFound:
		resp.ErrorMsg(c, status, 404, "video asset not found")
	case http.StatusConflict:
		resp.ErrorMsg(c, status, 409, "idempotency key conflicts with an existing request")
	case http.StatusServiceUnavailable:
		resp.ErrorMsg(c, status, 503, "video generation is unavailable")
	default:
		resp.ErrorMsg(c, status, status, "video generation failed")
	}
}

func decodeVideoRequest(c *gin.Context) (*canonical.VideoSpec, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxVideoRequestBytes)
	return prismv1.Decode(c.Request.Body)
}

func writeVideoDecodeError(c *gin.Context, err error) {
	setVideoAccessErrorDetail(c, err)
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		resp.ErrorMsg(c, http.StatusRequestEntityTooLarge, 413, "video request body is too large")
		return
	}
	resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
}

func newUnifiedVideoPublicID() string { return uuid.NewString() }

// createUnifiedVideoGeneration submits a video job. When the request registers
// a callback_url, the returned string carries the plaintext HMAC signing secret
// that the client must persist to verify inbound callback signatures. The
// secret is populated once on the initial submission and is empty on
// idempotent replays.
func createUnifiedVideoGeneration(ctx context.Context, req *video.CreateTaskRequest, idempotencyKey string) (string, bool, error) {
	if req == nil {
		return "", true, repository.ErrInvalidInput
	}
	callbackURL, err := gatewayruntime.NormalizeCallbackURL(req.Callback)
	if err != nil {
		return "", true, fmt.Errorf("%w: invalid callback_url", repository.ErrInvalidInput)
	}
	if callbackURL != "" {
		if err := safeurl.Validate(ctx, callbackURL); err != nil {
			return "", true, fmt.Errorf("%w: callback_url must resolve to a public address", repository.ErrInvalidInput)
		}
	}
	req.Callback = callbackURL
	plan, handled, err := planUnifiedVideoGeneration(ctx, req)
	if !handled || err != nil {
		return "", handled, err
	}
	policy, route, body := plan.Policy, plan.Route, plan.RequestPayload
	key := strings.TrimSpace(idempotencyKey)
	if policy.IdempotencyMode == "required" && key == "" || policy.IdempotencyMode == "forbidden" && key != "" {
		return "", true, repository.ErrInvalidInput
	}
	payloadKEK, err := gatewayKey("PRISM_GATEWAY_PAYLOAD_KEK_B64")
	if err != nil {
		return "", true, err
	}
	payloadHMAC, err := gatewayKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		clear(payloadKEK)
		return "", true, err
	}
	defer func() {
		clear(payloadKEK)
		clear(payloadHMAC)
		clear(body)
	}()
	var keyringID uint64
	var version uint32
	if err := unifiedVideoStore.DB().QueryRowContext(ctx, `SELECT k.id,k.current_version FROM crypto_keyring_state k JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version AND v.status='current' WHERE k.purpose='gateway-payload'`).Scan(&keyringID, &version); err != nil {
		return "", true, err
	}
	var idempotency *repository.IdempotencyInput
	if len(key) > 128 {
		return "", true, fmt.Errorf("%w: Idempotency-Key must not exceed 128 bytes", repository.ErrInvalidInput)
	}
	if key != "" {
		idempotencyRequestBytes, marshalErr := json.Marshal(struct {
			Request     json.RawMessage `json:"request"`
			CallbackURL string          `json:"callback_url"`
		}{Request: json.RawMessage(body), CallbackURL: callbackURL})
		if marshalErr != nil {
			return "", true, marshalErr
		}
		idempotency, err = buildRotationAwareIdempotency(uint64(req.TokenID), uint64(route.OperationContractID), version, payloadHMAC, key, idempotencyRequestBytes)
		if err != nil {
			return "", true, err
		}
	}
	runtimeService, err := gatewayruntime.New(unifiedVideoStore)
	if err != nil {
		return "", true, err
	}
	submission, err := runtimeService.SubmitVideo(ctx, gatewayruntime.VideoSubmitInput{
		PublicID: req.CallID, RequestID: req.RequestID, Endpoint: req.Endpoint, Operation: req.Operation,
		CallbackURL: req.Callback,
		UserID:      uint64(req.UserID), TokenID: uint64(req.TokenID), ServiceTier: req.ServiceTier, RequestPayload: body,
		PayloadKeyringID: keyringID, PayloadKEKVersion: version, PayloadKEK: payloadKEK, PayloadHMAC: payloadHMAC,
		Route:         gatewayruntime.VideoRoute{PublicModel: req.Model, VendorModel: route.VendorModel, ReleaseID: uint64(route.ReleaseID), OperationContractID: uint64(route.OperationContractID), ModelOperationID: uint64(route.ModelOperationID), SKUID: uint64(route.SKUID), RouteID: uint64(route.RouteID), OfferingID: uint64(route.OfferingID), CostPlanID: uint64(route.CostPlanID), ProductTransportID: uint64(route.ProductTransportID), CredentialPoolID: uint64(route.CredentialPoolID), CredentialID: uint64(route.CredentialID), CredentialVersionID: uint64(route.CredentialVersionID), PurposeGrantID: uint64(route.PurposeGrantID), Currency: route.Currency, CurrencyVersion: uint32(route.CurrencyVersion), Schedule: *route.SellSchedule, DeliveryMode: policy.DeliveryMode, IdempotencyMode: policy.IdempotencyMode, TaskScope: policy.TaskScope, CancelMode: policy.CancelMode, SourceURLPolicy: policy.SourceURLPolicy, UpstreamScopeKind: policy.UpstreamScopeKind, UpstreamScopeKey: policy.UpstreamScopeKey, ServiceTiers: policy.ServiceTiers},
		MediaAssetIDs: plan.MediaAssetIDs,
		Idempotency:   idempotency,
	})
	if err == nil && submission.PublicID != "" {
		req.CallID = submission.PublicID
	}
	var signingSecret string
	if len(submission.CallbackSigningSecret) != 0 {
		signingSecret = hex.EncodeToString(submission.CallbackSigningSecret)
		clear(submission.CallbackSigningSecret)
	}
	return signingSecret, true, err
}

type unifiedVideoPlan struct {
	Route          *routing.RouteResult
	Policy         repository.VideoRoutePolicy
	RequestPayload []byte
	MediaAssetIDs  []uint64
}

func planUnifiedVideoGeneration(ctx context.Context, req *video.CreateTaskRequest) (unifiedVideoPlan, bool, error) {
	if req == nil {
		return unifiedVideoPlan{}, true, repository.ErrInvalidInput
	}
	if unifiedVideoStore == nil || unifiedVideoRouter == nil {
		return unifiedVideoPlan{}, true, gatewayruntime.ErrNotReady
	}
	if err := gatewayruntime.RequireConfiguredReadiness(ctx, unifiedVideoStore.DB()); err != nil {
		return unifiedVideoPlan{}, true, err
	}
	selectionKey := req.CallID
	if selectionKey == "" {
		selectionKey = service.GenerateUnifiedCallID()
	}
	// Resolve media once; route-specific validation below uses the exact payload
	// that will be persisted and later submitted to the provider.
	mediaAssetIDs, err := resolveUnifiedVideoAssets(ctx, req)
	if err != nil {
		return unifiedVideoPlan{}, true, err
	}
	serviceTier := strings.TrimSpace(req.ServiceTier)
	if serviceTier == "" {
		serviceTier = "standard"
	}
	req.ServiceTier = serviceTier
	body, err := json.Marshal(gatewayruntime.VideoRequestPayload{Model: req.Model, Prompt: req.Prompt, TaskMode: req.TaskMode, Resolution: req.Resolution, Ratio: req.Ratio, Duration: req.Duration, GenerateAudio: req.Audio, ServiceTier: serviceTier, Content: req.Content, Params: req.Params})
	if err != nil {
		return unifiedVideoPlan{}, true, err
	}
	var excluded []uint
	var validationErr error
	for {
		route, selectErr := unifiedVideoRouter.SelectTransport(ctx, req.Model, routing.RouteRequirements{routing.CapabilityVideo: true}, routing.RouteOptions{
			SelectionKey:    selectionKey,
			OperationMethod: "POST", OperationPath: "/v1/videos/generations",
			AllowedTransports:   []model.UpstreamTransport{model.UpstreamTransportVideoGeneration},
			PreferredTransports: []model.UpstreamTransport{model.UpstreamTransportVideoGeneration},
			ExcludeOfferings:    excluded,
		})
		if selectErr != nil {
			if validationErr != nil && (errors.Is(selectErr, routing.ErrNoRoute) || errors.Is(selectErr, routing.ErrCapabilityUnavailable)) {
				return unifiedVideoPlan{}, true, validationErr
			}
			return unifiedVideoPlan{}, true, selectErr
		}
		if route == nil || route.ReleaseID == 0 || route.OfferingID == 0 || route.Transport != model.UpstreamTransportVideoGeneration {
			return unifiedVideoPlan{}, true, routing.ErrNoRoute
		}
		if route.SellSchedule == nil {
			return unifiedVideoPlan{}, true, repository.ErrConflict
		}
		policy, err := unifiedVideoStore.ReadVideoRoutePolicy(ctx, uint64(route.ReleaseID), uint64(route.SKUID), uint64(route.ProductTransportID))
		if err != nil {
			return unifiedVideoPlan{}, true, err
		}
		codec, supported := adapter.AsyncCodecFor(policy.AdapterCode, policy.AdapterVersion)
		if !supported {
			return unifiedVideoPlan{}, true, repository.ErrConflict
		}
		allowedTier := false
		for _, configured := range policy.ServiceTiers {
			if configured == serviceTier {
				allowedTier = true
				break
			}
		}
		if !allowedTier {
			validationErr = repository.ErrInvalidInput
			excluded = append(excluded, route.OfferingID)
			continue
		}
		requestMethod, _ := route.TransportConfig["request_method"].(string)
		requestMethod = strings.ToUpper(strings.TrimSpace(requestMethod))
		requestPath, _ := route.TransportConfig["request_path"].(string)
		if requestMethod == "" || strings.TrimSpace(requestPath) == "" {
			return unifiedVideoPlan{}, true, repository.ErrConflict
		}
		fixed := repository.AsyncDispatch{
			AdapterCode: policy.AdapterCode, AdapterVersion: policy.AdapterVersion,
			Protocol: string(route.Protocol), BaseURL: route.BaseURL, Method: requestMethod, Path: requestPath,
			VendorModel: route.VendorModel, PublicID: req.CallID, AdapterConfig: policy.AdapterConfig,
		}
		if _, err := codec.Prepare(ctx, "submit", fixed, body, ""); err != nil {
			if validationErr == nil {
				validationErr = fmt.Errorf("%w: %v", repository.ErrInvalidInput, err)
			}
			excluded = append(excluded, route.OfferingID)
			continue
		}
		return unifiedVideoPlan{Route: route, Policy: policy, RequestPayload: body, MediaAssetIDs: mediaAssetIDs}, true, nil
	}
}

func estimateUnifiedVideoGeneration(ctx context.Context, req *video.CreateTaskRequest) (videoEstimateResponse, bool, error) {
	plan, handled, err := planUnifiedVideoGeneration(ctx, req)
	if !handled || err != nil {
		return videoEstimateResponse{}, handled, err
	}
	quote, err := plan.Route.SellSchedule.Reserve()
	if err != nil {
		return videoEstimateResponse{}, true, err
	}
	return videoEstimateResponse{
		EstimatedCost: quote.Amount.String(), BaseCost: quote.Amount.String(), MarkupRatio: "0",
		PricingMode: "catalog", ServiceTier: req.ServiceTier, Currency: plan.Route.Currency,
	}, true, nil
}

// resolveUnifiedVideoAssets converts owned unified media references into safe
// public locators before the pinned adapter builds its provider request.
// Provider-specific object references are intentionally rejected here because
// the unified Seedance adapter accepts URLs only.
func resolveUnifiedVideoAssets(ctx context.Context, req *video.CreateTaskRequest) ([]uint64, error) {
	if req == nil || len(req.Content) == 0 {
		return nil, nil
	}
	assetIDs := make([]uint64, 0, len(req.Content))
	seen := make(map[uint64]struct{}, len(req.Content))
	for index := range req.Content {
		item := &req.Content[index]
		if item.StorageObjectID != "" {
			return nil, fmt.Errorf("%w: content item %d uses an unsupported provider object reference", video.ErrInvalidAsset, index)
		}
		if item.AssetID == "" {
			continue
		}
		assetID, err := strconv.ParseUint(strings.TrimSpace(item.AssetID), 10, 64)
		if err != nil || assetID == 0 {
			return nil, video.ErrAssetNotFound
		}
		var locator, state, contentType string
		var retentionUntil sql.NullTime
		if err := unifiedVideoStore.DB().QueryRowContext(ctx, `SELECT COALESCE(NULLIF(storage_locator,''),object_key),state,retention_until,content_type FROM gw_media_assets WHERE id=? AND user_id=? AND token_id=? AND purpose='input'`, assetID, req.UserID, req.TokenID).Scan(&locator, &state, &retentionUntil, &contentType); err == sql.ErrNoRows {
			return nil, video.ErrAssetNotFound
		} else if err != nil {
			return nil, err
		}
		if state != "active" || retentionUntil.Valid && !retentionUntil.Time.After(time.Now().UTC()) {
			return nil, video.ErrAssetNotReady
		}
		locator = strings.TrimSpace(locator)
		if locator == "" {
			return nil, video.ErrAssetNotReady
		}
		expectedKind := strings.TrimSuffix(strings.TrimSpace(item.Type), "_url")
		if expectedKind == "" || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), expectedKind+"/") {
			return nil, fmt.Errorf("%w: content item %d type does not match stored asset", video.ErrInvalidAsset, index)
		}
		if err := safeurl.Validate(ctx, locator); err != nil {
			return nil, fmt.Errorf("%w: unsafe stored asset URL", video.ErrInvalidAsset)
		}
		item.URL = locator
		item.AssetID = ""
		if _, exists := seen[assetID]; !exists {
			assetIDs = append(assetIDs, assetID)
			seen[assetID] = struct{}{}
		}
	}
	return assetIDs, nil
}

func ptrTime(value time.Time) *time.Time { return &value }

func gatewayKey(name string) ([]byte, error) {
	decoded, err := security.DecodeBase64Key(os.Getenv(name))
	if err != nil {
		return nil, errors.New(name + " must be a base64 encoded 32 byte key")
	}
	return decoded, nil
}

func classifyVideoCreateError(err error) (int, string, string) {
	switch {
	case errors.Is(err, video.ErrInvalidTaskRequest), errors.Is(err, video.ErrInvalidAsset), errors.Is(err, video.ErrAssetNotReady):
		return http.StatusBadRequest, "invalid_request_error", "invalid_video_request"
	case errors.Is(err, video.ErrAssetNotFound):
		return http.StatusNotFound, "invalid_request_error", "asset_not_found"
	case errors.Is(err, service.ErrInsufficientTokenBalance), errors.Is(err, service.ErrInsufficientUserBalance):
		return http.StatusBadRequest, "billing_error", "insufficient_quota"
	case errors.Is(err, repository.ErrInsufficient):
		return http.StatusBadRequest, "billing_error", "insufficient_quota"
	case errors.Is(err, repository.ErrIdempotencyConflict):
		return http.StatusConflict, "invalid_request_error", "idempotency_conflict"
	case errors.Is(err, repository.ErrInvalidInput):
		return http.StatusBadRequest, "invalid_request_error", "invalid_video_request"
	case errors.Is(err, gatewayruntime.ErrNotReady), errors.Is(err, routing.ErrNoRoute), errors.Is(err, routing.ErrNoCompatibleTransport), errors.Is(err, routing.ErrCapabilityUnavailable):
		return http.StatusServiceUnavailable, "service_unavailable_error", "video_channel_unavailable"
	default:
		return http.StatusInternalServerError, "video_error", "video_create_failed"
	}
}

func setVideoAccessErrorDetail(c *gin.Context, err error) {
	if c == nil || err == nil {
		return
	}
	if detail := video.AssetErrorDetail(err); detail != "" {
		middleware.SetAccessErrorDetail(c, detail)
		return
	}
	if detail, ok := routing.NoRouteDetailFrom(err); ok {
		// NoRouteDetail contains only route counts and retry timing; it does not
		// expose channel names, credentials, or upstream response bodies.
		middleware.SetAccessErrorDetail(c, detail.Error())
		return
	}
	switch {
	case errors.Is(err, video.ErrInvalidTaskRequest):
		middleware.SetAccessErrorDetail(c, "invalid video request")
	case errors.Is(err, service.ErrInsufficientTokenBalance), errors.Is(err, service.ErrInsufficientUserBalance), errors.Is(err, repository.ErrInsufficient):
		middleware.SetAccessErrorDetail(c, "insufficient quota")
	case errors.Is(err, routing.ErrNoRoute), errors.Is(err, routing.ErrNoCompatibleTransport), errors.Is(err, routing.ErrCapabilityUnavailable), errors.Is(err, gatewayruntime.ErrNotReady):
		middleware.SetAccessErrorDetail(c, "video channel is unavailable")
	case errors.Is(err, repository.ErrIdempotencyConflict):
		middleware.SetAccessErrorDetail(c, "idempotency key conflicts with an existing request")
	case errors.Is(err, repository.ErrInvalidInput):
		middleware.SetAccessErrorDetail(c, "invalid video request")
	case errors.Is(err, repository.ErrConflict):
		middleware.SetAccessErrorDetail(c, "video channel configuration is incomplete")
	}
}
