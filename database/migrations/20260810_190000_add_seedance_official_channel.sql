-- Reason: provision the Sub2API official Seedance route separately from the H channel.
-- Requirement: preserve provider-specific model limits while reusing the generic adapter and presigned upload plugin.
-- Impact: creates one active channel without credentials; API keys are provisioned separately and never stored in migrations.

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
    passthrough,
    extra_config
)
SELECT
    '官满血-Seedance',
    source.adapter_type,
    source.base_url,
    'active',
    20,
    JSON_ARRAY('seedance-2.0', 'seedance-2.0-fast'),
    JSON_OBJECT(
        'first_frame', CAST('true' AS JSON),
        'last_frame', CAST('true' AS JSON),
        'cancel', CAST('true' AS JSON),
        'audio', CAST('true' AS JSON),
        'web_search', CAST('true' AS JSON)
    ),
    source.pricing,
    source.asset_resolver,
    source.passthrough,
    JSON_SET(
        source.extra_config,
        '$.adapter.cancel.enabled', CAST('true' AS JSON),
        '$.adapter.local_cancel.enabled', CAST('true' AS JSON),
        '$.adapter.local_cancel.disabled_models', JSON_ARRAY(),
        '$.adapter.validation.models', JSON_OBJECT(
            'seedance-2.0', JSON_OBJECT(
                'duration_min', 4,
                'duration_max', 15,
                'resolutions', JSON_ARRAY('480p', '720p', '1080p', '4k'),
                'ratios', JSON_ARRAY('adaptive', '16:9', '4:3', '1:1', '3:4', '9:16', '21:9'),
                'task_modes', JSON_ARRAY('text', 'references'),
                'allow_generated_audio', CAST('true' AS JSON),
                'allowed_roles', JSON_ARRAY('first_frame', 'last_frame', 'reference_image', 'reference_video', 'reference_audio'),
                'max_images', 9,
                'max_videos', 3,
                'max_audios', 3,
                'max_media', 15,
                'media_duration_min', 2,
                'media_duration_max', 15,
                'max_video_duration_total', 15,
                'max_audio_duration_total', 15
            ),
            'seedance-2.0-fast', JSON_OBJECT(
                'duration_min', 4,
                'duration_max', 15,
                'resolutions', JSON_ARRAY('480p', '720p'),
                'ratios', JSON_ARRAY('adaptive', '16:9', '4:3', '1:1', '3:4', '9:16', '21:9'),
                'task_modes', JSON_ARRAY('text', 'references'),
                'allow_generated_audio', CAST('true' AS JSON),
                'allowed_roles', JSON_ARRAY('first_frame', 'last_frame', 'reference_image', 'reference_video', 'reference_audio'),
                'max_images', 9,
                'max_videos', 3,
                'max_audios', 3,
                'max_media', 15,
                'media_duration_min', 2,
                'media_duration_max', 15,
                'max_video_duration_total', 15,
                'max_audio_duration_total', 15
            )
        )
    )
FROM video_channels AS source
WHERE source.id = (
    SELECT selected.id
    FROM (
        SELECT id
        FROM video_channels
        WHERE adapter_type = 'generic'
          AND base_url LIKE 'https://sub2api.0x0.fan/api%'
          AND asset_resolver = 'presigned_upload'
          AND JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.adapter.profile')) = 'json_task_v1'
        ORDER BY priority DESC, id ASC
        LIMIT 1
    ) AS selected
)
AND NOT EXISTS (
    SELECT 1
    FROM video_channels AS existing
    WHERE existing.name = '官满血-Seedance'
      AND existing.base_url = source.base_url
);
