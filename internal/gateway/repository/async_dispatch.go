package repository

import (
	"context"
	"database/sql"
)

// AsyncDispatch fixes every execution input to the original attempt, including
// its credential version. Rotating the current credential cannot change a poll.
type AsyncDispatch struct {
	AdapterCode                                                   string
	AdapterVersion                                                uint32
	CallID, AttemptID, CredentialID, CredentialBlobID             uint64
	CredentialSecret                                              []byte
	RequestBlobID, TaskIdentityBlobID, CallbackBindingTokenBlobID uint64
	ChannelTransportID, ReleaseID                                 uint64
	PublicID, Protocol, BaseURL, Method, Path                     string
	AuthScheme, VendorModel, DeliveryMode, SourceURLPolicy        string
	AdapterConfig                                                 []byte
	// TransportTimeoutMS is the timeout for one HTTP exchange with the
	// provider. It deliberately does not represent the lifetime of the async
	// task; each later poll gets its own transport timeout.
	TransportTimeoutMS uint64
}

func (s *Store) ReadAsyncDispatch(ctx context.Context, asyncID uint64) (AsyncDispatch, error) {
	if asyncID == 0 {
		return AsyncDispatch{}, ErrInvalidInput
	}
	var out AsyncDispatch
	var identity, callbackToken sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT c.id,a.id,a.credential_id,credential.secret,COALESCE(v.encrypted_blob_id,0),p.encrypted_blob_id,i.encrypted_blob_id,
	(SELECT ba.encrypted_blob_id FROM gw_callback_binding_token_aliases ba WHERE ba.async_execution_id=x.id ORDER BY ba.hmac_key_version DESC LIMIT 1),
ct.id,a.catalog_release_id,c.public_id,ct.protocol,ct.base_url,ct.request_method,ct.request_path,ct.auth_scheme,
product.vendor_model,c.delivery_mode,pt.source_url_policy,COALESCE(product.capability_constraints,'{}'),ct.timeout_ms,adapter.adapter_code,adapter.contract_version
FROM gw_async_executions x
JOIN gw_api_call_attempts a ON a.id=x.attempt_id
JOIN gw_api_calls c ON c.id=a.call_id
JOIN gw_credentials credential ON credential.id=a.credential_id
JOIN gw_api_call_payloads p ON p.id=c.request_payload_id AND p.call_id=c.id AND p.kind='request'
JOIN gw_credential_versions v ON v.id=a.credential_version_id AND v.credential_id=a.credential_id
JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id
JOIN gw_products product ON product.id=pt.product_id AND product.release_id=pt.release_id
JOIN gw_channel_transports ct ON ct.id=pt.channel_transport_id AND ct.release_id=pt.release_id
JOIN gw_adapter_implementations adapter ON adapter.id=ct.adapter_implementation_id
LEFT JOIN gw_upstream_task_identities i ON i.async_execution_id=x.id AND i.status='bound'
	WHERE x.id=?`, asyncID).Scan(&out.CallID, &out.AttemptID, &out.CredentialID, &out.CredentialSecret, &out.CredentialBlobID, &out.RequestBlobID, &identity,
		&callbackToken, &out.ChannelTransportID, &out.ReleaseID, &out.PublicID, &out.Protocol, &out.BaseURL, &out.Method, &out.Path, &out.AuthScheme,
		&out.VendorModel, &out.DeliveryMode, &out.SourceURLPolicy, &out.AdapterConfig, &out.TransportTimeoutMS, &out.AdapterCode, &out.AdapterVersion)
	if err == sql.ErrNoRows {
		return AsyncDispatch{}, ErrNotFound
	}
	if identity.Valid {
		out.TaskIdentityBlobID = uint64(identity.Int64)
	}
	if callbackToken.Valid {
		out.CallbackBindingTokenBlobID = uint64(callbackToken.Int64)
	}
	return out, err
}
