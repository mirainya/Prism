-- Bound credential and pool admission checks to their own active slots.
-- Required for cross-process request/task limits and retained recovery slots.
-- MySQL DDL implicitly commits, so each index is guarded independently.

SET @prism_schema = DATABASE();

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema
          AND table_name = 'gw_credential_slots'
          AND index_name = 'idx_gw_slots_pool_scope_state'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_credential_slots`
        ADD INDEX `idx_gw_slots_pool_scope_state` (`credential_pool_id`, `scope`, `state`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema
          AND table_name = 'gw_credential_slots'
          AND index_name = 'idx_gw_slots_credential_scope_state'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_credential_slots`
        ADD INDEX `idx_gw_slots_credential_scope_state` (`credential_id`, `scope`, `state`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
