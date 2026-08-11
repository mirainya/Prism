-- Preserve the existing Sub2API Seedance channel after the official Ark adapter becomes the default.
-- Scope: known Sub2API-backed video channels only; official Seedance channels are unchanged.
UPDATE video_channels
SET extra_config = JSON_SET(COALESCE(extra_config, JSON_OBJECT()), '$.protocol', 'sub2api'),
    capabilities = JSON_SET(COALESCE(capabilities, JSON_OBJECT()), '$.audio', CAST('true' AS JSON))
WHERE adapter_type = 'seedance'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%';
