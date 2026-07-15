-- Reason: resume task finalization after an upstream submit succeeds but ledger finalization fails.
-- Requirement: retries must not repeat an acknowledged upstream submit.
-- Impact: adds a transient checkpoint, task worker lease, and poll sequence cursor; terminal transitions clear runtime state.

SET @prism_schema = DATABASE();
SET @task_submit_checkpoint_sql = IF(
    EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'tasks'
          AND column_name = 'submit_checkpoint'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD COLUMN submit_checkpoint JSON NULL COMMENT ''上游提交恢复检查点'''
);
PREPARE task_submit_checkpoint_stmt FROM @task_submit_checkpoint_sql;
EXECUTE task_submit_checkpoint_stmt;
DEALLOCATE PREPARE task_submit_checkpoint_stmt;

SET @task_worker_lease_owner_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'tasks' AND column_name = 'worker_lease_owner'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD COLUMN worker_lease_owner VARCHAR(64) NOT NULL DEFAULT '''' COMMENT ''任务执行租约所有者'''
);
PREPARE task_worker_lease_owner_stmt FROM @task_worker_lease_owner_sql;
EXECUTE task_worker_lease_owner_stmt;
DEALLOCATE PREPARE task_worker_lease_owner_stmt;

SET @task_worker_lease_stage_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'tasks' AND column_name = 'worker_lease_stage'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD COLUMN worker_lease_stage VARCHAR(24) NOT NULL DEFAULT '''' COMMENT ''任务执行租约阶段'''
);
PREPARE task_worker_lease_stage_stmt FROM @task_worker_lease_stage_sql;
EXECUTE task_worker_lease_stage_stmt;
DEALLOCATE PREPARE task_worker_lease_stage_stmt;

SET @task_worker_lease_expires_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'tasks' AND column_name = 'worker_lease_expires_at'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD COLUMN worker_lease_expires_at DATETIME(3) NULL COMMENT ''任务执行租约过期时间'''
);
PREPARE task_worker_lease_expires_stmt FROM @task_worker_lease_expires_sql;
EXECUTE task_worker_lease_expires_stmt;
DEALLOCATE PREPARE task_worker_lease_expires_stmt;

SET @task_poll_cursor_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'tasks' AND column_name = 'poll_cursor'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD COLUMN poll_cursor BIGINT NOT NULL DEFAULT 0 COMMENT ''下一轮轮询序号'''
);
PREPARE task_poll_cursor_stmt FROM @task_poll_cursor_sql;
EXECUTE task_poll_cursor_stmt;
DEALLOCATE PREPARE task_poll_cursor_stmt;

-- -1 lets the first already-queued Redis poll adopt its existing poll_count.
-- Run this backfill even when AutoMigrate created the column first. Active
-- new-worker leases are excluded so a poll that already validated cursor 0
-- cannot race this update. Stop every old and new worker while applying the
-- migration because old workers do not participate in the SQL lease.
SET @task_poll_cursor_legacy_sql =
    'UPDATE tasks SET poll_cursor = -1 WHERE poll_cursor = 0 AND status IN (''processing'', ''finalizing'') AND (worker_lease_owner = '''' OR worker_lease_owner IS NULL OR worker_lease_expires_at IS NULL OR worker_lease_expires_at <= CURRENT_TIMESTAMP(3))';
PREPARE task_poll_cursor_legacy_stmt FROM @task_poll_cursor_legacy_sql;
EXECUTE task_poll_cursor_legacy_stmt;
DEALLOCATE PREPARE task_poll_cursor_legacy_stmt;

SET @task_worker_lease_index_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'tasks' AND index_name = 'idx_tasks_worker_lease_expires_at'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD INDEX idx_tasks_worker_lease_expires_at (worker_lease_expires_at)'
);
PREPARE task_worker_lease_index_stmt FROM @task_worker_lease_index_sql;
EXECUTE task_worker_lease_index_stmt;
DEALLOCATE PREPARE task_worker_lease_index_stmt;
