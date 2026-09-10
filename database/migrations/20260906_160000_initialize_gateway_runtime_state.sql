-- Initialize the singleton needed by first-time console setup and catalog
-- activation. Existing deployments keep their active release and state version.
INSERT INTO gw_catalog_runtime_state (id, active_release_id, state_version, updated_at)
SELECT 1, NULL, 1, UTC_TIMESTAMP(3)
WHERE NOT EXISTS (SELECT 1 FROM gw_catalog_runtime_state WHERE id = 1);
