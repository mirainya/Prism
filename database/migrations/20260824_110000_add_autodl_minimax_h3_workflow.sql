-- Reason: expose the AutoDL ComfyUI H3 image/audio-to-video workflow through the
-- generic video adapter instead of adding an AutoDL-specific implementation.
-- Requirement: support the documented 1-15 second request, four resolution
-- values, optional seed, and numbered image/audio reference fields.
-- Impact: creates one active channel without credentials. The AutoDL token is
-- provisioned separately on the server and is never stored in migrations.

INSERT INTO video_channels (
    name,
    adapter_type,
    base_url,
    status,
    priority,
    models,
    capabilities,
    pricing,
    asset_resolver,
    extra_config
)
SELECT
    'AutoDL-Minimax H3 多图多音频生视频',
    'generic',
    'https://autodl.art',
    'active',
    5,
    JSON_ARRAY(JSON_OBJECT(
        'model_name', 'minimax-h3-image-audio-to-video-v2-15s',
        'vendor_model', 'minimax-h3-image-audio-to-video-v2-15s'
    )),
    JSON_OBJECT(
        'first_frame', CAST('true' AS JSON),
        'last_frame', CAST('true' AS JSON),
        'audio', CAST('true' AS JSON),
        'cancel', CAST('false' AS JSON),
        'web_search', CAST('false' AS JSON)
    ),
    JSON_OBJECT('mode', 'fixed', 'fixed_price', 0, 'markup_ratio', 1),
    'direct_url',
    CAST('{
      "result_storage": {"enabled": true},
      "adapter": {
        "profile": "json_task_v1",
        "auth_location": "header",
        "auth_key": "Authorization",
        "auth_prefix": "",
        "timeout_seconds": 300,
        "submit": {
          "enabled": true,
          "method": "POST",
          "path": "/api/v1/comfyui/comfyui_workflow/minimax_h3_image_audio_to_video_v2_15s"
        },
        "poll": {
          "enabled": true,
          "method": "GET",
          "path": "/api/v1/comfyui/comfyui_workflow/result/{task_id}"
        },
        "cancel": {"enabled": false},
        "request": {
          "fields": {
            "prompt": "prompt",
            "resolution": "resolution",
            "duration": "duration"
          },
          "include_content": false,
          "content_projections": [
            {"source":"url","target":"ref_image_0","output":"scalar","types":["image_url"],"index":0},
            {"source":"url","target":"ref_image_1","output":"scalar","types":["image_url"],"index":1},
            {"source":"url","target":"ref_image_2","output":"scalar","types":["image_url"],"index":2},
            {"source":"url","target":"ref_image_3","output":"scalar","types":["image_url"],"index":3},
            {"source":"url","target":"ref_image_4","output":"scalar","types":["image_url"],"index":4},
            {"source":"url","target":"ref_image_5","output":"scalar","types":["image_url"],"index":5},
            {"source":"url","target":"ref_image_6","output":"scalar","types":["image_url"],"index":6},
            {"source":"url","target":"ref_image_7","output":"scalar","types":["image_url"],"index":7},
            {"source":"url","target":"ref_image_8","output":"scalar","types":["image_url"],"index":8},
            {"source":"url","target":"ref_audio_0","output":"scalar","types":["audio_url"],"index":0},
            {"source":"url","target":"ref_audio_1","output":"scalar","types":["audio_url"],"index":1},
            {"source":"url","target":"ref_audio_2","output":"scalar","types":["audio_url"],"index":2}
          ],
          "params_mode": "merge_missing"
        },
        "response": {
          "task_id_paths": ["data.task_id"],
          "status_paths": ["data.status"],
          "video_url_paths": ["data.results.0.url"],
          "error_paths": ["data.message", "msg"],
          "status_map": {
            "queued": "submitted",
            "running": "tracking",
            "success": "completed",
            "failed": "failed"
          },
          "submit_default_status": "submitted",
          "poll_default_status": "tracking",
          "unknown_status": "tracking"
        },
        "validation": {
          "models": {
            "minimax-h3-image-audio-to-video-v2-15s": {
              "duration_min": 1,
              "duration_max": 15,
              "resolutions": ["480p竖", "768p竖", "480p横", "768p横"],
              "task_modes": ["text", "references"],
              "allow_generated_audio": false,
              "allowed_roles": ["first_frame", "last_frame", "reference_image", "reference_audio"],
              "max_images": 9,
              "max_audios": 3,
              "max_media": 12,
              "parameters": [
                {"name":"seed","label":"随机种子","type":"integer","min":1,"max":999999999999999,"options":[]}
              ]
            }
          }
        },
        "local_cancel": {"enabled": true}
      }
    }' AS JSON)
WHERE NOT EXISTS (
    SELECT 1 FROM video_channels
    WHERE name = 'AutoDL-Minimax H3 多图多音频生视频'
      AND base_url = 'https://autodl.art'
);
