-- 变更原因: 图片/视频能力端点与渠道 Key 需要多对多绑定，避免无能力的新 Key 被渠道级路由选中。
-- 相关需求: 一个 Key 可支持多个能力，一个能力也可由多个 Key 执行。
-- 影响范围: 新增 endpoint_accounts；复制 endpoints.account_id 中已有的单 Key 绑定。

CREATE TABLE IF NOT EXISTS `endpoint_accounts` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `endpoint_id` bigint unsigned NOT NULL COMMENT '端点ID',
  `account_id` bigint unsigned NOT NULL COMMENT '渠道账号(key)ID',
  `status` tinyint NOT NULL DEFAULT 1 COMMENT '状态(1启用/0禁用)',
  `priority` bigint NOT NULL DEFAULT 0 COMMENT '优先级(降序)',
  `weight` bigint NOT NULL DEFAULT 10 COMMENT '同优先级流量权重',
  `created_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_endpoint_account` (`endpoint_id`, `account_id`),
  KEY `idx_endpoint_accounts_account_id` (`account_id`),
  KEY `idx_endpoint_accounts_route` (`endpoint_id`, `status`, `priority`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO `endpoint_accounts`
  (`endpoint_id`, `account_id`, `status`, `priority`, `weight`, `created_at`, `updated_at`)
SELECT
  e.`id`, e.`account_id`, 1, 0, 10, CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3)
FROM `endpoints` e
JOIN `channel_accounts` a
  ON a.`id` = e.`account_id`
 AND a.`channel_id` = e.`channel_id`
 AND a.`deleted_at` IS NULL
WHERE e.`account_id` <> 0
  AND e.`deleted_at` IS NULL
ON DUPLICATE KEY UPDATE
  `updated_at` = VALUES(`updated_at`);
