-- Reason: remove AICost video offerings absent from the 2026-09-23 upstream model document.
-- Scope: nine active offerings for the listed AICost products in the current catalog.
-- Impact: stop new routing while retaining historical calls; append state and audit evidence.
-- This migration is safe after the operational change: already-disabled rows are not selected.

START TRANSACTION;

CREATE TEMPORARY TABLE tmp_aicost_doc_absent_20260923 (
  release_id BIGINT UNSIGNED NOT NULL,
  offering_id BIGINT UNSIGNED NOT NULL,
  state_version BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (release_id, offering_id)
);

INSERT INTO tmp_aicost_doc_absent_20260923
SELECT rs.release_id, rs.offering_id, rs.state_version
FROM gw_catalog_runtime_state active
JOIN gw_products p ON p.release_id = active.active_release_id
JOIN gateway_channels c ON c.id = p.channel_id
JOIN gw_product_transports pt
  ON pt.release_id = p.release_id AND pt.product_id = p.id
JOIN gw_offerings o
  ON o.release_id = pt.release_id AND o.product_transport_id = pt.id
JOIN gw_offering_runtime_state rs
  ON rs.release_id = o.release_id AND rs.offering_id = o.id
WHERE active.id = 1 AND c.channel_code = 'aicost'
  AND (p.id, p.vendor_model) IN (
    (173, 'seedance-2.0-ad-720p'),
    (124, 'seedance2.0-dj-480p'),
    (131, 'seedance2.0-fast-480p'),
    (125, 'seedance2.0-mini-480p'),
    (126, 'seedance2.0-mini-720p'),
    (133, 'seedance2.0-standard-480p'),
    (168, 'seedance2.5-dj-480p'),
    (169, 'seedance2.5-dj-720p'),
    (172, 'seedance2.5-md-1080p')
  )
  AND rs.state = 'active' AND rs.state_version = 1;

UPDATE gw_offering_runtime_state rs
JOIN tmp_aicost_doc_absent_20260923 t
  ON t.release_id = rs.release_id AND t.offering_id = rs.offering_id
SET rs.state = 'disabled',
    rs.state_version = rs.state_version + 1,
    rs.reason_code = 'aicost_doc_absent_20260923',
    rs.updated_at = UTC_TIMESTAMP(3)
WHERE rs.state = 'active' AND rs.state_version = t.state_version;

INSERT INTO gw_offering_state_events
  (release_id, offering_id, state_version, old_state, new_state, reason_code, created_at)
SELECT t.release_id, t.offering_id, t.state_version + 1,
       'active', 'disabled', 'aicost_doc_absent_20260923', UTC_TIMESTAMP(3)
FROM tmp_aicost_doc_absent_20260923 t;

INSERT INTO audit_events
  (actor_type, actor_user_id, action, resource_type, resource_id,
   outcome, http_status, metadata, created_at)
SELECT 'service', 0, 'unified.offering.runtime_state', 'offering',
       CAST(t.offering_id AS CHAR), 'success', 200,
       JSON_OBJECT('release_id', t.release_id, 'old_state', 'active',
                   'new_state', 'disabled',
                   'reason_code', 'aicost_doc_absent_20260923'),
       UTC_TIMESTAMP(3)
FROM tmp_aicost_doc_absent_20260923 t;

DROP TEMPORARY TABLE tmp_aicost_doc_absent_20260923;
COMMIT;
