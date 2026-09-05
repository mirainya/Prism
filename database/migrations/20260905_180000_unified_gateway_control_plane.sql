-- Unified catalog discovery, runtime activation and deployment readiness.

CREATE TABLE IF NOT EXISTS `gw_catalog_sources` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `channel_id` bigint unsigned NOT NULL,
    `source_code` varchar(128) NOT NULL,
    `adapter_implementation_id` bigint unsigned NOT NULL,
    `credential_id` bigint unsigned NOT NULL,
    `credential_purpose_grant_id` bigint unsigned NOT NULL,
    `status` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `nonterminal_run_count` bigint unsigned NOT NULL DEFAULT 0,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_sources_channel_code` (`channel_id`, `source_code`),
    UNIQUE KEY `uq_gw_catalog_sources_channel_id_id` (`channel_id`, `id`),
    CONSTRAINT `fk_gw_catalog_sources_channel`
        FOREIGN KEY (`channel_id`) REFERENCES `gateway_channels` (`id`),
    CONSTRAINT `fk_gw_catalog_sources_adapter`
        FOREIGN KEY (`adapter_implementation_id`) REFERENCES `gw_adapter_implementations` (`id`),
    CONSTRAINT `fk_gw_catalog_sources_credential`
        FOREIGN KEY (`credential_id`) REFERENCES `gw_credentials` (`id`),
    CONSTRAINT `fk_gw_catalog_sources_grant`
        FOREIGN KEY (`credential_id`, `credential_purpose_grant_id`)
        REFERENCES `gw_credential_purpose_grants` (`credential_id`, `id`),
    CONSTRAINT `ck_gw_catalog_sources_status`
        CHECK (`status` IN ('active', 'draining', 'disabled')),
    CONSTRAINT `ck_gw_catalog_sources_runs`
        CHECK (`nonterminal_run_count` >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_release_sources` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `catalog_source_id` bigint unsigned NOT NULL,
    `source_state_version` bigint unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_release_sources_release_source` (`release_id`, `catalog_source_id`),
    UNIQUE KEY `uq_gw_catalog_release_sources_release_id_id` (`release_id`, `id`),
    CONSTRAINT `fk_gw_catalog_release_sources_release`
        FOREIGN KEY (`release_id`) REFERENCES `gw_catalog_releases` (`id`),
    CONSTRAINT `fk_gw_catalog_release_sources_source`
        FOREIGN KEY (`catalog_source_id`) REFERENCES `gw_catalog_sources` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_control_plane_runs` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `action` varchar(32) NOT NULL,
    `catalog_release_source_id` bigint unsigned NULL,
    `credential_id` bigint unsigned NULL,
    `offering_id` bigint unsigned NULL,
    `target_fingerprint` char(64) NULL,
    `target_config_version` bigint unsigned NULL,
    `auth_credential_version_id` bigint unsigned NOT NULL,
    `auth_purpose_grant_id` bigint unsigned NOT NULL,
    `state` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `next_action_at` datetime(3) NULL,
    `lease_owner` varchar(128) NOT NULL DEFAULT '',
    `lease_expires_at` datetime(3) NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_control_plane_runs_catalog_target` (`catalog_release_source_id`, `action`, `target_fingerprint`, `target_config_version`),
    CONSTRAINT `fk_gw_control_plane_runs_catalog_source`
        FOREIGN KEY (`catalog_release_source_id`) REFERENCES `gw_catalog_release_sources` (`id`),
    CONSTRAINT `fk_gw_control_plane_runs_credential`
        FOREIGN KEY (`credential_id`) REFERENCES `gw_credentials` (`id`),
    CONSTRAINT `fk_gw_control_plane_runs_offering`
        FOREIGN KEY (`offering_id`) REFERENCES `gw_offerings` (`id`),
    CONSTRAINT `fk_gw_control_plane_runs_auth_version`
        FOREIGN KEY (`auth_credential_version_id`) REFERENCES `gw_credential_versions` (`id`),
    CONSTRAINT `fk_gw_control_plane_runs_auth_grant`
        FOREIGN KEY (`credential_id`, `auth_purpose_grant_id`)
        REFERENCES `gw_credential_purpose_grants` (`credential_id`, `id`),
    CONSTRAINT `ck_gw_control_plane_runs_action`
        CHECK (`action` IN ('catalog_discovery', 'entitlement_probe', 'commercial_check')),
    CONSTRAINT `ck_gw_control_plane_runs_state`
        CHECK (`state` IN ('scheduled', 'running', 'completed', 'failed', 'manual_review')),
    CONSTRAINT `ck_gw_control_plane_runs_target`
        CHECK ((`action` = 'catalog_discovery' AND `catalog_release_source_id` IS NOT NULL AND `credential_id` IS NOT NULL AND `offering_id` IS NULL) OR (`action` <> 'catalog_discovery' AND `catalog_release_source_id` IS NULL AND (`credential_id` IS NOT NULL OR `offering_id` IS NOT NULL)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_imports` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `release_source_id` bigint unsigned NOT NULL,
    `control_plane_run_id` bigint unsigned NOT NULL,
    `snapshot_hmac` char(64) NOT NULL,
    `result_schema_version` int unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_imports_release_source` (`release_source_id`),
    UNIQUE KEY `uq_gw_catalog_imports_run` (`control_plane_run_id`),
    CONSTRAINT `fk_gw_catalog_imports_release_source`
        FOREIGN KEY (`release_id`, `release_source_id`) REFERENCES `gw_catalog_release_sources` (`release_id`, `id`),
    CONSTRAINT `fk_gw_catalog_imports_run`
        FOREIGN KEY (`control_plane_run_id`) REFERENCES `gw_control_plane_runs` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_runtime_state` (
    `id` tinyint unsigned NOT NULL,
    `active_release_id` bigint unsigned NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT `ck_gw_catalog_runtime_state_singleton` CHECK (`id` = 1),
    CONSTRAINT `fk_gw_catalog_runtime_state_release`
        FOREIGN KEY (`active_release_id`) REFERENCES `gw_catalog_releases` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_offering_state_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `offering_id` bigint unsigned NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_offering_state_events_version` (`release_id`, `offering_id`, `state_version`),
    CONSTRAINT `fk_gw_offering_state_events_offering`
        FOREIGN KEY (`release_id`, `offering_id`) REFERENCES `gw_offerings` (`release_id`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_deployment_generations` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `generation_no` bigint unsigned NOT NULL,
    `status` varchar(16) NOT NULL,
    `semantic_version` varchar(64) NOT NULL,
    `semantic_digest` char(64) NOT NULL,
    `member_frozen_at` datetime(3) NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_deployment_generations_no` (`generation_no`),
    CONSTRAINT `ck_gw_deployment_generations_status`
        CHECK (`status` IN ('preparing', 'active', 'retired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_deployment_members` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `deployment_generation_id` bigint unsigned NOT NULL,
    `instance_id` varchar(128) NOT NULL,
    `role` varchar(32) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_deployment_members_generation_instance_role` (`deployment_generation_id`, `instance_id`, `role`),
    UNIQUE KEY `uq_gw_deployment_members_generation_id` (`deployment_generation_id`, `id`),
    CONSTRAINT `fk_gw_deployment_members_generation`
        FOREIGN KEY (`deployment_generation_id`) REFERENCES `gw_deployment_generations` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_readiness` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `deployment_generation_id` bigint unsigned NOT NULL,
    `deployment_member_id` bigint unsigned NOT NULL,
    `release_id` bigint unsigned NOT NULL,
    `content_hash` char(64) NOT NULL,
    `semantic_digest` char(64) NOT NULL,
    `adapter_digest` char(64) NOT NULL,
    `status` varchar(16) NOT NULL,
    `heartbeat_at` datetime(3) NOT NULL,
    `expires_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_readiness_member_release` (`deployment_member_id`, `release_id`),
    CONSTRAINT `fk_gw_catalog_readiness_member`
        FOREIGN KEY (`deployment_generation_id`, `deployment_member_id`) REFERENCES `gw_deployment_members` (`deployment_generation_id`, `id`),
    CONSTRAINT `fk_gw_catalog_readiness_release`
        FOREIGN KEY (`release_id`) REFERENCES `gw_catalog_releases` (`id`),
    CONSTRAINT `ck_gw_catalog_readiness_status`
        CHECK (`status` IN ('ready', 'failed', 'expired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
