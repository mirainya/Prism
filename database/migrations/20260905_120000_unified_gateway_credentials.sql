-- Unified gateway credential pools, secret identities, versions and purpose grants.
-- Secret material is referenced through encrypted_blobs and is never stored here.

CREATE TABLE IF NOT EXISTS `gw_credential_pools` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `channel_id` bigint unsigned NOT NULL,
    `pool_code` varchar(128) NOT NULL,
    `display_name` varchar(128) NOT NULL,
    `status` varchar(16) NOT NULL,
    `config_version` bigint unsigned NOT NULL DEFAULT 1,
    `request_limit` bigint unsigned NULL,
    `task_limit` bigint unsigned NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_credential_pools_channel_code` (`channel_id`, `pool_code`),
    UNIQUE KEY `uq_gw_credential_pools_channel_id_id` (`channel_id`, `id`),
    CONSTRAINT `fk_gw_credential_pools_channel`
        FOREIGN KEY (`channel_id`) REFERENCES `gateway_channels` (`id`),
    CONSTRAINT `ck_gw_credential_pools_status`
        CHECK (`status` IN ('active', 'draining', 'disabled')),
    CONSTRAINT `ck_gw_credential_pools_limits`
        CHECK ((`request_limit` IS NULL OR `request_limit` > 0) AND (`task_limit` IS NULL OR `task_limit` > 0))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_credential_secret_identities` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `channel_id` bigint unsigned NOT NULL,
    `secret_hmac` char(64) NOT NULL,
    `hmac_key_version` int unsigned NOT NULL,
    `status` varchar(24) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_secret_identities_channel_hmac` (`channel_id`, `hmac_key_version`, `secret_hmac`),
    UNIQUE KEY `uq_gw_secret_identities_channel_id_id` (`channel_id`, `id`),
    CONSTRAINT `fk_gw_secret_identities_channel`
        FOREIGN KEY (`channel_id`) REFERENCES `gateway_channels` (`id`),
    CONSTRAINT `ck_gw_secret_identities_status`
        CHECK (`status` IN ('active', 'security_revoked'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_credentials` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `channel_id` bigint unsigned NOT NULL,
    `credential_pool_id` bigint unsigned NULL,
    `secret_identity_id` bigint unsigned NOT NULL,
    `current_version_id` bigint unsigned NULL,
    `credential_code` varchar(128) NOT NULL,
    `status` varchar(16) NOT NULL,
    `config_version` bigint unsigned NOT NULL DEFAULT 1,
    `request_limit` bigint unsigned NULL,
    `task_limit` bigint unsigned NULL,
    `weight` bigint unsigned NOT NULL DEFAULT 1,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_credentials_channel_code` (`channel_id`, `credential_code`),
    UNIQUE KEY `uq_gw_credentials_channel_id_id` (`channel_id`, `id`),
    UNIQUE KEY `uq_gw_credentials_id_secret_identity` (`id`, `secret_identity_id`),
    UNIQUE KEY `uq_gw_credentials_pool_id` (`credential_pool_id`, `id`),
    UNIQUE KEY `uq_gw_credentials_secret_identity` (`secret_identity_id`),
    CONSTRAINT `fk_gw_credentials_channel`
        FOREIGN KEY (`channel_id`) REFERENCES `gateway_channels` (`id`),
    CONSTRAINT `fk_gw_credentials_pool`
        FOREIGN KEY (`channel_id`, `credential_pool_id`)
        REFERENCES `gw_credential_pools` (`channel_id`, `id`),
    CONSTRAINT `fk_gw_credentials_secret_identity`
        FOREIGN KEY (`channel_id`, `secret_identity_id`)
        REFERENCES `gw_credential_secret_identities` (`channel_id`, `id`),
    CONSTRAINT `ck_gw_credentials_status`
        CHECK (`status` IN ('active', 'draining', 'disabled')),
    CONSTRAINT `ck_gw_credentials_limits`
        CHECK ((`request_limit` IS NULL OR `request_limit` > 0) AND (`task_limit` IS NULL OR `task_limit` > 0) AND `weight` > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_credential_versions` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `channel_id` bigint unsigned NOT NULL,
    `credential_id` bigint unsigned NOT NULL,
    `secret_identity_id` bigint unsigned NOT NULL,
    `version_no` bigint unsigned NOT NULL,
    `encrypted_blob_id` bigint unsigned NOT NULL,
    `status` varchar(16) NOT NULL,
    `valid_until` datetime(3) NULL,
    `created_at` datetime(3) NOT NULL,
    `retired_at` datetime(3) NULL,
    `active_credential_id` bigint unsigned GENERATED ALWAYS AS (CASE WHEN `status` = 'active' THEN `credential_id` ELSE NULL END) STORED,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_credential_versions_credential_version` (`credential_id`, `version_no`),
    UNIQUE KEY `uq_gw_credential_versions_credential_id_id` (`credential_id`, `id`),
    UNIQUE KEY `uq_gw_credential_versions_channel_id_id` (`channel_id`, `id`),
    UNIQUE KEY `uq_gw_credential_versions_one_active` (`active_credential_id`),
    UNIQUE KEY `uq_gw_credential_versions_blob` (`encrypted_blob_id`),
    CONSTRAINT `fk_gw_credential_versions_credential`
        FOREIGN KEY (`channel_id`, `credential_id`) REFERENCES `gw_credentials` (`channel_id`, `id`),
    CONSTRAINT `fk_gw_credential_versions_secret_identity`
        FOREIGN KEY (`credential_id`, `secret_identity_id`)
        REFERENCES `gw_credentials` (`id`, `secret_identity_id`),
    CONSTRAINT `fk_gw_credential_versions_blob`
        FOREIGN KEY (`encrypted_blob_id`) REFERENCES `encrypted_blobs` (`id`),
    CONSTRAINT `ck_gw_credential_versions_status`
        CHECK (`status` IN ('preparing', 'active', 'superseded', 'retired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


CREATE TABLE IF NOT EXISTS `gw_credential_purpose_grants` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `credential_id` bigint unsigned NOT NULL,
    `purpose` varchar(32) NOT NULL,
    `grant_seq` bigint unsigned NOT NULL,
    `status` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `created_at` datetime(3) NOT NULL,
    `revoked_at` datetime(3) NULL,
    `active_grant_id` bigint unsigned GENERATED ALWAYS AS (
        CASE WHEN `status` = 'active' THEN `credential_id` ELSE NULL END
    ) STORED,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_credential_grants_seq` (`credential_id`, `purpose`, `grant_seq`),
    UNIQUE KEY `uq_gw_credential_grants_credential_id` (`credential_id`, `id`),
    UNIQUE KEY `uq_gw_credential_grants_purpose_id` (`credential_id`, `id`, `purpose`),
    UNIQUE KEY `uq_gw_credential_grants_one_active` (`active_grant_id`, `purpose`),
    CONSTRAINT `fk_gw_credential_grants_credential`
        FOREIGN KEY (`credential_id`) REFERENCES `gw_credentials` (`id`),
    CONSTRAINT `ck_gw_credential_grants_purpose`
        CHECK (`purpose` IN ('execution', 'catalog_discovery', 'upstream_callback_verify')),
    CONSTRAINT `ck_gw_credential_grants_status`
        CHECK (`status` IN ('active', 'draining', 'revoked'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_credential_fingerprints` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `secret_identity_id` bigint unsigned NOT NULL,
    `hmac_key_version` int unsigned NOT NULL,
    `secret_hmac` char(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_credential_fingerprints_identity_version` (`secret_identity_id`, `hmac_key_version`),
    UNIQUE KEY `uq_gw_credential_fingerprints_hmac` (`hmac_key_version`, `secret_hmac`),
    CONSTRAINT `fk_gw_credential_fingerprints_identity`
        FOREIGN KEY (`secret_identity_id`) REFERENCES `gw_credential_secret_identities` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_credential_pool_state_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `credential_pool_id` bigint unsigned NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_pool_state_events_version` (`credential_pool_id`, `state_version`),
    CONSTRAINT `fk_gw_pool_state_events_pool`
        FOREIGN KEY (`credential_pool_id`) REFERENCES `gw_credential_pools` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_credential_state_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `credential_id` bigint unsigned NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_credential_state_events_version` (`credential_id`, `state_version`),
    CONSTRAINT `fk_gw_credential_state_events_credential`
        FOREIGN KEY (`credential_id`) REFERENCES `gw_credentials` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
