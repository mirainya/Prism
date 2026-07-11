-- P5-D 清理 chat 迁移遗留脏数据
-- 变更原因: 网关 v2 重构完成后,chat 全链路(含 playground)已切到 gateway pipeline(gw_* 表),
--          老 account_models / 软删渠道孤儿端点账号 成为脏数据。
-- 相关需求: 网关 v2 重构 P5 收尾(见 plans/dazzling-brewing-ocean.md)。
-- 影响范围: account_models(清空)、endpoints/channel_accounts(软删软删渠道下的孤儿行)。
--          endpoints/channel_accounts/channels 活跃行不动(image/video 仍用)。
--          models 表 chat 行暂缓(仍被 ModelAdmin CRUD / conversation.go reasoning 判断读),单独排期。
-- 前置条件: 部署 playground 切网关的二进制并验证通过(md5 4f249c3e),否则会误伤 playground 路由。
-- 回滚: 执行前已 mysqldump 备份三表到服务器 backup_p5d_20260708_185435.sql。
-- 执行时间: 2026-07-08 18:54 (生产 MySQL 5.7.44)

-- 1. 清空 account_models(chat 已切 gw_abilities,该表全部 8 行为 chat 遗留,无孤儿)
TRUNCATE TABLE account_models;

-- 2. 软删软删渠道下的孤儿 endpoints(父渠道 deleted_at 非空)。
--    用软删(非硬删)因 tasks.endpoint_id 外键引用历史 task,硬删破坏溯源;
--    GORM 查询自动过滤 deleted_at,image 路由 findEndpointsForCapability 不再误选。
--    id 1=禁用Gemini(小天); 19-22=软删渠道12(OOJJ)下重复的 gpt_image2/edit 残留。
UPDATE endpoints SET deleted_at = NOW(3)
WHERE id IN (1, 19, 20, 21, 22) AND deleted_at IS NULL;

-- 3. 软删软删渠道下的孤儿 channel_accounts。
--    id 1=小天; 6,7,8=Claude Proxy; 16=OOJJ,均为已软删渠道下的残留账号。
UPDATE channel_accounts SET deleted_at = NOW(3)
WHERE id IN (1, 6, 7, 8, 16) AND deleted_at IS NULL;
