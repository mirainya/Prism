-- Reason: Sub2API v2.3 separates official, H-channel, and Jimeng capability matrices.
-- Requirement: discover the current key platform dynamically and enforce H-channel limits locally.
-- Impact: configures generic Sub2 video channels; no credentials or task data are changed.

UPDATE video_channels
SET extra_config = JSON_SET(
    COALESCE(extra_config, JSON_OBJECT()),
    '$.adapter.capabilities',
    JSON_OBJECT(
        'enabled', CAST('true' AS JSON),
        'method', 'GET',
        'path', '/v1/video-generations/capabilities',
        'root', 'data.models',
        'platform_path', 'data.platform',
        'fields', JSON_OBJECT(
            'model', 'model',
            'platform', 'platform',
            'resolutions', 'resolutions',
            'ratios', 'ratios',
            'supported_modes', 'supported_modes',
            'duration_options', 'duration_options',
            'duration_min', 'min_duration_seconds',
            'duration_max', 'max_duration_seconds',
            'supports_smart_duration', 'supports_smart_duration',
            'allow_generated_audio', 'supports_generate_audio',
            'require_visual_media_with_audio', 'audio_requires_visual_reference',
            'supports_cancel', 'supports_cancel',
            'max_images', 'max_reference_images',
            'max_videos', 'max_reference_videos',
            'max_audios', 'max_reference_audios',
            'max_media', 'max_references',
            'media_duration_min', 'min_reference_media_duration_seconds',
            'media_duration_max', 'max_reference_media_duration_seconds',
            'max_video_duration_total', 'max_reference_video_total_duration_seconds',
            'max_audio_duration_total', 'max_reference_audio_total_duration_seconds'
        ),
        'default_task_modes', JSON_ARRAY('text', 'references'),
        'task_mode_map', JSON_OBJECT(
            'default', JSON_ARRAY('text', 'references'),
            'video_extension', JSON_ARRAY('video_extension'),
            'omni_reference', JSON_ARRAY('references')
        )
    )
)
WHERE adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%'
  AND JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.adapter.profile')) = 'json_task_v1';

-- Official full-capability channel. Static rules are an upper bound; discovery may narrow them.
UPDATE video_channels
SET models = JSON_ARRAY('seedance-2.0', 'seedance-2.0-fast'),
    capabilities = JSON_OBJECT(
        'first_frame', CAST('true' AS JSON),
        'last_frame', CAST('true' AS JSON),
        'cancel', CAST('true' AS JSON),
        'audio', CAST('true' AS JSON),
        'web_search', CAST('true' AS JSON)
    ),
    extra_config = JSON_SET(
        COALESCE(extra_config, JSON_OBJECT()),
        '$.adapter.cancel.enabled', CAST('true' AS JSON),
        '$.adapter.local_cancel.enabled', CAST('true' AS JSON),
        '$.adapter.local_cancel.disabled_models', JSON_ARRAY(),
        '$.adapter.validation.models',
        JSON_OBJECT(
            'seedance-2.0', JSON_OBJECT(
                'duration_min', 4,
                'duration_max', 15,
                'resolutions', JSON_ARRAY('480p', '720p', '1080p', '4k'),
                'ratios', JSON_ARRAY('adaptive', '16:9', '4:3', '1:1', '3:4', '9:16', '21:9'),
                'task_modes', JSON_ARRAY('text', 'references'),
                'require_visual_media_with_audio', CAST('false' AS JSON),
                'allow_generated_audio', CAST('true' AS JSON),
                'allowed_roles', JSON_ARRAY('first_frame', 'last_frame', 'reference_image', 'reference_video', 'reference_audio'),
                'max_images', 9,
                'max_videos', 3,
                'max_audios', 3,
                'max_media', 15,
                'media_duration_min', 2,
                'media_duration_max', 15,
                'max_video_duration_total', 15,
                'max_audio_duration_total', 15,
                'parameters', JSON_ARRAY(
                    JSON_OBJECT(
                        'name', 'return_last_frame', 'label', '返回尾帧', 'type', 'select', 'default', CAST('false' AS JSON),
                        'options', JSON_ARRAY(
                            JSON_OBJECT('label', '关闭', 'value', CAST('false' AS JSON)),
                            JSON_OBJECT('label', '开启', 'value', CAST('true' AS JSON))
                        )
                    ),
                    JSON_OBJECT(
                        'name', 'web_search', 'label', '联网增强', 'type', 'select', 'default', CAST('false' AS JSON),
                        'options', JSON_ARRAY(
                            JSON_OBJECT('label', '关闭', 'value', CAST('false' AS JSON)),
                            JSON_OBJECT('label', '开启', 'value', CAST('true' AS JSON))
                        )
                    )
                )
            ),
            'seedance-2.0-fast', JSON_OBJECT(
                'duration_min', 4,
                'duration_max', 15,
                'resolutions', JSON_ARRAY('480p', '720p'),
                'ratios', JSON_ARRAY('adaptive', '16:9', '4:3', '1:1', '3:4', '9:16', '21:9'),
                'task_modes', JSON_ARRAY('text', 'references'),
                'require_visual_media_with_audio', CAST('false' AS JSON),
                'allow_generated_audio', CAST('true' AS JSON),
                'allowed_roles', JSON_ARRAY('first_frame', 'last_frame', 'reference_image', 'reference_video', 'reference_audio'),
                'max_images', 9,
                'max_videos', 3,
                'max_audios', 3,
                'max_media', 15,
                'media_duration_min', 2,
                'media_duration_max', 15,
                'max_video_duration_total', 15,
                'max_audio_duration_total', 15,
                'parameters', JSON_ARRAY(
                    JSON_OBJECT(
                        'name', 'return_last_frame', 'label', '返回尾帧', 'type', 'select', 'default', CAST('false' AS JSON),
                        'options', JSON_ARRAY(
                            JSON_OBJECT('label', '关闭', 'value', CAST('false' AS JSON)),
                            JSON_OBJECT('label', '开启', 'value', CAST('true' AS JSON))
                        )
                    ),
                    JSON_OBJECT(
                        'name', 'web_search', 'label', '联网增强', 'type', 'select', 'default', CAST('false' AS JSON),
                        'options', JSON_ARRAY(
                            JSON_OBJECT('label', '关闭', 'value', CAST('false' AS JSON)),
                            JSON_OBJECT('label', '开启', 'value', CAST('true' AS JSON))
                        )
                    )
                )
            )
        )
    )
