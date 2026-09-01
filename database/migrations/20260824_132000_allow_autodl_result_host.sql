-- Reason: AutoDL returns completed videos from Tencent COS. On this server,
-- the COS hostname resolves to the platform's link-local egress address.
-- Impact: only the configured AutoDL result hostname may use the trusted
-- provider download path; user-provided URLs keep strict public-IP checks.

UPDATE video_channels
SET extra_config = JSON_SET(
    COALESCE(extra_config, JSON_OBJECT()),
    '$.result_storage.trusted_hosts',
    JSON_ARRAY('codewithgpu-image-1310972338.cos.ap-beijing.myqcloud.com')
)
WHERE name = 'AutoDL-Minimax H3 多图多音频生视频'
  AND adapter_type = 'generic'
  AND base_url = 'https://autodl.art';
