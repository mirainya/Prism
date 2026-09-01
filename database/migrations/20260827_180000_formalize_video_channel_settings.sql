-- Reason: channel settings that control billing, routing, cancellation, and
-- transport defaults were stored in several unrelated JSON objects.
-- Requirement: make stable channel-level settings queryable and editable while
-- retaining provider-specific request/response mappings in extra_config.
-- Impact: adds metadata columns, backfills them, and removes the promoted JSON
-- keys. No task, credential, price value, or provider request mapping is changed. Safe to rerun only
-- through the migration runner, which records applied versions.

ALTER TABLE video_channels
    ADD COLUMN adapter_profile VARCHAR(32) NOT NULL DEFAULT '' COMMENT '适配器配置版本' AFTER adapter_type,
    ADD COLUMN request_timeout_seconds SMALLINT UNSIGNED NOT NULL DEFAULT 30 COMMENT '上游请求超时(秒)' AFTER priority,
    ADD COLUMN supports_first_frame TINYINT(1) NOT NULL DEFAULT 0 COMMENT '支持首帧' AFTER capabilities,
    ADD COLUMN supports_last_frame TINYINT(1) NOT NULL DEFAULT 0 COMMENT '支持尾帧' AFTER supports_first_frame,
    ADD COLUMN supports_audio TINYINT(1) NOT NULL DEFAULT 0 COMMENT '支持生成音频' AFTER supports_last_frame,
    ADD COLUMN supports_web_search TINYINT(1) NOT NULL DEFAULT 0 COMMENT '支持联网增强' AFTER supports_audio,
    ADD COLUMN cancel_mode VARCHAR(16) NOT NULL DEFAULT 'disabled' COMMENT '取消策略(disabled/local_only/provider)' AFTER supports_web_search,
    ADD COLUMN pricing_mode VARCHAR(24) NOT NULL DEFAULT 'fixed' COMMENT '计费模式(fixed/upstream_estimate)' AFTER pricing,
    ADD COLUMN fixed_price DECIMAL(10,4) NOT NULL DEFAULT 0 COMMENT '固定价格' AFTER pricing_mode,
    ADD COLUMN markup_ratio DECIMAL(4,2) NOT NULL DEFAULT 1 COMMENT '加价系数' AFTER fixed_price,
    ADD COLUMN result_storage_enabled TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否转存生成结果' AFTER asset_resolver;

UPDATE video_channels
SET adapter_profile = COALESCE(
        NULLIF(JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.adapter.profile')), ''),
        CASE WHEN adapter_type = 'generic' THEN 'json_task_v1' ELSE '' END
    ),
    request_timeout_seconds = COALESCE(
        CAST(NULLIF(JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.adapter.timeout_seconds')), '') AS UNSIGNED),
        CASE WHEN adapter_type = 'generic' THEN 30 ELSE 30 END
    ),
    supports_first_frame = CASE JSON_UNQUOTE(JSON_EXTRACT(capabilities, '$.first_frame'))
        WHEN 'true' THEN 1 ELSE 0 END,
    supports_last_frame = CASE JSON_UNQUOTE(JSON_EXTRACT(capabilities, '$.last_frame'))
        WHEN 'true' THEN 1 ELSE 0 END,
    supports_audio = CASE JSON_UNQUOTE(JSON_EXTRACT(capabilities, '$.audio'))
        WHEN 'true' THEN 1 ELSE 0 END,
    supports_web_search = CASE JSON_UNQUOTE(JSON_EXTRACT(capabilities, '$.web_search'))
        WHEN 'true' THEN 1 ELSE 0 END,
    cancel_mode = CASE
        WHEN adapter_type = 'generic' AND JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.adapter.cancel.enabled')) = 'true' THEN 'provider'
        WHEN adapter_type = 'generic' AND JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.adapter.local_cancel.enabled')) = 'true' THEN 'local_only'
        WHEN adapter_type = 'seedance' AND JSON_UNQUOTE(JSON_EXTRACT(capabilities, '$.cancel')) = 'true' THEN 'provider'
        ELSE 'disabled'
    END,
    pricing_mode = COALESCE(
        NULLIF(JSON_UNQUOTE(JSON_EXTRACT(pricing, '$.mode')), ''),
        'fixed'
    ),
    fixed_price = COALESCE(
        CAST(NULLIF(JSON_UNQUOTE(JSON_EXTRACT(pricing, '$.fixed_price')), '') AS DECIMAL(10,4)),
        0
    ),
    markup_ratio = COALESCE(
        CAST(NULLIF(JSON_UNQUOTE(JSON_EXTRACT(pricing, '$.markup_ratio')), '') AS DECIMAL(4,2)),
        1
    ),
    result_storage_enabled = CASE JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.result_storage.enabled'))
        WHEN 'true' THEN 1 ELSE 0 END;

UPDATE video_channels
SET capabilities = JSON_REMOVE(
        COALESCE(capabilities, JSON_OBJECT()),
        '$.first_frame',
        '$.last_frame',
        '$.audio',
        '$.web_search',
        '$.cancel'
    ),
    pricing = JSON_REMOVE(
        COALESCE(pricing, JSON_OBJECT()),
        '$.mode',
        '$.fixed_price',
        '$.markup_ratio'
    ),
    extra_config = JSON_REMOVE(
        COALESCE(extra_config, JSON_OBJECT()),
        '$.adapter.profile',
        '$.adapter.timeout_seconds',
        '$.adapter.cancel.enabled',
        '$.adapter.local_cancel.enabled',
        '$.result_storage.enabled'
    );
