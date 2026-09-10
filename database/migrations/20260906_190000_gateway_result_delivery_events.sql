-- Add delivery transitions and the HTTP evidence for each remote source.
-- Generation success remains independent of reference availability.
-- MySQL DDL implicitly commits; each schema fact is guarded independently.

SET @prism_schema = DATABASE();

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_result_deliveries'
          AND column_name = 'reason_code'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_result_deliveries` ADD COLUMN `reason_code` varchar(64) NOT NULL DEFAULT '''''
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_result_deliveries'
          AND index_name = 'idx_gw_result_deliveries_expiry'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_result_deliveries` ADD KEY `idx_gw_result_deliveries_expiry` (`state`, `expires_at`, `id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_result_delivery_sources'
          AND column_name = 'observed_request_log_id'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_result_delivery_sources` ADD COLUMN `observed_request_log_id` bigint unsigned NULL'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_result_delivery_sources'
          AND constraint_name = 'fk_gw_result_sources_observed_request' AND constraint_type = 'FOREIGN KEY'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_result_delivery_sources` ADD CONSTRAINT `fk_gw_result_sources_observed_request` FOREIGN KEY (`observed_request_log_id`) REFERENCES `gw_channel_request_logs` (`id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_state_transition_events'
          AND column_name = 'result_delivery_id'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_state_transition_events` ADD COLUMN `result_delivery_id` bigint unsigned NULL'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_state_transition_events'
          AND index_name = 'uq_gw_state_events_delivery_version'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_state_transition_events` ADD UNIQUE KEY `uq_gw_state_events_delivery_version` (`result_delivery_id`, `state_version`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_state_transition_events'
          AND constraint_name = 'fk_gw_state_events_delivery' AND constraint_type = 'FOREIGN KEY'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_state_transition_events` ADD CONSTRAINT `fk_gw_state_events_delivery` FOREIGN KEY (`result_delivery_id`) REFERENCES `gw_result_deliveries` (`id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Replace the parent exclusivity check. Drop and add are separate guarded DDL
-- statements so either side can be retried after an implicit commit.
SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_state_transition_events'
          AND constraint_name = 'ck_gw_state_events_one_parent' AND constraint_type = 'CHECK'
    ),
    'ALTER TABLE `gw_state_transition_events` DROP CHECK `ck_gw_state_events_one_parent`',
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_schema = @prism_schema AND table_name = 'gw_state_transition_events'
          AND constraint_name = 'ck_gw_state_events_one_parent' AND constraint_type = 'CHECK'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_state_transition_events` ADD CONSTRAINT `ck_gw_state_events_one_parent` CHECK ((`call_id` IS NOT NULL) + (`attempt_id` IS NOT NULL) + (`async_execution_id` IS NOT NULL) + (`task_identity_id` IS NOT NULL) + (`provider_state_ref_id` IS NOT NULL) + (`callback_receipt_id` IS NOT NULL) + (`result_delivery_id` IS NOT NULL) = 1)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
