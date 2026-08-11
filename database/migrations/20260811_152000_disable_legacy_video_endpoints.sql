-- Reason: new video generation is owned exclusively by the dedicated video engine.
-- Requirement: preserve legacy task rows for query, cancellation, and in-flight worker recovery.
-- Impact: disables creation through the five audited legacy video endpoints; no task data is removed.

UPDATE endpoints
SET status = 0
WHERE model_code IN (
    'Google_Veo',
    'sora2',
    'grok-imagine-video',
    'grok-imagine-video-1.5',
    'grok-video-1.5-preview'
);
