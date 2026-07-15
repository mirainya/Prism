-- Reason: persist session invalidation, call correlation, billing provenance,
-- upstream transport provenance, and resumable execution state in SQL.
-- Requirement: schema migrations must not depend on GORM AutoMigrate ordering.
-- Impact: creates append-only financial/access/audit tables and conditionally
-- adds columns and indexes to existing operational tables.
-- The information_schema guards also support instances where AutoMigrate has
-- already created some or all of these fields.

CREATE TABLE IF NOT EXISTS balance_entries (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    entry_key VARCHAR(100) NOT NULL,
    source_key VARCHAR(128) NOT NULL DEFAULT '',
    account_type VARCHAR(16) NOT NULL,
    account_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    token_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    direction VARCHAR(10) NOT NULL,
    category VARCHAR(32) NOT NULL,
    amount DECIMAL(20,8) NOT NULL,
    balance_before DECIMAL(20,8) NOT NULL,
    balance_after DECIMAL(20,8) NOT NULL,
    call_id VARCHAR(64) NOT NULL DEFAULT '',
    attempt_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    actor_user_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    metadata JSON NULL,
    created_at DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY idx_balance_entries_entry_key (entry_key),
    KEY idx_balance_entries_source_key (source_key),
    KEY idx_balance_account_created (account_type, account_id, created_at),
    KEY idx_balance_user_created (user_id, created_at),
    KEY idx_balance_token_created (token_id, created_at),
    KEY idx_balance_entries_direction (direction),
    KEY idx_balance_entries_category (category),
    KEY idx_balance_entries_call_id (call_id),
    KEY idx_balance_entries_attempt_id (attempt_id),
    KEY idx_balance_entries_actor_user_id (actor_user_id),
    KEY idx_balance_entries_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS api_access_logs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    request_id VARCHAR(128) NOT NULL DEFAULT '',
    call_id VARCHAR(64) NOT NULL DEFAULT '',
    user_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    token_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    actor_type VARCHAR(16) NOT NULL DEFAULT 'anonymous',
    method VARCHAR(10) NOT NULL,
    path VARCHAR(500) NOT NULL,
    route VARCHAR(500) NOT NULL DEFAULT '',
    query TEXT NULL,
    status_code BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    ip VARCHAR(64) NOT NULL DEFAULT '',
    user_agent VARCHAR(512) NOT NULL DEFAULT '',
    error_code VARCHAR(128) NOT NULL DEFAULT '',
    created_at DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_api_access_logs_request_id (request_id),
    KEY idx_api_access_logs_call_id (call_id),
    KEY idx_api_access_user_created (user_id, created_at),
    KEY idx_api_access_token_created (token_id, created_at),
    KEY idx_api_access_logs_actor_type (actor_type),
    KEY idx_api_access_logs_method (method),
    KEY idx_api_access_logs_route (route),
    KEY idx_api_access_logs_status_code (status_code),
    KEY idx_api_access_logs_error_code (error_code),
    KEY idx_api_access_logs_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS audit_events (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    request_id VARCHAR(128) NOT NULL DEFAULT '',
    actor_type VARCHAR(16) NOT NULL DEFAULT 'anonymous',
    actor_user_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    actor_token_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    action VARCHAR(128) NOT NULL,
    resource_type VARCHAR(64) NOT NULL DEFAULT '',
    resource_id VARCHAR(128) NOT NULL DEFAULT '',
    outcome VARCHAR(16) NOT NULL,
    http_status BIGINT NOT NULL DEFAULT 0,
    ip VARCHAR(64) NOT NULL DEFAULT '',
    metadata JSON NULL,
    created_at DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_audit_events_request_id (request_id),
    KEY idx_audit_events_actor_type (actor_type),
    KEY idx_audit_actor_created (actor_user_id, created_at),
    KEY idx_audit_events_actor_token_id (actor_token_id),
    KEY idx_audit_events_action (action),
    KEY idx_audit_events_resource_type (resource_type),
    KEY idx_audit_events_resource_id (resource_id),
    KEY idx_audit_events_outcome (outcome),
    KEY idx_audit_events_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

SET @prism_schema = DATABASE();

-- Console session invalidation.
SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'users' AND column_name = 'session_version'),
    'SELECT 1',
    'ALTER TABLE users ADD COLUMN session_version BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT ''控制台会话版本'''
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Responses ownership, transport provenance, idempotency digest, execution
-- lease, retry counter, and durable result checkpoint.
SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'ai_responses' AND column_name = 'call_id'),
    'SELECT 1',
    CONCAT('ALTER TABLE ai_responses ADD COLUMN call_id VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'ai_responses' AND column_name = 'upstream_transport'),
    'SELECT 1',
    CONCAT('ALTER TABLE ai_responses ADD COLUMN upstream_transport VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'ai_responses' AND column_name = 'request_hash'),
    'SELECT 1',
    CONCAT('ALTER TABLE ai_responses ADD COLUMN request_hash CHAR(64) NOT NULL DEFAULT ', QUOTE(''))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'ai_responses' AND column_name = 'lease_owner'),
    'SELECT 1',
    CONCAT('ALTER TABLE ai_responses ADD COLUMN lease_owner VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'ai_responses' AND column_name = 'lease_expires_at'),
    'SELECT 1',
    'ALTER TABLE ai_responses ADD COLUMN lease_expires_at DATETIME(3) NULL'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'ai_responses' AND column_name = 'execution_attempt'),
    'SELECT 1',
    'ALTER TABLE ai_responses ADD COLUMN execution_attempt BIGINT NOT NULL DEFAULT 0'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'ai_responses' AND column_name = 'result_ready_at'),
    'SELECT 1',
    'ALTER TABLE ai_responses ADD COLUMN result_ready_at DATETIME(3) NULL'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'ai_responses' AND index_name = 'idx_ai_responses_call'),
    'SELECT 1',
    'ALTER TABLE ai_responses ADD INDEX idx_ai_responses_call (call_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'ai_responses' AND index_name = 'idx_ai_responses_lease'),
    'SELECT 1',
    'ALTER TABLE ai_responses ADD INDEX idx_ai_responses_lease (lease_expires_at)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Conversation and message correlation, plus stateful upstream provenance.
SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'conversations' AND column_name = 'call_id'),
    'SELECT 1',
    CONCAT('ALTER TABLE conversations ADD COLUMN call_id VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''), ' COMMENT ', QUOTE('最近关联调用ID'))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'conversations' AND column_name = 'provider_key_id'),
    'SELECT 1',
    'ALTER TABLE conversations ADD COLUMN provider_key_id BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT ''上游状态所属网关Key'''
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND column_name = 'provider_key_id'
          AND is_nullable = 'YES'
    ),
    'UPDATE conversations SET provider_key_id = 0 WHERE provider_key_id IS NULL',
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND column_name = 'provider_key_id'
          AND LOWER(column_type) = 'bigint unsigned'
          AND is_nullable = 'NO'
          AND CAST(column_default AS CHAR) = '0'
          AND column_comment = '上游状态所属网关Key'
    ),
    'SELECT 1',
    'ALTER TABLE conversations MODIFY COLUMN provider_key_id BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT ''上游状态所属网关Key'''
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'conversations' AND column_name = 'upstream_transport'),
    'SELECT 1',
    CONCAT('ALTER TABLE conversations ADD COLUMN upstream_transport VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''), ' COMMENT ', QUOTE('上游状态所属Transport'))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND column_name = 'upstream_transport'
          AND is_nullable = 'YES'
    ),
    CONCAT('UPDATE conversations SET upstream_transport = ', QUOTE(''), ' WHERE upstream_transport IS NULL'),
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND column_name = 'upstream_transport'
          AND data_type = 'varchar'
          AND character_maximum_length = 64
          AND is_nullable = 'NO'
          AND column_default = ''
          AND column_comment = '上游状态所属Transport'
    ),
    'SELECT 1',
    CONCAT('ALTER TABLE conversations MODIFY COLUMN upstream_transport VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''), ' COMMENT ', QUOTE('上游状态所属Transport'))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'conversations' AND index_name = 'idx_conversations_call_id'),
    'SELECT 1',
    'ALTER TABLE conversations ADD INDEX idx_conversations_call_id (call_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'messages' AND column_name = 'call_id'),
    'SELECT 1',
    CONCAT('ALTER TABLE messages ADD COLUMN call_id VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''), ' COMMENT ', QUOTE('关联调用ID'))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'messages' AND index_name = 'idx_messages_call_id'),
    'SELECT 1',
    'ALTER TABLE messages ADD INDEX idx_messages_call_id (call_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Capability task correlation.
SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'tasks' AND column_name = 'call_id'),
    'SELECT 1',
    CONCAT('ALTER TABLE tasks ADD COLUMN call_id VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''), ' COMMENT ', QUOTE('关联调用ID'))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'tasks' AND index_name = 'idx_tasks_call_id'),
    'SELECT 1',
    'ALTER TABLE tasks ADD INDEX idx_tasks_call_id (call_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Concrete upstream request correlation and transport provenance.
SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'channel_request_logs' AND column_name = 'call_id'),
    'SELECT 1',
    CONCAT('ALTER TABLE channel_request_logs ADD COLUMN call_id VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'channel_request_logs' AND column_name = 'attempt_id'),
    'SELECT 1',
    'ALTER TABLE channel_request_logs ADD COLUMN attempt_id BIGINT UNSIGNED NOT NULL DEFAULT 0'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'channel_request_logs' AND column_name = 'upstream_transport'),
    'SELECT 1',
    CONCAT('ALTER TABLE channel_request_logs ADD COLUMN upstream_transport VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''), ' COMMENT ', QUOTE('实际上游传输协议'))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'channel_request_logs'
          AND column_name = 'upstream_transport'
          AND is_nullable = 'YES'
    ),
    CONCAT('UPDATE channel_request_logs SET upstream_transport = ', QUOTE(''), ' WHERE upstream_transport IS NULL'),
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'channel_request_logs'
          AND column_name = 'upstream_transport'
          AND data_type = 'varchar'
          AND character_maximum_length = 64
          AND is_nullable = 'NO'
          AND column_default = ''
          AND column_comment = '实际上游传输协议'
    ),
    'SELECT 1',
    CONCAT('ALTER TABLE channel_request_logs MODIFY COLUMN upstream_transport VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''), ' COMMENT ', QUOTE('实际上游传输协议'))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'channel_request_logs' AND index_name = 'idx_channel_request_logs_call_id'),
    'SELECT 1',
    'ALTER TABLE channel_request_logs ADD INDEX idx_channel_request_logs_call_id (call_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'channel_request_logs' AND index_name = 'idx_channel_request_logs_attempt_id'),
    'SELECT 1',
    'ALTER TABLE channel_request_logs ADD INDEX idx_channel_request_logs_attempt_id (attempt_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'channel_request_logs' AND index_name = 'idx_channel_request_logs_upstream_transport'),
    'SELECT 1',
    'ALTER TABLE channel_request_logs ADD INDEX idx_channel_request_logs_upstream_transport (upstream_transport)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Billing correlation, lifecycle phase, and immutable pricing provenance.
SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'billing_logs' AND column_name = 'call_id'),
    'SELECT 1',
    CONCAT('ALTER TABLE billing_logs ADD COLUMN call_id VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'billing_logs' AND column_name = 'attempt_id'),
    'SELECT 1',
    'ALTER TABLE billing_logs ADD COLUMN attempt_id BIGINT UNSIGNED NOT NULL DEFAULT 0'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'billing_logs' AND column_name = 'phase'),
    'SELECT 1',
    CONCAT('ALTER TABLE billing_logs ADD COLUMN phase VARCHAR(24) NOT NULL DEFAULT ', QUOTE(''))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'billing_logs' AND column_name = 'pricing_snapshot'),
    'SELECT 1',
    'ALTER TABLE billing_logs ADD COLUMN pricing_snapshot JSON NULL'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'billing_logs' AND index_name = 'idx_billing_logs_call_id'),
    'SELECT 1',
    'ALTER TABLE billing_logs ADD INDEX idx_billing_logs_call_id (call_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'billing_logs' AND index_name = 'idx_billing_logs_attempt_id'),
    'SELECT 1',
    'ALTER TABLE billing_logs ADD INDEX idx_billing_logs_attempt_id (attempt_id)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'billing_logs' AND index_name = 'idx_billing_logs_phase'),
    'SELECT 1',
    'ALTER TABLE billing_logs ADD INDEX idx_billing_logs_phase (phase)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Foreground call execution lease. These guards also upgrade an instance that
