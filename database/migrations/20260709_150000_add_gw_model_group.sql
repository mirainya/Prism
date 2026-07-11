-- 迁移:gw_model_meta 加手动分组字段 group_name(P6 对话模型分组)
-- 变更原因:对话模型页各家模型(gpt/claude/gemini/doubao…)全平铺混一起,无分组无排序。
--          分组策略=源渠道为默认 + 手动组覆盖:填了 group_name 用手动组名,
--          没填按「源渠道」(该模型最高优先级 ability 所属渠道名,ListModels 相关子查询取)分组。
-- 影响范围:仅 gw_model_meta 表加一列;AutoMigrate 亦会自动加,此文件仅作单一数据源留档。
-- 拖拽排序复用已有 gw_model_meta.sort 字段(ReorderModels 只更 sort,不整行覆盖)。

ALTER TABLE gw_model_meta ADD COLUMN group_name VARCHAR(80) NOT NULL DEFAULT '';
