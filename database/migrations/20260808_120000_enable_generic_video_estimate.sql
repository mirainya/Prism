-- Reason: use the upstream estimate endpoint for request-dependent video pricing.
-- Requirement: configure estimation through the generic JSON adapter without provider-specific runtime code.
-- Impact: generic video channels using the disposition upload protocol switch to upstream_estimate pricing.

UPDATE video_channels
SET pricing = JSON_SET(
        CASE WHEN JSON_TYPE(pricing) = 'OBJECT' THEN pricing ELSE JSON_OBJECT() END,
        '$.mode', 'upstream_estimate'
    ),
    extra_config = JSON_SET(
        CASE WHEN JSON_TYPE(extra_config) = 'OBJECT' THEN extra_config ELSE JSON_OBJECT() END,
        '$.adapter.estimate', JSON_OBJECT(
            'enabled', CAST('true' AS JSON),
            'method', 'POST',
            'path', '/v1/video-generations/estimate'
        ),
        '$.adapter.response.estimated_cost_paths', JSON_ARRAY(
            'data.estimated_cost',
            'estimated_cost'
        )
    )
WHERE adapter_type = 'generic'
  AND asset_resolver = 'presigned_upload'
  AND JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.adapter.profile')) = 'json_task_v1'
  AND JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.adapter.submit.path')) = '/v1/video-generations'
  AND JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.asset_resolver.apply_path')) = '/v1/video-generations/uploads';
