-- Persist authenticated callback payloads and schedule their isolated
-- processing. Callback ingress never mutates execution state directly.
-- Each schema fact is guarded independently because MySQL DDL commits
-- implicitly.

SET @prism_schema = DATABASE();

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_upstream_callback_receipts` ADD COLUMN `encrypted_payload_blob_id` bigint unsigned NULL AFTER `payload_hmac`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_upstream_callback_receipts'
      AND column_name = 'encrypted_payload_blob_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_upstream_callback_receipts` ADD KEY `idx_gw_callback_receipts_processing` (`status`, `expires_at`, `id`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = @prism_schema AND table_name = 'gw_upstream_callback_receipts'
      AND index_name = 'idx_gw_callback_receipts_processing'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_upstream_callback_receipts` ADD CONSTRAINT `fk_gw_callback_receipts_payload_blob` FOREIGN KEY (`encrypted_payload_blob_id`) REFERENCES `encrypted_blobs` (`id`)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_upstream_callback_receipts'
      AND constraint_name = 'fk_gw_callback_receipts_payload_blob'
      AND constraint_type = 'FOREIGN KEY'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_async_outbox` ADD COLUMN `callback_receipt_id` bigint unsigned NULL AFTER `async_execution_id`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_async_outbox'
      AND column_name = 'callback_receipt_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_async_outbox` ADD UNIQUE KEY `uq_gw_async_outbox_callback_receipt_seq` (`callback_receipt_id`, `action_seq`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = @prism_schema AND table_name = 'gw_async_outbox'
      AND index_name = 'uq_gw_async_outbox_callback_receipt_seq'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_async_outbox` ADD CONSTRAINT `fk_gw_async_outbox_callback_receipt` FOREIGN KEY (`callback_receipt_id`) REFERENCES `gw_upstream_callback_receipts` (`id`)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_async_outbox'
      AND constraint_name = 'fk_gw_async_outbox_callback_receipt'
      AND constraint_type = 'FOREIGN KEY'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Replace the outbox root check after the callback parent is available. A
-- later migration adds result_delivery_id; when this migration is replayed
-- after that change, retain the later five-root invariant instead of briefly
-- installing the obsolete four-root check.
SET @prism_outbox_root_check = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_async_outbox'
          AND column_name = 'result_delivery_id'
    ),
    'ALTER TABLE `gw_async_outbox` ADD CONSTRAINT `ck_gw_async_outbox_one_root` CHECK ((`call_id` IS NOT NULL) + (`attempt_id` IS NOT NULL) + (`async_execution_id` IS NOT NULL) + (`callback_receipt_id` IS NOT NULL) + (`result_delivery_id` IS NOT NULL) = 1)',
    'ALTER TABLE `gw_async_outbox` ADD CONSTRAINT `ck_gw_async_outbox_one_root` CHECK ((`call_id` IS NOT NULL) + (`attempt_id` IS NOT NULL) + (`async_execution_id` IS NOT NULL) + (`callback_receipt_id` IS NOT NULL) = 1)'
);
SET @prism_ddl = (
    SELECT IF(COUNT(*) > 0,
        'ALTER TABLE `gw_async_outbox` DROP CHECK `ck_gw_async_outbox_one_root`',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_async_outbox'
      AND constraint_name = 'ck_gw_async_outbox_one_root'
      AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0, @prism_outbox_root_check, 'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_async_outbox'
      AND constraint_name = 'ck_gw_async_outbox_one_root'
      AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
