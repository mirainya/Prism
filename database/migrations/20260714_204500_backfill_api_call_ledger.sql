-- Reason: migrate deterministic historical Task and Responses ownership into
-- the unified call ledger after all reference columns exist.
-- Requirement: preserve traceability without inventing unknown historical
-- routes or protocol-specific endpoints.
-- Impact: assigns stable call IDs, creates ledger rows, links deterministic
-- request/conversation/message/billing records, and creates account baselines.
-- Deployment: stop all application and worker instances before running this
-- migration so ownership links and opening balances use one stable snapshot.

UPDATE tasks
SET call_id = CONCAT('call_task_', id)
WHERE call_id = '';

INSERT IGNORE INTO api_calls (
    id, request_id, user_id, token_id, endpoint, operation, model, status,
    is_stream, background, store, retain_payload, resource_type, resource_id,
    reserved_amount, final_cost, refunded_amount, http_status, error_type,
    error_code, error_message, started_at, completed_at, duration_ms,
    created_at, updated_at
)
SELECT
    t.call_id,
    t.task_no,
    t.user_id,
    t.token_id,
    'legacy:capability-task',
    'generation',
    t.model_code,
    CASE t.status
        WHEN 'success' THEN 'completed'
        WHEN 'failed' THEN 'failed'
        WHEN 'cancelled' THEN 'cancelled'
        WHEN 'pending' THEN 'received'
        ELSE 'in_progress'
    END,
    0,
    1,
    0,
    0,
    'task',
    t.task_no,
    t.cost,
    CASE WHEN t.refunded = 1 THEN 0 ELSE t.cost END,
    CASE WHEN t.refunded = 1 THEN t.cost ELSE 0 END,
    CASE t.status WHEN 'success' THEN 200 WHEN 'failed' THEN 502 WHEN 'cancelled' THEN 499 ELSE 0 END,
    CASE WHEN t.status = 'failed' THEN 'upstream_error' WHEN t.status = 'cancelled' THEN 'cancelled' ELSE '' END,
    CASE WHEN t.status = 'failed' THEN 'legacy_task_failed' WHEN t.status = 'cancelled' THEN 'legacy_task_cancelled' ELSE '' END,
    t.error_message,
    COALESCE(t.started_at, t.created_at),
    t.completed_at,
    CASE
        WHEN t.completed_at IS NULL THEN 0
        ELSE TIMESTAMPDIFF(MICROSECOND, COALESCE(t.started_at, t.created_at), t.completed_at) DIV 1000
    END,
    t.created_at,
    t.updated_at
FROM tasks t
WHERE t.call_id <> '';

UPDATE ai_responses
SET call_id = CONCAT('call_resp_', SUBSTRING(SHA2(id, 256), 1, 32))
WHERE call_id = '';

INSERT IGNORE INTO api_calls (
    id, request_id, user_id, token_id, endpoint, operation, model, status,
    is_stream, background, store, retain_payload, resource_type, resource_id,
    input_tokens, output_tokens, total_tokens, usage_json, http_status,
    error_type, error_code, error_message, started_at, completed_at,
    duration_ms, created_at, updated_at
)
SELECT
    r.call_id,
    r.id,
    r.user_id,
    r.token_id,
    '/v1/responses',
    'responses',
    r.model,
    CASE r.status
        WHEN 'completed' THEN 'completed'
        WHEN 'incomplete' THEN 'completed'
        WHEN 'failed' THEN 'failed'
        WHEN 'cancelled' THEN 'cancelled'
        WHEN 'queued' THEN 'received'
        ELSE 'in_progress'
    END,
    0,
    r.background,
    r.store,
    0,
    'response',
    r.id,
    COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(r.usage_json, '$.input_tokens')) AS SIGNED), 0),
    COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(r.usage_json, '$.output_tokens')) AS SIGNED), 0),
    COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(r.usage_json, '$.total_tokens')) AS SIGNED), 0),
    r.usage_json,
    CASE r.status WHEN 'completed' THEN 200 WHEN 'incomplete' THEN 200 WHEN 'failed' THEN 502 WHEN 'cancelled' THEN 499 ELSE 0 END,
    CASE WHEN r.status = 'failed' THEN 'upstream_error' WHEN r.status = 'cancelled' THEN 'cancelled' ELSE '' END,
    CASE WHEN r.status = 'failed' THEN 'legacy_response_failed' WHEN r.status = 'cancelled' THEN 'legacy_response_cancelled' ELSE '' END,
    JSON_UNQUOTE(JSON_EXTRACT(r.error_json, '$.message')),
    r.created_at,
    r.completed_at,
    CASE WHEN r.completed_at IS NULL THEN 0 ELSE TIMESTAMPDIFF(MICROSECOND, r.created_at, r.completed_at) DIV 1000 END,
    r.created_at,
    COALESCE(r.completed_at, r.created_at)
FROM ai_responses r
WHERE r.call_id <> '';

