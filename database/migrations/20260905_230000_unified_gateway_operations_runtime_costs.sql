-- Remaining typed operation actions, runtime requirements, health and cost facts.

CREATE TABLE IF NOT EXISTS `gw_product_transport_actions` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `product_transport_id` bigint unsigned NOT NULL,
    `action_code` varchar(32) NOT NULL,
    `allowed_source_state` varchar(32) NOT NULL,
    `idempotency_mode` varchar(24) NOT NULL,
    `request_schema_version` int unsigned NOT NULL,
    `response_schema_version` int unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_product_transport_actions_code` (`release_id`, `product_transport_id`, `action_code`),
    CONSTRAINT `fk_gw_product_transport_actions_transport`
        FOREIGN KEY (`release_id`, `product_transport_id`) REFERENCES `gw_product_transports` (`release_id`, `id`),
    CONSTRAINT `ck_gw_product_transport_actions_code`
        CHECK (`action_code` IN ('submit', 'recover', 'query', 'cancel', 'named_action', 'result_fetch')),
    CONSTRAINT `ck_gw_product_transport_actions_idempotency`
        CHECK (`idempotency_mode` IN ('none', 'upstream', 'user_keyed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_catalog_source_state_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `catalog_source_id` bigint unsigned NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_catalog_source_state_events_version` (`catalog_source_id`, `state_version`),
    CONSTRAINT `fk_gw_catalog_source_state_events_source`
        FOREIGN KEY (`catalog_source_id`) REFERENCES `gw_catalog_sources` (`id`),
    CONSTRAINT `ck_gw_catalog_source_state_events_state`
        CHECK (`new_state` IN ('active', 'draining', 'disabled'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_execution_health` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `scope` varchar(16) NOT NULL,
    `channel_id` bigint unsigned NOT NULL,
    `execution_fingerprint` char(64) NOT NULL,
    `credential_version_id` bigint unsigned NULL,
    `credential_version_key` bigint unsigned GENERATED ALWAYS AS (CASE WHEN `scope` = 'credential' THEN `credential_version_id` ELSE 0 END) STORED,
    `state` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `retry_at` datetime(3) NULL,
    `lease_owner` varchar(128) NOT NULL DEFAULT '',
    `lease_expires_at` datetime(3) NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_execution_health_scope` (`scope`, `channel_id`, `execution_fingerprint`, `credential_version_key`),
    CONSTRAINT `fk_gw_execution_health_channel`
        FOREIGN KEY (`channel_id`) REFERENCES `gateway_channels` (`id`),
    CONSTRAINT `fk_gw_execution_health_credential_version`
        FOREIGN KEY (`channel_id`, `credential_version_id`) REFERENCES `gw_credential_versions` (`channel_id`, `id`),
    CONSTRAINT `ck_gw_execution_health_scope`
        CHECK ((`scope` = 'shared' AND `credential_version_id` IS NULL) OR (`scope` = 'credential' AND `credential_version_id` IS NOT NULL)),
    CONSTRAINT `ck_gw_execution_health_state`
        CHECK (`state` IN ('closed', 'open', 'half_open'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_execution_health_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `execution_health_id` bigint unsigned NOT NULL,
    `health_version` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `request_log_id` bigint unsigned NULL,
    `evidence_hmac` char(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_execution_health_events_version` (`execution_health_id`, `health_version`),
    CONSTRAINT `fk_gw_execution_health_events_health`
        FOREIGN KEY (`execution_health_id`) REFERENCES `gw_execution_health` (`id`),
    CONSTRAINT `fk_gw_execution_health_events_log`
        FOREIGN KEY (`request_log_id`) REFERENCES `gw_channel_request_logs` (`id`),
    CONSTRAINT `ck_gw_execution_health_events_state`
        CHECK (`new_state` IN ('closed', 'open', 'half_open'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `crypto_key_readiness` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `deployment_generation_id` bigint unsigned NOT NULL,
    `deployment_member_id` bigint unsigned NOT NULL,
    `keyring_id` bigint unsigned NOT NULL,
    `key_version` int unsigned NOT NULL,
    `operation` varchar(16) NOT NULL,
    `status` varchar(16) NOT NULL,
    `checked_at` datetime(3) NOT NULL,
    `expires_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_crypto_key_readiness_member_key_operation` (`deployment_member_id`, `keyring_id`, `key_version`, `operation`),
    CONSTRAINT `fk_crypto_key_readiness_member`
        FOREIGN KEY (`deployment_generation_id`, `deployment_member_id`) REFERENCES `gw_deployment_members` (`deployment_generation_id`, `id`),
    CONSTRAINT `fk_crypto_key_readiness_key_version`
        FOREIGN KEY (`keyring_id`, `key_version`) REFERENCES `crypto_key_versions` (`keyring_id`, `key_version`),
    CONSTRAINT `ck_crypto_key_readiness_operation`
        CHECK (`operation` IN ('mac', 'wrap', 'unwrap', 'encrypt', 'decrypt')),
    CONSTRAINT `ck_crypto_key_readiness_status`
        CHECK (`status` IN ('ready', 'failed', 'expired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_runtime_requirement_guards` (
    `shard_id` int unsigned NOT NULL,
    `generation` bigint unsigned NOT NULL DEFAULT 1,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`shard_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_runtime_requirements` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `shard_id` int unsigned NOT NULL,
    `kind` varchar(16) NOT NULL,
    `role` varchar(32) NOT NULL,
    `release_id` bigint unsigned NULL,
    `operation_contract_id` bigint unsigned NULL,
    `scope_kind` varchar(24) NULL,
    `scope_key` varchar(255) NULL,
    `compatibility_fingerprint` char(64) NULL,
    `adapter_implementation_id` bigint unsigned NULL,
    `release_key` bigint unsigned GENERATED ALWAYS AS (CASE WHEN `kind` = 'release' THEN `release_id` ELSE 0 END) STORED,
    `compatibility_contract_key` bigint unsigned GENERATED ALWAYS AS (CASE WHEN `kind` = 'compatibility' THEN `operation_contract_id` ELSE 0 END) STORED,
    `implementation_key` bigint unsigned GENERATED ALWAYS AS (CASE WHEN `kind` = 'implementation' THEN `adapter_implementation_id` ELSE 0 END) STORED,
    `active_ref_count` bigint unsigned NOT NULL DEFAULT 0,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_runtime_requirements_release` (`shard_id`, `role`, `kind`, `release_key`),
    UNIQUE KEY `uq_gw_runtime_requirements_compatibility` (`shard_id`, `role`, `kind`, `compatibility_contract_key`, `scope_kind`, `scope_key`, `compatibility_fingerprint`),
    UNIQUE KEY `uq_gw_runtime_requirements_implementation` (`shard_id`, `role`, `kind`, `implementation_key`),
    CONSTRAINT `fk_gw_runtime_requirements_guard`
        FOREIGN KEY (`shard_id`) REFERENCES `gw_runtime_requirement_guards` (`shard_id`),
    CONSTRAINT `fk_gw_runtime_requirements_release`
        FOREIGN KEY (`release_id`) REFERENCES `gw_catalog_releases` (`id`),
    CONSTRAINT `fk_gw_runtime_requirements_contract`
        FOREIGN KEY (`operation_contract_id`) REFERENCES `gw_operation_contracts` (`id`),
    CONSTRAINT `fk_gw_runtime_requirements_adapter`
        FOREIGN KEY (`adapter_implementation_id`) REFERENCES `gw_adapter_implementations` (`id`),
    CONSTRAINT `ck_gw_runtime_requirements_kind`
        CHECK ((`kind` = 'release' AND `release_id` IS NOT NULL AND `operation_contract_id` IS NULL AND `adapter_implementation_id` IS NULL) OR (`kind` = 'compatibility' AND `release_id` IS NULL AND `operation_contract_id` IS NOT NULL AND `adapter_implementation_id` IS NULL AND `scope_kind` IS NOT NULL AND `scope_key` IS NOT NULL AND `compatibility_fingerprint` IS NOT NULL) OR (`kind` = 'implementation' AND `release_id` IS NULL AND `operation_contract_id` IS NULL AND `adapter_implementation_id` IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_runtime_requirement_refs` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `requirement_id` bigint unsigned NOT NULL,
    `call_id` bigint unsigned NULL,
    `attempt_id` bigint unsigned NULL,
    `control_plane_run_id` bigint unsigned NULL,
    `provider_state_ref_id` bigint unsigned NULL,
    `credential_slot_id` bigint unsigned NULL,
    `state` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_runtime_requirement_refs_requirement_call` (`requirement_id`, `call_id`),
    UNIQUE KEY `uq_gw_runtime_requirement_refs_requirement_attempt` (`requirement_id`, `attempt_id`),
    UNIQUE KEY `uq_gw_runtime_requirement_refs_requirement_run` (`requirement_id`, `control_plane_run_id`),
    UNIQUE KEY `uq_gw_runtime_requirement_refs_requirement_provider_state` (`requirement_id`, `provider_state_ref_id`),
    UNIQUE KEY `uq_gw_runtime_requirement_refs_requirement_slot` (`requirement_id`, `credential_slot_id`),
    CONSTRAINT `fk_gw_runtime_requirement_refs_requirement`
        FOREIGN KEY (`requirement_id`) REFERENCES `gw_runtime_requirements` (`id`),
    CONSTRAINT `fk_gw_runtime_requirement_refs_call`
        FOREIGN KEY (`call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `fk_gw_runtime_requirement_refs_attempt`
        FOREIGN KEY (`attempt_id`) REFERENCES `gw_api_call_attempts` (`id`),
    CONSTRAINT `fk_gw_runtime_requirement_refs_run`
        FOREIGN KEY (`control_plane_run_id`) REFERENCES `gw_control_plane_runs` (`id`),
    CONSTRAINT `fk_gw_runtime_requirement_refs_provider_state`
        FOREIGN KEY (`provider_state_ref_id`) REFERENCES `gw_provider_state_refs` (`id`),
    CONSTRAINT `fk_gw_runtime_requirement_refs_slot`
        FOREIGN KEY (`credential_slot_id`) REFERENCES `gw_credential_slots` (`id`),
    CONSTRAINT `ck_gw_runtime_requirement_refs_parent`
        CHECK ((`call_id` IS NOT NULL) + (`attempt_id` IS NOT NULL) + (`control_plane_run_id` IS NOT NULL) + (`provider_state_ref_id` IS NOT NULL) + (`credential_slot_id` IS NOT NULL) = 1),
    CONSTRAINT `ck_gw_runtime_requirement_refs_state`
        CHECK (`state` IN ('active', 'released'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `billing_posting_rules` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `event_type` varchar(32) NOT NULL,
    `rule_version` int unsigned NOT NULL,
    `currency_code` varchar(16) NULL,
    `currency_version` int unsigned NULL,
    `status` varchar(16) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_billing_posting_rules_type_version` (`event_type`, `rule_version`),
    CONSTRAINT `fk_billing_posting_rules_currency`
        FOREIGN KEY (`currency_code`, `currency_version`) REFERENCES `billing_currency_definitions` (`currency_code`, `definition_version`),
    CONSTRAINT `ck_billing_posting_rules_status`
        CHECK (`status` IN ('active', 'retired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `token_budget_adjustment_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `budget_window_id` bigint unsigned NOT NULL,
    `adjustment_seq` bigint unsigned NOT NULL,
    `amount_delta` decimal(38,18) NOT NULL,
    `source_type` varchar(32) NOT NULL,
    `source_key` varchar(160) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_token_budget_adjustments_window_seq` (`budget_window_id`, `adjustment_seq`),
    UNIQUE KEY `uq_token_budget_adjustments_source` (`source_type`, `source_key`),
    CONSTRAINT `fk_token_budget_adjustments_window`
        FOREIGN KEY (`budget_window_id`) REFERENCES `token_budget_windows` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `fx_rate_snapshots` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `base_currency` varchar(16) NOT NULL,
    `quote_currency` varchar(16) NOT NULL,
    `effective_at` datetime(3) NOT NULL,
    `rate` decimal(38,18) NOT NULL,
    `evidence_hmac` char(64) NOT NULL,
    `status` varchar(16) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_fx_rate_snapshots_pair_time_evidence` (`base_currency`, `quote_currency`, `effective_at`, `evidence_hmac`),
    CONSTRAINT `ck_fx_rate_snapshots_distinct_currency`
        CHECK (`base_currency` <> `quote_currency`),
    CONSTRAINT `ck_fx_rate_snapshots_rate`
        CHECK (`rate` > 0),
    CONSTRAINT `ck_fx_rate_snapshots_status`
        CHECK (`status` IN ('pending', 'accepted', 'rejected'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_upstream_cost_evidence` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `channel_id` bigint unsigned NOT NULL,
    `source_type` varchar(24) NOT NULL,
    `external_key` varchar(255) NOT NULL,
    `fact_hmac` char(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_upstream_cost_evidence_source` (`channel_id`, `source_type`, `external_key`),
    CONSTRAINT `fk_gw_upstream_cost_evidence_channel`
        FOREIGN KEY (`channel_id`) REFERENCES `gateway_channels` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_upstream_cost_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `attempt_id` bigint unsigned NOT NULL,
    `component_code` varchar(64) NOT NULL,
    `event_seq` bigint unsigned NOT NULL,
    `direction` varchar(8) NOT NULL,
    `amount` decimal(38,18) NOT NULL,
    `quantity` decimal(38,18) NOT NULL,
    `currency_code` varchar(16) NOT NULL,
    `currency_version` int unsigned NOT NULL,
    `source_type` varchar(16) NOT NULL,
    `reconciliation_state` varchar(16) NOT NULL,
    `request_log_id` bigint unsigned NULL,
    `result_delivery_id` bigint unsigned NULL,
    `evidence_id` bigint unsigned NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_upstream_cost_events_attempt_component_seq` (`attempt_id`, `component_code`, `event_seq`),
    CONSTRAINT `fk_gw_upstream_cost_events_attempt`
        FOREIGN KEY (`attempt_id`) REFERENCES `gw_api_call_attempts` (`id`),
    CONSTRAINT `fk_gw_upstream_cost_events_currency`
        FOREIGN KEY (`currency_code`, `currency_version`) REFERENCES `billing_currency_definitions` (`currency_code`, `definition_version`),
    CONSTRAINT `fk_gw_upstream_cost_events_log`
        FOREIGN KEY (`request_log_id`) REFERENCES `gw_channel_request_logs` (`id`),
    CONSTRAINT `fk_gw_upstream_cost_events_delivery`
        FOREIGN KEY (`result_delivery_id`) REFERENCES `gw_result_deliveries` (`id`),
    CONSTRAINT `fk_gw_upstream_cost_events_evidence`
        FOREIGN KEY (`evidence_id`) REFERENCES `gw_upstream_cost_evidence` (`id`),
    CONSTRAINT `ck_gw_upstream_cost_events_direction`
        CHECK (`direction` IN ('increase', 'decrease')),
    CONSTRAINT `ck_gw_upstream_cost_events_amount`
        CHECK (`amount` >= 0 AND `quantity` >= 0),
    CONSTRAINT `ck_gw_upstream_cost_events_source`
        CHECK (`source_type` IN ('estimated', 'reported', 'manual')),
    CONSTRAINT `ck_gw_upstream_cost_events_reconciliation`
        CHECK (`reconciliation_state` IN ('pending', 'confirmed', 'disputed', 'voided'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
