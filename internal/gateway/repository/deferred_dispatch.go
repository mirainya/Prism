package repository

import (
	"context"
	"database/sql"
)

// DeferredDispatch contains only immutable facts pinned by an Attempt. Secret
// and request bytes remain encrypted and are opened by the worker.
type DeferredDispatch struct {
	CallID, AttemptID, ResourceID, UserID, TokenID            uint64
	ReleaseID, OperationContractID, ModelOperationID, SKUID   uint64
	RouteID, OfferingID, ProductTransportID, CredentialPoolID uint64
	CredentialID, CredentialVersionID, PurposeGrantID         uint64
	CredentialBlobID, RequestBlobID                           uint64
	CredentialSecret                                          []byte
	ChannelID                                                 uint64
	PublicID, PublicModel, VendorModel, Protocol, BaseURL     string
	RequestPath, DeliveryMode                                 string
	Capabilities                                              []byte
}

func (s *Store) ReadDeferredDispatch(ctx context.Context, attemptID uint64) (DeferredDispatch, error) {
	if s == nil || attemptID == 0 {
		return DeferredDispatch{}, ErrInvalidInput
	}
	var out DeferredDispatch
	err := s.db.QueryRowContext(ctx, `SELECT c.id,a.id,r.id,c.user_id,c.token_id,
c.catalog_release_id,c.operation_contract_id,c.model_operation_id,c.sku_id,
a.route_id,a.offering_id,a.product_transport_id,a.credential_pool_id,a.credential_id,a.credential_version_id,a.purpose_grant_id,
COALESCE(cv.encrypted_blob_id,0),credential.secret,payload.encrypted_blob_id,product.channel_id,c.public_id,product.vendor_model,ct.protocol,ct.base_url,ct.request_path,c.delivery_mode,cm.capability_tags
FROM gw_api_call_attempts a
JOIN gw_api_calls c ON c.id=a.call_id
JOIN gw_credentials credential ON credential.id=a.credential_id
JOIN gw_api_resources r ON r.call_id=c.id AND r.resource_kind='response'
JOIN gw_ai_responses response ON response.resource_id=r.id
JOIN gw_api_call_payloads payload ON payload.id=c.request_payload_id AND payload.call_id=c.id AND payload.kind='request'
JOIN gw_credential_versions cv ON cv.id=a.credential_version_id AND cv.credential_id=a.credential_id
JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id
JOIN gw_products product ON product.id=pt.product_id AND product.release_id=pt.release_id
JOIN gw_channel_transports ct ON ct.id=pt.channel_transport_id AND ct.release_id=pt.release_id
JOIN gw_catalog_models cm ON cm.release_id=c.catalog_release_id
JOIN gw_model_operations mo ON mo.id=c.model_operation_id AND mo.release_id=cm.release_id AND mo.catalog_model_id=cm.id
WHERE a.id=?`, attemptID).Scan(
		&out.CallID, &out.AttemptID, &out.ResourceID, &out.UserID, &out.TokenID,
		&out.ReleaseID, &out.OperationContractID, &out.ModelOperationID, &out.SKUID,
		&out.RouteID, &out.OfferingID, &out.ProductTransportID, &out.CredentialPoolID,
		&out.CredentialID, &out.CredentialVersionID, &out.PurposeGrantID,
		&out.CredentialBlobID, &out.CredentialSecret, &out.RequestBlobID, &out.ChannelID, &out.PublicID,
		&out.VendorModel, &out.Protocol, &out.BaseURL, &out.RequestPath, &out.DeliveryMode,
		&out.Capabilities,
	)
	if err == sql.ErrNoRows {
		return DeferredDispatch{}, ErrNotFound
	}
	return out, err
}
