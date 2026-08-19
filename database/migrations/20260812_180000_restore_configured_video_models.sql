-- Reason: the Sub2 v2.3 migration only selected H/Jimeng channels that already listed seedance-2.5.
-- Requirement: every configured validation model must remain selectable by routing and the playground.
-- Impact: appends Seedance 2.5 only to generic channels that already contain its validation rule.
-- Deployment: no task or credential data is changed; repeated execution is safe.

UPDATE video_channels
SET models = JSON_ARRAY_APPEND(COALESCE(models, JSON_ARRAY()), '$', 'seedance-2.5')
WHERE adapter_type = 'generic'
  AND JSON_EXTRACT(extra_config, '$.adapter.validation.models."seedance-2.5"') IS NOT NULL
  AND NOT JSON_CONTAINS(COALESCE(models, JSON_ARRAY()), JSON_QUOTE('seedance-2.5'));
