-- 迁移:gw_model_meta 加 config_version,支撑 v5 运营控制台的 A 类原地编辑
--
-- 变更原因:v5 spec §5.2.3 要求展示名/分组/排序的 PATCH 端点携带
-- expected_version 做乐观锁。gw_model_meta 此前只有 updated_at,毫秒时间戳做
-- 并发令牌时同一毫秒内的两次改动会互相覆盖,等于没锁。加显式版本列后语义与
-- gw_credential_pools、gw_catalog_releases 等其余 A 类表一致。
--
-- 影响范围:仅 gw_model_meta 加一列。该表是元数据面,永不参与选路
-- (internal/gateway/routing 不读它),既有行取默认值 1,读路径无需改动。
-- AutoMigrate 亦会自动加,此文件作单一数据源留档。
--
-- MySQL DDL 隐式提交,单列单独守卫,脏迁移可从任一 ALTER 之后续跑。

SET @prism_schema = DATABASE();

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_model_meta` ADD COLUMN `config_version` bigint unsigned NOT NULL DEFAULT 1 AFTER `sort`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_model_meta'
      AND column_name = 'config_version'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
