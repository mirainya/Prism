-- Move Files API metadata into the unified gateway and bind every file to one
-- immutable media object. Legacy ai_files rows remain read-only until the
-- dedicated object-storage importer proves every byte and ownership mapping.

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `tokens` ADD UNIQUE KEY `uq_tokens_id_user_id` (`id`, `user_id`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'tokens' AND index_name = 'uq_tokens_id_user_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_media_assets` ADD COLUMN `storage_locator` varchar(2048) NULL AFTER `object_key`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'gw_media_assets' AND column_name = 'storage_locator'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_media_assets` ADD UNIQUE KEY `uq_gw_media_assets_id_owner` (`id`, `user_id`, `token_id`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'gw_media_assets' AND index_name = 'uq_gw_media_assets_id_owner'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

CREATE TABLE IF NOT EXISTS `gw_file_resources` (
    `id` varchar(64) NOT NULL,
    `user_id` bigint unsigned NOT NULL,
    `token_id` bigint unsigned NOT NULL,
    `filename` varchar(255) NOT NULL,
    `purpose` varchar(40) NOT NULL,
    `bytes` bigint unsigned NOT NULL,
    `mime_type` varchar(120) NOT NULL,
    `status` varchar(24) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_file_resources_id_owner` (`id`, `user_id`, `token_id`),
    KEY `idx_gw_file_resources_token_created` (`token_id`, `created_at`, `id`),
    KEY `idx_gw_file_resources_user_created` (`user_id`, `created_at`, `id`),
    KEY `idx_gw_file_resources_purpose` (`purpose`),
    CONSTRAINT `fk_gw_file_resources_token_owner`
        FOREIGN KEY (`token_id`, `user_id`) REFERENCES `tokens` (`id`, `user_id`),
    CONSTRAINT `ck_gw_file_resources_bytes`
        CHECK (`bytes` > 0),
    CONSTRAINT `ck_gw_file_resources_status`
        CHECK (`status` IN ('uploading', 'processed', 'deleting', 'deleted', 'failed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @prism_ddl = (
    SELECT IF(COUNT(*) > 0,
        'ALTER TABLE `gw_media_asset_refs` DROP CHECK `ck_gw_media_asset_refs_parent`',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'gw_media_asset_refs'
      AND constraint_name = 'ck_gw_media_asset_refs_parent' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_media_asset_refs` ADD COLUMN `ai_file_id` varchar(64) NULL AFTER `ordinal`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'gw_media_asset_refs' AND column_name = 'ai_file_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_media_asset_refs` ADD UNIQUE KEY `uq_gw_media_asset_refs_file_role_ordinal` (`ai_file_id`, `role`, `ordinal`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'gw_media_asset_refs'
      AND index_name = 'uq_gw_media_asset_refs_file_role_ordinal'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_media_asset_refs` ADD CONSTRAINT `fk_gw_media_asset_refs_asset_owner` FOREIGN KEY (`media_asset_id`, `user_id`, `token_id`) REFERENCES `gw_media_assets` (`id`, `user_id`, `token_id`)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'gw_media_asset_refs'
      AND constraint_name = 'fk_gw_media_asset_refs_asset_owner' AND constraint_type = 'FOREIGN KEY'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_media_asset_refs` ADD CONSTRAINT `fk_gw_media_asset_refs_file_owner` FOREIGN KEY (`ai_file_id`, `user_id`, `token_id`) REFERENCES `gw_file_resources` (`id`, `user_id`, `token_id`)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'gw_media_asset_refs'
      AND constraint_name = 'fk_gw_media_asset_refs_file_owner' AND constraint_type = 'FOREIGN KEY'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_media_asset_refs` ADD CONSTRAINT `ck_gw_media_asset_refs_parent` CHECK ((`ai_file_id` IS NOT NULL) + (`call_id` IS NOT NULL) + (`result_delivery_id` IS NOT NULL) = 1)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'gw_media_asset_refs'
      AND constraint_name = 'ck_gw_media_asset_refs_parent' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

CREATE TABLE IF NOT EXISTS `gw_media_asset_state_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `media_asset_id` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `state_version` bigint unsigned NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_media_asset_state_events_version` (`media_asset_id`, `state_version`),
    CONSTRAINT `fk_gw_media_asset_state_events_asset`
        FOREIGN KEY (`media_asset_id`) REFERENCES `gw_media_assets` (`id`),
    CONSTRAINT `ck_gw_media_asset_state_events_old_state`
        CHECK (`old_state` IS NULL OR `old_state` IN ('staging', 'active', 'deleting', 'deleted')),
    CONSTRAINT `ck_gw_media_asset_state_events_new_state`
        CHECK (`new_state` IN ('staging', 'active', 'deleting', 'deleted'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
