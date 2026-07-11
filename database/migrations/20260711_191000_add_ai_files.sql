-- Reason: allow Files API uploads to be referenced safely by Chat and Responses requests.
-- Requirement: bind every file to one Token and retain metadata plus bounded file content.
-- Impact: creates ai_files; no existing table or row is modified.
CREATE TABLE ai_files (
    id VARCHAR(64) PRIMARY KEY,
    user_id BIGINT UNSIGNED NOT NULL,
    token_id BIGINT UNSIGNED NOT NULL,
    filename VARCHAR(255) NOT NULL,
    purpose VARCHAR(40) NOT NULL,
    bytes BIGINT NOT NULL,
    mime_type VARCHAR(120) NOT NULL DEFAULT '',
    content LONGBLOB NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'processed',
    created_at DATETIME(3) NOT NULL,
    INDEX idx_ai_files_user (user_id),
    INDEX idx_ai_files_token (token_id),
    INDEX idx_ai_files_purpose (purpose)
);
