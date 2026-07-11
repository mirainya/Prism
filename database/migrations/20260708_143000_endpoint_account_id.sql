-- 变更原因: 能力端点(image/video)绑定到具体 key。此前 endpoints 仅记 channel_id,
--          导致展开渠道时每个 key 都显示同批端点、看不出归属。新增 endpoints.account_id。
-- 相关需求: 渠道管理能力端点 per-key 归属
-- 影响范围: endpoints 表 image/video 端点(chat 已迁 account_models,不涉及)
-- 归属规则: ①多账号渠道按 supported_models 白名单精确匹配; ②其余绑到渠道唯一账号。
-- 注意: account_id 列由 GORM AutoMigrate 自动创建,本文件仅做数据回填。

-- 仅处理能力端点(chat 走 account_models,不参与)
-- 步骤1: 多账号渠道按白名单精确归属(如 ch#9 gpt_image2 -> acc#19)
UPDATE endpoints e
JOIN channel_accounts a
  ON a.channel_id = e.channel_id AND a.deleted_at IS NULL
 AND JSON_CONTAINS(a.supported_models, JSON_QUOTE(e.model_code))
SET e.account_id = a.id
WHERE e.account_id = 0 AND e.deleted_at IS NULL
  AND e.model_code IN (SELECT code FROM models WHERE type IN ('image','video'));

-- 步骤2: 仍未归属的绑到渠道唯一账号(单账号渠道无歧义)
UPDATE endpoints e
JOIN (SELECT channel_id, MIN(id) AS acc_id, COUNT(*) AS cnt
      FROM channel_accounts WHERE deleted_at IS NULL
      GROUP BY channel_id HAVING cnt = 1) s ON s.channel_id = e.channel_id
SET e.account_id = s.acc_id
WHERE e.account_id = 0 AND e.deleted_at IS NULL
  AND e.model_code IN (SELECT code FROM models WHERE type IN ('image','video'));
