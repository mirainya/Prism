-- Reason: move Xingjing Grok Preview into the dedicated video engine.
-- Requirement: map canonical duration, ratio, and image content through generic configuration.
-- Impact: creates one generic video channel and copies its existing bound credential.

INSERT INTO video_channels (
    name, adapter_type, base_url, status, priority, models, capabilities,
    pricing, asset_resolver, passthrough, extra_config
)
SELECT
    'Xingjing Grok Video',
    'generic',
    c.base_url,
    'active',
    10,
    JSON_ARRAY('grok-video-1.5-preview'),
    JSON_OBJECT('first_frame', CAST('true' AS JSON), 'audio', CAST('true' AS JSON)),
    JSON_OBJECT('mode', 'fixed', 'fixed_price', 0, 'markup_ratio', 1),
    'direct_url',
    NULL,
    CAST('{
      "adapter": {
        "profile": "json_task_v1",
        "auth_location": "header",
        "auth_key": "Authorization",
        "auth_prefix": "Bearer ",
        "timeout_seconds": 300,
        "submit": {"enabled": true, "method": "POST", "path": "/v1/videos"},
        "poll": {"enabled": true, "method": "GET", "path": "/v1/videos/{task_id}"},
        "cancel": {"enabled": false},
        "request": {
          "fields": {
            "model": "model",
            "prompt": "prompt",
            "duration": "seconds",
            "ratio": "aspect_ratio"
          },
          "include_content": false,
          "content_projections": [{
            "source": "url",
            "target": "images",
            "output": "array",
            "types": ["image_url"],
            "roles": ["first_frame", "reference_image"]
          }],
          "params_mode": "merge_missing"
        },
        "response": {
          "task_id_paths": ["id"],
          "status_paths": ["status"],
          "progress_paths": ["progress"],
          "video_url_paths": ["video_url"],
          "error_paths": ["error.message", "error", "message"],
          "status_map": {
            "queued": "submitted",
            "pending": "submitted",
            "in_progress": "tracking",
            "processing": "tracking",
            "completed": "completed",
            "failed": "failed"
          },
          "submit_default_status": "submitted",
          "poll_default_status": "tracking",
          "unknown_status": "tracking"
        },
        "validation": {
          "models": {
            "grok-video-1.5-preview": {
              "ratios": ["1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"],
              "task_modes": ["text", "references"],
              "allow_generated_audio": true,
              "allowed_roles": ["first_frame", "reference_image"],
              "max_images": 10,
              "max_media": 10
            }
          }
        },
        "local_cancel": {"enabled": true}
      }
    }' AS JSON)
FROM channels c
JOIN endpoints e ON e.channel_id = c.id
WHERE e.model_code = 'grok-video-1.5-preview'
  AND e.status = 1
  AND NOT EXISTS (
      SELECT 1 FROM video_channels vc
      WHERE vc.name = 'Xingjing Grok Video'
        AND vc.base_url COLLATE utf8mb4_unicode_ci = c.base_url COLLATE utf8mb4_unicode_ci
  )
LIMIT 1;

INSERT INTO video_channel_keys (
    channel_id, api_key, label, weight, max_concurrency, status,
    current_concurrency, total_calls, created_at
)
SELECT
    vc.id,
    ca.api_key,
    ca.name,
    CASE WHEN ea.weight > 0 THEN ea.weight ELSE 1 END,
    ca.max_tasks,
    CASE WHEN ca.status = 1 THEN 'active' ELSE 'disabled' END,
    0,
    0,
    NOW()
FROM video_channels vc
JOIN endpoints e ON e.model_code = 'grok-video-1.5-preview'
JOIN endpoint_accounts ea ON ea.endpoint_id = e.id AND ea.status = 1
JOIN channel_accounts ca ON ca.id = ea.account_id AND ca.channel_id = e.channel_id
JOIN channels c ON c.id = e.channel_id
  AND c.base_url COLLATE utf8mb4_unicode_ci = vc.base_url COLLATE utf8mb4_unicode_ci
WHERE vc.name = 'Xingjing Grok Video'
  AND NOT EXISTS (
      SELECT 1 FROM video_channel_keys existing
      WHERE existing.channel_id = vc.id
        AND existing.api_key COLLATE utf8mb4_unicode_ci = ca.api_key COLLATE utf8mb4_unicode_ci
  );
