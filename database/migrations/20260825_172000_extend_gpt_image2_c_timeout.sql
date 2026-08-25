-- 变更原因: gpt-image-2-c 的流式能力端点把 120 秒作为整次请求硬超时。
-- 相关需求: 允许上游排队和生成耗时超过两分钟，同时保留五分钟最大等待限制。
-- 影响范围: 仅更新 gpt-image-2-c 的生成与编辑端点。

UPDATE `endpoints`
SET
  `timeout` = 300,
  `updated_at` = CURRENT_TIMESTAMP(3)
WHERE `model_code` = 'gpt-image-2-c'
  AND `route_operation` IN ('images.generate', 'images.edit')
  AND `supports_stream` = 1
  AND `timeout` < 300
  AND `deleted_at` IS NULL;
