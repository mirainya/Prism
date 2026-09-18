package repository

import (
	"context"
	"database/sql"
)

// CapabilityDispatch is the immutable catalog and credential snapshot used
// by a synchronous capability request after its Attempt has been committed.
type CapabilityDispatch struct {
	CallID, AttemptID, CredentialID, CredentialBlobID uint64
	CredentialSecret                                  []byte
	RequestBlobID, ChannelTransportID, ReleaseID      uint64
	PublicID, Protocol, BaseURL, Method, Path         string
	AuthScheme, VendorModel, DeliveryMode             string
	SourceURLPolicy, AdapterCode                      string
	AdapterVersion, TimeoutMS                         uint32
}

func (s *Store) ReadCapabilityDispatch(ctx context.Context, attemptID uint64) (CapabilityDispatch, error) {
	if s == nil || attemptID == 0 {
		return CapabilityDispatch{}, ErrInvalidInput
	}
	var out CapabilityDispatch
	err := s.db.QueryRowContext(ctx, `SELECT c.id,a.id,a.credential_id,credential.secret,COALESCE(cv.encrypted_blob_id,0),p.encrypted_blob_id,
ct.id,a.catalog_release_id,c.public_id,ct.protocol,ct.base_url,ct.request_method,ct.request_path,
ct.auth_scheme,product.vendor_model,c.delivery_mode,pt.source_url_policy,adapter.adapter_code,
adapter.contract_version,LEAST(ct.timeout_ms,pt.timeout_ms)
FROM gw_api_call_attempts a
JOIN gw_api_calls c ON c.id=a.call_id
JOIN gw_credentials credential ON credential.id=a.credential_id
JOIN gw_api_resources resource ON resource.call_id=c.id AND resource.resource_kind='capability_task'
JOIN gw_capability_tasks task ON task.resource_id=resource.id
JOIN gw_api_call_payloads p ON p.id=c.request_payload_id AND p.call_id=c.id AND p.kind='request'
JOIN gw_credential_versions cv ON cv.id=a.credential_version_id AND cv.credential_id=a.credential_id
JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id
JOIN gw_products product ON product.id=pt.product_id AND product.release_id=pt.release_id
JOIN gw_channel_transports ct ON ct.id=pt.channel_transport_id AND ct.release_id=pt.release_id
JOIN gw_adapter_implementations adapter ON adapter.id=ct.adapter_implementation_id
WHERE a.id=?`, attemptID).Scan(
		&out.CallID, &out.AttemptID, &out.CredentialID, &out.CredentialSecret, &out.CredentialBlobID,
		&out.RequestBlobID, &out.ChannelTransportID, &out.ReleaseID, &out.PublicID,
		&out.Protocol, &out.BaseURL, &out.Method, &out.Path, &out.AuthScheme,
		&out.VendorModel, &out.DeliveryMode, &out.SourceURLPolicy, &out.AdapterCode,
		&out.AdapterVersion, &out.TimeoutMS,
	)
	if err == sql.ErrNoRows {
		return CapabilityDispatch{}, ErrNotFound
	}
	return out, err
}
