-- Unified Call/Attempt/AsyncExecution core.
-- This schema intentionally does not reuse the legacy api_calls/tasks tables.

CREATE TABLE IF NOT EXISTS `gw_api_calls` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `public_id` char(36) NOT NULL,
    `user_id` bigint unsigned NOT NULL,
    `token_id` bigint unsigned NOT NULL,
    `operation_contract_id` bigint unsigned NOT NULL,
    `catalog_release_id` bigint unsigned NOT NULL,
    `model_operation_id` bigint unsigned NOT NULL,
    `sku_id` bigint unsigned NOT NULL,
    `request_payload_id` bigint unsigned NULL,
    `result_payload_id` bigint unsigned NULL,
    `status` varchar(24) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `quoted_amount` decimal(38,18) NOT NULL DEFAULT 0,
    `price_currency` varchar(16) NOT NULL,
    `price_currency_version` int unsigned NOT NULL,
    `delivery_mode` varchar(24) NOT NULL,
    `current_attempt_id` bigint unsigned NULL,
    `final_attempt_id` bigint unsigned NULL,
    `callback_event_seq` bigint unsigned NOT NULL DEFAULT 0,
    `next_action_at` datetime(3) NULL,
    `lease_owner` varchar(128) NOT NULL DEFAULT '',
    `lease_expires_at` datetime(3) NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_api_calls_public_id` (`public_id`),
    UNIQUE KEY `uq_gw_api_calls_id_public_id` (`id`, `public_id`),
    CONSTRAINT `fk_gw_api_calls_operation_contract`
        FOREIGN KEY (`operation_contract_id`) REFERENCES `gw_operation_contracts` (`id`),
    CONSTRAINT `fk_gw_api_calls_release`
        FOREIGN KEY (`catalog_release_id`) REFERENCES `gw_catalog_releases` (`id`),
    CONSTRAINT `fk_gw_api_calls_model_operation`
        FOREIGN KEY (`catalog_release_id`, `model_operation_id`)
        REFERENCES `gw_model_operations` (`release_id`, `id`),
    CONSTRAINT `fk_gw_api_calls_sku`
        FOREIGN KEY (`catalog_release_id`, `sku_id`) REFERENCES `gw_skus` (`release_id`, `id`),
    CONSTRAINT `fk_gw_api_calls_model_operation_sku`
        FOREIGN KEY (`catalog_release_id`, `sku_id`, `model_operation_id`)
        REFERENCES `gw_skus` (`release_id`, `id`, `model_operation_id`),
    CONSTRAINT `ck_gw_api_calls_status`
        CHECK (`status` IN ('received', 'in_progress', 'retry_pending', 'completed', 'failed', 'cancelled', 'indeterminate')),
    CONSTRAINT `ck_gw_api_calls_amount`
        CHECK (`quoted_amount` >= 0),
    CONSTRAINT `ck_gw_api_calls_delivery_mode`
        CHECK (`delivery_mode` IN ('reference', 'managed_copy'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_api_call_payloads` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `call_id` bigint unsigned NOT NULL,
    `kind` varchar(16) NOT NULL,
    `schema_version` int unsigned NOT NULL,
    `encrypted_blob_id` bigint unsigned NULL,
    `content_hmac` char(64) NOT NULL,
    `content_length` bigint unsigned NOT NULL,
    `retention_until` datetime(3) NULL,
    `purged_at` datetime(3) NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_api_call_payloads_call_kind` (`call_id`, `kind`),
    CONSTRAINT `fk_gw_api_call_payloads_call`
        FOREIGN KEY (`call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `fk_gw_api_call_payloads_blob`
        FOREIGN KEY (`encrypted_blob_id`) REFERENCES `encrypted_blobs` (`id`),
    CONSTRAINT `ck_gw_api_call_payloads_kind`
        CHECK (`kind` IN ('request', 'result'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_api_call_idempotencies` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `token_id` bigint unsigned NOT NULL,
    `operation_contract_id` bigint unsigned NOT NULL,
    `call_id` bigint unsigned NULL,
    `key_hmac` char(64) NOT NULL,
    `hmac_key_version` int unsigned NOT NULL,
    `request_hmac` char(64) NOT NULL,
    `status` varchar(16) NOT NULL,
    `replay_expires_at` datetime(3) NULL,
    `key_reuse_after` datetime(3) NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_api_call_idempotencies_token_contract_key` (`token_id`, `operation_contract_id`, `hmac_key_version`, `key_hmac`),
    CONSTRAINT `fk_gw_api_call_idempotencies_operation_contract`
        FOREIGN KEY (`operation_contract_id`) REFERENCES `gw_operation_contracts` (`id`),
    CONSTRAINT `fk_gw_api_call_idempotencies_call`
        FOREIGN KEY (`call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `ck_gw_api_call_idempotencies_status`
        CHECK (`status` IN ('active', 'expired', 'retired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_api_call_idempotency_keys` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `idempotency_id` bigint unsigned NOT NULL,
    `token_id` bigint unsigned NOT NULL,
    `operation_contract_id` bigint unsigned NOT NULL,
    `key_hmac` char(64) NOT NULL,
    `hmac_key_version` int unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_api_call_idempotency_keys_fact_version` (`idempotency_id`, `hmac_key_version`),
    UNIQUE KEY `uq_gw_api_call_idempotency_keys_lookup` (`token_id`, `operation_contract_id`, `hmac_key_version`, `key_hmac`),
    CONSTRAINT `fk_gw_api_call_idempotency_keys_fact`
        FOREIGN KEY (`idempotency_id`) REFERENCES `gw_api_call_idempotencies` (`id`),
    CONSTRAINT `fk_gw_api_call_idempotency_keys_operation_contract`
        FOREIGN KEY (`operation_contract_id`) REFERENCES `gw_operation_contracts` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_api_resources` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `public_id` char(36) NOT NULL,
    `resource_kind` varchar(32) NOT NULL,
    `call_id` bigint unsigned NOT NULL,
    `user_id` bigint unsigned NOT NULL,
    `token_id` bigint unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_api_resources_public_id` (`public_id`),
    UNIQUE KEY `uq_gw_api_resources_call` (`call_id`),
    UNIQUE KEY `uq_gw_api_resources_id_public_id` (`id`, `public_id`),
    CONSTRAINT `fk_gw_api_resources_call`
        FOREIGN KEY (`call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `ck_gw_api_resources_kind`
        CHECK (`resource_kind` IN ('capability_task', 'response', 'video_task', 'file'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_api_call_attempts` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `call_id` bigint unsigned NOT NULL,
    `attempt_no` bigint unsigned NOT NULL,
    `catalog_release_id` bigint unsigned NOT NULL,
    `sku_id` bigint unsigned NOT NULL,
    `route_id` bigint unsigned NOT NULL,
    `offering_id` bigint unsigned NOT NULL,
    `product_transport_id` bigint unsigned NOT NULL,
    `credential_pool_id` bigint unsigned NOT NULL,
    `credential_id` bigint unsigned NOT NULL,
    `credential_version_id` bigint unsigned NOT NULL,
    `purpose_grant_id` bigint unsigned NOT NULL,
    `state` varchar(24) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `active_call_id` bigint unsigned GENERATED ALWAYS AS (
        CASE WHEN `state` IN ('started', 'recovery_pending') THEN `call_id` ELSE NULL END
    ) STORED,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_api_call_attempts_call_no` (`call_id`, `attempt_no`),
    UNIQUE KEY `uq_gw_api_call_attempts_one_active` (`active_call_id`),
    UNIQUE KEY `uq_gw_api_call_attempts_call_id_id` (`call_id`, `id`),
    CONSTRAINT `fk_gw_api_call_attempts_call`
        FOREIGN KEY (`call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `fk_gw_api_call_attempts_route`
        FOREIGN KEY (`catalog_release_id`, `route_id`) REFERENCES `gw_routes` (`release_id`, `id`),
    CONSTRAINT `fk_gw_api_call_attempts_route_selection`
        FOREIGN KEY (`catalog_release_id`, `route_id`, `sku_id`, `offering_id`)
        REFERENCES `gw_routes` (`release_id`, `id`, `sku_id`, `offering_id`),
    CONSTRAINT `fk_gw_api_call_attempts_sku`
        FOREIGN KEY (`catalog_release_id`, `sku_id`) REFERENCES `gw_skus` (`release_id`, `id`),
    CONSTRAINT `fk_gw_api_call_attempts_offering`
        FOREIGN KEY (`catalog_release_id`, `offering_id`) REFERENCES `gw_offerings` (`release_id`, `id`),
    CONSTRAINT `fk_gw_api_call_attempts_offering_transport`
        FOREIGN KEY (`catalog_release_id`, `offering_id`, `product_transport_id`)
        REFERENCES `gw_offerings` (`release_id`, `id`, `product_transport_id`),
    CONSTRAINT `fk_gw_api_call_attempts_product_transport`
        FOREIGN KEY (`catalog_release_id`, `product_transport_id`) REFERENCES `gw_product_transports` (`release_id`, `id`),
    CONSTRAINT `fk_gw_api_call_attempts_pool`
        FOREIGN KEY (`credential_pool_id`) REFERENCES `gw_credential_pools` (`id`),
    CONSTRAINT `fk_gw_api_call_attempts_credential`
        FOREIGN KEY (`credential_id`) REFERENCES `gw_credentials` (`id`),
    CONSTRAINT `fk_gw_api_call_attempts_pool_credential`
        FOREIGN KEY (`credential_pool_id`, `credential_id`)
        REFERENCES `gw_credentials` (`credential_pool_id`, `id`),
    CONSTRAINT `fk_gw_api_call_attempts_credential_version`
        FOREIGN KEY (`credential_id`, `credential_version_id`) REFERENCES `gw_credential_versions` (`credential_id`, `id`),
    CONSTRAINT `fk_gw_api_call_attempts_purpose_grant`
        FOREIGN KEY (`credential_id`, `purpose_grant_id`) REFERENCES `gw_credential_purpose_grants` (`credential_id`, `id`),
    CONSTRAINT `ck_gw_api_call_attempts_state`
        CHECK (`state` IN ('started', 'recovery_pending', 'completed', 'failed', 'cancelled', 'not_created', 'terminated_unknown'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_async_executions` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `attempt_id` bigint unsigned NOT NULL,
    `upstream_scope_kind` varchar(24) NOT NULL,
    `upstream_scope_key` varchar(255) NOT NULL,
    `state` varchar(32) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `cancel_intent_at` datetime(3) NULL,
    `cancel_resolved_at` datetime(3) NULL,
    `cancel_resolution` varchar(16) NULL,
    `next_action_at` datetime(3) NULL,
    `action_seq` bigint unsigned NOT NULL DEFAULT 0,
    `lease_owner` varchar(128) NOT NULL DEFAULT '',
    `lease_expires_at` datetime(3) NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_async_executions_attempt` (`attempt_id`),
    CONSTRAINT `fk_gw_async_executions_attempt`
        FOREIGN KEY (`attempt_id`) REFERENCES `gw_api_call_attempts` (`id`),
    CONSTRAINT `ck_gw_async_executions_state`
        CHECK (`state` IN ('allocated', 'submitting', 'submission_unknown', 'accepted', 'running', 'manual_review', 'cancel_requested', 'cancel_unknown', 'succeeded', 'failed', 'cancelled', 'not_created', 'terminated_unknown')),
    CONSTRAINT `ck_gw_async_executions_cancel`
        CHECK ((`cancel_intent_at` IS NULL AND `cancel_resolved_at` IS NULL AND `cancel_resolution` IS NULL) OR (`cancel_intent_at` IS NOT NULL AND (`cancel_resolved_at` IS NULL AND `cancel_resolution` IS NULL OR `cancel_resolved_at` IS NOT NULL AND `cancel_resolution` IN ('confirmed', 'rejected', 'superseded'))))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
