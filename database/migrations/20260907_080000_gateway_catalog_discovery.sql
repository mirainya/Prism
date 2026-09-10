-- Add fixed catalog-source profiles and immutable discovery evidence.
-- Remote catalog responses are reduced to allowlisted fields. They can only
-- feed a draft after review and never mutate an active catalog release.

CREATE TABLE IF NOT EXISTS `gw_catalog_source_profiles` (
    `catalog_source_id` bigint unsigned NOT NULL,
    `contract_code` varchar(64) NOT NULL,
    `base_url` varchar(2048) NOT NULL,
    `external_group` varchar(128) NOT NULL,
    `request_timeout_ms` int unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`catalog_source_id`),
    CONSTRAINT `fk_gw_catalog_source_profiles_source`
        FOREIGN KEY (`catalog_source_id`) REFERENCES `gw_catalog_sources` (`id`),
    CONSTRAINT `ck_gw_catalog_source_profiles_contract`
        CHECK (`contract_code` IN ('aicost_models_v1', 'aicost_pricing_v1')),
    CONSTRAINT `ck_gw_catalog_source_profiles_timeout`
        CHECK (`request_timeout_ms` BETWEEN 1000 AND 120000)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_discovery_snapshots` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `release_source_id` bigint unsigned NOT NULL,
    `control_plane_run_id` bigint unsigned NOT NULL,
    `contract_code` varchar(64) NOT NULL,
    `observed_at` datetime(3) NOT NULL,
    `response_hmac` char(64) NOT NULL,
    `result_schema_version` int unsigned NOT NULL,
    `item_count` int unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_discovery_snapshots_source` (`release_source_id`),
    UNIQUE KEY `uq_gw_catalog_discovery_snapshots_run` (`control_plane_run_id`),
    UNIQUE KEY `uq_gw_catalog_discovery_snapshots_release_id_id` (`release_id`, `id`),
    CONSTRAINT `fk_gw_catalog_discovery_snapshots_source`
        FOREIGN KEY (`release_id`, `release_source_id`)
        REFERENCES `gw_catalog_release_sources` (`release_id`, `id`),
    CONSTRAINT `fk_gw_catalog_discovery_snapshots_run`
        FOREIGN KEY (`control_plane_run_id`) REFERENCES `gw_control_plane_runs` (`id`),
    CONSTRAINT `ck_gw_catalog_discovery_snapshots_contract`
        CHECK (`contract_code` IN ('aicost_models_v1', 'aicost_pricing_v1')),
    CONSTRAINT `ck_gw_catalog_discovery_snapshots_count`
        CHECK (`item_count` <= 10000)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_discovery_items` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `snapshot_id` bigint unsigned NOT NULL,
    `ordinal` int unsigned NOT NULL,
    `model_code` varchar(128) NOT NULL,
    `description` varchar(1000) NOT NULL DEFAULT '',
    `tags` varchar(512) NOT NULL DEFAULT '',
    `vendor_id` bigint unsigned NULL,
    `provider_quota_type` tinyint unsigned NULL,
    `model_price` decimal(36,18) NULL,
    `model_ratio` decimal(36,18) NULL,
    `completion_ratio` decimal(36,18) NULL,
    `owner_by` varchar(128) NOT NULL DEFAULT '',
    `pricing_version` varchar(128) NOT NULL DEFAULT '',
    `selected_group_enabled` boolean NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_discovery_items_model` (`snapshot_id`, `model_code`),
    UNIQUE KEY `uq_gw_catalog_discovery_items_ordinal` (`snapshot_id`, `ordinal`),
    KEY `idx_gw_catalog_discovery_items_model` (`model_code`, `snapshot_id`),
    CONSTRAINT `fk_gw_catalog_discovery_items_snapshot`
        FOREIGN KEY (`snapshot_id`) REFERENCES `gw_catalog_discovery_snapshots` (`id`),
    CONSTRAINT `ck_gw_catalog_discovery_items_quota`
        CHECK (`provider_quota_type` IS NULL OR `provider_quota_type` IN (0, 1)),
    CONSTRAINT `ck_gw_catalog_discovery_items_prices`
        CHECK ((`model_price` IS NULL OR `model_price` >= 0) AND
               (`model_ratio` IS NULL OR `model_ratio` >= 0) AND
               (`completion_ratio` IS NULL OR `completion_ratio` >= 0))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_discovery_item_groups` (
    `snapshot_item_id` bigint unsigned NOT NULL,
    `group_code` varchar(128) NOT NULL,
    PRIMARY KEY (`snapshot_item_id`, `group_code`),
    CONSTRAINT `fk_gw_catalog_discovery_item_groups_item`
        FOREIGN KEY (`snapshot_item_id`) REFERENCES `gw_catalog_discovery_items` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_discovery_item_endpoints` (
    `snapshot_item_id` bigint unsigned NOT NULL,
    `endpoint_type` varchar(64) NOT NULL,
    PRIMARY KEY (`snapshot_item_id`, `endpoint_type`),
    CONSTRAINT `fk_gw_catalog_discovery_item_endpoints_item`
        FOREIGN KEY (`snapshot_item_id`) REFERENCES `gw_catalog_discovery_items` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_discovery_review_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `snapshot_id` bigint unsigned NOT NULL,
    `review_seq` bigint unsigned NOT NULL,
    `decision` varchar(16) NOT NULL,
    `reviewer_user_id` bigint unsigned NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_discovery_review_events_seq` (`snapshot_id`, `review_seq`),
    UNIQUE KEY `uq_gw_catalog_discovery_review_events_snapshot_id_id` (`snapshot_id`, `id`),
    CONSTRAINT `fk_gw_catalog_discovery_review_events_snapshot`
        FOREIGN KEY (`snapshot_id`) REFERENCES `gw_catalog_discovery_snapshots` (`id`),
    CONSTRAINT `fk_gw_catalog_discovery_review_events_reviewer`
        FOREIGN KEY (`reviewer_user_id`) REFERENCES `users` (`id`),
    CONSTRAINT `ck_gw_catalog_discovery_review_events_decision`
        CHECK (`decision` IN ('submitted', 'accepted', 'rejected'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_discovery_review_state` (
    `snapshot_id` bigint unsigned NOT NULL,
    `state` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `latest_review_event_id` bigint unsigned NOT NULL,
    PRIMARY KEY (`snapshot_id`),
    CONSTRAINT `fk_gw_catalog_discovery_review_state_snapshot`
        FOREIGN KEY (`snapshot_id`) REFERENCES `gw_catalog_discovery_snapshots` (`id`),
    CONSTRAINT `fk_gw_catalog_discovery_review_state_event`
        FOREIGN KEY (`snapshot_id`, `latest_review_event_id`)
        REFERENCES `gw_catalog_discovery_review_events` (`snapshot_id`, `id`),
    CONSTRAINT `ck_gw_catalog_discovery_review_state_value`
        CHECK (`state` IN ('submitted', 'accepted', 'rejected'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_import_snapshots` (
    `catalog_import_id` bigint unsigned NOT NULL,
    `snapshot_id` bigint unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`catalog_import_id`),
    UNIQUE KEY `uq_gw_catalog_import_snapshots_snapshot` (`snapshot_id`),
    CONSTRAINT `fk_gw_catalog_import_snapshots_import`
        FOREIGN KEY (`catalog_import_id`) REFERENCES `gw_catalog_imports` (`id`),
    CONSTRAINT `fk_gw_catalog_import_snapshots_snapshot`
        FOREIGN KEY (`snapshot_id`) REFERENCES `gw_catalog_discovery_snapshots` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_price_candidates` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `snapshot_item_id` bigint unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_price_candidates_item` (`snapshot_item_id`),
    CONSTRAINT `fk_gw_catalog_price_candidates_item`
        FOREIGN KEY (`snapshot_item_id`) REFERENCES `gw_catalog_discovery_items` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_price_candidate_reviews` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `candidate_id` bigint unsigned NOT NULL,
    `decision` varchar(16) NOT NULL,
    `unit_code` varchar(64) NULL,
    `currency_code` varchar(16) NULL,
    `currency_version` int unsigned NULL,
    `rate_evidence_id` bigint unsigned NULL,
    `reviewer_user_id` bigint unsigned NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_price_candidate_reviews_candidate` (`candidate_id`),
    CONSTRAINT `fk_gw_catalog_price_candidate_reviews_candidate`
        FOREIGN KEY (`candidate_id`) REFERENCES `gw_catalog_price_candidates` (`id`),
    CONSTRAINT `fk_gw_catalog_price_candidate_reviews_evidence`
        FOREIGN KEY (`rate_evidence_id`) REFERENCES `gw_rate_evidence` (`id`),
    CONSTRAINT `fk_gw_catalog_price_candidate_reviews_reviewer`
        FOREIGN KEY (`reviewer_user_id`) REFERENCES `users` (`id`),
    CONSTRAINT `ck_gw_catalog_price_candidate_reviews_decision`
        CHECK ((`decision` = 'confirmed' AND `unit_code` IS NOT NULL AND
                `currency_code` IS NOT NULL AND `currency_version` IS NOT NULL AND
                `rate_evidence_id` IS NOT NULL) OR
               (`decision` = 'dismissed' AND `unit_code` IS NULL AND
                `currency_code` IS NULL AND `currency_version` IS NULL AND
                `rate_evidence_id` IS NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
