-- Reason: the v2.3 H-channel profile was skipped for the original Sub2 channel
-- because its model list did not yet contain seedance-2.5; a later repair only
-- appended the model and left the Jimeng validation rules in place.
-- Requirement: make the hig逆-Seedance channel match the documented H matrix.
-- Impact: updates static video validation and local-cancel policy only; tasks,
-- keys, pricing, and adapter transport are unchanged. Safe to rerun.

UPDATE video_channels
SET models = JSON_ARRAY('seedance-2.0', 'seedance-2.5'),
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
            'seedance-2.0', JSON_OBJECT(
                'duration_min', 4,
                'duration_max', 15,
                'resolutions', JSON_ARRAY('1080p'),
                'ratios', JSON_ARRAY('16:9', '9:16', '1:1', '4:3', '3:4', '21:9'),
                'task_modes', JSON_ARRAY('text', 'references'),
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
            ),
            'seedance-2.5', JSON_OBJECT(
                'duration_min', 4,
                'duration_max', 20,
                'resolutions', JSON_ARRAY('480p', '720p'),
                'ratios', JSON_ARRAY('16:9', '9:16', '1:1', '4:3', '3:4', '21:9'),
                'task_modes', JSON_ARRAY('text', 'references', 'video_extension'),
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
            )
        )
    )
WHERE name = 'hig逆-Seedance'
  AND adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%';
