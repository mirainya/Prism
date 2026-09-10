-- Replace the legacy full-key SHA-256 lookup with versioned credentials.
-- Existing one-way digests become an explicit legacy digest version so active
-- API credentials remain usable without retaining plaintext or forcing an
-- uncoordinated client rotation.
SET @prism_legacy_token_key_present = (
    SELECT COUNT(*)
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'tokens' AND column_name = 'key'
);

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `tokens` ADD COLUMN `selector` varchar(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER `user_id`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'tokens' AND column_name = 'selector'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `tokens` ADD COLUMN `secret_digest` binary(32) NULL AFTER `selector`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'tokens' AND column_name = 'secret_digest'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `tokens` ADD COLUMN `secret_digest_version` smallint unsigned NOT NULL DEFAULT 1 AFTER `secret_digest`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'tokens' AND column_name = 'secret_digest_version'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `tokens` ADD COLUMN `auth_version` bigint unsigned NOT NULL DEFAULT 1 AFTER `secret_digest_version`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'tokens' AND column_name = 'auth_version'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `tokens` ADD COLUMN `expires_at` datetime(3) NULL AFTER `status`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'tokens' AND column_name = 'expires_at'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `tokens` ADD COLUMN `revoked_at` datetime(3) NULL AFTER `expires_at`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'tokens' AND column_name = 'revoked_at'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `tokens` ADD UNIQUE KEY `uq_tokens_selector` (`selector`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'tokens' AND index_name = 'uq_tokens_selector'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `tokens` ADD CONSTRAINT `ck_tokens_secret_digest_version` CHECK (`secret_digest_version` IN (0,1))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'tokens'
      AND constraint_name = 'ck_tokens_secret_digest_version' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

CREATE TABLE IF NOT EXISTS `token_auth_state_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `token_id` bigint unsigned NOT NULL,
    `auth_version` bigint unsigned NOT NULL,
    `old_status` tinyint NOT NULL,
    `new_status` tinyint NOT NULL,
    `reason_code` varchar(64) NOT NULL,
    `actor_user_id` bigint unsigned NOT NULL DEFAULT 0,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_token_auth_event_version` (`token_id`, `auth_version`),
    CONSTRAINT `fk_token_auth_event_token` FOREIGN KEY (`token_id`) REFERENCES `tokens` (`id`),
    CONSTRAINT `ck_token_auth_event_status` CHECK (`old_status` IN (0,1) AND `new_status` IN (0,1))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @prism_ddl = IF(
    @prism_legacy_token_key_present > 0,
    'SELECT COUNT(*) INTO @prism_invalid_legacy_tokens FROM `tokens` WHERE `key` IS NULL OR CHAR_LENGTH(`key`) <> 64 OR `key` NOT REGEXP ''^[0-9A-Fa-f]{64}$''',
    'SET @prism_invalid_legacy_tokens = 0'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    @prism_invalid_legacy_tokens = 0,
    'SELECT 1',
    -- SIGNAL is not supported by MySQL prepared statements. A fixed missing
    -- table keeps the validation failure conditional and deterministic.
    'SELECT * FROM `__prism_abort_invalid_legacy_token_digest__`'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

INSERT INTO `token_auth_state_events`
    (`token_id`,`auth_version`,`old_status`,`new_status`,`reason_code`,`actor_user_id`,`created_at`)
SELECT `id`,`auth_version` + 1,`status`,`status`,'legacy_credential_migrated',0,UTC_TIMESTAMP(3)
FROM `tokens`
WHERE @prism_legacy_token_key_present > 0
  AND `selector` IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM `token_auth_state_events` existing
      WHERE existing.token_id=`tokens`.id AND existing.auth_version=`tokens`.auth_version + 1
  );

SET @prism_ddl = IF(
    @prism_legacy_token_key_present > 0,
    'UPDATE `tokens` SET `selector`=LOWER(`key`),`secret_digest`=UNHEX(`key`),`secret_digest_version`=0,`auth_version`=`auth_version`+1,`revoked_at`=IF(`status`=0,COALESCE(`revoked_at`,UTC_TIMESTAMP(3)),NULL) WHERE `selector` IS NULL',
    'SELECT 1'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

UPDATE `tokens`
SET `revoked_at` = UTC_TIMESTAMP(3)
WHERE `status` = 0 AND `revoked_at` IS NULL;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `tokens` ADD CONSTRAINT `ck_tokens_auth_state` CHECK (`auth_version` > 0 AND `selector` IS NOT NULL AND `secret_digest` IS NOT NULL AND ((`status` = 1 AND `revoked_at` IS NULL) OR (`status` = 0 AND `revoked_at` IS NOT NULL)))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'tokens'
      AND constraint_name = 'ck_tokens_auth_state' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) > 0,
        'ALTER TABLE `tokens` DROP INDEX `idx_tokens_key`',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'tokens' AND index_name = 'idx_tokens_key'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) > 0,
        'ALTER TABLE `tokens` DROP COLUMN `key`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'tokens' AND column_name = 'key'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
