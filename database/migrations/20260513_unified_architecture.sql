-- 统一架构重构迁移
-- 备份文件: database/backup/20260513_before_refactor.sql

-- 1. 新建 models 表（统一 chat_models + capabilities）
CREATE TABLE IF NOT EXISTS models (
    code VARCHAR(80) NOT NULL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(20) NOT NULL DEFAULT 'chat',
    provider VARCHAR(30) DEFAULT '',
    description VARCHAR(500) DEFAULT '',
    features JSON,
    param_schema JSON,
    max_tokens INT DEFAULT 0,
    status TINYINT DEFAULT 1,
    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3) DEFAULT NULL,
    INDEX idx_type (type),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 2. 新建 endpoints 表（统一 chat_model_channels + channel_capabilities）
CREATE TABLE IF NOT EXISTS endpoints (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    model_code VARCHAR(80) NOT NULL,
    channel_id INT UNSIGNED NOT NULL,

    -- 协议
    protocol VARCHAR(20) NOT NULL DEFAULT 'openai',
    request_path VARCHAR(255) NOT NULL DEFAULT '/v1/chat/completions',
    request_method VARCHAR(10) NOT NULL DEFAULT 'POST',
    content_type VARCHAR(50) DEFAULT 'application/json',

    -- 鉴权
    auth_location VARCHAR(10) DEFAULT 'header',
    auth_key VARCHAR(50) DEFAULT 'Authorization',
    auth_value_prefix VARCHAR(30) DEFAULT 'Bearer ',

    -- 模型映射
    vendor_model VARCHAR(80) NOT NULL,

    -- 交互模式
    interaction_mode VARCHAR(10) NOT NULL DEFAULT 'stream',
    supports_stream TINYINT(1) DEFAULT 1,

    -- 定价
    price_mode VARCHAR(10) DEFAULT 'token',
    input_price DECIMAL(12,8) DEFAULT 0,
    output_price DECIMAL(12,8) DEFAULT 0,

    -- 映射配置
    param_mapping JSON,
    response_mapping JSON,

    -- 轮询
    poll_path VARCHAR(255) DEFAULT '',
    poll_method VARCHAR(10) DEFAULT 'GET',
    poll_interval INT DEFAULT 5,
    poll_max_attempts INT DEFAULT 60,
    poll_param_mapping JSON,
    poll_response_mapping JSON,

    -- 回调
    callback_mapping JSON,

    -- 扩展
    extra_headers JSON,
    extra_config JSON,
    timeout INT DEFAULT 120,
    priority INT DEFAULT 0,
    status TINYINT DEFAULT 1,

    created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3) DEFAULT NULL,

    INDEX idx_model_code (model_code),
    INDEX idx_channel_id (channel_id),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 3. 迁移数据：chat_models → models
INSERT INTO models (code, name, type, provider, max_tokens, status, created_at, updated_at)
SELECT code, name, 'chat', provider, max_tokens, status, created_at, updated_at
FROM chat_models
WHERE deleted_at IS NULL;

-- 4. 迁移数据：capabilities → models
INSERT INTO models (code, name, type, status, created_at, updated_at)
SELECT code, name, type, status, created_at, updated_at
FROM capabilities
WHERE deleted_at IS NULL
ON DUPLICATE KEY UPDATE name=VALUES(name);

-- 5. 迁移数据：chat_model_channels → endpoints
INSERT INTO endpoints (model_code, channel_id, protocol, request_path, vendor_model,
    interaction_mode, supports_stream, price_mode, input_price, output_price,
    extra_headers, extra_config, timeout, priority, status, created_at, updated_at)
SELECT
    model_code, channel_id, 'openai', request_path, vendor_model,
    'stream',
    COALESCE(supports_stream, 1),
    COALESCE(price_mode, 'token'),
    input_price, output_price,
    extra_headers, extra_config,
    timeout, priority, status, created_at, updated_at
FROM chat_model_channels
WHERE deleted_at IS NULL;

-- 6. 迁移数据：channel_capabilities → endpoints
INSERT INTO endpoints (model_code, channel_id, protocol, request_path, request_method,
    content_type, auth_location, auth_key, auth_value_prefix, vendor_model,
    interaction_mode, supports_stream, price_mode, input_price, output_price,
    param_mapping, response_mapping,
    poll_path, poll_method, poll_interval, poll_max_attempts,
    poll_param_mapping, poll_response_mapping, callback_mapping,
    extra_config, timeout, status, created_at, updated_at)
SELECT
    capability_code, channel_id, 'custom', request_path, COALESCE(request_method, 'POST'),
    COALESCE(content_type, 'application/json'),
    COALESCE(auth_location, 'header'),
    COALESCE(auth_key, 'Authorization'),
    COALESCE(auth_value_prefix, 'Bearer '),
    model,
    result_mode,
    0,
    'request',
    price, 0,
    param_mapping, response_mapping,
    poll_path, COALESCE(poll_method, 'GET'), COALESCE(poll_interval, 5), COALESCE(poll_max_attempts, 60),
    poll_param_mapping, poll_response_mapping, callback_mapping,
    extra_config, 120, status, created_at, updated_at
FROM channel_capabilities
WHERE deleted_at IS NULL;

-- 7. 删除旧表（数据已备份）
DROP TABLE IF EXISTS chat_model_channels;
DROP TABLE IF EXISTS chat_models;
DROP TABLE IF EXISTS channel_capabilities;
DROP TABLE IF EXISTS capabilities;
