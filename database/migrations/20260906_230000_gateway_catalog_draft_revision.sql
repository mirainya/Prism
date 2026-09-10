-- Track optimistic revisions for editable catalog drafts.
-- Published and retired releases remain immutable; this version only guards
-- concurrent administrative edits before publication.
-- MySQL DDL implicitly commits, so each column change is guarded independently.

SET @prism_schema = DATABASE();

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_catalog_releases` ADD COLUMN `config_version` bigint unsigned NOT NULL DEFAULT 1 AFTER `status`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_catalog_releases'
      AND column_name = 'config_version'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_catalog_releases` ADD COLUMN `updated_at` datetime(3) NULL AFTER `created_at`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_catalog_releases'
      AND column_name = 'updated_at'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

UPDATE `gw_catalog_releases`
SET `updated_at` = `created_at`
WHERE `updated_at` IS NULL;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 1 AND MAX(is_nullable) = 'YES',
        'ALTER TABLE `gw_catalog_releases` MODIFY COLUMN `updated_at` datetime(3) NOT NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_catalog_releases'
      AND column_name = 'updated_at'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
