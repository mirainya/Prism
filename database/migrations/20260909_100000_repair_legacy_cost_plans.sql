-- Repair the first legacy catalog import, whose draft offerings were created
-- before the importer persisted their corresponding cost plans.
--
-- Only the untouched legacy-import draft is eligible. Existing matching plans
-- are preserved, and each missing plan reuses the offering's selector and
-- creation timestamp so the repair does not invent pricing facts.

INSERT INTO `gw_cost_plans` (`release_id`, `offering_id`, `plan_code`, `created_at`)
SELECT o.`release_id`, o.`id`, o.`cost_plan_code`, o.`created_at`
FROM `gw_offerings` o
JOIN `gw_catalog_releases` r
  ON r.`id` = o.`release_id`
 AND r.`status` = 'draft'
 AND r.`semantic_version` = 'legacy-import-1'
WHERE NOT EXISTS (
    SELECT 1
    FROM `gw_cost_plans` p
    WHERE p.`release_id` = o.`release_id`
      AND p.`offering_id` = o.`id`
      AND p.`plan_code` = o.`cost_plan_code`
);
