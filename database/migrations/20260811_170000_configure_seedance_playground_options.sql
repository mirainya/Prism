-- Reason: remove Seedance model-name checks from the video playground.
-- Requirement: expose upstream extension parameters and media rules through generic adapter configuration.
-- Impact: updates only configured Sub2 generic channels that already declare Seedance model validation.

UPDATE video_channels
SET extra_config = JSON_SET(
    extra_config,
    '$.adapter.validation.models."seedance-2.0".require_visual_media_with_audio', CAST('true' AS JSON),
    '$.adapter.validation.models."seedance-2.0-fast".require_visual_media_with_audio', CAST('true' AS JSON),
    '$.adapter.validation.models."seedance-2.5".require_visual_media_with_audio', CAST('true' AS JSON),
    '$.adapter.validation.models."seedance-2.5".parameters', JSON_ARRAY(
        JSON_OBJECT(
            'name', 'priority',
            'label', '队列',
            'type', 'select',
            'default', 5,
            'options', JSON_ARRAY(
                JSON_OBJECT('label', '普通', 'value', 5),
                JSON_OBJECT('label', '优先', 'value', 4)
            )
        )
    )
)
WHERE adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan%'
  AND JSON_UNQUOTE(JSON_EXTRACT(extra_config, '$.adapter.profile')) = 'json_task_v1'
  AND JSON_EXTRACT(extra_config, '$.adapter.validation.models."seedance-2.5"') IS NOT NULL;
