-- Reason: move Sub2 ordinary video tasks from the provider-named Seedance adapter to the reusable generic JSON adapter.
-- Requirement: keep the existing request, response, model limits, and local-cancel behavior as channel configuration.
-- Impact: matching Sub2 channels become adapter_type=generic; the dedicated seedance protocol branch is no longer required.

UPDATE video_channels
SET adapter_type = 'generic',
    base_url = CASE
        WHEN RIGHT(TRIM(TRAILING '/' FROM base_url), 3) = '/v1'
            THEN LEFT(TRIM(TRAILING '/' FROM base_url), CHAR_LENGTH(TRIM(TRAILING '/' FROM base_url)) - 3)
        ELSE TRIM(TRAILING '/' FROM base_url)
    END,
    extra_config = JSON_SET(
        JSON_REMOVE(COALESCE(extra_config, JSON_OBJECT()), '$.protocol', '$.submit_path', '$.poll_path', '$.cancel_path'),
        '$.adapter',
        JSON_OBJECT(
            'profile', 'json_task_v1',
            'auth_header', 'Authorization',
            'auth_prefix', 'Bearer ',
            'timeout_seconds', 30,
            'submit', JSON_OBJECT('enabled', CAST('true' AS JSON), 'method', 'POST', 'path', '/v1/video-generations'),
            'poll', JSON_OBJECT('enabled', CAST('true' AS JSON), 'method', 'GET', 'path', '/v1/video-generations/{task_id}'),
            'cancel', JSON_OBJECT('enabled', CAST('false' AS JSON), 'method', 'POST', 'path', '/v1/video-generations/{task_id}/cancel', 'allowed_statuses', JSON_ARRAY('submitted', 'tracking')),
            'local_cancel', JSON_OBJECT(
                'enabled', CAST('true' AS JSON),
                'disabled_models', JSON_ARRAY('seedance-2.5')
            ),
            'request', JSON_OBJECT(
                'fields', JSON_OBJECT(
                    'model', 'model', 'prompt', 'prompt', 'resolution', 'resolution', 'ratio', 'ratio',
                    'duration', 'duration', 'audio', 'generate_audio', 'task_mode', 'task_mode'
                ),
                'content_path', 'content',
                'content_fields', JSON_OBJECT(
                    'type', 'type', 'role', 'role', 'text', 'text', 'url', 'url',
                    'provider_object', 'storage_object_id', 'duration', 'duration_seconds'
                ),
                'params_mode', 'merge_missing',
                'request_id_header', 'X-Request-ID'
            ),
            'response', JSON_OBJECT(
                'success_code_path', 'code',
                'success_code_values', JSON_ARRAY('0'),
                'success_code_optional', CAST('true' AS JSON),
                'task_id_paths', JSON_ARRAY('data.id', 'id'),
                'status_paths', JSON_ARRAY('data.status', 'status'),
                'progress_paths', JSON_ARRAY('data.progress', 'progress'),
                'video_url_paths', JSON_ARRAY(
                    'data.result.video_url', 'data.content.video_url', 'data.video_url',
                    'result.video_url', 'content.video_url', 'video_url'
                ),
                'thumbnail_url_paths', JSON_ARRAY(
                    'data.result.thumbnail_url', 'data.result.last_frame_url', 'data.content.last_frame_url', 'data.thumbnail_url',
                    'result.thumbnail_url', 'result.last_frame_url', 'content.last_frame_url', 'thumbnail_url'
                ),
                'duration_paths', JSON_ARRAY('data.result.duration', 'data.duration', 'result.duration', 'duration'),
                'error_paths', JSON_ARRAY('data.error.message', 'data.error', 'data.message', 'error.message', 'error', 'message'),
                'status_map', JSON_OBJECT(
                    'queued', 'submitted', 'created', 'submitted', 'submitted', 'submitted', 'pending', 'submitted', 'waiting', 'submitted',
                    'running', 'tracking', 'processing', 'tracking', 'in_progress', 'tracking',
                    'succeeded', 'completed', 'success', 'completed', 'completed', 'completed',
                    'failed', 'failed', 'error', 'failed', 'expired', 'failed', 'canceled', 'cancelled', 'cancelled', 'cancelled'
                ),
                'submit_default_status', 'submitted',
                'poll_default_status', 'tracking',
                'unknown_status', 'tracking'
            ),
            'validation', JSON_OBJECT(
                'models', JSON_OBJECT(
                    'seedance-2.0', JSON_OBJECT(
                        'duration_min', 2, 'duration_max', 15,
                        'resolutions', JSON_ARRAY('480p', '720p', '1080p', '4k'),
                        'max_images', 9, 'max_videos', 3, 'max_audios', 3, 'max_media', 15,
                        'media_duration_min', 2, 'media_duration_max', 15,
                        'max_video_duration_total', 15, 'max_audio_duration_total', 15
                    ),
                    'seedance-2.0-fast', JSON_OBJECT(
                        'duration_min', 2, 'duration_max', 15,
                        'resolutions', JSON_ARRAY('480p', '720p'),
                        'max_images', 9, 'max_videos', 3, 'max_audios', 3, 'max_media', 15,
                        'media_duration_min', 2, 'media_duration_max', 15,
                        'max_video_duration_total', 15, 'max_audio_duration_total', 15
                    ),
                    'seedance-2.5', JSON_OBJECT(
                        'duration_min', 4, 'duration_max', 30,
                        'resolutions', JSON_ARRAY('480p', '720p'),
                        'ratios', JSON_ARRAY('21:9', '16:9', '4:3', '1:1', '3:4', '9:16'),
                        'task_modes', JSON_ARRAY('references'),
                        'require_media', CAST('true' AS JSON),
                        'allow_generated_audio', CAST('false' AS JSON),
                        'allowed_roles', JSON_ARRAY('reference_image', 'reference_video', 'reference_audio'),
                        'max_images', 30, 'max_videos', 10, 'max_audios', 10, 'max_media', 50,
                        'media_duration_min', 2, 'media_duration_max', 30,
                        'max_video_duration_total', 30, 'max_audio_duration_total', 30
                    )
                )
            )
        )
    )
WHERE adapter_type = 'seedance'
  AND JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.protocol')) = 'sub2api';
