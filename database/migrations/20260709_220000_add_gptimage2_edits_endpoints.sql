-- 为 gpt_image2 添加图生图端点 (POST /v1/images/edits, multipart/form-data)
-- 背景：/v1/images/edits 接收参考图 + prompt，用 multipart/form-data 上传文件
-- 后端 file_resolver.go 负责在 doSubmit 前将图片 URL 下载并转换为 @base64:filename:data
-- 影响：endpoints id=36（gpt-image-2-edit）、id=37（gpt-image-2-[1.5k]-edit）
--       删除旧废弃端点 id=21/22（model_code='gpt_image2_edit'，旧版本遗留）

-- 清理旧废弃图生图端点（application/json 版本，从未正式使用）
DELETE FROM endpoints WHERE id IN (21, 22);

-- gpt-image-2 图生图端点
INSERT INTO endpoints (
  model_code, channel_id, account_id, protocol, interaction_mode,
  request_path, vendor_model, content_type,
  param_mapping, response_mapping, param_schema, extra_config,
  timeout, priority, created_at, updated_at
) VALUES (
  'gpt_image2', 9, 19, 'custom', 'stream',
  '/v1/images/edits', 'gpt-image-2', 'multipart/form-data',
  '{"fixed_params":{"model":"gpt-image-2"}}',
  '{"field_mapping":{"url":"data[0].url","urls":"data[].url","b64_json":"data.0.b64_json","revised_prompt":"data[0].revised_prompt"},"success_condition":{"field":"data","operator":"exists"}}',
  '{"image":{"name":"参考图片","type":"string","required":true,"description":"上传参考图进行编辑"},"prompt":{"name":"编辑描述","type":"string","required":true},"size":{"name":"图片尺寸","type":"enum","default":"1024x1024","options":["512x512","1024x1024","2048x1536"]},"response_format":{"name":"响应格式","type":"enum","default":"b64_json","options":["b64_json","url"]}}',
  '{"transfer_enabled":true}',
  300, 20, NOW(), NOW()
);

-- gpt-image-2-[1.5k] 图生图端点
INSERT INTO endpoints (
  model_code, channel_id, account_id, protocol, interaction_mode,
  request_path, vendor_model, content_type,
  param_mapping, response_mapping, param_schema, extra_config,
  timeout, priority, created_at, updated_at
) VALUES (
  'gpt_image2', 9, 21, 'custom', 'stream',
  '/v1/images/edits', 'gpt-image-2-[1.5k]', 'multipart/form-data',
  '{"fixed_params":{"model":"gpt-image-2-[1.5k]"}}',
  '{"field_mapping":{"url":"data[0].url","urls":"data[].url","b64_json":"data.0.b64_json","revised_prompt":"data[0].revised_prompt"},"success_condition":{"field":"data","operator":"exists"}}',
  '{"image":{"name":"参考图片","type":"string","required":true,"description":"上传参考图进行编辑"},"prompt":{"name":"编辑描述","type":"string","required":true},"size":{"name":"图片尺寸","type":"enum","default":"1024x1024","options":["512x512","1024x1024","2048x1536"]},"response_format":{"name":"响应格式","type":"enum","default":"b64_json","options":["b64_json","url"]}}',
  '{"transfer_enabled":true}',
  300, 20, NOW(), NOW()
);

-- 给文生图端点（id=13/35）添加可选 image 字段，触发前端上传按钮
-- 用户上传参考图后由后端 sortEndpointsByImageParam 自动路由到 multipart 图生图端点
-- （图生图端点已从渠道选择器隐藏，前端始终展示文生图端点的 schema）
UPDATE endpoints SET param_schema = JSON_SET(param_schema, '$.image',
  JSON_OBJECT('name', '参考图片(可选)', 'type', 'string', 'required', false,
    'description', '上传参考图则自动切换图生图模式，留空则纯文本生图'))
WHERE id IN (13, 35);
