-- 视频生成引擎：独立渠道/密钥/任务/素材表
-- 完全独立于现有 endpoints/channel_accounts/tasks 体系

CREATE TABLE IF NOT EXISTS video_channels (
    id              BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    name            VARCHAR(64) NOT NULL COMMENT '渠道名称',
    adapter_type    VARCHAR(32) NOT NULL COMMENT '适配器类型(seedance/generic)',
    base_url        VARCHAR(256) NOT NULL COMMENT '上游地址',
    status          VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT '状态(active/disabled)',
    priority        INT NOT NULL DEFAULT 0 COMMENT '选路优先级(降序)',
    models          JSON NOT NULL COMMENT '支持模型列表',
    capabilities    JSON DEFAULT NULL COMMENT '能力声明',
    pricing         JSON DEFAULT NULL COMMENT '计费配置',
    asset_resolver  VARCHAR(32) NOT NULL DEFAULT 'direct_url' COMMENT '素材解析器(sub2api/direct_url/tos)',
    passthrough     JSON DEFAULT NULL COMMENT '透传服务配置',
    extra_config    JSON DEFAULT NULL COMMENT '适配器附加配置',
    created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='视频渠道';

CREATE TABLE IF NOT EXISTS video_channel_keys (
    id                   BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    channel_id           BIGINT UNSIGNED NOT NULL COMMENT '所属渠道ID',
    api_key              VARCHAR(256) NOT NULL COMMENT 'API密钥',
    label                VARCHAR(64) DEFAULT NULL COMMENT '人类可读标签',
    weight               INT NOT NULL DEFAULT 1 COMMENT '同渠道内权重',
    max_concurrency      INT NOT NULL DEFAULT 0 COMMENT '最大并发(0=不限)',
    status               VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT '状态(active/disabled/exhausted)',
    current_concurrency  INT NOT NULL DEFAULT 0 COMMENT '当前并发',
    total_calls          BIGINT NOT NULL DEFAULT 0 COMMENT '总调用次数',
    consecutive_failures INT NOT NULL DEFAULT 0 COMMENT '连续失败次数',
    last_used_at         DATETIME(3) DEFAULT NULL COMMENT '最后使用时间',
    created_at           DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    INDEX idx_channel_status (channel_id, status),
    FOREIGN KEY (channel_id) REFERENCES video_channels(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='视频渠道密钥';

CREATE TABLE IF NOT EXISTS video_tasks (
    id                VARCHAR(32) NOT NULL PRIMARY KEY COMMENT '任务ID',
    token_id          BIGINT UNSIGNED NOT NULL COMMENT '令牌ID',
    model             VARCHAR(64) NOT NULL COMMENT '模型标识',
    status            VARCHAR(16) NOT NULL COMMENT '任务状态',
    progress          TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '进度(0-100)',
    task_mode         VARCHAR(16) NOT NULL COMMENT '任务模式(text/references)',
    prompt            TEXT DEFAULT NULL COMMENT '提示词',
    resolution        VARCHAR(16) DEFAULT NULL COMMENT '分辨率',
    ratio             VARCHAR(16) DEFAULT NULL COMMENT '比例',
    duration          SMALLINT DEFAULT NULL COMMENT '时长(秒)',
    generate_audio    TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否生成音频',
    content_json      JSON DEFAULT NULL COMMENT '内容项',
    params_json       JSON DEFAULT NULL COMMENT '附加参数',
    channel_id        BIGINT UNSIGNED DEFAULT NULL COMMENT '渠道ID',
    key_id            BIGINT UNSIGNED DEFAULT NULL COMMENT '密钥ID',
    adapter_type      VARCHAR(32) NOT NULL COMMENT '适配器类型',
    provider_task_id  VARCHAR(128) DEFAULT NULL COMMENT '上游任务ID',
    provider_response JSON DEFAULT NULL COMMENT '上游原始响应',
    estimated_cost    DECIMAL(10,4) DEFAULT NULL COMMENT '预估费用',
    markup_ratio      DECIMAL(4,2) DEFAULT NULL COMMENT '加价系数',
    final_cost        DECIMAL(10,4) DEFAULT NULL COMMENT '最终费用',
    billing_status    VARCHAR(16) DEFAULT NULL COMMENT '计费状态(pending/charged/refunded/partial_refund)',
    result_json       JSON DEFAULT NULL COMMENT '生成结果',
    error_message     TEXT DEFAULT NULL COMMENT '错误信息',
    worker_lease      VARCHAR(64) DEFAULT NULL COMMENT 'Worker租约',
    poll_count        INT NOT NULL DEFAULT 0 COMMENT '轮询次数',
    callback_url      VARCHAR(512) DEFAULT NULL COMMENT '回调地址',
    created_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    submitted_at      DATETIME(3) DEFAULT NULL COMMENT '提交时间',
    completed_at      DATETIME(3) DEFAULT NULL COMMENT '完成时间',
    INDEX idx_token_status (token_id, status, created_at DESC),
    INDEX idx_status_created (status, created_at),
    INDEX idx_channel_key (channel_id, key_id)
    -- 刻意不加 FK：渠道/Key 删除后历史任务仍需保留
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='视频生成任务';

CREATE TABLE IF NOT EXISTS video_assets (
    id               VARCHAR(32) NOT NULL PRIMARY KEY COMMENT '素材ID',
    token_id         BIGINT UNSIGNED NOT NULL COMMENT '令牌ID',
    sha256           CHAR(64) NOT NULL COMMENT '文件SHA-256',
    size_bytes       BIGINT NOT NULL COMMENT '文件大小(字节)',
    kind             VARCHAR(16) NOT NULL COMMENT '类型(image/video/audio)',
    content_type     VARCHAR(64) NOT NULL COMMENT 'MIME类型',
    duration_seconds DECIMAL(6,2) DEFAULT NULL COMMENT '时长(秒)',
    status           VARCHAR(16) NOT NULL COMMENT '状态(uploading/ready/expired)',
    storage_path     VARCHAR(256) DEFAULT NULL COMMENT '存储路径',
    upstream_refs    JSON DEFAULT NULL COMMENT '上游引用缓存',
    expires_at       DATETIME(3) NOT NULL COMMENT '过期时间',
    created_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    INDEX idx_token_sha256 (token_id, sha256, status),
    INDEX idx_expires (status, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='视频素材';
