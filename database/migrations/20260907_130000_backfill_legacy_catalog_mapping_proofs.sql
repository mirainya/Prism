-- Complete the audit evidence for V1 catalog imports registered after the
-- original import had already finished. Target graph integrity is verified by
-- the replacement command before any catalog row can be removed.

INSERT INTO `gw_migration_source_revisions`
    (`object_map_id`,`source_hmac`,`observed_at`)
SELECT mapping.id,run.source_revision_hmac,GREATEST(mapping.created_at,run.finished_at)
FROM `gw_migration_object_map` mapping
JOIN `gw_migration_runs` run
  ON run.id=mapping.run_id
 AND run.operation='legacy_gateway_import'
 AND run.status='succeeded'
 AND run.finished_at IS NOT NULL
WHERE NOT EXISTS (
    SELECT 1
    FROM `gw_migration_source_revisions` revision
    WHERE revision.object_map_id=mapping.id
      AND revision.source_hmac=run.source_revision_hmac
);

INSERT INTO `gw_migration_mapping_proofs`
    (`run_id`,`object_map_id`,`source_hmac`,`observed_at`)
SELECT run.id,mapping.id,revision.source_hmac,
       GREATEST(mapping.created_at,revision.observed_at,run.finished_at)
FROM `gw_migration_object_map` mapping
JOIN `gw_migration_runs` run
  ON run.id=mapping.run_id
 AND run.operation='legacy_gateway_import'
 AND run.status='succeeded'
 AND run.finished_at IS NOT NULL
JOIN `gw_migration_source_revisions` revision
  ON revision.object_map_id=mapping.id
 AND revision.source_hmac=run.source_revision_hmac
WHERE NOT EXISTS (
    SELECT 1
    FROM `gw_migration_mapping_proofs` proof
    WHERE proof.run_id=run.id
      AND proof.object_map_id=mapping.id
);
