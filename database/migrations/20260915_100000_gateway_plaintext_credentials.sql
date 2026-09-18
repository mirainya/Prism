-- Simplify upstream credential management.
-- New credentials use one direct secret column and no longer require a
-- credential keyring/version workflow. Existing encrypted rows remain readable
-- during migration and may be converted by the admin replace operation.

SET @prism_schema = DATABASE();
SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_credentials` ADD COLUMN `secret` TEXT NULL AFTER `credential_code`',
        'DO 0')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema
      AND table_name = 'gw_credentials'
      AND column_name = 'secret'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Plaintext-managed credentials still keep an active credential version for
-- joins, entitlement checks and request snapshots. Its encrypted blob is
-- intentionally NULL; legacy encrypted credentials continue using the blob.
SET @prism_ddl = (
    SELECT IF(COUNT(*) = 1,
        'ALTER TABLE `gw_credential_versions` MODIFY COLUMN `encrypted_blob_id` bigint unsigned NULL',
        'DO 0')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema
      AND table_name = 'gw_credential_versions'
      AND column_name = 'encrypted_blob_id'
      AND is_nullable = 'NO'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
