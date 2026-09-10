-- Bind asynchronous HTTP exchanges to their execution and leased action.
-- A submitted attempt and a live upstream identity each have one owner.
-- MySQL DDL implicitly commits, so every schema fact is guarded independently.

SET @prism_schema = DATABASE();

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_async_executions'
          AND index_name = 'uq_gw_async_id_attempt'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_async_executions` ADD UNIQUE KEY `uq_gw_async_id_attempt` (`id`, `attempt_id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_async_outbox'
          AND index_name = 'uq_gw_outbox_id_async'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_async_outbox` ADD UNIQUE KEY `uq_gw_outbox_id_async` (`id`, `async_execution_id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND column_name = 'async_execution_id'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD COLUMN `async_execution_id` bigint unsigned NULL'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND column_name = 'outbox_id'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD COLUMN `outbox_id` bigint unsigned NULL'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND column_name = 'outbox_attempt_count'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD COLUMN `outbox_attempt_count` bigint unsigned NULL'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND column_name = 'submitted_async_attempt_id'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD COLUMN `submitted_async_attempt_id` bigint unsigned GENERATED ALWAYS AS (CASE WHEN `outbox_id` IS NOT NULL AND `action`=''submit'' AND `status`<>''not_sent'' THEN `attempt_id` ELSE NULL END) STORED'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND index_name = 'uq_gw_request_outbox_lease'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD UNIQUE KEY `uq_gw_request_outbox_lease` (`outbox_id`, `outbox_attempt_count`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND index_name = 'uq_gw_request_async_submit'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD UNIQUE KEY `uq_gw_request_async_submit` (`submitted_async_attempt_id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND constraint_name = 'fk_gw_request_async_attempt' AND constraint_type = 'FOREIGN KEY'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD CONSTRAINT `fk_gw_request_async_attempt` FOREIGN KEY (`async_execution_id`, `attempt_id`) REFERENCES `gw_async_executions` (`id`, `attempt_id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND constraint_name = 'fk_gw_request_outbox_async' AND constraint_type = 'FOREIGN KEY'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD CONSTRAINT `fk_gw_request_outbox_async` FOREIGN KEY (`outbox_id`, `async_execution_id`) REFERENCES `gw_async_outbox` (`id`, `async_execution_id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_channel_request_logs'
          AND constraint_name = 'ck_gw_request_async_dispatch' AND constraint_type = 'CHECK'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_channel_request_logs` ADD CONSTRAINT `ck_gw_request_async_dispatch` CHECK ((`outbox_id` IS NULL AND `async_execution_id` IS NULL AND `outbox_attempt_count` IS NULL) OR (`outbox_id` IS NOT NULL AND `async_execution_id` IS NOT NULL AND `attempt_id` IS NOT NULL AND `outbox_attempt_count` IS NOT NULL AND `outbox_attempt_count` > 0))'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- The former index included identity_id and allowed the same provider ID to
-- bind to multiple executions. Inactive aliases remain available for audit.
SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_upstream_task_id_aliases'
          AND column_name = 'active_value_hmac'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_upstream_task_id_aliases` ADD COLUMN `active_value_hmac` char(64) GENERATED ALWAYS AS (CASE WHEN `matchable` THEN `value_hmac` ELSE NULL END) STORED'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_upstream_task_id_aliases'
          AND index_name = 'uq_gw_task_alias_one_owner'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_upstream_task_id_aliases` ADD UNIQUE KEY `uq_gw_task_alias_one_owner` (`scope_kind`, `scope_key`, `hmac_key_version`, `active_value_hmac`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
