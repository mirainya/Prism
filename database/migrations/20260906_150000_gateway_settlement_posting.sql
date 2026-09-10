-- Record full measured receivables, including contractual overruns. A debtor
-- account is frozen; budget ceilings constrain reservations, not actual facts.
-- Posting rules are immutable, versioned and currency-specific. Existing events
-- remain unverified until an explicit historical reconciliation binds a rule.
--
-- MySQL DDL implicitly commits. Every schema fact is guarded independently so a
-- dirty migration can resume after any prior statement has committed.

SET @prism_schema = DATABASE();

-- Replace the account amount check.
SET @prism_ddl = (
    SELECT IF(COUNT(*) > 0,
        'ALTER TABLE `billing_accounts` DROP CHECK `ck_billing_accounts_amounts`',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'billing_accounts'
      AND constraint_name = 'ck_billing_accounts_amounts' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `billing_accounts` ADD CONSTRAINT `ck_billing_accounts_amounts` CHECK (`held_amount` >= 0 AND `credit_limit` >= 0 AND (`posted_balance` >= 0 OR `status` = ''frozen''))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'billing_accounts'
      AND constraint_name = 'ck_billing_accounts_amounts' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Replace the token budget amount check.
SET @prism_ddl = (
    SELECT IF(COUNT(*) > 0,
        'ALTER TABLE `token_budget_windows` DROP CHECK `ck_token_budget_windows_amounts`',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'token_budget_windows'
      AND constraint_name = 'ck_token_budget_windows_amounts' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `token_budget_windows` ADD CONSTRAINT `ck_token_budget_windows_amounts` CHECK (`used_amount` >= 0 AND `held_amount` >= 0 AND (`limit_amount` IS NULL OR `limit_amount` >= 0))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'token_budget_windows'
      AND constraint_name = 'ck_token_budget_windows_amounts' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- The table itself is created by the runtime-cost migration. Add each field
-- and constraint independently.
SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `billing_posting_rules` ADD COLUMN `amount_source` varchar(32) NOT NULL DEFAULT ''event_amount''',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'billing_posting_rules' AND column_name = 'amount_source'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `billing_posting_rules` ADD COLUMN `user_direction` varchar(8) NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'billing_posting_rules' AND column_name = 'user_direction'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `billing_posting_rules` ADD COLUMN `system_account_code` varchar(64) NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'billing_posting_rules' AND column_name = 'system_account_code'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `billing_posting_rules` ADD COLUMN `budget_effect` varchar(16) NOT NULL DEFAULT ''none''',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'billing_posting_rules' AND column_name = 'budget_effect'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `billing_posting_rules` ADD UNIQUE KEY `uq_billing_posting_rule_identity` (`id`, `event_type`, `currency_code`, `currency_version`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = @prism_schema AND table_name = 'billing_posting_rules'
      AND index_name = 'uq_billing_posting_rule_identity'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `billing_posting_rules` ADD CONSTRAINT `ck_billing_posting_rule_amount` CHECK (`amount_source` = ''event_amount'')',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'billing_posting_rules'
      AND constraint_name = 'ck_billing_posting_rule_amount' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `billing_posting_rules` ADD CONSTRAINT `ck_billing_posting_rule_direction` CHECK ((`user_direction` IS NULL AND `system_account_code` IS NULL) OR (`user_direction` IS NOT NULL AND `user_direction` IN (''debit'',''credit'') AND `system_account_code` IS NOT NULL))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'billing_posting_rules'
      AND constraint_name = 'ck_billing_posting_rule_direction' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `billing_posting_rules` ADD CONSTRAINT `ck_billing_posting_rule_budget` CHECK (`budget_effect` IN (''none'',''hold'',''release'',''consume''))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'billing_posting_rules'
      AND constraint_name = 'ck_billing_posting_rule_budget' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- MySQL does not allow CREATE TRIGGER through the prepared-statement
-- protocol. These single-statement triggers are safe to recreate on retry.
DROP TRIGGER IF EXISTS `trg_billing_posting_rule_no_update`;
CREATE TRIGGER `trg_billing_posting_rule_no_update`
    BEFORE UPDATE ON `billing_posting_rules`
    FOR EACH ROW SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Posting rules are immutable; create a new version';

DROP TRIGGER IF EXISTS `trg_billing_posting_rule_no_delete`;
CREATE TRIGGER `trg_billing_posting_rule_no_delete`
    BEFORE DELETE ON `billing_posting_rules`
    FOR EACH ROW SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Posting rules are immutable';

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `billing_events` ADD COLUMN `posting_rule_id` bigint unsigned NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'billing_events' AND column_name = 'posting_rule_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `billing_events` ADD CONSTRAINT `fk_billing_event_posting_rule` FOREIGN KEY (`posting_rule_id`, `event_type`, `currency_code`, `currency_version`) REFERENCES `billing_posting_rules` (`id`, `event_type`, `currency_code`, `currency_version`)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'billing_events'
      AND constraint_name = 'fk_billing_event_posting_rule' AND constraint_type = 'FOREIGN KEY'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

CREATE TABLE IF NOT EXISTS `billing_settlements` (
    `reservation_id` bigint unsigned NOT NULL,
    `billing_event_id` bigint unsigned NOT NULL,
    `authorized_amount` decimal(38,18) NOT NULL,
    `actual_amount` decimal(38,18) NOT NULL,
    `authorization_excess` decimal(38,18) NOT NULL,
    `debt_after` decimal(38,18) NOT NULL,
    `budget_excess` decimal(38,18) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`reservation_id`),
    UNIQUE KEY `uq_billing_settlement_event` (`billing_event_id`),
    CONSTRAINT `fk_billing_settlement_reservation` FOREIGN KEY (`reservation_id`) REFERENCES `billing_reservations` (`id`),
    CONSTRAINT `fk_billing_settlement_event` FOREIGN KEY (`billing_event_id`) REFERENCES `billing_events` (`id`),
    CONSTRAINT `ck_billing_settlement_amounts` CHECK (`authorized_amount` >= 0 AND `actual_amount` >= 0 AND
        `authorization_excess` = GREATEST(`actual_amount` - `authorized_amount`,0) AND `debt_after` >= 0 AND `budget_excess` >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

DROP TRIGGER IF EXISTS `trg_billing_settlement_no_update`;
CREATE TRIGGER `trg_billing_settlement_no_update`
    BEFORE UPDATE ON `billing_settlements`
    FOR EACH ROW SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Settlements are immutable';

DROP TRIGGER IF EXISTS `trg_billing_settlement_no_delete`;
CREATE TRIGGER `trg_billing_settlement_no_delete`
    BEFORE DELETE ON `billing_settlements`
    FOR EACH ROW SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Settlements are immutable';
