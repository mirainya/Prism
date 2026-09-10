-- Persist the exact outbound request and received response bodies as separate
-- encrypted blobs. Metadata lists never join the ciphertext, so large payloads
-- are decrypted only when an administrator explicitly inspects one exchange.

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_channel_request_logs` ADD COLUMN `request_payload_blob_id` bigint unsigned NULL AFTER `diagnostic_blob_id`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_channel_request_logs'
      AND column_name = 'request_payload_blob_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_channel_request_logs` ADD COLUMN `response_payload_blob_id` bigint unsigned NULL AFTER `request_payload_blob_id`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'gw_channel_request_logs'
      AND column_name = 'response_payload_blob_id'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_channel_request_logs` ADD CONSTRAINT `fk_gw_request_logs_request_payload_blob` FOREIGN KEY (`request_payload_blob_id`) REFERENCES `encrypted_blobs` (`id`)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE()
      AND table_name = 'gw_channel_request_logs'
      AND constraint_name = 'fk_gw_request_logs_request_payload_blob'
      AND constraint_type = 'FOREIGN KEY'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_channel_request_logs` ADD CONSTRAINT `fk_gw_request_logs_response_payload_blob` FOREIGN KEY (`response_payload_blob_id`) REFERENCES `encrypted_blobs` (`id`)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE()
      AND table_name = 'gw_channel_request_logs'
      AND constraint_name = 'fk_gw_request_logs_response_payload_blob'
      AND constraint_type = 'FOREIGN KEY'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
