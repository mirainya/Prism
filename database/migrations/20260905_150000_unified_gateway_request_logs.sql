-- Unified outbound exchange logs and credential concurrency slots.
-- Request/response bodies, headers, URLs and provider identifiers are never
-- stored in these rows; only HMACs and bounded diagnostic blobs are allowed.

CREATE TABLE IF NOT EXISTS `gw_channel_request_logs` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `attempt_id` bigint unsigned NULL,
    `control_plane_run_id` bigint unsigned NULL,
    `request_seq` bigint unsigned NOT NULL,
    `action` varchar(32) NOT NULL,
    `status` varchar(24) NOT NULL,
    `request_mapping_hmac` char(64) NOT NULL,
    `request_bytes_hmac` char(64) NULL,
    `response_bytes_hmac` char(64) NULL,
    `request_bytes_complete` boolean NOT NULL DEFAULT false,
    `response_bytes_complete` boolean NOT NULL DEFAULT false,
    `diagnostic_blob_id` bigint unsigned NULL,
    `http_status` int unsigned NULL,
    `duration_ms` bigint unsigned NULL,
    `error_code` varchar(128) NOT NULL DEFAULT '',
    `created_at` datetime(3) NOT NULL,
    `completed_at` datetime(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_request_logs_attempt_seq` (`attempt_id`, `request_seq`),
    UNIQUE KEY `uq_gw_request_logs_control_plane_seq` (`control_plane_run_id`, `request_seq`),
    CONSTRAINT `fk_gw_request_logs_attempt`
        FOREIGN KEY (`attempt_id`) REFERENCES `gw_api_call_attempts` (`id`),
    CONSTRAINT `fk_gw_request_logs_diagnostic_blob`
        FOREIGN KEY (`diagnostic_blob_id`) REFERENCES `encrypted_blobs` (`id`),
    CONSTRAINT `ck_gw_request_logs_one_root`
        CHECK ((`attempt_id` IS NOT NULL AND `control_plane_run_id` IS NULL) OR (`attempt_id` IS NULL AND `control_plane_run_id` IS NOT NULL)),
    CONSTRAINT `ck_gw_request_logs_action`
        CHECK (`action` IN ('submit', 'recover', 'query', 'cancel', 'named_action', 'result_fetch', 'catalog_discovery', 'entitlement_probe', 'commercial_check')),
    CONSTRAINT `ck_gw_request_logs_status`
        CHECK (`status` IN ('prepared', 'dispatching', 'not_sent', 'sent', 'response_recorded', 'unknown'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_credential_slots` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `credential_id` bigint unsigned NOT NULL,
    `credential_pool_id` bigint unsigned NOT NULL,
    `scope` varchar(16) NOT NULL,
    `request_log_id` bigint unsigned NULL,
    `attempt_id` bigint unsigned NULL,
    `state` varchar(24) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `acquired_at` datetime(3) NOT NULL,
    `released_at` datetime(3) NULL,
    `active_request_id` bigint unsigned GENERATED ALWAYS AS (
        CASE WHEN `scope` = 'request' AND `state` IN ('active', 'recovery_required') THEN `request_log_id` ELSE NULL END
    ) STORED,
    `active_attempt_id` bigint unsigned GENERATED ALWAYS AS (
        CASE WHEN `scope` = 'task' AND `state` IN ('active', 'recovery_required') THEN `attempt_id` ELSE NULL END
    ) STORED,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_credential_slots_active_request` (`active_request_id`),
    UNIQUE KEY `uq_gw_credential_slots_active_attempt` (`active_attempt_id`),
    CONSTRAINT `fk_gw_credential_slots_credential`
        FOREIGN KEY (`credential_id`) REFERENCES `gw_credentials` (`id`),
    CONSTRAINT `fk_gw_credential_slots_pool_credential`
        FOREIGN KEY (`credential_pool_id`, `credential_id`)
        REFERENCES `gw_credentials` (`credential_pool_id`, `id`),
    CONSTRAINT `fk_gw_credential_slots_request_log`
        FOREIGN KEY (`request_log_id`) REFERENCES `gw_channel_request_logs` (`id`),
    CONSTRAINT `fk_gw_credential_slots_attempt`
        FOREIGN KEY (`attempt_id`) REFERENCES `gw_api_call_attempts` (`id`),
    CONSTRAINT `ck_gw_credential_slots_scope`
        CHECK ((`scope` = 'request' AND `request_log_id` IS NOT NULL AND `attempt_id` IS NULL) OR (`scope` = 'task' AND `request_log_id` IS NULL AND `attempt_id` IS NOT NULL)),
    CONSTRAINT `ck_gw_credential_slots_state`
        CHECK (`state` IN ('active', 'recovery_required', 'released')),
    CONSTRAINT `ck_gw_credential_slots_recovery_scope`
        CHECK (`state` <> 'recovery_required' OR `scope` = 'task')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
