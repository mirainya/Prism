-- 变更原因：同一上游端点可能同时支持图片生成与图片编辑（例如豆包），不能用两行重复端点表示。
-- 相关需求：端点声明支持的公共操作，保留 route_operation 兼容旧数据。
-- 影响范围：endpoints 增加 JSON 字段，并为已有单操作端点回填单元素列表。

SET @prism_schema = DATABASE();

SET @supported_operations_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'endpoints' AND column_name = 'supported_operations'
    ),
    'SELECT 1',
    'ALTER TABLE endpoints ADD COLUMN supported_operations JSON NULL COMMENT ''Supported public operations'' AFTER route_operation'
);
PREPARE supported_operations_stmt FROM @supported_operations_sql;
EXECUTE supported_operations_stmt;
DEALLOCATE PREPARE supported_operations_stmt;

UPDATE endpoints
SET supported_operations = JSON_ARRAY(route_operation)
WHERE route_operation <> ''
  AND (supported_operations IS NULL OR JSON_LENGTH(supported_operations) = 0);
