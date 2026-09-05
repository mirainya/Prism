-- Unified token routing policies and typed state transition audit facts.

CREATE TABLE IF NOT EXISTS `gw_routing_policies` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `token_id` bigint unsigned NOT NULL,
    `policy_code` varchar(128) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_routing_policies_token_code` (`token_id`, `policy_code`),
    UNIQUE KEY `uq_gw_routing_policies_token_id_id` (`token_id`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_routing_policy_versions` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `routing_policy_id` bigint unsigned NOT NULL,
    `version_no` bigint unsigned NOT NULL,
    `status` varchar(16) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    `published_at` datetime(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_routing_policy_versions_policy_version` (`routing_policy_id`, `version_no`),
    CONSTRAINT `fk_gw_routing_policy_versions_policy`
        FOREIGN KEY (`routing_policy_id`) REFERENCES `gw_routing_policies` (`id`),
    CONSTRAINT `ck_gw_routing_policy_versions_status`
        CHECK (`status` IN ('draft', 'published', 'retired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_routing_policy_entries` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `routing_policy_version_id` bigint unsigned NOT NULL,
    `operation_contract_id` bigint unsigned NULL,
    `model_id` bigint unsigned NULL,
    `channel_id` bigint unsigned NULL,
    `decision` varchar(16) NOT NULL,
    `priority` int unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_routing_policy_entries_version_order` (`routing_policy_version_id`, `priority`, `id`),
    CONSTRAINT `fk_gw_routing_policy_entries_version`
        FOREIGN KEY (`routing_policy_version_id`) REFERENCES `gw_routing_policy_versions` (`id`),
    CONSTRAINT `fk_gw_routing_policy_entries_contract`
        FOREIGN KEY (`operation_contract_id`) REFERENCES `gw_operation_contracts` (`id`),
    CONSTRAINT `fk_gw_routing_policy_entries_model`
        FOREIGN KEY (`model_id`) REFERENCES `gw_models` (`id`),
    CONSTRAINT `fk_gw_routing_policy_entries_channel`
        FOREIGN KEY (`channel_id`) REFERENCES `gateway_channels` (`id`),
    CONSTRAINT `ck_gw_routing_policy_entries_decision`
        CHECK (`decision` IN ('allow', 'deny'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_state_transition_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `call_id` bigint unsigned NULL,
    `attempt_id` bigint unsigned NULL,
    `async_execution_id` bigint unsigned NULL,
    `task_identity_id` bigint unsigned NULL,
    `provider_state_ref_id` bigint unsigned NULL,
    `callback_receipt_id` bigint unsigned NULL,
    `old_state` varchar(32) NULL,
    `new_state` varchar(32) NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_state_events_call_version` (`call_id`, `state_version`),
    UNIQUE KEY `uq_gw_state_events_attempt_version` (`attempt_id`, `state_version`),
    UNIQUE KEY `uq_gw_state_events_async_version` (`async_execution_id`, `state_version`),
    UNIQUE KEY `uq_gw_state_events_task_identity_version` (`task_identity_id`, `state_version`),
    UNIQUE KEY `uq_gw_state_events_provider_state_version` (`provider_state_ref_id`, `state_version`),
    UNIQUE KEY `uq_gw_state_events_receipt_version` (`callback_receipt_id`, `state_version`),
    CONSTRAINT `fk_gw_state_events_call`
        FOREIGN KEY (`call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `fk_gw_state_events_attempt`
        FOREIGN KEY (`attempt_id`) REFERENCES `gw_api_call_attempts` (`id`),
    CONSTRAINT `fk_gw_state_events_async`
        FOREIGN KEY (`async_execution_id`) REFERENCES `gw_async_executions` (`id`),
    CONSTRAINT `fk_gw_state_events_task_identity`
        FOREIGN KEY (`task_identity_id`) REFERENCES `gw_upstream_task_identities` (`id`),
    CONSTRAINT `fk_gw_state_events_provider_state`
        FOREIGN KEY (`provider_state_ref_id`) REFERENCES `gw_provider_state_refs` (`id`),
    CONSTRAINT `fk_gw_state_events_receipt`
        FOREIGN KEY (`callback_receipt_id`) REFERENCES `gw_upstream_callback_receipts` (`id`),
    CONSTRAINT `ck_gw_state_events_one_parent`
        CHECK ((`call_id` IS NOT NULL) + (`attempt_id` IS NOT NULL) + (`async_execution_id` IS NOT NULL) + (`task_identity_id` IS NOT NULL) + (`provider_state_ref_id` IS NOT NULL) + (`callback_receipt_id` IS NOT NULL) = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
