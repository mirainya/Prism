-- 新增 gpt-image-2-[1.5k] 生图账号及流式端点
-- 背景：sub2api.mirainya.com 新增一个仅支持 gpt-image-2-[1.5k] 定价档的生图 Key
--       该模型不接受 gpt-image-2（无定价后缀），需独立端点配置 vendor_model
-- 影响：channel_accounts 新增 id=21，endpoints 新增 id=35，原有配置不变

-- Step 1: 新增账号（channel_id=9, sub2api.mirainya.com）
INSERT INTO channel_accounts (channel_id, name, api_key, weight, status, supported_models, created_at, updated_at)
VALUES (9, 'gptimage2-1.5k',
  'REPLACE_WITH_GPTIMAGE2_API_KEY',
  10, 1, '["gpt_image2"]', NOW(), NOW());

-- Step 2: 新增流式端点，复制端点 id=13 的 schema/响应映射/extra_config
--         仅修改 vendor_model 和 account_id
INSERT INTO endpoints (
  model_code, channel_id, account_id, protocol, interaction_mode,
  request_path, vendor_model, param_mapping,
  response_mapping, param_schema, extra_config, timeout, priority,
  created_at, updated_at
)
SELECT
  'gpt_image2', 9, LAST_INSERT_ID(), 'custom', 'stream',
  '/v1/images/generations', 'gpt-image-2-[1.5k]',
  '{"fixed_params":{"model":"gpt-image-2-[1.5k]"}}',
  response_mapping, param_schema, extra_config,
  timeout, 20, NOW(), NOW()
FROM endpoints WHERE id = 13;
