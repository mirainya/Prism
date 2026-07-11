-- Reason: Gateway V2 routes one ability through one explicit upstream transport.
-- Impact: creates transport and circuit-state tables; adds transport provenance to Responses,
-- conversations, and request logs. Existing ability records receive a conservative transport.

CREATE TABLE IF NOT EXISTS gw_ability_transports (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    ability_id BIGINT UNSIGNED NOT NULL,
    transport VARCHAR(64) NOT NULL,
    status TINYINT NOT NULL DEFAULT 1,
    config JSON NULL,
    checked_at DATETIME(3) NULL,
    last_error VARCHAR(1000) NOT NULL DEFAULT '',
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY idx_gw_ability_transport (ability_id, transport),
    KEY idx_gw_ability_transport_status (ability_id, transport, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS gw_route_states (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    key_id BIGINT UNSIGNED NOT NULL,
    model_name VARCHAR(80) NOT NULL,
    transport VARCHAR(64) NOT NULL,
    disabled_until DATETIME(3) NOT NULL,
    reason VARCHAR(500) NOT NULL DEFAULT '',
    status_code INT NOT NULL DEFAULT 0,
    fail_count INT NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY idx_gw_route_state (key_id, model_name, transport),
    KEY idx_gw_route_states_disabled_until (disabled_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- The application AutoMigrate step adds provenance columns and indexes. Keeping
-- ALTER TABLE statements out of this data migration makes repeated execution safe.

-- Conservative initial mapping: OpenAI-compatible routes use Chat until an explicit
-- probe enables native Responses. The V2 engine can convert supported basic Requests.
INSERT IGNORE INTO gw_ability_transports (ability_id, transport, status, created_at, updated_at)
SELECT ab.id,
    CASE gc.protocol
        WHEN 'anthropic' THEN 'anthropic_messages'
        WHEN 'google' THEN 'google_generate_content'
        WHEN 'volcengine' THEN 'volcengine_responses_v3'
        ELSE 'openai_chat'
    END,
    1, NOW(3), NOW(3)
FROM gw_abilities ab
JOIN gw_channels gc ON gc.id = ab.channel_id
WHERE gc.deleted_at IS NULL;

-- Materialize the historical capability defaults once. Protocol endpoint flags
-- are intentionally omitted: V2 derives endpoint support from transports.
UPDATE gw_abilities ab
JOIN gw_channels gc ON gc.id = ab.channel_id
SET ab.capabilities = CASE gc.protocol
    WHEN 'anthropic' THEN JSON_OBJECT(
        'stream', TRUE, 'vision', TRUE, 'files', TRUE, 'tools', TRUE,
        'structured_output', TRUE, 'reasoning', TRUE
    )
    WHEN 'google' THEN JSON_OBJECT(
        'stream', TRUE, 'vision', TRUE, 'files', TRUE, 'tools', TRUE,
        'structured_output', TRUE
    )
    WHEN 'volcengine' THEN JSON_OBJECT(
        'stream', TRUE, 'vision', TRUE, 'files', TRUE, 'audio', TRUE,
        'video', TRUE, 'tools', TRUE, 'structured_output', TRUE, 'reasoning', TRUE,
        'web_search', TRUE
    )
    ELSE JSON_OBJECT(
        'stream', TRUE, 'vision', TRUE, 'files', TRUE, 'tools', TRUE,
        'structured_output', TRUE, 'reasoning', TRUE
    )
END
WHERE ab.capabilities IS NULL
   OR JSON_TYPE(ab.capabilities) = 'NULL'
   OR JSON_LENGTH(ab.capabilities) = 0;
