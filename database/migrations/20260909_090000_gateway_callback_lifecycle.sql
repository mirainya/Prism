-- Bound callback tokens and authenticated callback bodies are temporary
-- execution evidence. Persist their lifecycle state so an old token cannot
-- be used after its execution window and retention workers can remove the
-- encrypted material without deleting immutable audit facts.
--
-- Every schema change is guarded by information_schema. The migration is
-- intentionally safe to execute again after a partial DDL application.

-- The alias Blob reference is detached before ciphertext deletion. It must be
-- nullable so expired aliases can retain immutable identity metadata without
-- keeping the ciphertext alive through a foreign key.
SET @prism_ddl = (
    SELECT IF(COUNT(*) = 1 AND MAX(is_nullable) = 'NO',
        'ALTER TABLE `gw_callback_binding_token_aliases`
            MODIFY COLUMN `encrypted_blob_id` bigint unsigned NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_callback_binding_token_aliases'
      AND column_name = 'encrypted_blob_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Add lifecycle columns independently. Independent guards handle an
-- interrupted ALTER that added only a subset of the columns.
SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_callback_binding_token_aliases`
            ADD COLUMN `status` varchar(16) NULL AFTER `encrypted_blob_id`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_callback_binding_token_aliases'
      AND column_name = 'status'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_callback_binding_token_aliases`
            ADD COLUMN `expires_at` datetime(3) NULL AFTER `status`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_callback_binding_token_aliases'
      AND column_name = 'expires_at'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_callback_binding_token_aliases`
            ADD COLUMN `invalidated_at` datetime(3) NULL AFTER `expires_at`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_callback_binding_token_aliases'
      AND column_name = 'invalidated_at'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Legacy aliases have no lifecycle fields. A nullable add leaves them NULL,
-- allowing an exact backfill from their immutable creation time. Invalid
-- sentinel values from a partial run are corrected as well.
UPDATE `gw_callback_binding_token_aliases`
SET `status` = 'active'
WHERE `status` IS NULL OR `status` = '';

UPDATE `gw_callback_binding_token_aliases`
SET `expires_at` = DATE_ADD(`created_at`, INTERVAL 24 HOUR)
WHERE `expires_at` IS NULL OR `expires_at` <= `created_at`;

-- Enforce the runtime shape after data backfill. These MODIFY statements are
-- guarded so re-running the migration does not rewrite an already-correct
-- column definition.
SET @prism_ddl = (
    SELECT IF(COUNT(*) = 1 AND (MAX(is_nullable) = 'YES' OR MAX(column_default) IS NULL),
        'ALTER TABLE `gw_callback_binding_token_aliases`
            MODIFY COLUMN `status` varchar(16) NOT NULL DEFAULT ''active''',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_callback_binding_token_aliases'
      AND column_name = 'status'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 1 AND MAX(is_nullable) = 'YES',
        'ALTER TABLE `gw_callback_binding_token_aliases`
            MODIFY COLUMN `expires_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_callback_binding_token_aliases'
      AND column_name = 'expires_at'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_callback_binding_token_aliases`
            ADD KEY `idx_gw_callback_binding_alias_lifecycle` (`status`, `expires_at`, `async_execution_id`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_callback_binding_token_aliases'
      AND index_name = 'idx_gw_callback_binding_alias_lifecycle'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_callback_binding_token_aliases`
            ADD CONSTRAINT `ck_gw_callback_binding_alias_status`
            CHECK (`status` IN (''active'', ''expired'', ''revoked''))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_callback_binding_token_aliases'
      AND constraint_name = 'ck_gw_callback_binding_alias_status'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- The parent relation is exclusive: one Receipt belongs either to an
-- upstream task identity or to an async execution, never both.
SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_upstream_callback_receipts`
            ADD CONSTRAINT `ck_gw_callback_receipts_parent_exclusive`
            CHECK ((`task_identity_id` IS NOT NULL) + (`async_execution_id` IS NOT NULL) = 1)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_upstream_callback_receipts'
      AND constraint_name = 'ck_gw_callback_receipts_parent_exclusive'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
