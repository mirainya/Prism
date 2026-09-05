-- Unified asynchronous identities, callback binding aliases and internal outbox.

CREATE TABLE IF NOT EXISTS `gw_upstream_task_identities` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `async_execution_id` bigint unsigned NULL,
    `scope_kind` varchar(24) NOT NULL,
    `scope_key` varchar(255) NOT NULL,
    `encrypted_blob_id` bigint unsigned NOT NULL,
    `status` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `expires_at` datetime(3) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_task_identities_async_execution` (`async_execution_id`),
    UNIQUE KEY `uq_gw_task_identities_id_blob` (`id`, `encrypted_blob_id`),
    CONSTRAINT `fk_gw_task_identities_async_execution`
        FOREIGN KEY (`async_execution_id`) REFERENCES `gw_async_executions` (`id`),
    CONSTRAINT `fk_gw_task_identities_blob`
        FOREIGN KEY (`encrypted_blob_id`) REFERENCES `encrypted_blobs` (`id`),
    CONSTRAINT `ck_gw_task_identities_scope`
        CHECK (`scope_kind` IN ('global', 'channel', 'product_transport', 'credential_pool', 'credential')),
    CONSTRAINT `ck_gw_task_identities_status`
        CHECK (`status` IN ('unbound', 'bound', 'expired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_upstream_task_id_aliases` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `task_identity_id` bigint unsigned NOT NULL,
    `scope_kind` varchar(24) NOT NULL,
    `scope_key` varchar(255) NOT NULL,
    `hmac_key_version` int unsigned NOT NULL,
    `value_hmac` char(64) NOT NULL,
    `matchable` boolean NOT NULL DEFAULT true,
    `created_at` datetime(3) NOT NULL,
    `invalidated_at` datetime(3) NULL,
    `matchable_identity_id` bigint unsigned GENERATED ALWAYS AS (CASE WHEN `matchable` THEN `task_identity_id` ELSE NULL END) STORED,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_task_aliases_identity_version` (`task_identity_id`, `hmac_key_version`),
    UNIQUE KEY `uq_gw_task_aliases_match` (`scope_kind`, `scope_key`, `hmac_key_version`, `value_hmac`, `matchable_identity_id`),
    CONSTRAINT `fk_gw_task_aliases_identity`
        FOREIGN KEY (`task_identity_id`) REFERENCES `gw_upstream_task_identities` (`id`),
    CONSTRAINT `ck_gw_task_aliases_scope`
        CHECK (`scope_kind` IN ('global', 'channel', 'product_transport', 'credential_pool', 'credential'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_callback_binding_token_aliases` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `async_execution_id` bigint unsigned NOT NULL,
    `hmac_key_version` int unsigned NOT NULL,
    `value_hmac` char(64) NOT NULL,
    `encrypted_blob_id` bigint unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_callback_binding_alias_execution_version` (`async_execution_id`, `hmac_key_version`),
    UNIQUE KEY `uq_gw_callback_binding_alias_match` (`hmac_key_version`, `value_hmac`),
    CONSTRAINT `fk_gw_callback_binding_alias_execution`
        FOREIGN KEY (`async_execution_id`) REFERENCES `gw_async_executions` (`id`),
    CONSTRAINT `fk_gw_callback_binding_alias_blob`
        FOREIGN KEY (`encrypted_blob_id`) REFERENCES `encrypted_blobs` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_async_outbox` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `call_id` bigint unsigned NULL,
    `attempt_id` bigint unsigned NULL,
    `async_execution_id` bigint unsigned NULL,
    `action_seq` bigint unsigned NOT NULL,
    `action` varchar(32) NOT NULL,
    `status` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `available_at` datetime(3) NOT NULL,
    `lease_owner` varchar(128) NOT NULL DEFAULT '',
    `lease_expires_at` datetime(3) NULL,
    `attempt_count` bigint unsigned NOT NULL DEFAULT 0,
    `last_error_code` varchar(128) NOT NULL DEFAULT '',
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_async_outbox_call_seq` (`call_id`, `action_seq`),
    UNIQUE KEY `uq_gw_async_outbox_attempt_seq` (`attempt_id`, `action_seq`),
    UNIQUE KEY `uq_gw_async_outbox_async_seq` (`async_execution_id`, `action_seq`),
    CONSTRAINT `fk_gw_async_outbox_call`
        FOREIGN KEY (`call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `fk_gw_async_outbox_attempt`
        FOREIGN KEY (`attempt_id`) REFERENCES `gw_api_call_attempts` (`id`),
    CONSTRAINT `fk_gw_async_outbox_async_execution`
        FOREIGN KEY (`async_execution_id`) REFERENCES `gw_async_executions` (`id`),
    CONSTRAINT `ck_gw_async_outbox_one_root`
        CHECK ((`call_id` IS NOT NULL) + (`attempt_id` IS NOT NULL) + (`async_execution_id` IS NOT NULL) = 1),
    CONSTRAINT `ck_gw_async_outbox_action`
        CHECK (`action` IN ('submit', 'recover', 'query', 'cancel', 'reconcile_delivery', 'callback')),
    CONSTRAINT `ck_gw_async_outbox_status`
        CHECK (`status` IN ('pending', 'dispatching', 'succeeded', 'failed', 'dead_letter'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
