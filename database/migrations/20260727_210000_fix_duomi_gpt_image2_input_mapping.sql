-- 变更原因: Duomi gpt-image-2 使用 image 接收参考图，并使用 size 接收宽高比。
-- 相关需求: Prism 继续接收统一字段 image_urls/aspect_ratio，并在端点层转换为 Duomi 字段。
-- 影响范围: 仅更新 Duomi gpt-image-2 异步端点的参数映射与 URL 图生图字段。

UPDATE `endpoints` AS e
JOIN `channels` AS ch
  ON ch.`id` = e.`channel_id`
 AND ch.`deleted_at` IS NULL
SET
  e.`param_mapping` = JSON_SET(
    COALESCE(e.`param_mapping`, JSON_OBJECT()),
    '$.field_mapping.image_urls', 'image',
    '$.field_mapping.aspect_ratio', 'size'
  ),
  e.`extra_config` = JSON_SET(
    COALESCE(e.`extra_config`, JSON_OBJECT()),
    '$.image_edit.enabled', TRUE,
    '$.image_edit.input_mode', 'url',
    '$.image_edit.file_field', 'image'
  ),
  e.`updated_at` = CURRENT_TIMESTAMP(3)
WHERE ch.`type` = 'duomi'
  AND e.`vendor_model` = 'duomiapi-gpt-image2'
  AND e.`request_path` = '/v1/images/generations?async=true'
  AND e.`deleted_at` IS NULL;
