-- Normalize the first legacy import's transport identifiers to the concrete
-- adapters registered by Gateway V2. The imported release is still draft, so
-- this repair preserves its immutable published semantics.
UPDATE gw_channel_transports
SET transport_code = CASE protocol
    WHEN 'anthropic' THEN CONCAT('anthropic_messages-', channel_id)
    WHEN 'google' THEN CONCAT('google_generate_content-', channel_id)
    WHEN 'volcengine' THEN CONCAT('volcengine_responses_v3-', channel_id)
    ELSE CONCAT('openai_chat-', channel_id)
END
WHERE release_id IN (SELECT id FROM gw_catalog_releases WHERE status = 'draft' AND semantic_version = 'legacy-import-1');
