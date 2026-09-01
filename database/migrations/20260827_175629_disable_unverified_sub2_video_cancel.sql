-- Reason: earlier Sub2 Seedance migrations advertised provider cancellation,
-- but no documented or verified upstream cancellation contract exists.
-- Requirement: expose cancellation only while a task is still queued locally
-- and has not received a provider task id.
-- Impact: updates Sub2 video channel metadata only. Existing tasks, keys,
-- pricing, and provider tasks are unchanged. Safe to rerun.

UPDATE video_channels
SET capabilities = JSON_SET(
        COALESCE(capabilities, JSON_OBJECT()),
        '$.cancel', CAST('false' AS JSON)
    ),
    extra_config = JSON_SET(
        COALESCE(extra_config, JSON_OBJECT()),
        '$.adapter.cancel.enabled', CAST('false' AS JSON),
        '$.adapter.local_cancel.enabled', CAST('true' AS JSON)
    )
WHERE adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%'
  AND JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.adapter.profile')) = 'json_task_v1';
