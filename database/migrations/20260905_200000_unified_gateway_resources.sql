-- Unified domain resource projections and provider state/callback identities.

CREATE TABLE IF NOT EXISTS `gw_capability_tasks` (
    `resource_id` bigint unsigned NOT NULL,
    `task_no` char(36) NOT NULL,
    `status` varchar(24) NOT NULL,
    `progress` tinyint unsigned NOT NULL DEFAULT 0,
    `prompt_preview` varchar(2000) NOT NULL DEFAULT '',
    `parameter_summary` json NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`resource_id`),
    UNIQUE KEY `uq_gw_capability_tasks_task_no` (`task_no`),
    CONSTRAINT `fk_gw_capability_tasks_resource`
        FOREIGN KEY (`resource_id`) REFERENCES `gw_api_resources` (`id`),
    CONSTRAINT `ck_gw_capability_tasks_progress`
        CHECK (`progress` <= 100)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_ai_responses` (
    `resource_id` bigint unsigned NOT NULL,
    `response_no` char(36) NOT NULL,
    `status` varchar(24) NOT NULL,
    `previous_response_resource_id` bigint unsigned NULL,
    `result_summary` json NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`resource_id`),
    UNIQUE KEY `uq_gw_ai_responses_response_no` (`response_no`),
    CONSTRAINT `fk_gw_ai_responses_resource`
        FOREIGN KEY (`resource_id`) REFERENCES `gw_api_resources` (`id`),
    CONSTRAINT `fk_gw_ai_responses_previous_resource`
        FOREIGN KEY (`previous_response_resource_id`) REFERENCES `gw_api_resources` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_video_tasks` (
    `resource_id` bigint unsigned NOT NULL,
    `task_no` char(36) NOT NULL,
    `status` varchar(24) NOT NULL,
    `progress` tinyint unsigned NOT NULL DEFAULT 0,
    `specification_summary` json NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`resource_id`),
    UNIQUE KEY `uq_gw_video_tasks_task_no` (`task_no`),
    CONSTRAINT `fk_gw_video_tasks_resource`
        FOREIGN KEY (`resource_id`) REFERENCES `gw_api_resources` (`id`),
    CONSTRAINT `ck_gw_video_tasks_progress`
        CHECK (`progress` <= 100)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_provider_state_refs` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `created_attempt_id` bigint unsigned NOT NULL,
    `product_transport_id` bigint unsigned NOT NULL,
    `offering_id` bigint unsigned NOT NULL,
    `credential_id` bigint unsigned NOT NULL,
    `state_compatibility_fingerprint` char(64) NOT NULL,
    `scope_kind` varchar(24) NOT NULL,
    `scope_key` varchar(255) NOT NULL,
    `encrypted_state_blob_id` bigint unsigned NOT NULL,
    `status` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `expires_at` datetime(3) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_provider_state_refs_id_attempt` (`id`, `created_attempt_id`),
    CONSTRAINT `fk_gw_provider_state_refs_attempt`
        FOREIGN KEY (`created_attempt_id`) REFERENCES `gw_api_call_attempts` (`id`),
    CONSTRAINT `fk_gw_provider_state_refs_transport`
        FOREIGN KEY (`product_transport_id`) REFERENCES `gw_product_transports` (`id`),
    CONSTRAINT `fk_gw_provider_state_refs_offering`
        FOREIGN KEY (`offering_id`) REFERENCES `gw_offerings` (`id`),
    CONSTRAINT `fk_gw_provider_state_refs_credential`
        FOREIGN KEY (`credential_id`) REFERENCES `gw_credentials` (`id`),
    CONSTRAINT `fk_gw_provider_state_refs_blob`
        FOREIGN KEY (`encrypted_state_blob_id`) REFERENCES `encrypted_blobs` (`id`),
    CONSTRAINT `ck_gw_provider_state_refs_scope`
        CHECK (`scope_kind` IN ('product_transport', 'credential_pool', 'credential', 'credential_version')),
    CONSTRAINT `ck_gw_provider_state_refs_status`
        CHECK (`status` IN ('active', 'expired', 'revoked'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_provider_state_id_aliases` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `provider_state_ref_id` bigint unsigned NOT NULL,
    `scope_kind` varchar(24) NOT NULL,
    `scope_key` varchar(255) NOT NULL,
    `hmac_key_version` int unsigned NOT NULL,
    `value_hmac` char(64) NOT NULL,
    `matchable` boolean NOT NULL DEFAULT true,
    `created_at` datetime(3) NOT NULL,
    `matchable_ref_id` bigint unsigned GENERATED ALWAYS AS (CASE WHEN `matchable` THEN `provider_state_ref_id` ELSE NULL END) STORED,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_provider_state_alias_ref_version` (`provider_state_ref_id`, `hmac_key_version`),
    UNIQUE KEY `uq_gw_provider_state_alias_match` (`scope_kind`, `scope_key`, `hmac_key_version`, `value_hmac`, `matchable_ref_id`),
    CONSTRAINT `fk_gw_provider_state_alias_ref`
        FOREIGN KEY (`provider_state_ref_id`) REFERENCES `gw_provider_state_refs` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_upstream_callback_receipts` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `task_identity_id` bigint unsigned NULL,
    `async_execution_id` bigint unsigned NULL,
    `event_scope` varchar(255) NOT NULL,
    `hmac_key_version` int unsigned NOT NULL,
    `event_hmac` char(64) NOT NULL,
    `verified_credential_version_id` bigint unsigned NOT NULL,
    `status` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `payload_hmac` char(64) NOT NULL,
    `expires_at` datetime(3) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_callback_receipts_event` (`event_scope`, `hmac_key_version`, `event_hmac`),
    CONSTRAINT `fk_gw_callback_receipts_task_identity`
        FOREIGN KEY (`task_identity_id`) REFERENCES `gw_upstream_task_identities` (`id`),
    CONSTRAINT `fk_gw_callback_receipts_async_execution`
        FOREIGN KEY (`async_execution_id`) REFERENCES `gw_async_executions` (`id`),
    CONSTRAINT `fk_gw_callback_receipts_credential_version`
        FOREIGN KEY (`verified_credential_version_id`) REFERENCES `gw_credential_versions` (`id`),
    CONSTRAINT `ck_gw_callback_receipts_parent`
        CHECK (`task_identity_id` IS NOT NULL OR `async_execution_id` IS NOT NULL),
    CONSTRAINT `ck_gw_callback_receipts_status`
        CHECK (`status` IN ('received', 'processed', 'manual_review', 'rejected'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_upstream_callback_receipt_aliases` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `receipt_id` bigint unsigned NOT NULL,
    `hmac_key_version` int unsigned NOT NULL,
    `event_hmac` char(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_callback_receipt_alias_receipt_version` (`receipt_id`, `hmac_key_version`),
    UNIQUE KEY `uq_gw_callback_receipt_alias_match` (`hmac_key_version`, `event_hmac`),
    CONSTRAINT `fk_gw_callback_receipt_alias_receipt`
        FOREIGN KEY (`receipt_id`) REFERENCES `gw_upstream_callback_receipts` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
