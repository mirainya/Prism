package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// AttemptDispatch is the immutable execution snapshot selected before a
// deferred synchronous request is queued. Payloads remain behind encrypted
// blob references; credentials may be direct plaintext or legacy blobs.
type AttemptDispatch struct {
	CallID, AttemptID, ResourceID                                       uint64
	ReleaseID, OperationContractID, ModelOperationID, SKUID             uint64
	RouteID, OfferingID, ProductTransportID, ChannelTransportID         uint64
	CredentialPoolID, CredentialID, CredentialVersionID, PurposeGrantID uint64
	CredentialBlobID, RequestBlobID, ResultBlobID                       uint64
	CredentialSecret                                                    []byte
	ChannelID, AbilityID                                                uint64
	PublicID, ResourceKind, ResourceStatus                              string
	Protocol, BaseURL, RequestMethod, RequestPath, AuthScheme           string
	VendorModel, DeliveryMode                                           string
	TimeoutMS                                                           uint64
	CreatedAt                                                           time.Time
}

func (s *Store) ReadAttemptDispatch(ctx context.Context, attemptID uint64) (AttemptDispatch, error) {
	if s == nil || attemptID == 0 {
		return AttemptDispatch{}, ErrInvalidInput
	}
	var out AttemptDispatch
	var resultBlobID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT
c.id,a.id,r.id,a.catalog_release_id,c.operation_contract_id,c.model_operation_id,c.sku_id,
a.route_id,a.offering_id,a.product_transport_id,ct.id,a.credential_pool_id,a.credential_id,credential.secret,
a.credential_version_id,a.purpose_grant_id,COALESCE(cv.encrypted_blob_id,0),request_blob.encrypted_blob_id,
result_blob.encrypted_blob_id,ch.id,c.model_operation_id,c.public_id,r.resource_kind,response.status,
ct.protocol,ct.base_url,ct.request_method,ct.request_path,ct.auth_scheme,product.vendor_model,
c.delivery_mode,LEAST(ct.timeout_ms,pt.timeout_ms),c.created_at
FROM gw_api_call_attempts a
JOIN gw_api_calls c ON c.id=a.call_id
JOIN gw_credentials credential ON credential.id=a.credential_id
JOIN gw_api_resources r ON r.call_id=c.id
JOIN gw_ai_responses response ON response.resource_id=r.id AND r.resource_kind='response'
JOIN gw_api_call_payloads request_blob ON request_blob.id=c.request_payload_id AND request_blob.call_id=c.id AND request_blob.kind='request' AND request_blob.encrypted_blob_id IS NOT NULL
LEFT JOIN gw_api_call_payloads result_blob ON result_blob.id=c.result_payload_id AND result_blob.call_id=c.id AND result_blob.kind='result' AND result_blob.encrypted_blob_id IS NOT NULL
JOIN gw_credential_versions cv ON cv.id=a.credential_version_id AND cv.credential_id=a.credential_id
JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id
JOIN gw_products product ON product.id=pt.product_id AND product.release_id=pt.release_id
JOIN gw_channel_transports ct ON ct.id=pt.channel_transport_id AND ct.release_id=pt.release_id
JOIN gateway_channels ch ON ch.id=product.channel_id AND ch.id=ct.channel_id
WHERE a.id=?`, attemptID).Scan(
		&out.CallID, &out.AttemptID, &out.ResourceID, &out.ReleaseID, &out.OperationContractID,
		&out.ModelOperationID, &out.SKUID, &out.RouteID, &out.OfferingID,
		&out.ProductTransportID, &out.ChannelTransportID, &out.CredentialPoolID,
		&out.CredentialID, &out.CredentialSecret, &out.CredentialVersionID, &out.PurposeGrantID,
		&out.CredentialBlobID, &out.RequestBlobID, &resultBlobID, &out.ChannelID,
		&out.AbilityID, &out.PublicID, &out.ResourceKind, &out.ResourceStatus,
		&out.Protocol, &out.BaseURL, &out.RequestMethod, &out.RequestPath,
		&out.AuthScheme, &out.VendorModel, &out.DeliveryMode, &out.TimeoutMS,
		&out.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return AttemptDispatch{}, ErrNotFound
	}
	if err != nil {
		return AttemptDispatch{}, err
	}
	if resultBlobID.Valid {
		out.ResultBlobID = uint64(resultBlobID.Int64)
	}
	return out, nil
}

type ResponseResource struct {
	ResourceID, CallID, AttemptID, UserID, TokenID uint64
	RequestBlobID, ResultBlobID                    uint64
	PublicID, Status, CallStatus, AttemptStatus    string
	PreviousPublicID                               string
	Summary                                        json.RawMessage
	CreatedAt, UpdatedAt                           time.Time
}

// ReadResponseResource is the public-resource lookup for the unified
// Responses API. Ownership is enforced in SQL rather than after decryption.
func (s *Store) ReadResponseResource(ctx context.Context, tokenID uint64, publicID string) (ResponseResource, error) {
	if s == nil || tokenID == 0 || publicID == "" {
		return ResponseResource{}, ErrInvalidInput
	}
	var out ResponseResource
	var previous sql.NullString
	var summary []byte
	var resultBlobID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT r.id,c.id,a.id,r.user_id,r.token_id,
request_blob.encrypted_blob_id,result_blob.encrypted_blob_id,r.public_id,response.status,
c.status,a.state,previous.public_id,response.result_summary,r.created_at,response.updated_at
FROM gw_api_resources r
JOIN gw_ai_responses response ON response.resource_id=r.id AND r.resource_kind='response'
JOIN gw_api_calls c ON c.id=r.call_id
JOIN gw_api_call_attempts a ON a.call_id=c.id
JOIN gw_api_call_payloads request_blob ON request_blob.id=c.request_payload_id AND request_blob.call_id=c.id AND request_blob.kind='request' AND request_blob.encrypted_blob_id IS NOT NULL
LEFT JOIN gw_api_call_payloads result_blob ON result_blob.id=c.result_payload_id AND result_blob.call_id=c.id AND result_blob.kind='result' AND result_blob.encrypted_blob_id IS NOT NULL
LEFT JOIN gw_api_resources previous ON previous.id=response.previous_response_resource_id AND previous.resource_kind='response'
WHERE r.public_id=? AND r.token_id=? AND r.deleted_at IS NULL
ORDER BY a.attempt_no DESC LIMIT 1`, publicID, tokenID).Scan(
		&out.ResourceID, &out.CallID, &out.AttemptID, &out.UserID, &out.TokenID,
		&out.RequestBlobID, &resultBlobID, &out.PublicID, &out.Status,
		&out.CallStatus, &out.AttemptStatus, &previous, &summary, &out.CreatedAt,
		&out.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return ResponseResource{}, ErrNotFound
	}
	if err != nil {
		return ResponseResource{}, err
	}
	if resultBlobID.Valid {
		out.ResultBlobID = uint64(resultBlobID.Int64)
	}
	if previous.Valid {
		out.PreviousPublicID = previous.String
	}
	out.Summary = append(json.RawMessage(nil), summary...)
	return out, nil
}

type AttemptRequestState struct {
	RequestID uint64
	Status    string
}

func (s *Store) ReadLatestAttemptRequest(ctx context.Context, attemptID uint64, action string) (AttemptRequestState, error) {
	if s == nil || attemptID == 0 || action == "" {
		return AttemptRequestState{}, ErrInvalidInput
	}
	var out AttemptRequestState
	err := s.db.QueryRowContext(ctx, `SELECT id,status FROM gw_channel_request_logs WHERE attempt_id=? AND action=? ORDER BY request_seq DESC LIMIT 1`, attemptID, action).Scan(&out.RequestID, &out.Status)
	if err == sql.ErrNoRows {
		return AttemptRequestState{}, ErrNotFound
	}
	return out, err
}
