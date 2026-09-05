-- Unified catalog transport and routing relations.
-- All rows are release-scoped and immutable after the release is published.

CREATE TABLE IF NOT EXISTS `gw_skus` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `model_operation_id` bigint unsigned NOT NULL,
    `sku_code` varchar(128) NOT NULL,
    `delivery_mode` varchar(24) NOT NULL,
    `max_results` int unsigned NOT NULL DEFAULT 1,
    `idempotency_mode` varchar(24) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_skus_release_code` (`release_id`, `sku_code`),
    UNIQUE KEY `uq_gw_skus_release_id_id` (`release_id`, `id`),
    UNIQUE KEY `uq_gw_skus_release_id_model_operation` (`release_id`, `id`, `model_operation_id`),
    CONSTRAINT `fk_gw_skus_release`
        FOREIGN KEY (`release_id`) REFERENCES `gw_catalog_releases` (`id`),
    CONSTRAINT `fk_gw_skus_model_operation`
        FOREIGN KEY (`release_id`, `model_operation_id`)
        REFERENCES `gw_model_operations` (`release_id`, `id`),
    CONSTRAINT `ck_gw_skus_delivery_mode`
        CHECK (`delivery_mode` IN ('reference', 'managed_copy')),
    CONSTRAINT `ck_gw_skus_idempotency_mode`
        CHECK (`idempotency_mode` IN ('required', 'optional', 'forbidden')),
    CONSTRAINT `ck_gw_skus_max_results`
        CHECK (`max_results` > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_channel_transports` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `channel_id` bigint unsigned NOT NULL,
    `adapter_implementation_id` bigint unsigned NOT NULL,
    `transport_code` varchar(128) NOT NULL,
    `base_url` varchar(500) NOT NULL,
    `protocol` varchar(32) NOT NULL,
    `request_method` varchar(10) NOT NULL,
    `request_path` varchar(500) NOT NULL,
    `auth_scheme` varchar(32) NOT NULL,
    `execution_fingerprint` char(64) NOT NULL,
    `state_compatibility_fingerprint` char(64) NULL,
    `timeout_ms` bigint unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_channel_transports_release_code` (`release_id`, `transport_code`),
    UNIQUE KEY `uq_gw_channel_transports_release_id_id` (`release_id`, `id`),
    CONSTRAINT `fk_gw_channel_transports_release`
        FOREIGN KEY (`release_id`) REFERENCES `gw_catalog_releases` (`id`),
    CONSTRAINT `fk_gw_channel_transports_channel`
        FOREIGN KEY (`channel_id`) REFERENCES `gateway_channels` (`id`),
    CONSTRAINT `fk_gw_channel_transports_adapter`
        FOREIGN KEY (`adapter_implementation_id`) REFERENCES `gw_adapter_implementations` (`id`),
    CONSTRAINT `ck_gw_channel_transports_timeout`
        CHECK (`timeout_ms` > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_transport_allowed_hosts` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `channel_transport_id` bigint unsigned NOT NULL,
    `protocol` varchar(8) NOT NULL,
    `host_pattern` varchar(255) NOT NULL,
    `port` int unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_transport_hosts_release_transport_host` (`release_id`, `channel_transport_id`, `host_pattern`, `port`),
    CONSTRAINT `fk_gw_transport_hosts_transport`
        FOREIGN KEY (`release_id`, `channel_transport_id`)
        REFERENCES `gw_channel_transports` (`release_id`, `id`),
    CONSTRAINT `ck_gw_transport_hosts_protocol`
        CHECK (`protocol` IN ('http', 'https')),
    CONSTRAINT `ck_gw_transport_hosts_port`
        CHECK (`port` BETWEEN 1 AND 65535)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_products` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `channel_id` bigint unsigned NOT NULL,
    `product_code` varchar(128) NOT NULL,
    `vendor_model` varchar(255) NOT NULL,
    `capability_constraints` json NULL,
    `constraints_schema_version` int unsigned NOT NULL DEFAULT 1,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_products_release_code` (`release_id`, `product_code`),
    UNIQUE KEY `uq_gw_products_release_id_id` (`release_id`, `id`),
    CONSTRAINT `fk_gw_products_release`
        FOREIGN KEY (`release_id`) REFERENCES `gw_catalog_releases` (`id`),
    CONSTRAINT `fk_gw_products_channel`
        FOREIGN KEY (`channel_id`) REFERENCES `gateway_channels` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_product_transports` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `product_id` bigint unsigned NOT NULL,
    `channel_transport_id` bigint unsigned NOT NULL,
    `task_scope` varchar(24) NOT NULL,
    `cancel_mode` varchar(24) NOT NULL,
    `source_url_policy` varchar(24) NOT NULL,
    `upstream_scope_kind` varchar(24) NOT NULL,
    `upstream_scope_key` varchar(255) NOT NULL,
    `execution_fingerprint` char(64) NOT NULL,
    `state_compatibility_fingerprint` char(64) NULL,
    `timeout_ms` bigint unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_product_transports_release_product_transport` (`release_id`, `product_id`, `channel_transport_id`),
    UNIQUE KEY `uq_gw_product_transports_release_id_id` (`release_id`, `id`),
    CONSTRAINT `fk_gw_product_transports_product`
        FOREIGN KEY (`release_id`, `product_id`) REFERENCES `gw_products` (`release_id`, `id`),
    CONSTRAINT `fk_gw_product_transports_transport`
        FOREIGN KEY (`release_id`, `channel_transport_id`) REFERENCES `gw_channel_transports` (`release_id`, `id`),
    CONSTRAINT `ck_gw_product_transports_task_scope`
        CHECK (`task_scope` IN ('none', 'request', 'task')),
    CONSTRAINT `ck_gw_product_transports_cancel_mode`
        CHECK (`cancel_mode` IN ('none', 'upstream', 'local_only')),
    CONSTRAINT `ck_gw_product_transports_source_policy`
        CHECK (`source_url_policy` IN ('fixed', 'refreshable')),
    CONSTRAINT `ck_gw_product_transports_timeout`
        CHECK (`timeout_ms` > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_offerings` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `product_transport_id` bigint unsigned NOT NULL,
    `credential_pool_id` bigint unsigned NOT NULL,
    `commercial_fingerprint` char(64) NOT NULL,
    `cost_plan_code` varchar(128) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_offerings_release_transport_pool` (`release_id`, `product_transport_id`, `credential_pool_id`),
    UNIQUE KEY `uq_gw_offerings_release_id_id` (`release_id`, `id`),
    UNIQUE KEY `uq_gw_offerings_release_id_product_transport` (`release_id`, `id`, `product_transport_id`),
    CONSTRAINT `fk_gw_offerings_product_transport`
        FOREIGN KEY (`release_id`, `product_transport_id`) REFERENCES `gw_product_transports` (`release_id`, `id`),
    CONSTRAINT `fk_gw_offerings_pool`
        FOREIGN KEY (`credential_pool_id`) REFERENCES `gw_credential_pools` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_routes` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `sku_id` bigint unsigned NOT NULL,
    `offering_id` bigint unsigned NOT NULL,
    `priority` int unsigned NOT NULL,
    `weight` bigint unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_routes_release_sku_offering` (`release_id`, `sku_id`, `offering_id`),
    UNIQUE KEY `uq_gw_routes_release_id_id` (`release_id`, `id`),
    UNIQUE KEY `uq_gw_routes_release_id_sku_offering` (`release_id`, `id`, `sku_id`, `offering_id`),
    CONSTRAINT `fk_gw_routes_sku`
        FOREIGN KEY (`release_id`, `sku_id`) REFERENCES `gw_skus` (`release_id`, `id`),
    CONSTRAINT `fk_gw_routes_offering`
        FOREIGN KEY (`release_id`, `offering_id`) REFERENCES `gw_offerings` (`release_id`, `id`),
    CONSTRAINT `ck_gw_routes_weight`
        CHECK (`weight` > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_offering_runtime_state` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `offering_id` bigint unsigned NOT NULL,
    `state` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `reason_code` varchar(64) NOT NULL DEFAULT '',
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_offering_runtime_state_release_offering` (`release_id`, `offering_id`),
    CONSTRAINT `fk_gw_offering_runtime_state_offering`
        FOREIGN KEY (`release_id`, `offering_id`) REFERENCES `gw_offerings` (`release_id`, `id`),
    CONSTRAINT `ck_gw_offering_runtime_state_state`
        CHECK (`state` IN ('active', 'draining', 'disabled'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
