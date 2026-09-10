-- Bind every reused source-to-target mapping to the exact import run and
-- source revision that verified it. A mapping can outlive a failed run, but
-- cleanup accepts it only after the latest snapshot proves it again.
CREATE TABLE IF NOT EXISTS `gw_migration_mapping_proofs` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `run_id` bigint unsigned NOT NULL,
    `object_map_id` bigint unsigned NOT NULL,
    `source_hmac` char(64) NOT NULL,
    `observed_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_migration_mapping_proof_run_map` (`run_id`,`object_map_id`),
    KEY `idx_gw_migration_mapping_proof_map` (`object_map_id`,`source_hmac`),
    CONSTRAINT `fk_gw_migration_mapping_proof_run`
        FOREIGN KEY (`run_id`) REFERENCES `gw_migration_runs` (`id`),
    CONSTRAINT `fk_gw_migration_mapping_proof_revision`
        FOREIGN KEY (`object_map_id`,`source_hmac`)
        REFERENCES `gw_migration_source_revisions` (`object_map_id`,`source_hmac`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Existing mappings are proved only when their creating run already
-- succeeded. Failed runs must be replayed before cleanup can trust them.
INSERT INTO `gw_migration_mapping_proofs` (`run_id`,`object_map_id`,`source_hmac`,`observed_at`)
SELECT m.run_id,m.id,sr.source_hmac,GREATEST(m.created_at,sr.observed_at)
FROM gw_migration_object_map m
JOIN gw_migration_runs r ON r.id=m.run_id AND r.status='succeeded'
JOIN gw_migration_source_revisions sr ON sr.id=(
    SELECT MAX(sr2.id)
    FROM gw_migration_source_revisions sr2
    WHERE sr2.object_map_id=m.id
) WHERE NOT EXISTS (
    SELECT 1
    FROM gw_migration_mapping_proofs p
    WHERE p.run_id=m.run_id AND p.object_map_id=m.id
);

-- MySQL does not allow CREATE TRIGGER through the prepared-statement
-- protocol. These single-statement triggers are safe to recreate on retry.
DROP TRIGGER IF EXISTS `trg_gw_migration_mapping_proof_no_update`;
CREATE TRIGGER `trg_gw_migration_mapping_proof_no_update`
    BEFORE UPDATE ON `gw_migration_mapping_proofs`
    FOR EACH ROW SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Migration mapping proofs are immutable';

DROP TRIGGER IF EXISTS `trg_gw_migration_mapping_proof_no_delete`;
CREATE TRIGGER `trg_gw_migration_mapping_proof_no_delete`
    BEFORE DELETE ON `gw_migration_mapping_proofs`
    FOR EACH ROW SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Migration mapping proofs are immutable';
