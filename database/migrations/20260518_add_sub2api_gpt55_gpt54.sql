-- 为 sub2api.mirainya.com 添加 gpt-5.5/gpt-5.4 + claude-opus-4-6/claude-sonnet-4-6
-- 注意: 不同 key 支持不同模型，必须分渠道

-- 1. GPT 模型使用渠道 9 (sub2API-MiraiNya, base_url: https://sub2api.mirainya.com)
INSERT INTO channel_accounts (channel_id, name, api_key, config, weight, status, max_tasks, current_tasks, created_at, updated_at)
VALUES (9, 'gpt5-key', 'REPLACE_WITH_GPT_API_KEY', '{}', 10, 1, 0, 0, NOW(3), NOW(3));

INSERT INTO models (code, name, type, provider, description, status, created_at, updated_at)
VALUES
  ('gpt-5.5', 'GPT-5.5', 'chat', 'openai', '', 1, NOW(3), NOW(3)),
  ('gpt-5.4', 'GPT-5.4', 'chat', 'openai', '', 1, NOW(3), NOW(3));

INSERT INTO endpoints (model_code, channel_id, protocol, request_path, request_method, content_type, vendor_model, interaction_mode, supports_stream, default_stream, status, timeout, created_at, updated_at)
VALUES
  ('gpt-5.5', 9, 'openai', '/v1/chat/completions', 'POST', 'application/json', 'gpt-5.5', 'stream', 1, 1, 1, 120, NOW(3), NOW(3)),
  ('gpt-5.4', 9, 'openai', '/v1/chat/completions', 'POST', 'application/json', 'gpt-5.4', 'stream', 1, 1, 1, 120, NOW(3), NOW(3));

-- 2. Claude 模型使用独立渠道 (sub2api-claude)，避免 key 混用
INSERT INTO channels (type, name, base_url, config, status, created_at, updated_at)
VALUES ('sub2api-claude', 'MiraiNya-Claude', 'https://sub2api.mirainya.com', '{}', 1, NOW(3), NOW(3));

SET @claude_ch = LAST_INSERT_ID();

INSERT INTO channel_accounts (channel_id, name, api_key, config, weight, status, max_tasks, current_tasks, created_at, updated_at)
VALUES (@claude_ch, 'claude-key', 'REPLACE_WITH_CLAUDE_API_KEY', '{}', 10, 1, 0, 0, NOW(3), NOW(3));

INSERT INTO models (code, name, type, provider, description, status, created_at, updated_at)
VALUES
  ('claude-opus-4-6', 'Claude Opus 4.6', 'chat', 'anthropic', '', 1, NOW(3), NOW(3)),
  ('claude-sonnet-4-6', 'Claude Sonnet 4.6', 'chat', 'anthropic', '', 1, NOW(3), NOW(3));

INSERT INTO endpoints (model_code, channel_id, protocol, request_path, request_method, content_type, vendor_model, interaction_mode, supports_stream, default_stream, status, timeout, created_at, updated_at)
VALUES
  ('claude-opus-4-6', @claude_ch, 'openai', '/v1/chat/completions', 'POST', 'application/json', 'claude-opus-4-6', 'stream', 1, 1, 1, 300, NOW(3), NOW(3)),
  ('claude-sonnet-4-6', @claude_ch, 'openai', '/v1/chat/completions', 'POST', 'application/json', 'claude-sonnet-4-6', 'stream', 1, 1, 1, 300, NOW(3), NOW(3));
