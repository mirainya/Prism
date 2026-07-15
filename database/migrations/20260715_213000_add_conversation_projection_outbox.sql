-- Reason: make public API conversation projection recoverable after process or database failures.
-- Requirement: stage canonical input/output by unique call_id and retain retry state until projection succeeds.
-- Impact: marks projected API calls, adds one outbox table, and caches canonical history counters used by automatic conversation matching.

SET @prism_schema = DATABASE();

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'api_calls'
          AND column_name = 'project_conversation'
    ),
    'SELECT 1',
    'ALTER TABLE api_calls ADD COLUMN project_conversation TINYINT(1) NOT NULL DEFAULT 0 AFTER conversation_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND column_name = 'system_prompt'
          AND data_type = 'longtext'
    ),
    'SELECT 1',
    'ALTER TABLE conversations MODIFY COLUMN system_prompt LONGTEXT NULL'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND column_name = 'model'
          AND data_type = 'varchar'
          AND character_maximum_length >= 80
    ),
    'SELECT 1',
    'ALTER TABLE conversations MODIFY COLUMN model VARCHAR(80) NULL'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND column_name = 'canonical_item_count'
    ),
    'SELECT 1',
    'ALTER TABLE conversations ADD COLUMN canonical_item_count BIGINT UNSIGNED NOT NULL DEFAULT 0'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND column_name = 'canonical_bytes'
    ),
    'SELECT 1',
    'ALTER TABLE conversations ADD COLUMN canonical_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND column_name = 'canonical_match_hash'
    ),
    'SELECT 1',
    'ALTER TABLE conversations ADD COLUMN canonical_match_hash CHAR(64) NOT NULL DEFAULT '''''
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND column_name = 'canonical_state_version'
    ),
    'SELECT 1',
    'ALTER TABLE conversations ADD COLUMN canonical_state_version TINYINT UNSIGNED NOT NULL DEFAULT 0'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND index_name = 'idx_conversations_canonical_match'
    ),
    'SELECT 1',
    'ALTER TABLE conversations ADD INDEX idx_conversations_canonical_match (user_id, token_id, status, updated_at, id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND index_name = 'idx_conversations_provider_response_id'
    ),
    'SELECT 1',
    'ALTER TABLE conversations ADD INDEX idx_conversations_provider_response_id (provider_response_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

CREATE TABLE IF NOT EXISTS conversation_projection_outbox (
    call_id VARCHAR(64) NOT NULL,
    conversation_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    previous_response_id VARCHAR(128) NOT NULL DEFAULT '',
    request_log_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    provider_response_id VARCHAR(128) NOT NULL DEFAULT '',
    finish_reason VARCHAR(50) NOT NULL DEFAULT '',
    input_json LONGBLOB NULL,
    output_json LONGBLOB NULL,
    input_ready TINYINT(1) NOT NULL DEFAULT 0,
    input_prepared TINYINT(1) NOT NULL DEFAULT 0,
    output_ready TINYINT(1) NOT NULL DEFAULT 0,
    retry_count BIGINT NOT NULL DEFAULT 0,
    last_error TEXT NULL,
    last_attempt_at DATETIME(3) NULL,
    next_attempt_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    PRIMARY KEY (call_id),
    KEY idx_conversation_projection_conversation_id (conversation_id),
    KEY idx_conversation_projection_previous_response_id (previous_response_id),
    KEY idx_conversation_projection_request_log_id (request_log_id),
    KEY idx_conversation_projection_retry (next_attempt_at, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversation_projection_outbox'
          AND column_name = 'input_prepared'
    ),
    'SELECT 1',
    'ALTER TABLE conversation_projection_outbox ADD COLUMN input_prepared TINYINT(1) NOT NULL DEFAULT 0 AFTER input_ready'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema
          AND table_name = 'conversation_projection_outbox'
          AND index_name = 'idx_conversation_projection_previous_response_id'
    ),
    'SELECT 1',
    'ALTER TABLE conversation_projection_outbox ADD INDEX idx_conversation_projection_previous_response_id (previous_response_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema
          AND table_name = 'conversation_projection_outbox'
          AND index_name = 'idx_conversation_projection_request_log_id'
    ),
    'SELECT 1',
    'ALTER TABLE conversation_projection_outbox ADD INDEX idx_conversation_projection_request_log_id (request_log_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema
          AND table_name = 'conversation_projection_outbox'
          AND index_name = 'idx_conversation_projection_conversation_id'
    ),
    'SELECT 1',
    'ALTER TABLE conversation_projection_outbox ADD INDEX idx_conversation_projection_conversation_id (conversation_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema
          AND table_name = 'conversation_projection_outbox'
          AND index_name = 'idx_conversation_projection_retry'
    ),
    'SELECT 1',
    'ALTER TABLE conversation_projection_outbox ADD INDEX idx_conversation_projection_retry (next_attempt_at, updated_at)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
