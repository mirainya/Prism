-- Add a typed ResultDelivery outbox parent and bind every recovery query to
-- the delivery it can refresh. Provider URLs remain encrypted source blobs.
-- Each schema fact is guarded independently because MySQL DDL commits
-- implicitly.

SET @prism_schema = DATABASE();

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_result_deliveries'
          AND column_name = 'action_seq'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_result_deliveries` ADD COLUMN `action_seq` bigint unsigned NOT NULL DEFAULT 0 AFTER `state_version`'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_result_deliveries'
          AND index_name = 'uq_gw_result_deliveries_id_attempt'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_result_deliveries` ADD UNIQUE KEY `uq_gw_result_deliveries_id_attempt` (`id`, `attempt_id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_result_deliveries'
          AND index_name = 'idx_gw_result_deliveries_recovery'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_result_deliveries` ADD KEY `idx_gw_result_deliveries_recovery` (`state`, `retry_at`, `id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_async_outbox'
          AND column_name = 'result_delivery_id'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_async_outbox` ADD COLUMN `result_delivery_id` bigint unsigned NULL AFTER `callback_receipt_id`'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_async_outbox'
          AND index_name = 'uq_gw_async_outbox_delivery_seq'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_async_outbox` ADD UNIQUE KEY `uq_gw_async_outbox_delivery_seq` (`result_delivery_id`, `action_seq`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_async_outbox'
          AND index_name = 'uq_gw_outbox_id_delivery'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_async_outbox` ADD UNIQUE KEY `uq_gw_outbox_id_delivery` (`id`, `result_delivery_id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_async_outbox'
          AND index_name = 'idx_gw_async_outbox_delivery_claim'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_async_outbox` ADD KEY `idx_gw_async_outbox_delivery_claim` (`action`, `status`, `available_at`, `lease_expires_at`, `id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_async_outbox'
          AND constraint_name = 'fk_gw_async_outbox_result_delivery'
          AND constraint_type = 'FOREIGN KEY'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_async_outbox` ADD CONSTRAINT `fk_gw_async_outbox_result_delivery` FOREIGN KEY (`result_delivery_id`) REFERENCES `gw_result_deliveries` (`id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_async_outbox'
          AND constraint_name = 'ck_gw_async_outbox_one_root'
          AND constraint_type = 'CHECK'
    ),
    'ALTER TABLE `gw_async_outbox` DROP CHECK `ck_gw_async_outbox_one_root`',
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_async_outbox'
          AND constraint_name = 'ck_gw_async_outbox_one_root'
          AND constraint_type = 'CHECK'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_async_outbox` ADD CONSTRAINT `ck_gw_async_outbox_one_root` CHECK ((`call_id` IS NOT NULL) + (`attempt_id` IS NOT NULL) + (`async_execution_id` IS NOT NULL) + (`callback_receipt_id` IS NOT NULL) + (`result_delivery_id` IS NOT NULL) = 1)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND column_name = 'result_delivery_id'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD COLUMN `result_delivery_id` bigint unsigned NULL AFTER `async_execution_id`'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND constraint_name = 'fk_gw_request_result_delivery_attempt'
          AND constraint_type = 'FOREIGN KEY'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD CONSTRAINT `fk_gw_request_result_delivery_attempt` FOREIGN KEY (`result_delivery_id`, `attempt_id`) REFERENCES `gw_result_deliveries` (`id`, `attempt_id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND constraint_name = 'fk_gw_request_outbox_delivery'
          AND constraint_type = 'FOREIGN KEY'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD CONSTRAINT `fk_gw_request_outbox_delivery` FOREIGN KEY (`outbox_id`, `result_delivery_id`) REFERENCES `gw_async_outbox` (`id`, `result_delivery_id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND constraint_name = 'ck_gw_request_async_dispatch'
          AND constraint_type = 'CHECK'
    ),
    'ALTER TABLE `gw_channel_request_logs` DROP CHECK `ck_gw_request_async_dispatch`',
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND constraint_name = 'ck_gw_request_async_dispatch'
          AND constraint_type = 'CHECK'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD CONSTRAINT `ck_gw_request_async_dispatch` CHECK ((`outbox_id` IS NULL AND `async_execution_id` IS NULL AND `result_delivery_id` IS NULL AND `outbox_attempt_count` IS NULL) OR (`outbox_id` IS NOT NULL AND `attempt_id` IS NOT NULL AND `outbox_attempt_count` IS NOT NULL AND `outbox_attempt_count` > 0 AND ((`async_execution_id` IS NOT NULL AND `result_delivery_id` IS NULL) OR (`async_execution_id` IS NULL AND `result_delivery_id` IS NOT NULL))))'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Extend the action allow-list for delivery reconciliation operations.
SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND constraint_name = 'ck_gw_request_logs_action'
          AND constraint_type = 'CHECK'
    ),
    'ALTER TABLE `gw_channel_request_logs` DROP CHECK `ck_gw_request_logs_action`',
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND constraint_name = 'ck_gw_request_logs_action'
          AND constraint_type = 'CHECK'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD CONSTRAINT `ck_gw_request_logs_action` CHECK (`action` IN (''submit'', ''recover'', ''query'', ''cancel'', ''reconcile_delivery'', ''named_action'', ''result_fetch'', ''catalog_discovery'', ''entitlement_probe'', ''commercial_check''))'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
