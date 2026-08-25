-- 变更原因：Duomi gpt-image-2 已配置图生图输入，但端点操作列表只包含文生图。
-- 相关需求：允许同一 Duomi 端点同时处理 images.generate 与 images.edit。
-- 影响范围：Duomi 渠道的 duomiapi-gpt-image2 端点。

UPDATE `endpoints` AS e
JOIN `channels` AS ch
  ON ch.`id` = e.`channel_id`
 AND ch.`deleted_at` IS NULL
SET
  e.`supported_operations` = JSON_ARRAY('images.generate', 'images.edit'),
  e.`updated_at` = CURRENT_TIMESTAMP(3)
WHERE ch.`type` = 'duomi'
  AND e.`vendor_model` = 'duomiapi-gpt-image2'
  AND e.`deleted_at` IS NULL
  AND (
    e.`supported_operations` IS NULL
    OR JSON_CONTAINS(e.`supported_operations`, JSON_QUOTE('images.edit')) = 0
  );
