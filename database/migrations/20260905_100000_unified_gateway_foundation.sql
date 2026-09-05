-- Unified gateway foundation: stable identities and immutable catalog releases.
-- This migration is a target-schema change only. It is not an online compatibility
-- layer and must be applied before the one-time offline import described in the
-- architecture specification.

CREATE TABLE IF NOT EXISTS `gw_models` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `model_code` varchar(128) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_models_model_code` (`model_code`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_model_names` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `model_id` bigint unsigned NOT NULL,
    `api_name` varchar(128) NOT NULL,
    `is_primary` boolean NOT NULL DEFAULT false,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_model_names_api_name` (`api_name`),
    UNIQUE KEY `uq_gw_model_names_model_id_id` (`model_id`, `id`),
    CONSTRAINT `fk_gw_model_names_model`
        FOREIGN KEY (`model_id`) REFERENCES `gw_models` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_operation_contracts` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `operation_code` varchar(128) NOT NULL,
    `contract_version` int unsigned NOT NULL,
    `status` varchar(16) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_operation_contracts_code_version` (`operation_code`, `contract_version`),
    CONSTRAINT `ck_gw_operation_contracts_status`
        CHECK (`status` IN ('active', 'retired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_operation_routes` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `operation_contract_id` bigint unsigned NOT NULL,
    `http_method` varchar(10) NOT NULL,
    `route_template` varchar(255) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_operation_routes_method_template` (`http_method`, `route_template`),
    UNIQUE KEY `uq_gw_operation_routes_contract_id_id` (`operation_contract_id`, `id`),
    CONSTRAINT `fk_gw_operation_routes_contract`
        FOREIGN KEY (`operation_contract_id`) REFERENCES `gw_operation_contracts` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_adapter_implementations` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `adapter_code` varchar(128) NOT NULL,
    `contract_version` int unsigned NOT NULL,
    `implementation_digest` char(64) NOT NULL,
    `minimum_semantic_version` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_adapter_implementations_code_version` (`adapter_code`, `contract_version`),
    UNIQUE KEY `uq_gw_adapter_implementations_digest` (`implementation_digest`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gateway_channels` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `channel_code` varchar(128) NOT NULL,
    `display_name` varchar(128) NOT NULL,
    `status` varchar(16) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gateway_channels_code` (`channel_code`),
    CONSTRAINT `ck_gateway_channels_status`
        CHECK (`status` IN ('active', 'disabled'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_releases` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_no` bigint unsigned NOT NULL,
    `status` varchar(16) NOT NULL,
    `serialization_version` int unsigned NOT NULL,
    `content_hash_algorithm` varchar(16) NOT NULL,
    `content_hash` char(64) NOT NULL,
    `semantic_version` varchar(64) NOT NULL,
    `semantic_digest` char(64) NOT NULL,
    `reviewed_by` bigint unsigned NULL,
    `published_at` datetime(3) NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_releases_release_no` (`release_no`),
    UNIQUE KEY `uq_gw_catalog_releases_content_hash` (`content_hash`),
    CONSTRAINT `ck_gw_catalog_releases_status`
        CHECK (`status` IN ('draft', 'published', 'retired')),
    CONSTRAINT `ck_gw_catalog_releases_hash_algorithm`
        CHECK (`content_hash_algorithm` IN ('sha256'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_models` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `model_id` bigint unsigned NOT NULL,
    `display_name` varchar(128) NOT NULL,
    `description` varchar(1000) NOT NULL DEFAULT '',
    `capability_tags` json NULL,
    `sort_order` int NOT NULL DEFAULT 0,
    `visibility` varchar(16) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_models_release_model` (`release_id`, `model_id`),
    UNIQUE KEY `uq_gw_catalog_models_release_id_id` (`release_id`, `id`),
    UNIQUE KEY `uq_gw_catalog_models_release_id_model_id` (`release_id`, `id`, `model_id`),
    CONSTRAINT `fk_gw_catalog_models_release`
        FOREIGN KEY (`release_id`) REFERENCES `gw_catalog_releases` (`id`),
    CONSTRAINT `fk_gw_catalog_models_model`
        FOREIGN KEY (`model_id`) REFERENCES `gw_models` (`id`),
    CONSTRAINT `ck_gw_catalog_models_visibility`
        CHECK (`visibility` IN ('visible', 'deprecated', 'hidden'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_model_names` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `catalog_model_id` bigint unsigned NOT NULL,
    `model_id` bigint unsigned NOT NULL,
    `model_name_id` bigint unsigned NOT NULL,
    `is_primary` boolean NOT NULL DEFAULT false,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_model_names_release_name` (`release_id`, `model_name_id`),
    UNIQUE KEY `uq_gw_catalog_model_names_release_id_id` (`release_id`, `id`),
    CONSTRAINT `fk_gw_catalog_model_names_release`
        FOREIGN KEY (`release_id`) REFERENCES `gw_catalog_releases` (`id`),
    CONSTRAINT `fk_gw_catalog_model_names_catalog_model`
        FOREIGN KEY (`release_id`, `catalog_model_id`, `model_id`)
        REFERENCES `gw_catalog_models` (`release_id`, `id`, `model_id`),
    CONSTRAINT `fk_gw_catalog_model_names_model_name`
        FOREIGN KEY (`model_id`, `model_name_id`)
        REFERENCES `gw_model_names` (`model_id`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_model_operations` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `catalog_model_id` bigint unsigned NOT NULL,
    `operation_contract_id` bigint unsigned NOT NULL,
    `normalization_version` int unsigned NOT NULL,
    `semantic_digest` char(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_model_operations_release_model_contract` (`release_id`, `catalog_model_id`, `operation_contract_id`),
    UNIQUE KEY `uq_gw_model_operations_release_id_id` (`release_id`, `id`),
    CONSTRAINT `fk_gw_model_operations_release`
        FOREIGN KEY (`release_id`) REFERENCES `gw_catalog_releases` (`id`),
    CONSTRAINT `fk_gw_model_operations_catalog_model`
        FOREIGN KEY (`release_id`, `catalog_model_id`)
        REFERENCES `gw_catalog_models` (`release_id`, `id`),
    CONSTRAINT `fk_gw_model_operations_contract`
        FOREIGN KEY (`operation_contract_id`) REFERENCES `gw_operation_contracts` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
