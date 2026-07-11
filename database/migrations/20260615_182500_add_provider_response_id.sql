-- 为 conversations 表添加 provider_response_id 字段
-- 用途：存储上游有状态对话 ID（如火山方舟 Responses API 的 response_id）
--   实现「B为主 + A兜底」上下文策略：
--   有有效 response_id 时只发新消息（省 token），失效时用本地全量历史(A)重建。
-- 影响范围：仅 conversations 表新增一列，无数据变更
ALTER TABLE conversations ADD COLUMN provider_response_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '上游有状态对话ID(如火山response_id)' AFTER last_status;
