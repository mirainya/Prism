-- Unified media objects, result deliveries and callback delivery facts.

CREATE TABLE IF NOT EXISTS `gw_media_assets` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `user_id` bigint unsigned NOT NULL,
    `token_id` bigint unsigned NOT NULL,
    `purpose` varchar(16) NOT NULL,
    `object_key` varchar(512) NOT NULL,
    `object_version` varchar(255) NULL,
    `content_type` varchar(128) NOT NULL,
    `content_length` bigint unsigned NOT NULL,
    `sha256` char(64) NOT NULL,
    `state` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `retention_until` datetime(3) NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_media_assets_object_key` (`object_key`),
    CONSTRAINT `ck_gw_media_assets_purpose`
        CHECK (`purpose` IN ('input', 'result', 'file')),
    CONSTRAINT `ck_gw_media_assets_state`
        CHECK (`state` IN ('staging', 'active', 'deleting', 'deleted')),
    CONSTRAINT `ck_gw_media_assets_length`
        CHECK (`content_length` > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_result_deliveries` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `call_id` bigint unsigned NOT NULL,
    `attempt_id` bigint unsigned NOT NULL,
    `result_ordinal` int unsigned NOT NULL,
    `user_id` bigint unsigned NOT NULL,
    `token_id` bigint unsigned NOT NULL,
    `delivery_mode` varchar(24) NOT NULL,
    `source_kind` varchar(24) NOT NULL,
    `current_source_id` bigint unsigned NULL,
    `media_asset_ref_id` bigint unsigned NULL,
    `state` varchar(24) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `content_sha256` char(64) NULL,
    `content_length` bigint unsigned NULL,
    `content_type` varchar(128) NULL,
    `retry_at` datetime(3) NULL,
    `expires_at` datetime(3) NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_result_deliveries_attempt_ordinal` (`attempt_id`, `result_ordinal`),
    UNIQUE KEY `uq_gw_result_deliveries_id_call_attempt` (`id`, `call_id`, `attempt_id`),
    CONSTRAINT `fk_gw_result_deliveries_call`
        FOREIGN KEY (`call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `fk_gw_result_deliveries_attempt`
        FOREIGN KEY (`call_id`, `attempt_id`) REFERENCES `gw_api_call_attempts` (`call_id`, `id`),
    CONSTRAINT `ck_gw_result_deliveries_mode`
        CHECK (`delivery_mode` IN ('reference', 'managed_copy')),
    CONSTRAINT `ck_gw_result_deliveries_source`
        CHECK ((`delivery_mode` = 'reference' AND `source_kind` = 'remote_url') OR (`delivery_mode` = 'managed_copy' AND `source_kind` IN ('remote_url', 'inline_response'))),
    CONSTRAINT `ck_gw_result_deliveries_state`
        CHECK (`state` IN ('pending', 'ready', 'delivery_failed', 'expired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_result_delivery_sources` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `result_delivery_id` bigint unsigned NOT NULL,
    `source_seq` bigint unsigned NOT NULL,
    `encrypted_url_blob_id` bigint unsigned NOT NULL,
    `url_hmac` char(64) NOT NULL,
    `provider_result_identity_hmac` char(64) NULL,
    `observed_at` datetime(3) NOT NULL,
    `expires_at` datetime(3) NULL,
    `state` varchar(16) NOT NULL,
    `active_delivery_id` bigint unsigned GENERATED ALWAYS AS (CASE WHEN `state` = 'active' THEN `result_delivery_id` ELSE NULL END) STORED,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_result_delivery_sources_delivery_seq` (`result_delivery_id`, `source_seq`),
    UNIQUE KEY `uq_gw_result_delivery_sources_delivery_id` (`result_delivery_id`, `id`),
    UNIQUE KEY `uq_gw_result_delivery_sources_one_active` (`active_delivery_id`),
    CONSTRAINT `fk_gw_result_delivery_sources_delivery`
        FOREIGN KEY (`result_delivery_id`) REFERENCES `gw_result_deliveries` (`id`),
    CONSTRAINT `fk_gw_result_delivery_sources_blob`
        FOREIGN KEY (`encrypted_url_blob_id`) REFERENCES `encrypted_blobs` (`id`),
    CONSTRAINT `ck_gw_result_delivery_sources_state`
        CHECK (`state` IN ('active', 'superseded', 'invalid', 'consumed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_media_asset_refs` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `media_asset_id` bigint unsigned NOT NULL,
    `user_id` bigint unsigned NOT NULL,
    `token_id` bigint unsigned NOT NULL,
    `role` varchar(16) NOT NULL,
    `ordinal` int unsigned NOT NULL DEFAULT 0,
    `call_id` bigint unsigned NULL,
    `result_delivery_id` bigint unsigned NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_media_asset_refs_call_role_ordinal` (`call_id`, `role`, `ordinal`),
    UNIQUE KEY `uq_gw_media_asset_refs_delivery_role_ordinal` (`result_delivery_id`, `role`, `ordinal`),
    CONSTRAINT `fk_gw_media_asset_refs_asset`
        FOREIGN KEY (`media_asset_id`) REFERENCES `gw_media_assets` (`id`),
    CONSTRAINT `fk_gw_media_asset_refs_call`
        FOREIGN KEY (`call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `fk_gw_media_asset_refs_delivery`
        FOREIGN KEY (`result_delivery_id`) REFERENCES `gw_result_deliveries` (`id`),
    CONSTRAINT `ck_gw_media_asset_refs_parent`
        CHECK ((`call_id` IS NOT NULL AND `result_delivery_id` IS NULL) OR (`call_id` IS NULL AND `result_delivery_id` IS NOT NULL)),
    CONSTRAINT `ck_gw_media_asset_refs_role`
        CHECK (`role` IN ('input', 'result', 'file'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_callback_targets` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `call_id` bigint unsigned NOT NULL,
    `user_id` bigint unsigned NOT NULL,
    `token_id` bigint unsigned NOT NULL,
    `encrypted_config_blob_id` bigint unsigned NOT NULL,
    `target_hmac` char(64) NOT NULL,
    `algorithm` varchar(32) NOT NULL,
    `policy_version` int unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_callback_targets_call` (`call_id`),
    UNIQUE KEY `uq_gw_callback_targets_call_id` (`call_id`, `id`),
    CONSTRAINT `fk_gw_callback_targets_call`
        FOREIGN KEY (`call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `fk_gw_callback_targets_blob`
        FOREIGN KEY (`encrypted_config_blob_id`) REFERENCES `encrypted_blobs` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_callback_deliveries` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `call_id` bigint unsigned NOT NULL,
    `callback_target_id` bigint unsigned NOT NULL,
    `callback_event_seq` bigint unsigned NOT NULL,
    `encrypted_payload_blob_id` bigint unsigned NOT NULL,
    `payload_hmac` char(64) NOT NULL,
    `state` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `replay_expires_at` datetime(3) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_callback_deliveries_call_target_seq` (`call_id`, `callback_target_id`, `callback_event_seq`),
    CONSTRAINT `fk_gw_callback_deliveries_target`
        FOREIGN KEY (`call_id`, `callback_target_id`) REFERENCES `gw_callback_targets` (`call_id`, `id`),
    CONSTRAINT `fk_gw_callback_deliveries_blob`
        FOREIGN KEY (`encrypted_payload_blob_id`) REFERENCES `encrypted_blobs` (`id`),
    CONSTRAINT `ck_gw_callback_deliveries_state`
        CHECK (`state` IN ('pending', 'sending', 'succeeded', 'failed', 'dead_letter'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_callback_delivery_attempts` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `callback_delivery_id` bigint unsigned NOT NULL,
    `attempt_no` bigint unsigned NOT NULL,
    `state` varchar(16) NOT NULL,
    `request_hmac` char(64) NOT NULL,
    `response_hmac` char(64) NULL,
    `request_bytes_complete` boolean NOT NULL DEFAULT false,
    `response_bytes_complete` boolean NOT NULL DEFAULT false,
    `http_status` int unsigned NULL,
    `error_code` varchar(128) NOT NULL DEFAULT '',
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_callback_delivery_attempts_delivery_no` (`callback_delivery_id`, `attempt_no`),
    CONSTRAINT `fk_gw_callback_delivery_attempts_delivery`
        FOREIGN KEY (`callback_delivery_id`) REFERENCES `gw_callback_deliveries` (`id`),
    CONSTRAINT `ck_gw_callback_delivery_attempts_state`
        CHECK (`state` IN ('dispatching', 'succeeded', 'failed', 'unknown'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
