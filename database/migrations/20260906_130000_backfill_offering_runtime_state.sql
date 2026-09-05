-- Give imported offerings an explicit runtime state before unified traffic is enabled.
-- The importer predates the runtime-state table and left these rows implicit;
-- backfilling active rows preserves the imported status while keeping selection
-- dependent on an auditable state transition.
INSERT INTO gw_offering_runtime_state
    (release_id, offering_id, state, state_version, reason_code, updated_at)
SELECT o.release_id, o.id, 'active', 1, 'legacy_import_backfill', UTC_TIMESTAMP(3)
FROM gw_offerings o
LEFT JOIN gw_offering_runtime_state s
    ON s.release_id = o.release_id AND s.offering_id = o.id
WHERE s.id IS NULL;
