-- Reason: endpoint configuration must retain its creation source independently from current Key bindings.
-- Requirement: distinguish manual, Key-discovered, imported, and historical endpoints in admin views.
-- Impact: adds provenance metadata to endpoints and infers a non-authoritative source snapshot for existing rows.

ALTER TABLE `endpoints`
  ADD COLUMN `origin_type` varchar(32) NOT NULL DEFAULT 'legacy_unknown' AFTER `account_id`,
  ADD COLUMN `origin_account_id` bigint unsigned NOT NULL DEFAULT 0 AFTER `origin_type`,
  ADD COLUMN `origin_snapshot` JSON NULL AFTER `origin_account_id`,
  ADD COLUMN `discovered_at` datetime(3) NULL AFTER `origin_snapshot`,
  ADD KEY `idx_endpoints_origin_type` (`origin_type`),
  ADD KEY `idx_endpoints_origin_account` (`origin_account_id`);

UPDATE `endpoints` AS e
LEFT JOIN `channels` AS c ON c.`id` = e.`channel_id`
LEFT JOIN (
  SELECT `endpoint_id`, MIN(`account_id`) AS `account_id`
  FROM `endpoint_accounts`
  GROUP BY `endpoint_id`
) AS binding_source ON binding_source.`endpoint_id` = e.`id`
LEFT JOIN `channel_accounts` AS ca
  ON ca.`id` = CASE
    WHEN e.`account_id` <> 0 THEN e.`account_id`
    ELSE COALESCE(binding_source.`account_id`, 0)
  END
SET
  e.`origin_type` = CASE
    WHEN ca.`id` IS NULL THEN 'legacy_unknown'
    ELSE 'legacy_inferred'
  END,
  e.`origin_account_id` = COALESCE(ca.`id`, 0),
  e.`origin_snapshot` = JSON_OBJECT(
    'channel_id', e.`channel_id`,
    'channel_name', COALESCE(c.`name`, ''),
    'channel_type', COALESCE(c.`type`, ''),
    'account_id', COALESCE(ca.`id`, 0),
    'account_name', COALESCE(ca.`name`, ''),
    'vendor_model', e.`vendor_model`,
    'inferred', TRUE
  )
WHERE e.`deleted_at` IS NULL;
