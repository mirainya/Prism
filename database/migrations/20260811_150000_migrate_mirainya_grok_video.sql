-- Reason: move the two MiraiNya Grok video models into the dedicated video engine.
-- Requirement: express image-to-video input through generic content projection without provider-specific code.
-- Impact: creates one generic video channel and copies its existing bound credential without exposing the key.

INSERT INTO video_channels (
    name, adapter_type, base_url, status, priority, models, capabilities,
    pricing, asset_resolver, passthrough, extra_config
)
SELECT
    'MiraiNya Grok Video',
    'generic',
    c.base_url,
    'active',
    15,
    JSON_ARRAY('grok-imagine-video', 'grok-imagine-video-1.5'),
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
        "submit": {"enabled": true, "method": "POST", "path": "/v1/videos/generations"},
        "poll": {"enabled": true, "method": "GET", "path": "/v1/videos/{task_id}"},
        "cancel": {"enabled": false},
        "request": {
          "fields": {
            "model": "model",
            "prompt": "prompt",
            "resolution": "resolution",
            "ratio": "aspect_ratio",
            "duration": "duration"
          },
          "include_content": false,
          "content_projections": [{
            "source": "url",
            "target": "image_url",
            "output": "scalar",
            "models": ["grok-imagine-video-1.5"],
            "types": ["image_url"],
            "roles": ["first_frame"],
            "required": true
          }],
          "params_mode": "merge_missing"
        },
        "response": {
          "task_id_paths": ["request_id", "id"],
          "status_paths": ["status"],
          "progress_paths": ["progress"],
          "video_url_paths": ["video.url", "video_url"],
          "error_paths": ["error.message", "error", "message"],
          "status_map": {
            "queued": "submitted",
            "pending": "tracking",
            "running": "tracking",
            "processing": "tracking",
            "done": "completed",
            "completed": "completed",
            "failed": "failed",
            "error": "failed"
          },
          "submit_default_status": "submitted",
          "poll_default_status": "tracking",
          "unknown_status": "tracking"
        },
        "validation": {
          "models": {
            "grok-imagine-video": {
              "duration_max": 8,
              "ratios": ["16:9", "9:16", "1:1", "4:3", "3:4"],
              "task_modes": ["text"],
              "allow_generated_audio": true
            },
            "grok-imagine-video-1.5": {
              "duration_max": 15,
              "resolutions": ["480p", "720p", "1080p"],
              "ratios": ["16:9", "9:16", "1:1", "4:3", "3:4", "3:2", "2:3"],
              "task_modes": ["references"],
              "require_media": true,
              "allow_generated_audio": true,
              "allowed_roles": ["first_frame"],
              "max_images": 1,
              "max_media": 1
            }
          }
        },
        "local_cancel": {"enabled": true}
      }
    }' AS JSON)
FROM channels c
JOIN endpoints e ON e.channel_id = c.id
WHERE e.model_code = 'grok-imagine-video'
  AND e.status = 1
  AND NOT EXISTS (
      SELECT 1 FROM video_channels vc
      WHERE vc.name = 'MiraiNya Grok Video' AND vc.base_url = c.base_url
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
JOIN endpoints e ON e.model_code = 'grok-imagine-video'
JOIN endpoint_accounts ea ON ea.endpoint_id = e.id AND ea.status = 1
JOIN channel_accounts ca ON ca.id = ea.account_id AND ca.channel_id = e.channel_id
JOIN channels c ON c.id = e.channel_id AND c.base_url = vc.base_url
WHERE vc.name = 'MiraiNya Grok Video'
  AND NOT EXISTS (
      SELECT 1 FROM video_channel_keys existing
      WHERE existing.channel_id = vc.id AND existing.api_key = ca.api_key
  );
