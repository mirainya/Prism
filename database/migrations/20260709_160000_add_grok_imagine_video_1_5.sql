-- 变更说明：新增 grok-imagine-video-1.5 图生视频模型及端点
-- 需求：接入 xAI Grok Imagine Video 1.5（image-to-video）
-- 影响：models + endpoints 各1行，复用渠道9 account_id=20
-- ⚠️ 注意：grok-imagine-video-1.5 是图生视频，image_url 必填；与 1.0 文生视频不同

INSERT INTO models (name, type, provider, code, param_schema, created_at, updated_at)
VALUES (
  'Grok Imagine Video 1.5',
  'video',
  'grok',
  'grok-imagine-video-1.5',
  '{"image_url":{"name":"图片URL","type":"string","required":true},"prompt":{"name":"动作描述","type":"string","required":false},"duration":{"name":"时长(秒,1-15)","type":"number","required":false},"aspect_ratio":{"name":"宽高比","type":"enum","options":["16:9","9:16","1:1","4:3","3:4","3:2","2:3"],"required":false},"resolution":{"name":"分辨率","type":"enum","options":["480p","720p","1080p"],"required":false}}',
  NOW(), NOW()
);

INSERT INTO endpoints (model_code, channel_id, account_id, protocol, interaction_mode,
  request_path, vendor_model, param_mapping, response_mapping,
  poll_path, poll_method, poll_interval, poll_max_attempts, poll_response_mapping,
  price_mode, input_price, output_price, created_at, updated_at)
VALUES (
  'grok-imagine-video-1.5', 9, 20, 'openai', 'poll',
  '/v1/videos/generations', 'grok-imagine-video-1.5',
  '{"fixed_params":{"model":"grok-imagine-video-1.5"}}',
  '{"field_mapping":{"task_id":"request_id"}}',
  '/v1/videos/{task_id}', 'GET', 5, 360,
  '{"field_mapping":{"status":"status","progress":"progress","url":"video.url","error":"error.message"},"value_mapping":{"status":{"done":"success","pending":"processing","failed":"fail","error":"fail"}}}',
  'input', 0, 0, NOW(), NOW()
);
