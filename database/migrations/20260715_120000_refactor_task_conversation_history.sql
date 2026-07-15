-- Reason: store Playground history as ordered canonical turns and support stable operational queries.
-- Requirement: the migration must be idempotent and remain compatible when GORM AutoMigrate ran first.
-- Impact: adds conversation turn/item projections and composite task/conversation indexes. Existing
-- conversations and messages are intentionally not backfilled.

SET @prism_schema = DATABASE();

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND column_name = 'turn_sequence'
    ),
    'SELECT 1',
    'ALTER TABLE conversations ADD COLUMN turn_sequence BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT ''Last allocated turn sequence'''
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

CREATE TABLE IF NOT EXISTS conversation_turns (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    conversation_id BIGINT UNSIGNED NOT NULL,
    turn_sequence BIGINT UNSIGNED NOT NULL,
    call_id VARCHAR(64) NOT NULL,
    request_log_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    model VARCHAR(80) NOT NULL DEFAULT '',
    provider_response_id VARCHAR(128) NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL DEFAULT 'completed',
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    cost DECIMAL(20,8) NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    finish_reason VARCHAR(50) NOT NULL DEFAULT '',
    error_type VARCHAR(64) NOT NULL DEFAULT '',
    error_code VARCHAR(128) NOT NULL DEFAULT '',
    error_message TEXT NULL,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY idx_conversation_turn_sequence (conversation_id, turn_sequence),
    UNIQUE KEY idx_conversation_turns_call_id (call_id),
    KEY idx_conversation_turn_created (conversation_id, created_at),
    KEY idx_conversation_turns_request_log_id (request_log_id),
    KEY idx_conversation_turns_model (model),
    KEY idx_conversation_turns_provider_response_id (provider_response_id),
    KEY idx_conversation_turns_status (status),
    KEY idx_conversation_turns_error_code (error_code)
);

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversation_turns' AND index_name = 'idx_conversation_turn_sequence'),
    'SELECT 1',
    'ALTER TABLE conversation_turns ADD UNIQUE INDEX idx_conversation_turn_sequence (conversation_id, turn_sequence)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversation_turns' AND index_name = 'idx_conversation_turns_call_id'),
    'SELECT 1',
    'ALTER TABLE conversation_turns ADD UNIQUE INDEX idx_conversation_turns_call_id (call_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversation_turns' AND index_name = 'idx_conversation_turn_created'),
    'SELECT 1',
    'ALTER TABLE conversation_turns ADD INDEX idx_conversation_turn_created (conversation_id, created_at)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversation_turns' AND index_name = 'idx_conversation_turns_request_log_id'),
    'SELECT 1',
    'ALTER TABLE conversation_turns ADD INDEX idx_conversation_turns_request_log_id (request_log_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversation_turns' AND index_name = 'idx_conversation_turns_model'),
    'SELECT 1',
    'ALTER TABLE conversation_turns ADD INDEX idx_conversation_turns_model (model)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversation_turns' AND index_name = 'idx_conversation_turns_provider_response_id'),
    'SELECT 1',
    'ALTER TABLE conversation_turns ADD INDEX idx_conversation_turns_provider_response_id (provider_response_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversation_turns' AND index_name = 'idx_conversation_turns_status'),
    'SELECT 1',
    'ALTER TABLE conversation_turns ADD INDEX idx_conversation_turns_status (status)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversation_turns' AND index_name = 'idx_conversation_turns_error_code'),
    'SELECT 1',
    'ALTER TABLE conversation_turns ADD INDEX idx_conversation_turns_error_code (error_code)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

CREATE TABLE IF NOT EXISTS conversation_items (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    conversation_id BIGINT UNSIGNED NOT NULL,
    turn_id BIGINT UNSIGNED NOT NULL,
    turn_sequence BIGINT UNSIGNED NOT NULL,
    direction VARCHAR(16) NOT NULL,
    ordinal BIGINT NOT NULL,
    canonical_json JSON NOT NULL,
    created_at DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY idx_conversation_item_ordinal (turn_id, ordinal),
    KEY idx_conversation_items_order (conversation_id, turn_sequence, ordinal),
    KEY idx_conversation_items_direction (direction)
);

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversation_items' AND index_name = 'idx_conversation_item_ordinal'),
    'SELECT 1',
    'ALTER TABLE conversation_items ADD UNIQUE INDEX idx_conversation_item_ordinal (turn_id, ordinal)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversation_items' AND index_name = 'idx_conversation_items_order'),
    'SELECT 1',
    'ALTER TABLE conversation_items ADD INDEX idx_conversation_items_order (conversation_id, turn_sequence, ordinal)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversation_items' AND index_name = 'idx_conversation_items_direction'),
    'SELECT 1',
    'ALTER TABLE conversation_items ADD INDEX idx_conversation_items_direction (direction)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'tasks' AND index_name = 'idx_tasks_user_created_id'),
    'SELECT 1',
    'ALTER TABLE tasks ADD INDEX idx_tasks_user_created_id (user_id, created_at, id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'tasks' AND index_name = 'idx_tasks_token_created_id'),
    'SELECT 1',
    'ALTER TABLE tasks ADD INDEX idx_tasks_token_created_id (token_id, created_at, id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'tasks' AND index_name = 'idx_tasks_status_updated_id'),
    'SELECT 1',
    'ALTER TABLE tasks ADD INDEX idx_tasks_status_updated_id (status, updated_at, id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'tasks' AND index_name = 'idx_tasks_status_lease_id'),
    'SELECT 1',
    'ALTER TABLE tasks ADD INDEX idx_tasks_status_lease_id (status, worker_lease_expires_at, id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversations' AND index_name = 'idx_conversations_user_token_updated_id'),
    'SELECT 1',
    'ALTER TABLE conversations ADD INDEX idx_conversations_user_token_updated_id (user_id, token_id, updated_at, id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
