-- Pin the exact upstream cost plan to every execution attempt.
-- Historical and in-flight attempts must not resolve cost through a mutable
-- offering selector after the provider request has been made.

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_plans` ADD UNIQUE KEY `uq_gw_cost_plans_release_offering_id` (`release_id`, `offering_id`, `id`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'gw_cost_plans'
      AND index_name = 'uq_gw_cost_plans_release_offering_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_api_call_attempts` ADD COLUMN `cost_plan_id` bigint unsigned NULL AFTER `offering_id`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'gw_api_call_attempts'
      AND column_name = 'cost_plan_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

UPDATE `gw_api_call_attempts` a
JOIN `gw_offerings` o
  ON o.release_id = a.catalog_release_id AND o.id = a.offering_id
JOIN `gw_cost_plans` p
  ON p.release_id = o.release_id
 AND p.offering_id = o.id
 AND p.plan_code = o.cost_plan_code
SET a.cost_plan_id = p.id
WHERE a.cost_plan_id IS NULL;

-- Refuse to finalize the migration while historical attempts remain unmapped.
-- This keeps a dirty run explicit and allows a later retry after the source
-- rows or catalog mappings have been repaired.
SELECT COUNT(*) INTO @prism_missing_cost_plan_ids
FROM `gw_api_call_attempts`
WHERE `cost_plan_id` IS NULL;
SET @prism_ddl = IF(
    @prism_missing_cost_plan_ids > 0,
    -- SIGNAL is not supported by MySQL prepared statements. A fixed missing
    -- table keeps the validation failure conditional and deterministic.
    'SELECT * FROM `__prism_abort_missing_cost_plan_ids__`',
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 1 AND MAX(is_nullable) = 'YES',
        'ALTER TABLE `gw_api_call_attempts` MODIFY COLUMN `cost_plan_id` bigint unsigned NOT NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'gw_api_call_attempts'
      AND column_name = 'cost_plan_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_api_call_attempts` ADD CONSTRAINT `fk_gw_api_call_attempts_cost_plan` FOREIGN KEY (`catalog_release_id`, `offering_id`, `cost_plan_id`) REFERENCES `gw_cost_plans` (`release_id`, `offering_id`, `id`)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'gw_api_call_attempts'
      AND constraint_name = 'fk_gw_api_call_attempts_cost_plan'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
