-- Reason: store short-lived Responses idempotency results without enabling response retention.
-- Requirement: coordinate concurrent Idempotency-Key requests, reject hash conflicts, and expire cached results.
-- Impact: creates ai_response_idempotency_cache and makes stored response keys byte-exact.
CREATE TABLE IF NOT EXISTS ai_response_idempotency_cache (
    token_id BIGINT UNSIGNED NOT NULL,
    idempotency_key VARBINARY(128) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    owner VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL,
    response_id VARCHAR(64) NOT NULL DEFAULT '',
    response_json JSON NULL,
    expires_at DATETIME(3) NOT NULL,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    PRIMARY KEY (token_id, idempotency_key),
    INDEX idx_ai_response_idempotency_status (status),
    INDEX idx_ai_response_idempotency_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @prism_schema = DATABASE();

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'ai_responses'
          AND column_name = 'idempotency_key'
          AND data_type = 'varbinary'
          AND character_maximum_length = 512
    ),
    'SELECT 1',
    CONCAT('ALTER TABLE ai_responses MODIFY COLUMN idempotency_key VARBINARY(512) NOT NULL DEFAULT ', QUOTE(''))
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'ai_response_idempotency_cache'
          AND column_name = 'idempotency_key'
          AND data_type = 'varbinary'
          AND character_maximum_length = 128
    ),
    'SELECT 1',
    'ALTER TABLE ai_response_idempotency_cache MODIFY COLUMN idempotency_key VARBINARY(128) NOT NULL'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_schema = NULL;
SET @prism_ddl = NULL;
