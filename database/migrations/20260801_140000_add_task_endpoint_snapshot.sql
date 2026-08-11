-- Reason: asynchronous Endpoint execution must remain stable after management edits.
-- Requirement: persist the selected Adapter revision and a complete Endpoint configuration snapshot on tasks.
-- Impact: adds internal task metadata used by submit, poll, callback, upload, and billing recovery workers.
-- Deployment: idempotent ALTER statements; existing tasks keep an empty snapshot and use legacy live Endpoint loading.

SET @prism_schema = DATABASE();

SET @task_adapter_id_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'tasks' AND column_name = 'adapter_id'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD COLUMN adapter_id BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT ''Endpoint Adapter ID'' AFTER account_id'
);
PREPARE task_adapter_id_stmt FROM @task_adapter_id_sql;
EXECUTE task_adapter_id_stmt;
DEALLOCATE PREPARE task_adapter_id_stmt;

SET @task_adapter_revision_id_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'tasks' AND column_name = 'adapter_revision_id'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD COLUMN adapter_revision_id BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT ''Endpoint Adapter Revision ID'' AFTER adapter_id'
);
PREPARE task_adapter_revision_id_stmt FROM @task_adapter_revision_id_sql;
EXECUTE task_adapter_revision_id_stmt;
DEALLOCATE PREPARE task_adapter_revision_id_stmt;

SET @task_endpoint_snapshot_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'tasks' AND column_name = 'endpoint_snapshot'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD COLUMN endpoint_snapshot JSON NULL COMMENT ''Endpoint execution snapshot'' AFTER mapped_params'
);
PREPARE task_endpoint_snapshot_stmt FROM @task_endpoint_snapshot_sql;
EXECUTE task_endpoint_snapshot_stmt;
DEALLOCATE PREPARE task_endpoint_snapshot_stmt;

SET @task_adapter_index_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'tasks' AND index_name = 'idx_tasks_adapter_id'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD INDEX idx_tasks_adapter_id (adapter_id)'
);
PREPARE task_adapter_index_stmt FROM @task_adapter_index_sql;
EXECUTE task_adapter_index_stmt;
DEALLOCATE PREPARE task_adapter_index_stmt;

SET @task_adapter_revision_index_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'tasks' AND index_name = 'idx_tasks_adapter_revision_id'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD INDEX idx_tasks_adapter_revision_id (adapter_revision_id)'
);
PREPARE task_adapter_revision_index_stmt FROM @task_adapter_revision_index_sql;
EXECUTE task_adapter_revision_index_stmt;
DEALLOCATE PREPARE task_adapter_revision_index_stmt;