-- already ran the original ledger migration before lease support was added.
SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'api_calls' AND column_name = 'lease_owner'),
    'SELECT 1',
    CONCAT('ALTER TABLE api_calls ADD COLUMN lease_owner VARCHAR(64) NOT NULL DEFAULT ', QUOTE(''))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = @prism_schema AND table_name = 'api_calls' AND column_name = 'lease_expires_at'),
    'SELECT 1',
    'ALTER TABLE api_calls ADD COLUMN lease_expires_at DATETIME(3) NULL'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'api_calls' AND index_name = 'idx_api_calls_lease_expires_at'),
    'SELECT 1',
    'ALTER TABLE api_calls ADD INDEX idx_api_calls_lease_expires_at (lease_expires_at)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Keep previously created ledger tables consistent with the final model.
SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'api_calls'
          AND column_name = 'endpoint'
          AND data_type = 'varchar'
          AND character_maximum_length >= 255
    ),
    'SELECT 1',
    CONCAT('ALTER TABLE api_calls MODIFY COLUMN endpoint VARCHAR(255) NOT NULL DEFAULT ', QUOTE(''))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'api_calls' AND index_name = 'idx_api_calls_updated_at'),
    'SELECT 1',
    'ALTER TABLE api_calls ADD INDEX idx_api_calls_updated_at (updated_at)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'api_call_attempts' AND index_name = 'idx_api_call_attempts_call_id'),
    'ALTER TABLE api_call_attempts DROP INDEX idx_api_call_attempts_call_id',
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'balance_entries' AND index_name = 'idx_balance_user_created'),
    'SELECT 1',
    'ALTER TABLE balance_entries ADD INDEX idx_balance_user_created (user_id, created_at)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'balance_entries' AND index_name = 'idx_balance_token_created'),
    'SELECT 1',
    'ALTER TABLE balance_entries ADD INDEX idx_balance_token_created (token_id, created_at)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'balance_entries' AND index_name = 'idx_balance_entries_user_id'),
    'ALTER TABLE balance_entries DROP INDEX idx_balance_entries_user_id',
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = @prism_schema AND table_name = 'balance_entries' AND index_name = 'idx_balance_entries_token_id'),
    'ALTER TABLE balance_entries DROP INDEX idx_balance_entries_token_id',
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_schema = NULL;
SET @prism_ddl = NULL;
