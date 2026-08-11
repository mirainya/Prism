-- Reason: the H channel rejects Seedance 2.0 resolutions other than 1080p.
-- Requirement: prevent Prism validation and the playground from offering invalid resolutions.
-- Impact: matching Sub2 H channels advertise only 1080p for seedance-2.0.

UPDATE video_channels
SET extra_config = JSON_SET(
    COALESCE(extra_config, JSON_OBJECT()),
    '$.adapter.validation.models."seedance-2.0".resolutions',
    JSON_ARRAY('1080p')
)
WHERE adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%'
  AND JSON_EXTRACT(extra_config, '$.adapter.validation.models."seedance-2.0"') IS NOT NULL;
