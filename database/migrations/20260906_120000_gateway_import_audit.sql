-- Immutable audit facts for the one-way legacy import.
CREATE TABLE IF NOT EXISTS `gw_migration_runs` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `operation` varchar(64) NOT NULL,
    `source_revision_hmac` char(64) NOT NULL,
    `status` varchar(16) NOT NULL,
    `started_at` datetime(3) NOT NULL,
    `finished_at` datetime(3) NULL,
    `error_code` varchar(128) NOT NULL DEFAULT '',
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_migration_runs_operation_revision` (`operation`,`source_revision_hmac`),
    CONSTRAINT `ck_gw_migration_runs_status` CHECK (`status` IN ('scheduled','running','succeeded','failed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_migration_object_map` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `run_id` bigint unsigned NOT NULL,
    `source_table` varchar(128) NOT NULL,
    `source_pk` varchar(128) NOT NULL,
    `target_type` varchar(64) NOT NULL,
    `target_id` bigint unsigned NOT NULL,
    `target_discriminator` varchar(255) NOT NULL DEFAULT '',
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_migration_object_map_source_target` (`source_table`,`source_pk`,`target_type`,`target_discriminator`),
    CONSTRAINT `fk_gw_migration_object_map_run` FOREIGN KEY (`run_id`) REFERENCES `gw_migration_runs` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_migration_source_revisions` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `object_map_id` bigint unsigned NOT NULL,
    `source_hmac` char(64) NOT NULL,
    `observed_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_migration_source_revision` (`object_map_id`,`source_hmac`),
    CONSTRAINT `fk_gw_migration_source_revision_map` FOREIGN KEY (`object_map_id`) REFERENCES `gw_migration_object_map` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `gw_migration_issues` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `run_id` bigint unsigned NOT NULL,
    `source_table` varchar(128) NOT NULL,
    `source_pk` varchar(128) NOT NULL,
    `issue_code` varchar(128) NOT NULL,
    `detail` varchar(1000) NOT NULL,
    `status` varchar(16) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT `fk_gw_migration_issue_run` FOREIGN KEY (`run_id`) REFERENCES `gw_migration_runs` (`id`),
    CONSTRAINT `ck_gw_migration_issue_status` CHECK (`status` IN ('open','resolved','waived'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- The already completed first import is registered as an immutable succeeded
-- run and receives stable source-to-target maps for the rows it imported.
INSERT INTO gw_migration_runs(operation,source_revision_hmac,status,started_at,finished_at)
SELECT 'legacy_gateway_import', r.content_hash, 'succeeded', r.created_at, r.created_at
FROM gw_catalog_releases r
WHERE r.semantic_version='legacy-import-1'
  AND NOT EXISTS (SELECT 1 FROM gw_migration_runs m WHERE m.operation='legacy_gateway_import' AND m.source_revision_hmac=r.content_hash);

INSERT INTO gw_migration_object_map(run_id,source_table,source_pk,target_type,target_id,target_discriminator,created_at)
SELECT m.id,'gw_channels',SUBSTRING_INDEX(c.channel_code,'-',-1),'gateway_channel',c.id,c.channel_code,c.created_at
FROM gw_migration_runs m JOIN gateway_channels c ON c.channel_code LIKE 'legacy-channel-%'
WHERE m.operation='legacy_gateway_import'
  AND NOT EXISTS (SELECT 1 FROM gw_migration_object_map x WHERE x.source_table='gw_channels' AND x.source_pk=SUBSTRING_INDEX(c.channel_code,'-',-1) AND x.target_type='gateway_channel');

INSERT INTO gw_migration_object_map(run_id,source_table,source_pk,target_type,target_id,target_discriminator,created_at)
SELECT m.id,'gw_channel_keys',SUBSTRING_INDEX(c.credential_code,'-',-1),'credential',c.id,c.credential_code,c.created_at
FROM gw_migration_runs m JOIN gw_credentials c ON c.credential_code LIKE 'legacy-key-%'
WHERE m.operation='legacy_gateway_import'
  AND NOT EXISTS (SELECT 1 FROM gw_migration_object_map x WHERE x.source_table='gw_channel_keys' AND x.source_pk=SUBSTRING_INDEX(c.credential_code,'-',-1) AND x.target_type='credential');

INSERT INTO gw_migration_object_map(run_id,source_table,source_pk,target_type,target_id,target_discriminator,created_at)
SELECT m.id,'gw_abilities',SUBSTRING_INDEX(p.product_code,'-',-1),'product',p.id,p.product_code,p.created_at
FROM gw_migration_runs m JOIN gw_products p ON p.product_code LIKE 'legacy-product-%'
WHERE m.operation='legacy_gateway_import'
  AND NOT EXISTS (SELECT 1 FROM gw_migration_object_map x WHERE x.source_table='gw_abilities' AND x.source_pk=SUBSTRING_INDEX(p.product_code,'-',-1) AND x.target_type='product');
