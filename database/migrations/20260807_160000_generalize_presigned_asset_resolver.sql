-- Reason: replace the provider-named Sub2 R2 resolver with the reusable presigned upload plugin.
-- Requirement: preserve the v2.2 disposition upload behavior as validated channel configuration.
-- Impact: updates matching video channel resolver names and adds their asset resolver configuration.

UPDATE video_channels
SET asset_resolver = 'presigned_upload',
    extra_config = JSON_SET(
        COALESCE(extra_config, JSON_OBJECT()),
        '$.asset_resolver',
        JSON_OBJECT(
            'profile', 'disposition_v1',
            'apply_path', '/v1/video-generations/uploads',
            'wait_path', '/v1/video-generations/uploads/wait',
            'complete_path', '/v1/video-generations/uploads/complete',
            'abort_path', '/v1/video-generations/uploads/abort',
            'response_root', 'data',
            'success_code_path', 'code',
            'reference_paths', JSON_ARRAY(
                'storage_object_id',
                'reference.storage_object_id',
                'reference.id',
                'reference'
            ),
            'reference_expiry_paths', JSON_ARRAY('expires_at', 'reference.expires_at'),
            'idempotency_header', 'Idempotency-Key',
            'purpose', 'reference',
            'max_wait_seconds', 120,
            'part_concurrency', 3,
            'control_retries', 2,
            'part_retries', 2,
            'max_session_restarts', 1
        )
    )
WHERE asset_resolver = 'sub2_r2';
