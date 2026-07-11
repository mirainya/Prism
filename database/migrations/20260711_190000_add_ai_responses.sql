-- Reason: implement Prism-owned Responses IDs, continuation, background execution, and retrieval.
-- Requirement: persist request identity, lifecycle, provider state, normalized items, usage, and errors.
-- Impact: creates ai_responses; no existing table or row is modified.
CREATE TABLE ai_responses (
    id VARCHAR(64) PRIMARY KEY,
    user_id BIGINT UNSIGNED NOT NULL,
    token_id BIGINT UNSIGNED NOT NULL,
    model VARCHAR(80) NOT NULL,
    status VARCHAR(24) NOT NULL,
    background TINYINT(1) NOT NULL DEFAULT 0,
    store TINYINT(1) NOT NULL DEFAULT 1,
    previous_response_id VARCHAR(64) NOT NULL DEFAULT '',
    provider_response_id VARCHAR(128) NOT NULL DEFAULT '',
    channel_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    key_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    request_log_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    request_json JSON NULL,
    input_items JSON NULL,
    output_items JSON NULL,
    response_json JSON NULL,
    usage_json JSON NULL,
    metadata JSON NULL,
    error_json JSON NULL,
    idempotency_key VARCHAR(128) NOT NULL DEFAULT '',
    created_at DATETIME(3) NOT NULL,
    completed_at DATETIME(3) NULL,
    INDEX idx_ai_responses_user (user_id),
    INDEX idx_ai_responses_token (token_id),
    INDEX idx_ai_responses_model (model),
    INDEX idx_ai_responses_status (status),
    INDEX idx_ai_responses_previous (previous_response_id),
    INDEX idx_ai_responses_request_log (request_log_id),
    UNIQUE INDEX idx_ai_responses_idempotency (token_id, idempotency_key)
);
