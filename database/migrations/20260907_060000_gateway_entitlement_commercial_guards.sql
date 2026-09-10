-- Make entitlement and commercial validation first-class routing facts.
-- Existing offerings receive a deterministic entitlement identity, but no
-- validation state is invented; they remain ineligible until explicitly
-- verified by a control-plane run.

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_offerings` ADD COLUMN `entitlement_fingerprint` char(64) NULL AFTER `credential_pool_id`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_offerings'
      AND column_name = 'entitlement_fingerprint'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

UPDATE `gw_offerings` o
JOIN `gw_product_transports` pt
  ON pt.release_id=o.release_id AND pt.id=o.product_transport_id
JOIN `gw_products` p
  ON p.release_id=pt.release_id AND p.id=pt.product_id
JOIN `gw_channel_transports` ct
  ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id
SET o.entitlement_fingerprint=LOWER(SHA2(CONCAT(
    CAST(p.channel_id AS CHAR), CHAR(0), p.vendor_model, CHAR(0), ct.protocol,
    CHAR(0), pt.upstream_scope_kind, CHAR(0), pt.upstream_scope_key
), 256))
WHERE o.entitlement_fingerprint IS NULL;

-- Do not hide an incomplete backfill behind a nullable column. A dirty run is
-- retriable only after every offering has a deterministic fingerprint.
SELECT COUNT(*) INTO @prism_missing_entitlement_fingerprints
FROM `gw_offerings`
WHERE `entitlement_fingerprint` IS NULL;
SET @prism_ddl = IF(
    @prism_missing_entitlement_fingerprints > 0,
    -- SIGNAL is not supported by MySQL prepared statements. A fixed missing
    -- table keeps the validation failure conditional and deterministic.
    'SELECT * FROM `__prism_abort_missing_entitlement_fingerprints__`',
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 1 AND MAX(is_nullable) = 'YES',
        'ALTER TABLE `gw_offerings` MODIFY COLUMN `entitlement_fingerprint` char(64) NOT NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'gw_offerings'
      AND column_name = 'entitlement_fingerprint'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_offerings` ADD KEY `idx_gw_offerings_entitlement` (`entitlement_fingerprint`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_offerings'
      AND index_name = 'idx_gw_offerings_entitlement'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_credential_validation_events` ADD UNIQUE KEY `uq_gw_credential_validation_identity` (`id`, `credential_id`, `credential_version_id`, `entitlement_fingerprint`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_credential_validation_events'
      AND index_name = 'uq_gw_credential_validation_identity'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) > 0,
        'ALTER TABLE `gw_credential_entitlement_state` DROP FOREIGN KEY `fk_gw_credential_entitlement_state_event`',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE()
      AND table_name = 'gw_credential_entitlement_state'
      AND constraint_name = 'fk_gw_credential_entitlement_state_event'
      AND constraint_type = 'FOREIGN KEY'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_credential_entitlement_state` ADD CONSTRAINT `fk_gw_credential_entitlement_state_event_v2` FOREIGN KEY (`latest_event_id`, `credential_id`, `credential_version_id`, `entitlement_fingerprint`) REFERENCES `gw_credential_validation_events` (`id`, `credential_id`, `credential_version_id`, `entitlement_fingerprint`)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE()
      AND table_name = 'gw_credential_entitlement_state'
      AND constraint_name = 'fk_gw_credential_entitlement_state_event_v2'
      AND constraint_type = 'FOREIGN KEY'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_commercial_validation_events` ADD UNIQUE KEY `uq_gw_commercial_validation_identity` (`id`, `commercial_fingerprint`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_commercial_validation_events'
      AND index_name = 'uq_gw_commercial_validation_identity'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) > 0,
        'ALTER TABLE `gw_commercial_state` DROP FOREIGN KEY `fk_gw_commercial_state_event`',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE()
      AND table_name = 'gw_commercial_state'
      AND constraint_name = 'fk_gw_commercial_state_event'
      AND constraint_type = 'FOREIGN KEY'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_commercial_state` ADD CONSTRAINT `fk_gw_commercial_state_event_v2` FOREIGN KEY (`latest_event_id`, `commercial_fingerprint`) REFERENCES `gw_commercial_validation_events` (`id`, `commercial_fingerprint`)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE()
      AND table_name = 'gw_commercial_state'
      AND constraint_name = 'fk_gw_commercial_state_event_v2'
      AND constraint_type = 'FOREIGN KEY'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
