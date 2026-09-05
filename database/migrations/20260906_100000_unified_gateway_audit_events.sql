-- Append-only audit facts for configuration and security state changes.
-- These tables are separate from runtime transition events so administrative
-- actions cannot be confused with execution state changes.

CREATE TABLE IF NOT EXISTS `gw_credential_purpose_grant_state_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `grant_id` bigint unsigned NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_grant_state_events_version` (`grant_id`, `state_version`),
    CONSTRAINT `fk_gw_grant_state_events_grant`
        FOREIGN KEY (`grant_id`) REFERENCES `gw_credential_purpose_grants` (`id`),
    CONSTRAINT `ck_gw_grant_state_events_state`
        CHECK (`new_state` IN ('active', 'draining', 'revoked'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_credential_version_state_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `credential_version_id` bigint unsigned NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_credential_version_state_events_version` (`credential_version_id`, `state_version`),
    CONSTRAINT `fk_gw_credential_version_state_events_version`
        FOREIGN KEY (`credential_version_id`) REFERENCES `gw_credential_versions` (`id`),
    CONSTRAINT `ck_gw_credential_version_state_events_state`
        CHECK (`new_state` IN ('preparing', 'active', 'superseded', 'retired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_release_state_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT `fk_gw_catalog_release_state_events_release`
        FOREIGN KEY (`release_id`) REFERENCES `gw_catalog_releases` (`id`),
    CONSTRAINT `ck_gw_catalog_release_state_events_state`
        CHECK (`new_state` IN ('draft', 'published', 'retired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_routing_policy_version_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `routing_policy_version_id` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT `fk_gw_routing_policy_version_events_version`
        FOREIGN KEY (`routing_policy_version_id`) REFERENCES `gw_routing_policy_versions` (`id`),
    CONSTRAINT `ck_gw_routing_policy_version_events_state`
        CHECK (`new_state` IN ('draft', 'published', 'retired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `billing_account_state_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `billing_account_id` bigint unsigned NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_billing_account_state_events_version` (`billing_account_id`, `state_version`),
    CONSTRAINT `fk_billing_account_state_events_account`
        FOREIGN KEY (`billing_account_id`) REFERENCES `billing_accounts` (`id`),
    CONSTRAINT `ck_billing_account_state_events_state`
        CHECK (`new_state` IN ('open', 'frozen', 'closed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
