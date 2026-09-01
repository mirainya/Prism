-- Reason: Sub2API v2.7 replaces task_mode with provider_mode, adds H video
-- editing, introduces Leonardo Seedance 2.5, and prefers upload idempotency in
-- the request body.
-- Requirement: express the new wire contract through generic adapter and
-- presigned-upload configuration without adding a Sub2-specific adapter.
-- Impact: updates matching Sub2 video channel metadata only. Existing tasks,
-- credentials, billing data, and applied migrations are unchanged. Safe to rerun.

UPDATE video_channels
SET extra_config = JSON_SET(
    COALESCE(extra_config, JSON_OBJECT()),
    '$.asset_resolver.idempotency_body_field', 'idempotency_key',
    '$.adapter.capabilities.fields.duration_max_with_video_reference', 'max_duration_seconds_with_video_reference',
    '$.adapter.capabilities.task_mode_map', JSON_OBJECT(
        'default', JSON_ARRAY('text', 'references'),
        'references', JSON_ARRAY('references'),
        'first_last_frame', JSON_ARRAY('references'),
        'video_edit', JSON_ARRAY('video_edit'),
        'video_extension', JSON_ARRAY('video_extension'),
        'omni_reference', JSON_ARRAY('references')
    )
)
WHERE adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%'
  AND asset_resolver = 'presigned_upload'
  AND JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.adapter.profile')) = 'json_task_v1';

-- Official full-capability keys do not need provider_mode.
UPDATE video_channels
SET extra_config = JSON_REMOVE(
    COALESCE(extra_config, JSON_OBJECT()),
    '$.adapter.request.fields.task_mode',
    '$.adapter.request.task_mode_map',
    '$.adapter.request.content_role_map'
)
WHERE adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%'
  AND JSON_UNQUOTE(JSON_EXTRACT(capabilities, '$.web_search')) = 'true';

-- H-channel Seedance 2.0 expired on 2026-08-14. Seedance 2.5 now supports
-- default generation, video editing, and video extension.
UPDATE video_channels
SET models = JSON_ARRAY('seedance-2.5'),
    capabilities = JSON_OBJECT(
        'first_frame', CAST('false' AS JSON),
        'last_frame', CAST('false' AS JSON),
        'cancel', CAST('true' AS JSON),
        'audio', CAST('true' AS JSON),
        'web_search', CAST('false' AS JSON)
    ),
    extra_config = JSON_SET(
        COALESCE(extra_config, JSON_OBJECT()),
        '$.adapter.request.fields.task_mode', 'provider_mode',
        '$.adapter.request.task_mode_map', JSON_OBJECT(
            'text', 'default',
            'references', 'default',
            'multimodal', 'default',
            'video_edit', 'video_edit',
            'video_extension', 'video_extension'
        ),
        '$.adapter.request.content_role_map', JSON_OBJECT(
            'video_edit', JSON_OBJECT('edit_source', 'edit_source'),
            'video_extension', JSON_OBJECT('source_video', 'extension_source')
        ),
        '$.adapter.validation.models', JSON_OBJECT(
            'seedance-2.5', JSON_OBJECT(
                'duration_min', 4,
                'duration_max', 20,
                'resolutions', JSON_ARRAY('480p', '720p'),
                'ratios', JSON_ARRAY('16:9', '9:16', '1:1', '4:3', '3:4', '21:9'),
                'task_modes', JSON_ARRAY('text', 'references', 'video_edit', 'video_extension'),
                'allow_generated_audio', CAST('true' AS JSON),
                'allowed_roles', JSON_ARRAY(
                    'reference_image', 'reference_video', 'reference_audio',
                    'edit_source', 'source_video'
                ),
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
                    'video_edit', JSON_OBJECT(
                        'allowed_roles', JSON_ARRAY('edit_source', 'reference_image', 'reference_audio'),
                        'min_media', 1,
                        'max_media', 41,
                        'exact_role_counts', JSON_OBJECT('edit_source', 1)
                    ),
                    'video_extension', JSON_OBJECT(
                        'allowed_roles', JSON_ARRAY('source_video', 'reference_image', 'reference_audio'),
                        'min_media', 1,
                        'max_media', 41,
                        'exact_role_counts', JSON_OBJECT('source_video', 1)
                    )
                ),
                'parameters', JSON_ARRAY(
                    JSON_OBJECT(
                        'name', 'h_channel_genre', 'label', '类型', 'type', 'select', 'default', 'general',
                        'options', JSON_ARRAY(
                            JSON_OBJECT('label', '通用', 'value', 'general'),
                            JSON_OBJECT('label', '电影', 'value', 'cinematic'),
                            JSON_OBJECT('label', '商业广告', 'value', 'commercial'),
                            JSON_OBJECT('label', '纪录片', 'value', 'documentary'),
                            JSON_OBJECT('label', '时尚', 'value', 'fashion'),
                            JSON_OBJECT('label', '动作', 'value', 'action')
                        )
                    ),
                    JSON_OBJECT(
                        'name', 'h_channel_style', 'label', '风格', 'type', 'select', 'default', 'auto',
                        'options', JSON_ARRAY(
                            JSON_OBJECT('label', '自动', 'value', 'auto'),
                            JSON_OBJECT('label', '写实', 'value', 'realistic'),
                            JSON_OBJECT('label', '电影感', 'value', 'cinematic'),
                            JSON_OBJECT('label', '黑色电影', 'value', 'film-noir'),
                            JSON_OBJECT('label', '复古胶片', 'value', 'vintage'),
                            JSON_OBJECT('label', '动画', 'value', 'anime')
                        )
                    ),
                    JSON_OBJECT(
                        'name', 'h_channel_camera', 'label', '镜头', 'type', 'select', 'default', 'auto',
                        'options', JSON_ARRAY(
                            JSON_OBJECT('label', '自动', 'value', 'auto'),
                            JSON_OBJECT('label', '24mm', 'value', '24mm'),
                            JSON_OBJECT('label', '35mm', 'value', '35mm'),
                            JSON_OBJECT('label', '50mm', 'value', '50mm'),
                            JSON_OBJECT('label', '75mm', 'value', '75mm'),
                            JSON_OBJECT('label', '85mm', 'value', '85mm')
                        )
                    ),
                    JSON_OBJECT(
                        'name', 'h_channel_priority_queue', 'label', '优先队列', 'type', 'select',
                        'default', CAST('false' AS JSON),
                        'conflicts_with', JSON_ARRAY('h_channel_points_vip'),
                        'options', JSON_ARRAY(
                            JSON_OBJECT('label', '关闭', 'value', CAST('false' AS JSON)),
                            JSON_OBJECT('label', '开启', 'value', CAST('true' AS JSON))
                        )
                    ),
                    JSON_OBJECT(
                        'name', 'h_channel_points_vip', 'label', '积分 VIP', 'type', 'select',
                        'default', CAST('false' AS JSON),
                        'conflicts_with', JSON_ARRAY('h_channel_priority_queue'),
                        'options', JSON_ARRAY(
                            JSON_OBJECT('label', '关闭', 'value', CAST('false' AS JSON)),
                            JSON_OBJECT(
                                'label', '开启', 'value', CAST('true' AS JSON),
                                'adds_resolutions', JSON_ARRAY('1080p')
                            )
                        )
                    ),
                    JSON_OBJECT(
                        'name', 'extension_mode', 'label', '拓展方向', 'type', 'select', 'default', 'forward',
                        'task_modes', JSON_ARRAY('video_extension'),
                        'options', JSON_ARRAY(
                            JSON_OBJECT('label', '向后续写', 'value', 'forward'),
                            JSON_OBJECT('label', '向前补写', 'value', 'backward')
                        )
                    )
                )
            )
        )
    )
