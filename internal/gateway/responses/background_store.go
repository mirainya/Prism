package responses

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	openairesponses "github.com/mirainya/Prism/internal/gateway/codec/openai_responses"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"gorm.io/datatypes"
)

func prepareBackgroundCanonical(ctx context.Context, userID, tokenID uint, previousID string, request canonical.Request) (canonical.Request, []uint64, error) {
	db, err := model.DB().DB()
	if err != nil {
		return canonical.Request{}, nil, err
	}
	store, err := repository.New(db)
	if err != nil {
		return canonical.Request{}, nil, err
	}
	knownAssets := make(map[string]uint64)
	if strings.TrimSpace(previousID) != "" {
		var previousCallID, requestPayloadID uint64
		var resultPayloadID sql.NullInt64
		var previousUserID, previousTokenID uint
		var callStatus, responseStatus string
		err := db.QueryRowContext(ctx, `SELECT c.id,c.request_payload_id,c.result_payload_id,r.user_id,r.token_id,c.status,response.status
FROM gw_api_resources r
JOIN gw_api_calls c ON c.id=r.call_id
JOIN gw_ai_responses response ON response.resource_id=r.id
WHERE r.public_id=? AND r.resource_kind='response' AND r.token_id=? AND r.deleted_at IS NULL`, previousID, tokenID).
			Scan(&previousCallID, &requestPayloadID, &resultPayloadID, &previousUserID, &previousTokenID, &callStatus, &responseStatus)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (previousUserID != userID || previousTokenID != tokenID || callStatus != "completed" || responseStatus != "completed" || !resultPayloadID.Valid) {
			return canonical.Request{}, nil, domain.ErrBadRequest("previous_response_id was not found")
		}
		if err != nil {
			return canonical.Request{}, nil, err
		}
		requestBody, err := readUnifiedCallPayload(ctx, db, previousCallID, requestPayloadID, "request")
		if err != nil {
			return canonical.Request{}, nil, err
		}
		defer clear(requestBody)
		resultBody, err := readUnifiedCallPayload(ctx, db, previousCallID, uint64(resultPayloadID.Int64), "result")
		if err != nil {
			return canonical.Request{}, nil, err
		}
		defer clear(resultBody)
		var previousEnvelope backgroundRequestEnvelope
		var previousResponse protocol.Response
		if json.Unmarshal(requestBody, &previousEnvelope) != nil || previousEnvelope.Version != 1 || json.Unmarshal(resultBody, &previousResponse) != nil {
			return canonical.Request{}, nil, repository.ErrConflict
		}
		output, err := openairesponses.DecodeItems(previousResponse.Output)
		if err != nil {
			return canonical.Request{}, nil, err
		}
		previousFileIDs := backgroundFileIDs(previousEnvelope.Canonical)
		previousAssets, err := store.ReadCallInputAssets(ctx, previousCallID, uint64(userID), uint64(tokenID))
		if err != nil {
			return canonical.Request{}, nil, err
		}
		if len(previousFileIDs) != len(previousAssets) {
			return canonical.Request{}, nil, repository.ErrConflict
		}
		for index, fileID := range previousFileIDs {
			knownAssets[fileID] = previousAssets[index].ID
		}
		items := make([]canonical.Item, 0, len(previousEnvelope.Canonical.Items)+len(output)+len(request.Items))
		items = append(items, canonical.CloneItems(previousEnvelope.Canonical.Items)...)
		items = append(items, canonical.CloneItems(output)...)
		items = append(items, canonical.CloneItems(request.Items)...)
		request.Items = items
	}
	fileIDs := backgroundFileIDs(request)
	assets := make([]uint64, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		assetID, exists := knownAssets[fileID]
		if !exists {
			assetID, err = store.ResolveFileAsset(ctx, uint64(userID), uint64(tokenID), fileID)
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					return canonical.Request{}, nil, domain.ErrBadRequest("file_id was not found")
				}
				return canonical.Request{}, nil, err
			}
		}
		assets = append(assets, assetID)
	}
	request.PreviousResponseID = ""
	return request, assets, nil
}

