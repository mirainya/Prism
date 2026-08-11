-- Reason: Sub2 Seedance 2.0 models accept generation durations from 4 to 15 seconds.
-- Requirement: reject unsupported durations before submitting tasks upstream.
-- Impact: generic video channels advertise a minimum duration of 4 seconds for these models.

UPDATE video_channels
SET extra_config = JSON_SET(
    COALESCE(extra_config, JSON_OBJECT()),
    '$.adapter.validation.models."seedance-2.0".duration_min', 4
)
WHERE adapter_type = 'generic'
  AND JSON_EXTRACT(extra_config, '$.adapter.validation.models."seedance-2.0"') IS NOT NULL;

UPDATE video_channels
SET extra_config = JSON_SET(
    COALESCE(extra_config, JSON_OBJECT()),
    '$.adapter.validation.models."seedance-2.0-fast".duration_min', 4
)
WHERE adapter_type = 'generic'
  AND JSON_EXTRACT(extra_config, '$.adapter.validation.models."seedance-2.0-fast"') IS NOT NULL;
