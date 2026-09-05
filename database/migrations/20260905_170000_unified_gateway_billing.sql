-- Unified user billing, token budgets and double-entry ledger.
-- Amounts are DECIMAL values bound to an immutable currency definition.

CREATE TABLE IF NOT EXISTS `billing_currency_definitions` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `currency_code` varchar(16) NOT NULL,
    `definition_version` int unsigned NOT NULL,
    `fraction_digits` tinyint unsigned NOT NULL,
    `rounding_mode` varchar(16) NOT NULL,
    `max_amount` decimal(38,18) NOT NULL,
    `status` varchar(16) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_billing_currency_code_version` (`currency_code`, `definition_version`),
    UNIQUE KEY `uq_billing_currency_id_code_version` (`id`, `currency_code`, `definition_version`),
    CONSTRAINT `ck_billing_currency_fraction_digits`
        CHECK (`fraction_digits` <= 18),
    CONSTRAINT `ck_billing_currency_rounding`
        CHECK (`rounding_mode` IN ('half_even', 'half_up', 'floor', 'ceiling')),
    CONSTRAINT `ck_billing_currency_status`
        CHECK (`status` IN ('active', 'retired')),
    CONSTRAINT `ck_billing_currency_max_amount`
        CHECK (`max_amount` > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `billing_system_state` (
    `id` tinyint unsigned NOT NULL,
    `currency_code` varchar(16) NOT NULL,
    `currency_version` int unsigned NOT NULL,
    `initialization_version` bigint unsigned NOT NULL DEFAULT 1,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_billing_system_state_currency` (`id`, `currency_code`, `currency_version`),
    CONSTRAINT `ck_billing_system_state_singleton` CHECK (`id` = 1),
    CONSTRAINT `fk_billing_system_state_currency`
        FOREIGN KEY (`currency_code`, `currency_version`)
        REFERENCES `billing_currency_definitions` (`currency_code`, `definition_version`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `token_budget_policies` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `token_id` bigint unsigned NOT NULL,
    `policy_code` varchar(128) NOT NULL,
    `window_kind` varchar(16) NOT NULL,
    `limit_amount` decimal(38,18) NULL,
    `period_seconds` bigint unsigned NULL,
    `timezone_name` varchar(64) NOT NULL DEFAULT 'UTC',
    `algorithm_version` int unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_token_budget_policies_token_code` (`token_id`, `policy_code`),
    UNIQUE KEY `uq_token_budget_policies_token_id_id` (`token_id`, `id`),
    CONSTRAINT `ck_token_budget_policies_kind`
        CHECK (`window_kind` IN ('periodic', 'lifetime', 'unlimited')),
    CONSTRAINT `ck_token_budget_policies_limit`
        CHECK ((`window_kind` = 'unlimited' AND `limit_amount` IS NULL AND `period_seconds` IS NULL) OR (`window_kind` <> 'unlimited' AND `limit_amount` IS NOT NULL AND `limit_amount` >= 0)),
    CONSTRAINT `ck_token_budget_policies_period`
        CHECK (`window_kind` <> 'periodic' OR (`period_seconds` IS NOT NULL AND `period_seconds` > 0))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `token_budget_policy_activations` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `token_id` bigint unsigned NOT NULL,
    `policy_id` bigint unsigned NOT NULL,
    `activation_seq` bigint unsigned NOT NULL,
    `predecessor_id` bigint unsigned NULL,
    `effective_at` datetime(3) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_token_budget_activations_token_seq` (`token_id`, `activation_seq`),
    UNIQUE KEY `uq_token_budget_activations_token_effective` (`token_id`, `effective_at`),
    UNIQUE KEY `uq_token_budget_activations_predecessor` (`token_id`, `predecessor_id`),
    UNIQUE KEY `uq_token_budget_activations_token_id_id` (`token_id`, `id`),
    CONSTRAINT `fk_token_budget_activations_policy`
        FOREIGN KEY (`token_id`, `policy_id`) REFERENCES `token_budget_policies` (`token_id`, `id`),
    CONSTRAINT `fk_token_budget_activations_predecessor`
        FOREIGN KEY (`token_id`, `predecessor_id`) REFERENCES `token_budget_policy_activations` (`token_id`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `token_budget_windows` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `token_id` bigint unsigned NOT NULL,
    `policy_id` bigint unsigned NOT NULL,
    `activation_id` bigint unsigned NOT NULL,
    `window_start` datetime(3) NOT NULL,
    `window_end` datetime(3) NULL,
    `limit_amount` decimal(38,18) NULL,
    `used_amount` decimal(38,18) NOT NULL DEFAULT 0,
    `held_amount` decimal(38,18) NOT NULL DEFAULT 0,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_token_budget_windows_policy_start` (`policy_id`, `window_start`),
    UNIQUE KEY `uq_token_budget_windows_token_id_id` (`token_id`, `id`),
    CONSTRAINT `fk_token_budget_windows_policy`
        FOREIGN KEY (`token_id`, `policy_id`) REFERENCES `token_budget_policies` (`token_id`, `id`),
    CONSTRAINT `fk_token_budget_windows_activation`
        FOREIGN KEY (`token_id`, `activation_id`) REFERENCES `token_budget_policy_activations` (`token_id`, `id`),
    CONSTRAINT `ck_token_budget_windows_amounts`
        CHECK (`used_amount` >= 0 AND `held_amount` >= 0 AND (`limit_amount` IS NULL OR `limit_amount` >= `used_amount` + `held_amount`))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `billing_accounts` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `user_id` bigint unsigned NOT NULL,
    `system_state_id` tinyint unsigned NOT NULL DEFAULT 1,
    `currency_code` varchar(16) NOT NULL,
    `currency_version` int unsigned NOT NULL,
    `posted_balance` decimal(38,18) NOT NULL DEFAULT 0,
    `held_amount` decimal(38,18) NOT NULL DEFAULT 0,
    `credit_limit` decimal(38,18) NOT NULL DEFAULT 0,
    `status` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_billing_accounts_user` (`user_id`),
    UNIQUE KEY `uq_billing_accounts_id_currency` (`id`, `currency_code`, `currency_version`),
    CONSTRAINT `fk_billing_accounts_system_currency`
        FOREIGN KEY (`system_state_id`, `currency_code`, `currency_version`)
        REFERENCES `billing_system_state` (`id`, `currency_code`, `currency_version`),
    CONSTRAINT `ck_billing_accounts_amounts`
        CHECK (`posted_balance` >= 0 AND `held_amount` >= 0 AND `credit_limit` >= 0),
    CONSTRAINT `ck_billing_accounts_status`
        CHECK (`status` IN ('open', 'frozen', 'closed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `billing_reservations` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `call_id` bigint unsigned NOT NULL,
    `billing_account_id` bigint unsigned NOT NULL,
    `budget_window_id` bigint unsigned NOT NULL,
    `amount` decimal(38,18) NOT NULL,
    `state` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL DEFAULT 1,
    `created_at` datetime(3) NOT NULL,
    `resolved_at` datetime(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_billing_reservations_call` (`call_id`),
    CONSTRAINT `fk_billing_reservations_call`
        FOREIGN KEY (`call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `fk_billing_reservations_account`
        FOREIGN KEY (`billing_account_id`) REFERENCES `billing_accounts` (`id`),
    CONSTRAINT `fk_billing_reservations_budget_window`
        FOREIGN KEY (`budget_window_id`) REFERENCES `token_budget_windows` (`id`),
    CONSTRAINT `ck_billing_reservations_amount`
        CHECK (`amount` >= 0),
    CONSTRAINT `ck_billing_reservations_state`
        CHECK (`state` IN ('active', 'settled', 'released', 'unknown_hold'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `billing_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `billing_account_id` bigint unsigned NOT NULL,
    `reservation_id` bigint unsigned NULL,
    `call_id` bigint unsigned NULL,
    `event_key` varchar(160) NOT NULL,
    `event_type` varchar(32) NOT NULL,
    `amount` decimal(38,18) NOT NULL DEFAULT 0,
    `currency_code` varchar(16) NOT NULL,
    `currency_version` int unsigned NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_billing_events_account_key` (`billing_account_id`, `event_key`),
    UNIQUE KEY `uq_billing_events_id_currency` (`id`, `currency_code`, `currency_version`),
    CONSTRAINT `fk_billing_events_account`
        FOREIGN KEY (`billing_account_id`, `currency_code`, `currency_version`)
        REFERENCES `billing_accounts` (`id`, `currency_code`, `currency_version`),
    CONSTRAINT `fk_billing_events_reservation`
        FOREIGN KEY (`reservation_id`) REFERENCES `billing_reservations` (`id`),
    CONSTRAINT `fk_billing_events_call`
        FOREIGN KEY (`call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `ck_billing_events_type`
        CHECK (`event_type` IN ('funding_credit', 'funding_debit', 'reservation_created', 'reservation_held_unknown', 'reservation_settled', 'reservation_released', 'refund', 'charge_adjustment', 'budget_limit_exceeded')),
    CONSTRAINT `ck_billing_events_amount`
        CHECK (`amount` >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `ledger_accounts` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `billing_account_id` bigint unsigned NULL,
    `system_account_code` varchar(64) NULL,
    `currency_code` varchar(16) NOT NULL,
    `currency_version` int unsigned NOT NULL,
    `balance` decimal(38,18) NOT NULL DEFAULT 0,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_ledger_accounts_user_account_currency` (`billing_account_id`, `currency_code`, `currency_version`),
    UNIQUE KEY `uq_ledger_accounts_system_currency` (`system_account_code`, `currency_code`, `currency_version`),
    UNIQUE KEY `uq_ledger_accounts_id_currency` (`id`, `currency_code`, `currency_version`),
    CONSTRAINT `fk_ledger_accounts_billing_account`
        FOREIGN KEY (`billing_account_id`, `currency_code`, `currency_version`) REFERENCES `billing_accounts` (`id`, `currency_code`, `currency_version`),
    CONSTRAINT `ck_ledger_accounts_owner`
        CHECK ((`billing_account_id` IS NOT NULL AND `system_account_code` IS NULL) OR (`billing_account_id` IS NULL AND `system_account_code` IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `ledger_transactions` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `billing_event_id` bigint unsigned NOT NULL,
    `currency_code` varchar(16) NOT NULL,
    `currency_version` int unsigned NOT NULL,
    `state` varchar(16) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    `posted_at` datetime(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_ledger_transactions_billing_event` (`billing_event_id`),
    UNIQUE KEY `uq_ledger_transactions_id_currency` (`id`, `currency_code`, `currency_version`),
    CONSTRAINT `fk_ledger_transactions_event`
        FOREIGN KEY (`billing_event_id`, `currency_code`, `currency_version`) REFERENCES `billing_events` (`id`, `currency_code`, `currency_version`),
    CONSTRAINT `ck_ledger_transactions_state`
        CHECK (`state` IN ('assembling', 'posted'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `ledger_entries` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `ledger_transaction_id` bigint unsigned NOT NULL,
    `ledger_account_id` bigint unsigned NOT NULL,
    `currency_code` varchar(16) NOT NULL,
    `currency_version` int unsigned NOT NULL,
    `entry_code` varchar(64) NOT NULL,
    `direction` varchar(8) NOT NULL,
    `amount` decimal(38,18) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_ledger_entries_transaction_code` (`ledger_transaction_id`, `entry_code`),
    CONSTRAINT `fk_ledger_entries_transaction`
        FOREIGN KEY (`ledger_transaction_id`, `currency_code`, `currency_version`) REFERENCES `ledger_transactions` (`id`, `currency_code`, `currency_version`),
    CONSTRAINT `fk_ledger_entries_account`
        FOREIGN KEY (`ledger_account_id`, `currency_code`, `currency_version`) REFERENCES `ledger_accounts` (`id`, `currency_code`, `currency_version`),
    CONSTRAINT `ck_ledger_entries_direction`
        CHECK (`direction` IN ('debit', 'credit')),
    CONSTRAINT `ck_ledger_entries_amount`
        CHECK (`amount` > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