func backgroundFileIDs(request canonical.Request) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, item := range request.Items {
		for _, content := range item.Content {
			id := strings.TrimSpace(content.FileID)
			if id == "" {
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

func submitUnifiedBackgroundResponse(ctx context.Context, record *model.AIResponse, route *routing.RouteResult, payload, idempotencyRequest []byte, mediaAssetIDs []uint64, idempotency *repository.IdempotencyInput, previousID string) (gatewayruntime.Submission, error) {
	if record == nil || route == nil || route.SellSchedule == nil || len(payload) == 0 {
		return gatewayruntime.Submission{}, repository.ErrInvalidInput
	}
	db, err := model.DB().DB()
	if err != nil {
		return gatewayruntime.Submission{}, err
	}
	store, err := repository.New(db)
	if err != nil {
		return gatewayruntime.Submission{}, err
	}
	service, err := gatewayruntime.New(store)
	if err != nil {
		return gatewayruntime.Submission{}, err
	}
	keys, err := loadBackgroundKeys()
	if err != nil {
		return gatewayruntime.Submission{}, err
	}
	defer clearBackgroundKeys(&keys)
	var keyringID uint64
	var keyVersion uint32
	if err := db.QueryRowContext(ctx, `SELECT k.id,k.current_version FROM crypto_keyring_state k JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version AND v.status='current' WHERE k.purpose='gateway-payload'`).Scan(&keyringID, &keyVersion); err != nil {
		return gatewayruntime.Submission{}, err
	}
	var idempotencyMode string
	if err := db.QueryRowContext(ctx, `SELECT idempotency_mode FROM gw_skus WHERE id=? AND release_id=?`, route.SKUID, route.ReleaseID).Scan(&idempotencyMode); err != nil {
		return gatewayruntime.Submission{}, err
	}
	hasIdempotencyKey := idempotency != nil
	if idempotencyMode != "required" && idempotencyMode != "optional" && idempotencyMode != "forbidden" ||
		idempotencyMode == "required" && !hasIdempotencyKey || idempotencyMode == "forbidden" && hasIdempotencyKey {
		return gatewayruntime.Submission{}, repository.ErrInvalidInput
	}
	var previousResourceID *uint64
	if strings.TrimSpace(previousID) != "" {
		var id uint64
		if err := db.QueryRowContext(ctx, `SELECT id FROM gw_api_resources WHERE public_id=? AND resource_kind='response' AND token_id=? AND deleted_at IS NULL`, previousID, record.TokenID).Scan(&id); err != nil {
			return gatewayruntime.Submission{}, err
		}
		previousResourceID = &id
	}
	return service.SubmitResponse(ctx, gatewayruntime.ResponseSubmitInput{
		PublicID: record.ID, UserID: uint64(record.UserID), TokenID: uint64(record.TokenID), RequestPayload: payload,
		IdempotencyRequest: idempotencyRequest, MediaAssetIDs: mediaAssetIDs,
		PayloadKeyringID: keyringID, PayloadKEKVersion: keyVersion, PayloadKEK: keys.PayloadKEK, PayloadHMAC: keys.PayloadHMAC,
		Route: gatewayruntime.ResponseRoute{
			ReleaseID: uint64(route.ReleaseID), OperationContractID: uint64(route.OperationContractID), ModelOperationID: uint64(route.ModelOperationID),
			SKUID: uint64(route.SKUID), RouteID: uint64(route.RouteID), OfferingID: uint64(route.OfferingID), CostPlanID: uint64(route.CostPlanID), ProductTransportID: uint64(route.ProductTransportID),
			CredentialPoolID: uint64(route.CredentialPoolID), CredentialID: uint64(route.CredentialID), CredentialVersionID: uint64(route.CredentialVersionID),
			PurposeGrantID: uint64(route.PurposeGrantID), Currency: route.Currency, CurrencyVersion: uint32(route.CurrencyVersion),
			DeliveryMode: route.DeliveryMode, Schedule: *route.SellSchedule,
		},
		Idempotency: idempotency, PreviousResponseResourceID: previousResourceID,
		Summary: map[string]any{"model": record.Model, "background": true, "store": true},
	})
}

func unifiedResponseRuntime() (*gatewayruntime.Service, error) {
	db, err := model.DB().DB()
	if err != nil {
		return nil, err
	}
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	return gatewayruntime.New(store)
}

type unifiedResponseRow struct {
	ResourceID, CallID, UserID, TokenID uint64
	RequestPayloadID                    uint64
	ResultPayloadID                     sql.NullInt64
	CredentialID, ChannelID             sql.NullInt64
	ResourcePublicID, CallPublicID      string
	CallStatus, ResponseStatus          string
	Protocol                            sql.NullString
	Summary                             []byte
	CreatedAt                           time.Time
}

func readUnifiedResponseRow(ctx context.Context, userID, tokenID uint, id string) (unifiedResponseRow, error) {
	if userID == 0 || tokenID == 0 || strings.TrimSpace(id) == "" {
		return unifiedResponseRow{}, repository.ErrInvalidInput
	}
	db, err := model.DB().DB()
	if err != nil {
		return unifiedResponseRow{}, err
	}
	var row unifiedResponseRow
	err = db.QueryRowContext(ctx, `SELECT r.id,c.id,c.user_id,c.token_id,c.request_payload_id,c.result_payload_id,
r.public_id,c.public_id,c.status,response.status,response.result_summary,c.created_at,
a.credential_id,product.channel_id,ct.protocol
FROM gw_api_resources r
JOIN gw_api_calls c ON c.id=r.call_id
JOIN gw_ai_responses response ON response.resource_id=r.id
LEFT JOIN gw_api_call_attempts a ON a.id=COALESCE(c.final_attempt_id,c.current_attempt_id)
LEFT JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id
LEFT JOIN gw_products product ON product.id=pt.product_id AND product.release_id=pt.release_id
LEFT JOIN gw_channel_transports ct ON ct.id=pt.channel_transport_id AND ct.release_id=pt.release_id
WHERE r.public_id=? AND r.resource_kind='response' AND r.user_id=? AND r.token_id=? AND r.deleted_at IS NULL`, id, userID, tokenID).Scan(
		&row.ResourceID, &row.CallID, &row.UserID, &row.TokenID, &row.RequestPayloadID, &row.ResultPayloadID,
		&row.ResourcePublicID, &row.CallPublicID, &row.CallStatus, &row.ResponseStatus, &row.Summary, &row.CreatedAt,
		&row.CredentialID, &row.ChannelID, &row.Protocol,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return unifiedResponseRow{}, repository.ErrNotFound
	}
	return row, err
}

func readUnifiedResponseRowByCallID(ctx context.Context, userID, tokenID uint, callID uint64) (unifiedResponseRow, error) {
	if userID == 0 || tokenID == 0 || callID == 0 {
		return unifiedResponseRow{}, repository.ErrInvalidInput
	}
	db, err := model.DB().DB()
	if err != nil {
		return unifiedResponseRow{}, err
	}
	var row unifiedResponseRow
	err = db.QueryRowContext(ctx, `SELECT r.id,c.id,c.user_id,c.token_id,c.request_payload_id,c.result_payload_id,
r.public_id,c.public_id,c.status,response.status,response.result_summary,c.created_at,
a.credential_id,product.channel_id,ct.protocol
FROM gw_api_resources r
JOIN gw_api_calls c ON c.id=r.call_id
JOIN gw_ai_responses response ON response.resource_id=r.id
LEFT JOIN gw_api_call_attempts a ON a.id=COALESCE(c.final_attempt_id,c.current_attempt_id)
LEFT JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id
LEFT JOIN gw_products product ON product.id=pt.product_id AND product.release_id=pt.release_id
LEFT JOIN gw_channel_transports ct ON ct.id=pt.channel_transport_id AND ct.release_id=pt.release_id
WHERE c.id=? AND r.resource_kind='response' AND r.user_id=? AND r.token_id=?`, callID, userID, tokenID).Scan(
		&row.ResourceID, &row.CallID, &row.UserID, &row.TokenID, &row.RequestPayloadID, &row.ResultPayloadID,
		&row.ResourcePublicID, &row.CallPublicID, &row.CallStatus, &row.ResponseStatus, &row.Summary, &row.CreatedAt,
		&row.CredentialID, &row.ChannelID, &row.Protocol,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return unifiedResponseRow{}, repository.ErrNotFound
	}
	return row, err
}

func readUnifiedResponseRequest(ctx context.Context, row unifiedResponseRow) (protocol.Request, canonical.Request, error) {
	db, err := model.DB().DB()
	if err != nil {
		return protocol.Request{}, canonical.Request{}, err
	}
	body, err := readUnifiedCallPayload(ctx, db, row.CallID, row.RequestPayloadID, "request")
	if err != nil {
		return protocol.Request{}, canonical.Request{}, err
	}
	defer clear(body)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return protocol.Request{}, canonical.Request{}, repository.ErrConflict
	}
	if _, ok := fields["request"]; ok {
		var envelope backgroundRequestEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil || envelope.Version != 1 || strings.TrimSpace(envelope.Request.Model) == "" {
			return protocol.Request{}, canonical.Request{}, repository.ErrConflict
		}
		return envelope.Request, envelope.Canonical, nil
	}
	if _, ok := fields["input"]; ok {
		var request protocol.Request
		if err := json.Unmarshal(body, &request); err != nil || strings.TrimSpace(request.Model) == "" {
			return protocol.Request{}, canonical.Request{}, repository.ErrConflict
		}
		decoded, err := openairesponses.DecodeRequest(request)
		return request, decoded, err
	}
	var request canonical.Request
	if err := json.Unmarshal(body, &request); err != nil || strings.TrimSpace(request.Model) == "" {
		return protocol.Request{}, canonical.Request{}, repository.ErrConflict
	}
	input, err := json.Marshal(request.Items)
	if err != nil {
		return protocol.Request{}, canonical.Request{}, err
	}
	return protocol.Request{
		Model: request.Model, Input: input, Instructions: request.Instructions,
		Store: request.Store, Background: request.Background, PreviousResponseID: request.PreviousResponseID,
		Metadata: request.Metadata, ServiceTier: request.ServiceTier,
	}, request, nil
}

func responseRequestStored(request protocol.Request) bool {
	return request.Store == nil || *request.Store
}

func readUnifiedResponseResult(ctx context.Context, row unifiedResponseRow, request protocol.Request) (*protocol.Response, *canonical.Response, error) {
	if !row.ResultPayloadID.Valid || row.ResultPayloadID.Int64 <= 0 {
		return nil, nil, repository.ErrNotFound
	}
	db, err := model.DB().DB()
	if err != nil {
		return nil, nil, err
	}
	body, err := readUnifiedCallPayload(ctx, db, row.CallID, uint64(row.ResultPayloadID.Int64), "result")
	if err != nil {
		return nil, nil, err
	}
	defer clear(body)
	var shape struct {
		Object string `json:"object"`
	}
	if err := json.Unmarshal(body, &shape); err != nil {
		return nil, nil, repository.ErrConflict
	}
	if shape.Object == "response" {
		var response protocol.Response
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, nil, err
		}
		items, err := openairesponses.DecodeItems(response.Output)
		if err != nil {
			return nil, nil, err
		}
		canonicalResponse := &canonical.Response{
			ID: response.ID, Model: response.Model, Status: response.Status,
			CreatedAt: response.CreatedAt, Output: items,
		}
		response.ID, response.Object = row.ResourcePublicID, "response"
		response.Model, response.Status = request.Model, row.ResponseStatus
		response.Store, response.Background = responseRequestStored(request), request.Background
		setPublicPreviousResponseID(&response, request.PreviousResponseID)
		applyResponseRequestFields(&response, &request)
		return &response, canonicalResponse, nil
	}
	var canonicalResponse canonical.Response
	if err := json.Unmarshal(body, &canonicalResponse); err != nil {
		return nil, nil, err
	}
	encoded, err := openairesponses.EncodeResponseJSON(canonicalResponse)
	if err != nil {
		return nil, nil, err
	}
	var response protocol.Response
	if err := json.Unmarshal(encoded, &response); err != nil {
		return nil, nil, err
	}
	response.ID, response.Object = row.ResourcePublicID, "response"
	response.Model, response.Status = request.Model, row.ResponseStatus
	response.Store, response.Background = responseRequestStored(request), request.Background
	setPublicPreviousResponseID(&response, request.PreviousResponseID)
	applyResponseRequestFields(&response, &request)
	return &response, &canonicalResponse, nil
}

func getUnifiedResponse(ctx context.Context, userID, tokenID uint, id string) (*protocol.Response, error) {
	row, err := readUnifiedResponseRow(ctx, userID, tokenID, id)
	if err != nil {
		return nil, err
	}
	request, _, err := readUnifiedResponseRequest(ctx, row)
	if err != nil {
		return nil, err
	}
	return unifiedResponseFromRow(ctx, row, request, true)
}

func unifiedResponseFromRow(ctx context.Context, row unifiedResponseRow, request protocol.Request, requireStored bool) (*protocol.Response, error) {
	stored := responseRequestStored(request)
	if requireStored && !stored {
		return nil, repository.ErrNotFound
	}
	if row.ResultPayloadID.Valid && row.CallStatus == "completed" {
		response, _, err := readUnifiedResponseResult(ctx, row, request)
		return response, err
	}
	status := row.ResponseStatus
	if row.CallStatus == "indeterminate" {
		status = "incomplete"
	} else if row.CallStatus == "failed" {
		status = "failed"
	}
	response := &protocol.Response{ID: row.ResourcePublicID, Object: "response", CreatedAt: row.CreatedAt.Unix(), Status: status,
		Background: request.Background, Store: stored, Model: request.Model, Output: json.RawMessage(`[]`), Tools: json.RawMessage(`[]`)}
	setPublicPreviousResponseID(response, request.PreviousResponseID)
	if status == "failed" || status == "incomplete" {
		var detail struct {
			ErrorCode string `json:"error_code"`
		}
		_ = json.Unmarshal(row.Summary, &detail)
		message := "Response execution failed"
		if status == "incomplete" {
			message = "Response execution could not be verified"
		}
		response.Error = &protocol.Error{Type: "server_error", Code: detail.ErrorCode, Message: message}
	}
	applyResponseRequestFields(response, &request)
	return response, nil
}

func unifiedResponseReplay(ctx context.Context, row unifiedResponseRow) (*protocol.Response, *model.AIResponse, error) {
	request, _, err := readUnifiedResponseRequest(ctx, row)
	if err != nil {
		return nil, nil, err
	}
	response, err := unifiedResponseFromRow(ctx, row, request, false)
	if err != nil {
		return nil, nil, err
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return nil, nil, err
	}
	record := &model.AIResponse{
		ID: row.ResourcePublicID, UserID: uint(row.UserID), TokenID: uint(row.TokenID), CallID: row.CallPublicID,
		Model: request.Model, Status: response.Status, Background: request.Background, Store: responseRequestStored(request),
		PreviousResponseID: request.PreviousResponseID, RequestJSON: mustJSON(request), InputItems: append(datatypes.JSON(nil), request.Input...),
		ResponseJSON: responseJSON, OutputItems: append(datatypes.JSON(nil), response.Output...), CreatedAt: row.CreatedAt,
	}
	if row.CredentialID.Valid && row.CredentialID.Int64 > 0 {
		record.KeyID = routing.UnifiedCredentialKeyID(uint(row.CredentialID.Int64))
	}
	if row.ChannelID.Valid && row.ChannelID.Int64 > 0 {
		record.ChannelID = uint(row.ChannelID.Int64)
	}
	if row.Protocol.Valid {
		record.UpstreamTransport = responseTransportForProtocol(row.Protocol.String)
	}
	if row.ResultPayloadID.Valid && row.CallStatus == "completed" {
		_, canonicalResponse, resultErr := readUnifiedResponseResult(ctx, row, request)
		if resultErr != nil {
			return nil, nil, resultErr
		}
		if canonicalResponse != nil {
			record.ProviderResponseID = canonicalResponse.ProviderResponseID
			if record.ProviderResponseID == "" {
				record.ProviderResponseID = canonicalResponse.ID
			}
		}
	}
	return response, record, nil
}

func readUnifiedCallPayload(ctx context.Context, db *sql.DB, callID, payloadID uint64, kind string) ([]byte, error) {
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	var blobID uint64
	if err := db.QueryRowContext(ctx, `SELECT encrypted_blob_id FROM gw_api_call_payloads WHERE id=? AND call_id=? AND kind=?`, payloadID, callID, kind).Scan(&blobID); err != nil {
		return nil, err
	}
	envelope, err := store.ReadEncryptedBlob(ctx, db, blobID)
	if err != nil {
		return nil, err
	}
	keys, err := loadBackgroundKeys()
	if err != nil {
		return nil, err
	}
	defer clearBackgroundKeys(&keys)
	if envelope.Purpose != "gateway-payload" || envelope.SchemaVersion != 1 {
		return nil, security.ErrAuthentication
	}
	return repository.OpenBlob(envelope, blobID, []byte(fmt.Sprintf("call:%d:%s", callID, kind)), keys.PayloadKEK, keys.PayloadHMAC)
}

func unifiedResponseInput(ctx context.Context, userID, tokenID uint, id string) ([]json.RawMessage, error) {
	row, err := readUnifiedResponseRow(ctx, userID, tokenID, id)
	if err != nil {
		return nil, err
	}
	request, _, err := readUnifiedResponseRequest(ctx, row)
	if err != nil {
		return nil, err
	}
	if !responseRequestStored(request) {
		return nil, repository.ErrNotFound
	}
	return decodeInputItems(request.Input)
}

func loadUnifiedResponseRecord(ctx context.Context, userID, tokenID uint, id string, requireStored bool) (*model.AIResponse, uint64, error) {
	row, err := readUnifiedResponseRow(ctx, userID, tokenID, id)
	if err != nil {
		return nil, 0, err
	}
	if userID != 0 && row.UserID != uint64(userID) {
		return nil, 0, repository.ErrNotFound
	}
	request, _, err := readUnifiedResponseRequest(ctx, row)
	if err != nil {
		return nil, 0, err
	}
	stored := responseRequestStored(request)
	if requireStored && !stored {
		return nil, 0, repository.ErrNotFound
	}
	if row.CallStatus != "completed" || row.ResponseStatus != "completed" {
		return nil, 0, repository.ErrConflict
	}
	response, canonicalResponse, err := readUnifiedResponseResult(ctx, row, request)
	if err != nil {
		return nil, 0, err
	}
	input := append(datatypes.JSON(nil), request.Input...)
	output := append(datatypes.JSON(nil), response.Output...)
	responseJSON, _ := json.Marshal(response)
	usageJSON, _ := json.Marshal(response.Usage)
	record := &model.AIResponse{
		ID: row.ResourcePublicID, UserID: uint(row.UserID), TokenID: uint(row.TokenID), CallID: row.CallPublicID,
		Model: request.Model, Status: row.ResponseStatus, Background: request.Background, Store: stored,
		PreviousResponseID: request.PreviousResponseID, InputItems: input, OutputItems: output,
		ResponseJSON: responseJSON, UsageJSON: usageJSON, CreatedAt: row.CreatedAt,
	}
	if canonicalResponse != nil {
		record.ProviderResponseID = canonicalResponse.ProviderResponseID
		if record.ProviderResponseID == "" {
			record.ProviderResponseID = canonicalResponse.ID
		}
	}
	if row.CredentialID.Valid && row.CredentialID.Int64 > 0 {
		record.KeyID = routing.UnifiedCredentialKeyID(uint(row.CredentialID.Int64))
	}
	if row.ChannelID.Valid && row.ChannelID.Int64 > 0 {
		record.ChannelID = uint(row.ChannelID.Int64)
	}
	if row.Protocol.Valid {
		record.UpstreamTransport = responseTransportForProtocol(row.Protocol.String)
	}
	return record, row.ResourceID, nil
}

func responseTransportForProtocol(protocolName string) model.UpstreamTransport {
	switch strings.ToLower(strings.TrimSpace(protocolName)) {
	case "openai", "openai_chat":
		return model.UpstreamTransportOpenAIChat
	case "openai_responses":
		return model.UpstreamTransportOpenAIResponses
	case "anthropic", "anthropic_messages":
		return model.UpstreamTransportAnthropic
	case "google", "google_generate_content":
		return model.UpstreamTransportGoogle
	case "volcengine", "volcengine_responses", "volcengine_responses_v3":
		return model.UpstreamTransportVolcengineV3
	default:
		return ""
	}
}

func clearBackgroundKeys(keys *gatewayruntime.AsyncKeys) {
	if keys == nil {
		return
	}
	clear(keys.CredentialKEK)
	clear(keys.CredentialHMAC)
	clear(keys.PayloadKEK)
	clear(keys.PayloadHMAC)
}
