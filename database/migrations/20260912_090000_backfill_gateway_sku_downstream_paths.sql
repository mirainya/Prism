-- 变更原因：为 gw_sku_downstream_paths 创建前导入的 SKU 回填下游入口，
-- 使运行时按请求路径选路时不丢失既有目录内容。
-- 影响范围：所有已有 release 的 gw_skus；只从同一 model operation 的
-- operation route 复制 route_template，不创建新的 SKU 或操作契约。
-- 部署约束：必须在 20260910_120000_gateway_pricing_expressions.sql 创建
-- gw_sku_downstream_paths 后执行；INSERT IGNORE 允许脏迁移和重复执行。

INSERT IGNORE INTO `gw_sku_downstream_paths` (`release_id`, `sku_id`, `path`, `created_at`)
SELECT sku.`release_id`, sku.`id`, operation_route.`route_template`, UTC_TIMESTAMP(3)
FROM `gw_skus` sku
JOIN `gw_model_operations` model_operation
  ON model_operation.`release_id` = sku.`release_id`
 AND model_operation.`id` = sku.`model_operation_id`
JOIN `gw_operation_routes` operation_route
  ON operation_route.`operation_contract_id` = model_operation.`operation_contract_id`
WHERE operation_route.`route_template` LIKE '/%';
