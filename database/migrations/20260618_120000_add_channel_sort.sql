-- 变更原因: 渠道管理页面无法调整顺序，对齐模型管理(models.sort)的排序能力
-- 相关需求: 渠道列表支持拖拽排序
-- 影响范围: channels 表新增 sort 字段；列表查询由 id ASC 改为 sort DESC, id ASC
-- 回滚: ALTER TABLE channels DROP COLUMN sort;

ALTER TABLE channels ADD COLUMN sort INT NOT NULL DEFAULT 0 COMMENT '排序(降序)';
