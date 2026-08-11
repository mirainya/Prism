-- Reason: prevent duplicate video submissions and preserve retry state across worker crashes.
-- Requirement: add renewable worker ownership and a persistent submit checkpoint.
-- Impact: adds three nullable/defaulted columns and one recovery index to video_tasks.

SET @prism_schema = DATABASE();

UPDATE video_tasks SET worker_lease = '' WHERE worker_lease IS NULL;
ALTER TABLE video_tasks MODIFY COLUMN worker_lease VARCHAR(64) NOT NULL DEFAULT '' COMMENT 'Worker lease owner';

SET @video_worker_stage_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'video_tasks' AND column_name = 'worker_lease_stage'
    ),
    'SELECT 1',
    'ALTER TABLE video_tasks ADD COLUMN worker_lease_stage VARCHAR(16) NOT NULL DEFAULT '''' AFTER worker_lease'
);
PREPARE video_worker_stage_stmt FROM @video_worker_stage_sql;
EXECUTE video_worker_stage_stmt;
DEALLOCATE PREPARE video_worker_stage_stmt;

SET @video_worker_expiry_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'video_tasks' AND column_name = 'worker_lease_expires_at'
    ),
    'SELECT 1',
    'ALTER TABLE video_tasks ADD COLUMN worker_lease_expires_at DATETIME(3) NULL AFTER worker_lease_stage'
);
PREPARE video_worker_expiry_stmt FROM @video_worker_expiry_sql;
EXECUTE video_worker_expiry_stmt;
DEALLOCATE PREPARE video_worker_expiry_stmt;

SET @video_submit_checkpoint_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'video_tasks' AND column_name = 'submit_checkpoint'
    ),
    'SELECT 1',
    'ALTER TABLE video_tasks ADD COLUMN submit_checkpoint JSON NULL AFTER worker_lease_expires_at'
);
PREPARE video_submit_checkpoint_stmt FROM @video_submit_checkpoint_sql;
EXECUTE video_submit_checkpoint_stmt;
DEALLOCATE PREPARE video_submit_checkpoint_stmt;

SET @video_worker_recovery_index_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'video_tasks' AND index_name = 'idx_video_worker_recovery'
    ),
    'SELECT 1',
    'ALTER TABLE video_tasks ADD INDEX idx_video_worker_recovery (status, worker_lease_expires_at)'
);
PREPARE video_worker_recovery_index_stmt FROM @video_worker_recovery_index_sql;
EXECUTE video_worker_recovery_index_stmt;
DEALLOCATE PREPARE video_worker_recovery_index_stmt;
