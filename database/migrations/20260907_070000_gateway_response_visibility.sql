-- Preserve immutable execution and billing facts when a client deletes a
-- stored Responses resource; only its public visibility is removed.

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_api_resources` ADD COLUMN `deleted_at` datetime(3) NULL AFTER `created_at`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_api_resources'
      AND column_name = 'deleted_at'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_api_resources` ADD KEY `idx_gw_api_resources_visibility` (`token_id`, `resource_kind`, `deleted_at`, `public_id`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_api_resources'
      AND index_name = 'idx_gw_api_resources_visibility'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
