-- 迁移:清理 models 表 chat 遗留行(P5 未尽①收尾)
-- 变更原因:chat 全链路已切网关 v2(gw_abilities 路由 + gw_model_meta 元数据),
--          老 models 表的 chat 行元数据已迁移完毕,不再有任何路由/消费依赖。
-- 影响范围:仅 type='chat' 行(14 行);image/video 行(9 行)不受影响,仍活跃使用 endpoints。
-- 前置依赖(本次已一并完成):
--   1. conversation.go 的 anthropic-reasoning 判断改从 gw_channels.protocol 取,不再读 models.provider
--   2. 删除 chat_model.go / chat_model_channel.go admin CRUD + 路由 + console ListChatModelChannelsForToken
--   3. 删除前端 chatModelApi.ts / ChatModelChannelModal.tsx,ChatLogs 模型筛选改指 gw 模型
-- 采用软删(与 20260708_185554 P5-D 孤儿行处理一致):models 带 deleted_at,GORM 自动过滤,
--          效果等同删除但保留溯源可回滚。备份见服务器 backup_models_chat_20260709.sql(如需)。

UPDATE models SET deleted_at = NOW(3)
WHERE type = 'chat' AND deleted_at IS NULL;
-- 实测影响 14 行:deepseek-v4-flash / doubao-seed-2-0-lite/pro / doubao-seed-2-1-pro /
--   gemini-3-pro-preview / gpt-5.4 / gpt-5.5 / grok-4.20-*(×5) / grok-4.3 / llama3.1-8B