WHERE adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%'
  AND JSON_UNQUOTE(JSON_EXTRACT(capabilities, '$.web_search')) = 'true';

-- H-channel profile. Seedance 2.0 expires at the documented boundary.
UPDATE video_channels
SET models = JSON_ARRAY('seedance-2.5', 'seedance-2.0'),
    capabilities = JSON_OBJECT(
        'first_frame', CAST('false' AS JSON),
        'last_frame', CAST('false' AS JSON),
        'cancel', CAST('true' AS JSON),
        'audio', CAST('true' AS JSON),
        'web_search', CAST('false' AS JSON)
    ),
    extra_config = JSON_SET(
        COALESCE(extra_config, JSON_OBJECT()),
        '$.adapter.cancel.enabled', CAST('false' AS JSON),
        '$.adapter.local_cancel.enabled', CAST('true' AS JSON),
        '$.adapter.local_cancel.disabled_models', JSON_ARRAY(),
        '$.adapter.validation.models',
        JSON_OBJECT(
            'seedance-2.5', JSON_OBJECT(
                'duration_min', 4,
                'duration_max', 20,
                'resolutions', JSON_ARRAY('480p', '720p'),
                'ratios', JSON_ARRAY('16:9', '9:16', '1:1', '4:3', '3:4', '21:9'),
                'task_modes', JSON_ARRAY('text', 'references', 'video_extension'),
                'require_visual_media_with_audio', CAST('false' AS JSON),
                'allow_generated_audio', CAST('true' AS JSON),
                'allowed_roles', JSON_ARRAY('reference_image', 'reference_video', 'reference_audio'),
                'max_images', 30,
                'max_videos', 10,
                'max_audios', 10,
                'max_media', 50,
                'media_duration_min', 2,
                'media_duration_max', 30,
                'max_video_duration_total', 30,
                'max_audio_duration_total', 30,
                'forbidden_parameters', JSON_ARRAY('priority', 'return_last_frame', 'web_search'),
                'task_mode_rules', JSON_OBJECT(
                    'video_extension', JSON_OBJECT(
                        'allowed_roles', JSON_ARRAY('reference_video'),
                        'min_media', 1,
                        'max_media', 1,
                        'exact_role_counts', JSON_OBJECT('reference_video', 1)
                    )
                )
            ),
            'seedance-2.0', JSON_OBJECT(
                'duration_min', 4,
                'duration_max', 15,
                'resolutions', JSON_ARRAY('1080p'),
                'ratios', JSON_ARRAY('16:9', '9:16', '1:1', '4:3', '3:4', '21:9'),
                'task_modes', JSON_ARRAY('text', 'references'),
                'require_visual_media_with_audio', CAST('false' AS JSON),
                'allow_generated_audio', CAST('true' AS JSON),
                'allowed_roles', JSON_ARRAY('reference_image', 'reference_video', 'reference_audio'),
                'max_images', 9,
                'max_videos', 3,
                'max_audios', 3,
                'max_media', 15,
                'media_duration_min', 2,
                'media_duration_max', 15,
                'max_video_duration_total', 15,
                'max_audio_duration_total', 15,
                'forbidden_parameters', JSON_ARRAY('priority', 'return_last_frame', 'web_search'),
                'available_until', '2026-08-14T00:00:00+08:00'
            )
        )
    )
WHERE adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%'
  AND JSON_CONTAINS(models, JSON_QUOTE('seedance-2.5'))
  AND JSON_CONTAINS(models, JSON_QUOTE('seedance-2.0'))
  AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(capabilities, '$.web_search')), 'false') <> 'true';
