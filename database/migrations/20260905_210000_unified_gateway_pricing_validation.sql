-- Unified credential validation and immutable commercial evidence/rates.

CREATE TABLE IF NOT EXISTS `gw_credential_version_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `credential_version_id` bigint unsigned NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_credential_version_events_version` (`credential_version_id`, `state_version`),
    CONSTRAINT `fk_gw_credential_version_events_version`
        FOREIGN KEY (`credential_version_id`) REFERENCES `gw_credential_versions` (`id`),
    CONSTRAINT `ck_gw_credential_version_events_state`
        CHECK (`new_state` IN ('preparing', 'active', 'superseded', 'retired', 'security_revoked'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_credential_purpose_grant_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `purpose_grant_id` bigint unsigned NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_credential_grant_events_version` (`purpose_grant_id`, `state_version`),
    CONSTRAINT `fk_gw_credential_grant_events_grant`
        FOREIGN KEY (`purpose_grant_id`) REFERENCES `gw_credential_purpose_grants` (`id`),
    CONSTRAINT `ck_gw_credential_grant_events_state`
        CHECK (`new_state` IN ('active', 'draining', 'revoked'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_credential_secret_identity_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `secret_identity_id` bigint unsigned NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `new_state` varchar(24) NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_secret_identity_events_version` (`secret_identity_id`, `state_version`),
    CONSTRAINT `fk_gw_secret_identity_events_identity`
        FOREIGN KEY (`secret_identity_id`) REFERENCES `gw_credential_secret_identities` (`id`),
    CONSTRAINT `ck_gw_secret_identity_events_state`
        CHECK (`new_state` IN ('active', 'security_revoked'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_credential_validation_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `credential_id` bigint unsigned NOT NULL,
    `credential_version_id` bigint unsigned NOT NULL,
    `entitlement_fingerprint` char(64) NOT NULL,
    `validation_seq` bigint unsigned NOT NULL,
    `control_plane_run_id` bigint unsigned NOT NULL,
    `state` varchar(16) NOT NULL,
    `response_hmac` char(64) NOT NULL,
    `checked_at` datetime(3) NOT NULL,
    `valid_until` datetime(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_credential_validation_seq` (`credential_id`, `credential_version_id`, `entitlement_fingerprint`, `validation_seq`),
    CONSTRAINT `fk_gw_credential_validation_credential`
        FOREIGN KEY (`credential_id`) REFERENCES `gw_credentials` (`id`),
    CONSTRAINT `fk_gw_credential_validation_version`
        FOREIGN KEY (`credential_id`, `credential_version_id`) REFERENCES `gw_credential_versions` (`credential_id`, `id`),
    CONSTRAINT `fk_gw_credential_validation_run`
        FOREIGN KEY (`control_plane_run_id`) REFERENCES `gw_control_plane_runs` (`id`),
    CONSTRAINT `ck_gw_credential_validation_state`
        CHECK (`state` IN ('valid', 'drift', 'unknown', 'expired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_credential_entitlement_state` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `credential_id` bigint unsigned NOT NULL,
    `credential_version_id` bigint unsigned NOT NULL,
    `entitlement_fingerprint` char(64) NOT NULL,
    `state` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `latest_event_id` bigint unsigned NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_credential_entitlement_state_key` (`credential_id`, `credential_version_id`, `entitlement_fingerprint`),
    CONSTRAINT `fk_gw_credential_entitlement_state_event`
        FOREIGN KEY (`latest_event_id`) REFERENCES `gw_credential_validation_events` (`id`),
    CONSTRAINT `ck_gw_credential_entitlement_state_state`
        CHECK (`state` IN ('valid', 'drift', 'unknown', 'expired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_commercial_validation_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `commercial_fingerprint` char(64) NOT NULL,
    `validation_seq` bigint unsigned NOT NULL,
    `control_plane_run_id` bigint unsigned NOT NULL,
    `state` varchar(16) NOT NULL,
    `observation_hmac` char(64) NOT NULL,
    `checked_at` datetime(3) NOT NULL,
    `valid_until` datetime(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_commercial_validation_seq` (`commercial_fingerprint`, `validation_seq`),
    CONSTRAINT `fk_gw_commercial_validation_run`
        FOREIGN KEY (`control_plane_run_id`) REFERENCES `gw_control_plane_runs` (`id`),
    CONSTRAINT `ck_gw_commercial_validation_state`
        CHECK (`state` IN ('valid', 'drift', 'unknown', 'expired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_commercial_state` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `commercial_fingerprint` char(64) NOT NULL,
    `state` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `latest_event_id` bigint unsigned NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_commercial_state_fingerprint` (`commercial_fingerprint`),
    CONSTRAINT `fk_gw_commercial_state_event`
        FOREIGN KEY (`latest_event_id`) REFERENCES `gw_commercial_validation_events` (`id`),
    CONSTRAINT `ck_gw_commercial_state_state`
        CHECK (`state` IN ('valid', 'drift', 'unknown', 'expired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_rate_evidence` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `source_type` varchar(32) NOT NULL,
    `authority_level` varchar(16) NOT NULL,
    `source_reference` varchar(512) NOT NULL,
    `observed_at` datetime(3) NOT NULL,
    `fact_hmac` char(64) NOT NULL,
    `unit_code` varchar(32) NOT NULL,
    `unit_price` decimal(38,18) NOT NULL,
    `currency_code` varchar(16) NOT NULL,
    `currency_version` int unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_rate_evidence_fact` (`source_type`, `source_reference`, `fact_hmac`),
    CONSTRAINT `fk_gw_rate_evidence_currency`
        FOREIGN KEY (`currency_code`, `currency_version`) REFERENCES `billing_currency_definitions` (`currency_code`, `definition_version`),
    CONSTRAINT `ck_gw_rate_evidence_price`
        CHECK (`unit_price` >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_rate_evidence_review_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `rate_evidence_id` bigint unsigned NOT NULL,
    `review_seq` bigint unsigned NOT NULL,
    `decision` varchar(16) NOT NULL,
    `reviewer_user_id` bigint unsigned NOT NULL,
    `reason_code` varchar(128) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_rate_review_events_seq` (`rate_evidence_id`, `review_seq`),
    CONSTRAINT `fk_gw_rate_review_events_evidence`
        FOREIGN KEY (`rate_evidence_id`) REFERENCES `gw_rate_evidence` (`id`),
    CONSTRAINT `ck_gw_rate_review_events_decision`
        CHECK (`decision` IN ('submitted', 'accepted', 'rejected', 'superseded'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_rate_evidence_review_state` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `rate_evidence_id` bigint unsigned NOT NULL,
    `state` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `latest_review_event_id` bigint unsigned NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_rate_review_state_evidence` (`rate_evidence_id`),
    CONSTRAINT `fk_gw_rate_review_state_evidence`
        FOREIGN KEY (`rate_evidence_id`) REFERENCES `gw_rate_evidence` (`id`),
    CONSTRAINT `fk_gw_rate_review_state_event`
        FOREIGN KEY (`latest_review_event_id`) REFERENCES `gw_rate_evidence_review_events` (`id`),
    CONSTRAINT `ck_gw_rate_review_state_state`
        CHECK (`state` IN ('submitted', 'accepted', 'rejected', 'superseded'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_sell_rates` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `sku_id` bigint unsigned NOT NULL,
    `rate_evidence_review_event_id` bigint unsigned NOT NULL,
    `unit_code` varchar(32) NOT NULL,
    `unit_price` decimal(38,18) NOT NULL,
    `currency_code` varchar(16) NOT NULL,
    `currency_version` int unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_sell_rates_release_sku` (`release_id`, `sku_id`),
    CONSTRAINT `fk_gw_sell_rates_sku`
        FOREIGN KEY (`release_id`, `sku_id`) REFERENCES `gw_skus` (`release_id`, `id`),
    CONSTRAINT `fk_gw_sell_rates_review_event`
        FOREIGN KEY (`rate_evidence_review_event_id`) REFERENCES `gw_rate_evidence_review_events` (`id`),
    CONSTRAINT `fk_gw_sell_rates_currency`
        FOREIGN KEY (`currency_code`, `currency_version`) REFERENCES `billing_currency_definitions` (`currency_code`, `definition_version`),
    CONSTRAINT `ck_gw_sell_rates_price`
        CHECK (`unit_price` >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_cost_plans` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `offering_id` bigint unsigned NOT NULL,
    `plan_code` varchar(128) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_cost_plans_release_offering_code` (`release_id`, `offering_id`, `plan_code`),
    UNIQUE KEY `uq_gw_cost_plans_release_id_id` (`release_id`, `id`),
    CONSTRAINT `fk_gw_cost_plans_offering`
        FOREIGN KEY (`release_id`, `offering_id`) REFERENCES `gw_offerings` (`release_id`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_cost_rates` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `cost_plan_id` bigint unsigned NOT NULL,
    `rate_evidence_review_event_id` bigint unsigned NOT NULL,
    `unit_code` varchar(32) NOT NULL,
    `unit_price` decimal(38,18) NOT NULL,
    `currency_code` varchar(16) NOT NULL,
    `currency_version` int unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_cost_rates_plan_unit` (`cost_plan_id`, `unit_code`),
    CONSTRAINT `fk_gw_cost_rates_plan`
        FOREIGN KEY (`release_id`, `cost_plan_id`) REFERENCES `gw_cost_plans` (`release_id`, `id`),
    CONSTRAINT `fk_gw_cost_rates_review_event`
        FOREIGN KEY (`rate_evidence_review_event_id`) REFERENCES `gw_rate_evidence_review_events` (`id`),
    CONSTRAINT `fk_gw_cost_rates_currency`
        FOREIGN KEY (`currency_code`, `currency_version`) REFERENCES `billing_currency_definitions` (`currency_code`, `definition_version`),
    CONSTRAINT `ck_gw_cost_rates_price`
        CHECK (`unit_price` >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
