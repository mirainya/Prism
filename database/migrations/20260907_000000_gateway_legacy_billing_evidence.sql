-- Preserve the stopped legacy balance snapshot and verified billing history
-- before the legacy accounting columns and tables are retired. The imported
-- opening position is posted once; historical rows remain evidence and are
-- never replayed as additional money movements.

CREATE TABLE IF NOT EXISTS `gw_legacy_account_snapshots` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `migration_run_id` bigint unsigned NOT NULL,
    `subject_type` varchar(16) NOT NULL,
    `source_id` bigint unsigned NOT NULL,
    `user_id` bigint unsigned NOT NULL,
    `token_id` bigint unsigned NULL,
    `balance` decimal(38,18) NOT NULL,
    `total_used` decimal(38,18) NOT NULL DEFAULT 0,
    `billing_account_id` bigint unsigned NOT NULL,
    `budget_window_id` bigint unsigned NULL,
    `source_hmac` char(64) NOT NULL,
    `source_created_at` datetime(3) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_legacy_account_snapshot_source` (`subject_type`,`source_id`),
    UNIQUE KEY `uq_gw_legacy_account_snapshot_hmac` (`subject_type`,`source_id`,`source_hmac`),
    CONSTRAINT `fk_gw_legacy_account_snapshot_run`
        FOREIGN KEY (`migration_run_id`) REFERENCES `gw_migration_runs` (`id`),
    CONSTRAINT `fk_gw_legacy_account_snapshot_account`
        FOREIGN KEY (`billing_account_id`) REFERENCES `billing_accounts` (`id`),
    CONSTRAINT `fk_gw_legacy_account_snapshot_window`
        FOREIGN KEY (`budget_window_id`) REFERENCES `token_budget_windows` (`id`),
    CONSTRAINT `ck_gw_legacy_account_snapshot_subject`
        CHECK ((`subject_type`='user' AND `token_id` IS NULL AND `budget_window_id` IS NULL) OR
               (`subject_type`='token' AND `token_id`=`source_id` AND `budget_window_id` IS NOT NULL)),
    CONSTRAINT `ck_gw_legacy_account_snapshot_used` CHECK (`total_used` >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_legacy_billing_evidence` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `migration_run_id` bigint unsigned NOT NULL,
    `source_table` varchar(64) NOT NULL,
    `source_pk` varchar(128) NOT NULL,
    `record_kind` varchar(24) NOT NULL,
    `user_id` bigint unsigned NOT NULL,
    `token_id` bigint unsigned NOT NULL,
    `account_type` varchar(16) NULL,
    `account_id` bigint unsigned NULL,
    `legacy_type` varchar(24) NULL,
    `legacy_status` varchar(24) NULL,
    `direction` varchar(8) NULL,
    `category` varchar(64) NULL,
    `amount` decimal(38,18) NOT NULL,
    `balance_before` decimal(38,18) NULL,
    `balance_after` decimal(38,18) NULL,
    `target_call_id` bigint unsigned NULL,
    `target_attempt_id` bigint unsigned NULL,
    `evidence_hmacs` json NOT NULL,
    `source_hmac` char(64) NOT NULL,
    `source_created_at` datetime(3) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_legacy_billing_evidence_source` (`source_table`,`source_pk`),
    CONSTRAINT `fk_gw_legacy_billing_evidence_run`
        FOREIGN KEY (`migration_run_id`) REFERENCES `gw_migration_runs` (`id`),
    CONSTRAINT `fk_gw_legacy_billing_evidence_call`
        FOREIGN KEY (`target_call_id`) REFERENCES `gw_api_calls` (`id`),
    CONSTRAINT `fk_gw_legacy_billing_evidence_attempt`
        FOREIGN KEY (`target_attempt_id`) REFERENCES `gw_api_call_attempts` (`id`),
    CONSTRAINT `ck_gw_legacy_billing_evidence_kind`
        CHECK ((`record_kind`='billing_log' AND `source_table`='billing_logs') OR
               (`record_kind`='balance_entry' AND `source_table`='balance_entries')),
    CONSTRAINT `ck_gw_legacy_billing_evidence_amount` CHECK (`amount` > 0),
    CONSTRAINT `ck_gw_legacy_billing_evidence_direction`
        CHECK (`direction` IS NULL OR `direction` IN ('debit','credit'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- MySQL does not allow CREATE TRIGGER through the prepared-statement
-- protocol. These single-statement triggers are safe to recreate on retry.
DROP TRIGGER IF EXISTS `trg_gw_legacy_account_snapshots_no_update`;
CREATE TRIGGER `trg_gw_legacy_account_snapshots_no_update`
    BEFORE UPDATE ON `gw_legacy_account_snapshots`
    FOR EACH ROW SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Legacy account snapshots are immutable';

DROP TRIGGER IF EXISTS `trg_gw_legacy_account_snapshots_no_delete`;
CREATE TRIGGER `trg_gw_legacy_account_snapshots_no_delete`
    BEFORE DELETE ON `gw_legacy_account_snapshots`
    FOR EACH ROW SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Legacy account snapshots are immutable';

DROP TRIGGER IF EXISTS `trg_gw_legacy_billing_evidence_no_update`;
CREATE TRIGGER `trg_gw_legacy_billing_evidence_no_update`
    BEFORE UPDATE ON `gw_legacy_billing_evidence`
    FOR EACH ROW SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Legacy billing evidence is immutable';

DROP TRIGGER IF EXISTS `trg_gw_legacy_billing_evidence_no_delete`;
CREATE TRIGGER `trg_gw_legacy_billing_evidence_no_delete`
    BEFORE DELETE ON `gw_legacy_billing_evidence`
    FOR EACH ROW SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Legacy billing evidence is immutable';
