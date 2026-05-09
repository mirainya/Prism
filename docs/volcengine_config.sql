-- ============================================
-- 豆包（火山引擎 Ark）渠道配置
-- ============================================

-- 1. 创建渠道
INSERT INTO `channels` (`type`, `name`, `base_url`, `config`, `status`, `created_at`, `updated_at`)
VALUES ('volcengine', '火山引擎-豆包', 'https://ark.cn-beijing.volces.com', '{}', 1, NOW(), NOW());

-- 2. 创建渠道账号（请替换 YOUR_ARK_API_KEY 为实际密钥）
INSERT INTO `channel_accounts` (`channel_id`, `name`, `api_key`, `config`, `weight`, `status`, `created_at`, `updated_at`)
VALUES (
    (SELECT `id` FROM `channels` WHERE `type` = 'volcengine' LIMIT 1),
    '豆包默认账号',
    'YOUR_ARK_API_KEY',
    '{}',
    10, 1, NOW(), NOW()
);

-- 3. 创建模型
INSERT INTO `chat_models` (`code`, `name`, `provider`, `description`, `status`, `created_at`, `updated_at`)
VALUES ('doubao-seed-2-0-pro', '豆包 Seed 2.0 Pro', 'volcengine', '豆包深度思考模型，支持 reasoning 推理', 1, NOW(), NOW());

-- 4. 创建模型渠道映射
--    关键配置：
--    - request_path: /api/v3/chat/completions（豆包使用 v3 路径）
--    - vendor_model: doubao-seed-2-0-pro-260215（供应商侧实际模型名）
--    - extra_config.extra_body: 注入 max_output_tokens 和 reasoning 等特有字段
INSERT INTO `chat_model_channels` (
    `model_code`, `channel_id`, `vendor_model`, `priority`,
    `price_mode`, `input_price`, `output_price`,
    `request_path`, `timeout`, `extra_headers`, `extra_config`,
    `status`, `created_at`, `updated_at`
)
VALUES (
    'doubao-seed-2-0-pro',
    (SELECT `id` FROM `channels` WHERE `type` = 'volcengine' LIMIT 1),
    'doubao-seed-2-0-pro-260215',
    0,
    'token', 4.00000000, 16.00000000,
    '/api/v3/chat/completions',
    300,
    '{}',
    '{
        "extra_body": {
            "max_output_tokens": 131072,
            "reasoning": {
                "effort": "high"
            }
        }
    }',
    1, NOW(), NOW()
);
