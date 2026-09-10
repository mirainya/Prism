-- Make client callback delivery durable and lease-fenced. Callback URLs and
-- payloads remain encrypted; only scheduling and delivery evidence is stored
-- in queryable columns.
-- MySQL DDL implicitly commits, so each column and index is guarded separately.

SET @prism_schema = DATABASE();

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_callback_deliveries'
          AND column_name = 'available_at'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_callback_deliveries` ADD COLUMN `available_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) AFTER `state_version`'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_callback_deliveries'
          AND column_name = 'attempt_count'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_callback_deliveries` ADD COLUMN `attempt_count` bigint unsigned NOT NULL DEFAULT 0 AFTER `available_at`'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_callback_deliveries'
          AND column_name = 'max_attempts'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_callback_deliveries` ADD COLUMN `max_attempts` int unsigned NOT NULL DEFAULT 5 AFTER `attempt_count`'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_callback_deliveries'
          AND column_name = 'lease_owner'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_callback_deliveries` ADD COLUMN `lease_owner` varchar(128) NOT NULL DEFAULT '''' AFTER `max_attempts`'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_callback_deliveries'
          AND column_name = 'lease_expires_at'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_callback_deliveries` ADD COLUMN `lease_expires_at` datetime(3) NULL AFTER `lease_owner`'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_callback_deliveries'
          AND column_name = 'last_error_code'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_callback_deliveries` ADD COLUMN `last_error_code` varchar(128) NOT NULL DEFAULT '''' AFTER `lease_expires_at`'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'gw_callback_deliveries'
          AND column_name = 'completed_at'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_callback_deliveries` ADD COLUMN `completed_at` datetime(3) NULL AFTER `last_error_code`'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_callback_deliveries'
          AND index_name = 'idx_gw_callback_deliveries_dispatch'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_callback_deliveries` ADD KEY `idx_gw_callback_deliveries_dispatch` (`state`, `available_at`, `id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'gw_callback_deliveries'
          AND index_name = 'idx_gw_callback_deliveries_lease'
    ),
    'SELECT 1',
    'ALTER TABLE `gw_callback_deliveries` ADD KEY `idx_gw_callback_deliveries_lease` (`state`, `lease_expires_at`, `id`)'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

UPDATE `gw_callback_deliveries` AS delivery
LEFT JOIN (
    SELECT `callback_delivery_id`, MAX(`attempt_no`) AS `attempt_count`
    FROM `gw_callback_delivery_attempts`
    GROUP BY `callback_delivery_id`
) AS attempts ON attempts.`callback_delivery_id` = delivery.`id`
SET delivery.`attempt_count` = GREATEST(delivery.`attempt_count`, COALESCE(attempts.`attempt_count`, 0));

UPDATE `gw_callback_delivery_attempts` AS attempt
JOIN `gw_callback_deliveries` AS delivery
  ON delivery.`id` = attempt.`callback_delivery_id`
 AND delivery.`attempt_count` = attempt.`attempt_no`
SET attempt.`state` = 'unknown', attempt.`error_code` = 'worker_lease_expired'
WHERE delivery.`state` = 'sending' AND delivery.`lease_expires_at` IS NULL
  AND attempt.`state` = 'dispatching';

UPDATE `gw_callback_deliveries`
SET `state` = 'failed', `available_at` = UTC_TIMESTAMP(3),
    `lease_owner` = '', `last_error_code` = 'worker_lease_expired', `updated_at` = UTC_TIMESTAMP(3)
WHERE `state` = 'sending' AND `lease_expires_at` IS NULL;

UPDATE `gw_callback_deliveries`
SET `completed_at` = `updated_at`
WHERE `state` IN ('succeeded', 'dead_letter') AND `completed_at` IS NULL;
