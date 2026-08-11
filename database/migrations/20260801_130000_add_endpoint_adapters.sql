-- Reason: Endpoint protocol behavior must be versioned independently from gw_* routing.
-- Requirement: store Endpoint adapter bindings, immutable revisions, and discovery route state.
-- Impact: adds three Endpoint-only tables; existing channels, accounts, models, and endpoints remain unchanged.
-- Deployment: CREATE TABLE IF NOT EXISTS is metadata-only for an already upgraded database.

CREATE TABLE IF NOT EXISTS `endpoint_adapters` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `endpoint_id` bigint unsigned NOT NULL,
  `code` varchar(64) NOT NULL,
  `active_revision_id` bigint unsigned NOT NULL DEFAULT 0,
  `status` tinyint NOT NULL DEFAULT 1,
  `config` JSON,
  `created_at` datetime(3) NULL,
  `updated_at` datetime(3) NULL,
  `deleted_at` datetime(3) NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_endpoint_adapter` (`endpoint_id`,`code`),
  KEY `idx_endpoint_adapters_revision` (`active_revision_id`),
  KEY `idx_endpoint_adapters_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `endpoint_adapter_revisions` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `adapter_id` bigint unsigned NOT NULL,
  `version` bigint NOT NULL,
  `digest` char(64) NOT NULL,
  `config` JSON NOT NULL,
  `created_by` bigint unsigned NOT NULL DEFAULT 0,
  `created_at` datetime(3) NULL,
  `updated_at` datetime(3) NULL,
  `deleted_at` datetime(3) NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_endpoint_adapter_revision` (`adapter_id`,`version`),
  KEY `idx_endpoint_adapter_revisions_digest` (`digest`),
  KEY `idx_endpoint_adapter_revisions_created_by` (`created_by`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `endpoint_route_states` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `endpoint_id` bigint unsigned NOT NULL,
  `account_id` bigint unsigned NOT NULL,
  `route_operation` varchar(40) NOT NULL,
  `adapter_id` bigint unsigned NOT NULL,
  `revision_id` bigint unsigned NOT NULL,
  `status` tinyint NOT NULL DEFAULT 1,
  `disabled_until` datetime(3) NULL,
  `status_code` bigint NOT NULL DEFAULT 0,
  `fail_count` bigint NOT NULL DEFAULT 0,
  `last_discovery_at` datetime(3) NULL,
  `last_success_at` datetime(3) NULL,
  `last_error` varchar(1000) NOT NULL DEFAULT '',
  `discovered_models` JSON,
  `created_at` datetime(3) NULL,
  `updated_at` datetime(3) NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_endpoint_route_state` (`endpoint_id`,`account_id`,`route_operation`),
  KEY `idx_endpoint_route_states_account` (`account_id`),
  KEY `idx_endpoint_route_states_revision` (`revision_id`),
  KEY `idx_endpoint_route_states_disabled_until` (`disabled_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
