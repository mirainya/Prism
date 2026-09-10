-- Bind the runtime catalog pointer to one active deployment generation.
-- Worker claim transactions lock this singleton row as their execution fence.
--
-- MySQL DDL implicitly commits.  Add the column and foreign key in separate,
-- conditional statements so a dirty migration can resume after either DDL
-- statement has already committed.

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_catalog_runtime_state` ADD COLUMN `active_deployment_generation_id` bigint unsigned NULL AFTER `active_release_id`',
        'DO 0')
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_catalog_runtime_state'
      AND column_name = 'active_deployment_generation_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_catalog_runtime_state`
            ADD CONSTRAINT `fk_gw_catalog_runtime_state_deployment`
            FOREIGN KEY (`active_deployment_generation_id`) REFERENCES `gw_deployment_generations` (`id`)',
        'DO 0')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE()
      AND table_name = 'gw_catalog_runtime_state'
      AND constraint_name = 'fk_gw_catalog_runtime_state_deployment'
      AND constraint_type = 'FOREIGN KEY'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

UPDATE `gw_catalog_runtime_state` AS runtime
SET runtime.`active_deployment_generation_id` = (
    SELECT generation.`id`
    FROM `gw_deployment_generations` AS generation
    WHERE generation.`status` = 'active'
    ORDER BY generation.`id` DESC
    LIMIT 1
)
WHERE runtime.`id` = 1
  AND runtime.`active_deployment_generation_id` IS NULL;

-- CREATE TRIGGER is not supported by MySQL prepared statements. Migrations
-- run while application traffic is stopped, so recreate each protection
-- directly and make dirty retries deterministic.
DROP TRIGGER IF EXISTS `trg_gw_runtime_state_validate_insert`;
CREATE TRIGGER `trg_gw_runtime_state_validate_insert`
    BEFORE INSERT ON `gw_catalog_runtime_state`
    FOR EACH ROW
BEGIN
    IF NEW.`id` <> 1 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'gateway runtime state must be singleton';
    END IF;
    IF NEW.`active_release_id` IS NOT NULL AND
       (SELECT COUNT(*) FROM `gw_catalog_releases` WHERE `id`=NEW.`active_release_id` AND `status`='published') <> 1 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'active catalog release must be published';
    END IF;
    IF NEW.`active_deployment_generation_id` IS NOT NULL AND
       (SELECT COUNT(*) FROM `gw_deployment_generations` WHERE `id`=NEW.`active_deployment_generation_id` AND `status`='active') <> 1 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'active deployment generation must be active';
    END IF;
END;

DROP TRIGGER IF EXISTS `trg_gw_runtime_state_validate_update`;
CREATE TRIGGER `trg_gw_runtime_state_validate_update`
    BEFORE UPDATE ON `gw_catalog_runtime_state`
    FOR EACH ROW
BEGIN
    IF NEW.`id` <> 1 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'gateway runtime state must be singleton';
    END IF;
    IF NEW.`active_release_id` IS NOT NULL AND
       (SELECT COUNT(*) FROM `gw_catalog_releases` WHERE `id`=NEW.`active_release_id` AND `status`='published') <> 1 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'active catalog release must be published';
    END IF;
    IF NEW.`active_deployment_generation_id` IS NOT NULL AND
       (SELECT COUNT(*) FROM `gw_deployment_generations` WHERE `id`=NEW.`active_deployment_generation_id` AND `status`='active') <> 1 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'active deployment generation must be active';
    END IF;
END;