WHERE name = 'hig逆-Seedance'
  AND adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%';

-- Jimeng accepts only omni_reference for Seedance 2.5.
UPDATE video_channels
SET extra_config = JSON_SET(
    JSON_REMOVE(COALESCE(extra_config, JSON_OBJECT()), '$.adapter.request.content_role_map'),
    '$.adapter.request.fields.task_mode', 'provider_mode',
    '$.adapter.request.task_mode_map', JSON_OBJECT(
        'references', 'omni_reference',
        'multimodal', 'omni_reference'
    )
)
WHERE adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%'
  AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(capabilities, '$.web_search')), 'false') <> 'true'
  AND JSON_CONTAINS(models, JSON_QUOTE('seedance-2.5'))
  AND JSON_UNQUOTE(JSON_EXTRACT(
      extra_config,
      '$.adapter.validation.models."seedance-2.5".require_media'
  )) = 'true'
  AND JSON_UNQUOTE(JSON_EXTRACT(
      extra_config,
      '$.adapter.validation.models."seedance-2.5".duration_max'
  )) = '30';

-- Configure Leonardo only when an administrator has already created the
-- channel and key. This migration never invents credentials or a new route.
UPDATE video_channels
SET models = JSON_ARRAY('seedance-2.5'),
    capabilities = JSON_OBJECT(
        'first_frame', CAST('true' AS JSON),
        'last_frame', CAST('true' AS JSON),
        'cancel', CAST('true' AS JSON),
        'audio', CAST('true' AS JSON),
        'web_search', CAST('false' AS JSON)
    ),
    extra_config = JSON_SET(
        COALESCE(extra_config, JSON_OBJECT()),
        '$.adapter.request.fields.task_mode', 'provider_mode',
        '$.adapter.request.task_mode_map', JSON_OBJECT(
            'text', 'default',
            'references', 'references',
            'first_frame', 'default',
            'first_last_frame', 'first_last_frame',
            'multimodal', 'references'
        ),
        '$.adapter.request.content_fields.client_ref_id', 'client_ref_id',
        '$.adapter.validation.models', JSON_OBJECT(
            'seedance-2.5', JSON_OBJECT(
                'duration_min', 4,
                'duration_max', 29,
                'duration_max_with_video_reference', 18,
                'resolutions', JSON_ARRAY('480p', '720p'),
                'ratios', JSON_ARRAY('21:9', '16:9', '4:3', '1:1', '3:4', '9:16'),
                'task_modes', JSON_ARRAY('text', 'references'),
                'allow_generated_audio', CAST('true' AS JSON),
                'require_visual_media_with_audio', CAST('true' AS JSON),
                'allowed_roles', JSON_ARRAY(
                    'first_frame', 'last_frame', 'reference_image', 'reference_video', 'reference_audio'
                ),
                'max_images', 30,
                'max_videos', 10,
                'max_audios', 10,
                'max_media', 50,
                'media_duration_min', 2,
                'media_duration_max', 15,
                'max_video_duration_total', 15,
                'max_audio_duration_total', 15,
                'forbidden_parameters', JSON_ARRAY('priority', 'return_last_frame', 'web_search', 'seed')
            )
        )
    )
WHERE adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%'
  AND name LIKE '%Leonardo%';