-- Link historical upstream request logs only where ownership is deterministic.
-- attempt_id remains 0 because old rows do not contain enough routing data to
-- reconstruct a trustworthy APICallAttempt.
UPDATE channel_request_logs l
JOIN tasks t ON l.task_id = t.id OR (l.task_id = 0 AND l.task_no = t.task_no)
SET l.call_id = t.call_id
WHERE l.call_id = '' AND t.call_id <> '';

UPDATE channel_request_logs l
JOIN ai_responses r ON l.id = r.request_log_id
SET l.call_id = r.call_id
WHERE l.call_id = '' AND r.call_id <> '';

UPDATE conversations c
JOIN channel_request_logs l ON l.id = c.last_request_log_id
SET c.call_id = l.call_id
WHERE c.call_id = '' AND l.call_id <> '';

UPDATE messages m
JOIN channel_request_logs l ON l.id = m.request_log_id
SET m.call_id = l.call_id
WHERE m.call_id = '' AND l.call_id <> '';

UPDATE api_calls c
JOIN conversations conversation ON conversation.call_id = c.id
SET c.conversation_id = conversation.id
WHERE c.conversation_id = 0;

UPDATE api_calls c
JOIN messages message ON message.call_id = c.id
SET c.conversation_id = message.conversation_id
WHERE c.conversation_id = 0;

UPDATE billing_logs b
JOIN tasks t
    ON b.idempotent_key = t.task_no
    OR LEFT(b.idempotent_key, CHAR_LENGTH(t.task_no) + 1) = CONCAT(t.task_no, ':')
SET
    b.call_id = t.call_id,
    b.phase = CASE
        WHEN b.idempotent_key LIKE '%:reserve' THEN 'reserve'
        WHEN b.idempotent_key LIKE '%:settle' THEN 'settle'
        WHEN b.type = 'refund' THEN 'refund'
        ELSE b.phase
    END
WHERE b.call_id = '';

UPDATE billing_logs b
JOIN ai_responses r
    ON LEFT(b.idempotent_key, CHAR_LENGTH(r.id) + 1) = CONCAT(r.id, ':')
SET
    b.call_id = r.call_id,
    b.phase = CASE
        WHEN b.idempotent_key LIKE '%:reserve' THEN 'reserve'
        WHEN b.idempotent_key LIKE '%:settle' THEN 'settle'
        WHEN b.type = 'refund' THEN 'refund'
        ELSE b.phase
    END
WHERE b.call_id = '';

UPDATE api_calls c
JOIN (
    SELECT
        call_id,
        SUM(CASE WHEN phase = 'reserve' AND type = 'deduct' THEN amount ELSE 0 END) AS reserved_amount,
        SUM(CASE WHEN type = 'deduct' THEN amount ELSE -amount END) AS final_cost,
        SUM(CASE WHEN type = 'refund' THEN amount ELSE 0 END) AS refunded_amount
    FROM billing_logs
    WHERE call_id <> ''
    GROUP BY call_id
) totals ON totals.call_id = c.id
SET
    c.reserved_amount = GREATEST(c.reserved_amount, totals.reserved_amount),
    c.final_cost = GREATEST(totals.final_cost, 0),
    c.refunded_amount = totals.refunded_amount;

-- Establish an idempotent opening balance for each existing account. Historical
-- mutations cannot be reconstructed completely, so future entries continue
-- from this explicit migration baseline.
INSERT IGNORE INTO balance_entries (
    entry_key, source_key, account_type, account_id, user_id, token_id,
    direction, category, amount, balance_before, balance_after,
    call_id, attempt_id, actor_user_id, metadata, created_at
)
SELECT
    CONCAT('bal_opening_user_', u.id),
    CONCAT('migration:20260714_204500:user:', u.id),
    'user',
    u.id,
    u.id,
    0,
    CASE WHEN u.balance < 0 THEN 'debit' ELSE 'credit' END,
    'opening_balance',
    ABS(u.balance),
    0,
    u.balance,
    '',
    0,
    0,
    JSON_OBJECT('migration', '20260714_204500', 'kind', 'opening_balance'),
    CURRENT_TIMESTAMP(3)
FROM users u
WHERE u.balance <> 0;

INSERT IGNORE INTO balance_entries (
    entry_key, source_key, account_type, account_id, user_id, token_id,
    direction, category, amount, balance_before, balance_after,
    call_id, attempt_id, actor_user_id, metadata, created_at
)
SELECT
    CONCAT('bal_opening_token_', t.id),
    CONCAT('migration:20260714_204500:token:', t.id),
    'token',
    t.id,
    t.user_id,
    t.id,
    CASE WHEN t.balance < 0 THEN 'debit' ELSE 'credit' END,
    'opening_balance',
    ABS(t.balance),
    0,
    t.balance,
    '',
    0,
    0,
    JSON_OBJECT('migration', '20260714_204500', 'kind', 'opening_balance'),
    CURRENT_TIMESTAMP(3)
FROM tokens t
WHERE t.balance <> 0;

-- Historical terminal Task and store=false Responses bodies are cleared by the
-- hourly retention job so retain_api_call_payloads and retention_hours apply.
