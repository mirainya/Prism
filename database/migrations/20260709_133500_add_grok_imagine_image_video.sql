-- =====================================================================
-- 迁移: 接入 grok(xAI Imagine) 图像 + 视频能力
-- 时间: 2026-07-09 13:35
-- 需求: 把 grok 渠道的 image / video 接入 Prism 能力体系
-- 结论: 纯配置驱动(BaseProvider),零后端代码。复用已存在渠道 id=9(sub2api.mirainya.com)
--
-- 变更内容(全部新增,零修改零删除):
--   1) models        新增 2 行: grok-imagine-image(image) / grok-imagine-video(video)
--   2) channel_accounts 新增 1 行: 渠道9 下的 grok-imagine key 账号
--   3) endpoints      新增 2 行: 图像 sync 端点 / 视频 poll 端点(绑定上面账号)
--
-- 关键设计(实测验证):
--   - model_code = 上游 vendor 名(带横线),而非下划线。
--     原因: 视频对外 handler(generation.go)会把请求 model 塞进 params,
--     而 mapParams 的 fixed_params 是「不存在才填」语义 → 若 model_code≠上游名,
--     上游会收到 Prism 的 code 报 404。沿用现有 grok-video-1.5-preview 惯例。
--   - 图像: 同步返 URL, 响应 data[0].url / data[].url
--   - 视频: 异步 POST 返 {request_id} → 轮询 GET /v1/videos/{request_id}
--     终态 {"status":"done","video":{"url":"...mp4"},"progress":N}
--     状态映射 done→success / pending→processing / failed|error→fail
--
-- 影响范围: 仅新增 grok image/video 两个能力, 不动任何现有渠道/端点/模型
-- 实测: 图像 7s 出图; 视频异步链路 pending→processing→success 出 mp4, 全链路通过
--
-- ⚠️ 真实 api_key 已在生产库注入(会话内),此处用占位符,勿提交真实密钥
-- =====================================================================

-- 幂等清理(重跑用): 先删端点再删模型(外键 fk_endpoints_model),账号按 name 删
-- DELETE FROM endpoints WHERE model_code IN ('grok-imagine-image','grok-imagine-video');
-- DELETE FROM models    WHERE code       IN ('grok-imagine-image','grok-imagine-video');
-- DELETE FROM channel_accounts WHERE channel_id=9 AND name='grok-imagine(e8835)';

-- param_schema 设计要点(实测):
--   - key 直接用上游真实字段名(prompt/n/duration/aspect_ratio),playground/capability 路径 params 原样透传,
--     无需 field_mapping(仅 fixed_params 补 model)。
--   - duration 用 type:number(前端 normalizeCapabilityValue 只有 number 才转数字,enum 会传字符串→上游要数字会400)。
--   - aspect_ratio 用 enum(传字符串,上游接受);size 上游不支持(400),故不放。
--   - duration 实测合法 1~8,20/30 非法。

-- ① 图像模型
INSERT INTO models (code, name, type, provider, features, param_schema, sort, status, created_at, updated_at)
VALUES ('grok-imagine-image', 'Grok Imagine 绘图', 'image', 'grok',
  '["text-to-image"]',
  '{"prompt":{"name":"提示词","type":"string","required":true},"aspect_ratio":{"name":"宽高比","type":"enum","options":["16:9","9:16","1:1","4:3","3:4"],"required":false},"n":{"name":"生成数量","type":"number","required":false}}',
  0, 1, NOW(), NOW());

-- ② 视频模型
INSERT INTO models (code, name, type, provider, features, param_schema, sort, status, created_at, updated_at)
VALUES ('grok-imagine-video', 'Grok Imagine 视频', 'video', 'grok',
  '["text-to-video"]',
  '{"prompt":{"name":"提示词","type":"string","required":true},"duration":{"name":"时长(秒,1-8)","type":"number","required":false},"aspect_ratio":{"name":"宽高比","type":"enum","options":["16:9","9:16","1:1","4:3","3:4"],"required":false}}',
  0, 1, NOW(), NOW());

-- ③ 新账号(渠道9=sub2api.mirainya.com), api_key 用占位符, 生产已注入真实值
INSERT INTO channel_accounts (channel_id, name, api_key, weight, status, max_tasks, current_tasks, supported_models, created_at, updated_at)
VALUES (9, 'grok-imagine(e8835)', 'sk-REPLACE_WITH_REAL_KEY', 10, 1, 0, 0,
  '["grok-imagine-image","grok-imagine-video"]', NOW(), NOW());
-- 记新账号自增 id 为 @ACC (生产实际 = 20)
SET @ACC = (SELECT id FROM channel_accounts WHERE channel_id=9 AND name='grok-imagine(e8835)' ORDER BY id DESC LIMIT 1);

-- ④ 图像端点(sync)
INSERT INTO endpoints (model_code, channel_id, account_id, protocol, request_path, request_method, content_type,
  auth_location, auth_key, auth_value_prefix, vendor_model, interaction_mode, supports_stream, default_stream,
  price_mode, input_price, param_mapping, response_mapping, poll_interval, poll_max_attempts, timeout, priority, status, created_at, updated_at)
VALUES ('grok-imagine-image', 9, @ACC, 'openai', '/v1/images/generations', 'POST', 'application/json',
  'header', 'Authorization', 'Bearer ', 'grok-imagine-image', 'sync', 0, 0,
  'request', 0,
  '{"fixed_params":{"model":"grok-imagine-image"}}',
  '{"field_mapping":{"url":"data[0].url","urls":"data[].url","b64_json":"data[0].b64_json"}}',
  5, 60, 120, 0, 1, NOW(), NOW());

-- ⑤ 视频端点(poll)
INSERT INTO endpoints (model_code, channel_id, account_id, protocol, request_path, request_method, content_type,
  auth_location, auth_key, auth_value_prefix, vendor_model, interaction_mode, supports_stream, default_stream,
  price_mode, input_price, param_mapping, response_mapping, poll_path, poll_method, poll_interval, poll_max_attempts,
  poll_response_mapping, timeout, priority, status, created_at, updated_at)
VALUES ('grok-imagine-video', 9, @ACC, 'openai', '/v1/videos/generations', 'POST', 'application/json',
  'header', 'Authorization', 'Bearer ', 'grok-imagine-video', 'poll', 0, 0,
  'request', 0,
  '{"fixed_params":{"model":"grok-imagine-video"}}',
  '{"field_mapping":{"task_id":"request_id"}}',
  '/v1/videos/{task_id}', 'GET', 5, 360,
  '{"field_mapping":{"status":"status","progress":"progress","url":"video.url","error":"error.message"},"value_mapping":{"status":{"done":"success","pending":"processing","failed":"fail","error":"fail"}}}',
  300, 0, 1, NOW(), NOW());
