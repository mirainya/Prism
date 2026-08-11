-- Reason: integrate video generation with the unified API call ledger and material lifecycle.
-- Requirement: persist task ownership/call identity and allow public material URLs.
-- Impact: adds two video_tasks columns, their indexes, and expands video_assets.storage_path.

SET @prism_schema = DATABASE();

SET @video_task_call_id_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'video_tasks' AND column_name = 'call_id'
    ),
    'SELECT 1',
    'ALTER TABLE video_tasks ADD COLUMN call_id VARCHAR(64) NOT NULL DEFAULT '''' AFTER id'
);
PREPARE video_task_call_id_stmt FROM @video_task_call_id_sql;
EXECUTE video_task_call_id_stmt;
DEALLOCATE PREPARE video_task_call_id_stmt;

SET @video_task_user_id_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'video_tasks' AND column_name = 'user_id'
    ),
    'SELECT 1',
    'ALTER TABLE video_tasks ADD COLUMN user_id BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER call_id'
);
PREPARE video_task_user_id_stmt FROM @video_task_user_id_sql;
EXECUTE video_task_user_id_stmt;
DEALLOCATE PREPARE video_task_user_id_stmt;

SET @video_task_call_index_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'video_tasks' AND index_name = 'idx_video_tasks_call_id'
    ),
    'SELECT 1',
    'ALTER TABLE video_tasks ADD INDEX idx_video_tasks_call_id (call_id)'
);
PREPARE video_task_call_index_stmt FROM @video_task_call_index_sql;
EXECUTE video_task_call_index_stmt;
DEALLOCATE PREPARE video_task_call_index_stmt;

SET @video_task_user_status_index_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'video_tasks' AND index_name = 'idx_user_status'
    ),
    'SELECT 1',
    'ALTER TABLE video_tasks ADD INDEX idx_user_status (user_id, status, created_at DESC)'
);
PREPARE video_task_user_status_index_stmt FROM @video_task_user_status_index_sql;
EXECUTE video_task_user_status_index_stmt;
DEALLOCATE PREPARE video_task_user_status_index_stmt;

ALTER TABLE video_assets MODIFY COLUMN storage_path VARCHAR(1024) NULL COMMENT 'Storage path or public URL';
