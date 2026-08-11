-- Use the v2.2 Cloudflare R2 material resolver for Sub2API Seedance channels.
-- Scope: Sub2API-backed Seedance channels only; official Seedance channels remain direct_url.
UPDATE video_channels
SET asset_resolver = 'sub2_r2'
WHERE adapter_type = 'seedance'
  AND JSON_UNQUOTE(JSON_EXTRACT(COALESCE(extra_config, JSON_OBJECT()), '$.protocol')) = 'sub2api'
  AND base_url LIKE '%sub2api.0x0.fan%';
