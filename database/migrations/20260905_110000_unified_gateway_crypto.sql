-- Unified gateway cryptographic storage primitives.
-- KEK material is held by the configured KMS/keyring, never in MySQL. These
-- tables store ciphertext, wrapped DEKs, versions, and integrity metadata only.

CREATE TABLE IF NOT EXISTS `crypto_keyring_state` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `purpose` varchar(64) NOT NULL,
    `current_version` int unsigned NULL,
    `created_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_crypto_keyring_state_purpose` (`purpose`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `crypto_key_versions` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `keyring_id` bigint unsigned NOT NULL,
    `key_version` int unsigned NOT NULL,
    `status` varchar(16) NOT NULL,
    `provider_key_ref` varchar(255) NOT NULL,
    `algorithm` varchar(32) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    `retired_at` datetime(3) NULL,
    `current_keyring_id` bigint unsigned GENERATED ALWAYS AS (
        CASE WHEN `status` = 'current' THEN `keyring_id` ELSE NULL END
    ) STORED,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_crypto_key_versions_ring_version` (`keyring_id`, `key_version`),
    UNIQUE KEY `uq_crypto_key_versions_ring_id_id` (`keyring_id`, `id`),
    UNIQUE KEY `uq_crypto_key_versions_one_current` (`current_keyring_id`),
    CONSTRAINT `fk_crypto_key_versions_keyring`
        FOREIGN KEY (`keyring_id`) REFERENCES `crypto_keyring_state` (`id`),
    CONSTRAINT `ck_crypto_key_versions_status`
        CHECK (`status` IN ('preparing', 'readable', 'current', 'retired', 'security_revoked')),
    CONSTRAINT `ck_crypto_key_versions_algorithm`
        CHECK (`algorithm` IN ('aes-256-gcm'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `encrypted_blobs` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `keyring_id` bigint unsigned NOT NULL,
    `purpose` varchar(64) NOT NULL,
    `schema_version` int unsigned NOT NULL,
    `aad_hash` char(64) NOT NULL,
    `nonce` varbinary(32) NOT NULL,
    `ciphertext` longblob NOT NULL,
    `content_hmac` char(64) NOT NULL,
    `content_length` bigint unsigned NOT NULL,
    `created_at` datetime(3) NOT NULL,
    `purged_at` datetime(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_encrypted_blobs_keyring_id_id` (`keyring_id`, `id`),
    UNIQUE KEY `uq_encrypted_blobs_id_keyring_id` (`id`, `keyring_id`),
    INDEX `idx_encrypted_blobs_purpose_created` (`purpose`, `created_at`),
    INDEX `idx_encrypted_blobs_purged_at` (`purged_at`),
    CONSTRAINT `fk_encrypted_blobs_keyring`
        FOREIGN KEY (`keyring_id`) REFERENCES `crypto_keyring_state` (`id`),
    CONSTRAINT `ck_encrypted_blobs_length`
        CHECK (`content_length` <= 1073741824)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `encrypted_blob_key_wraps` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `encrypted_blob_id` bigint unsigned NOT NULL,
    `keyring_id` bigint unsigned NOT NULL,
    `kek_version` int unsigned NOT NULL,
    `wrap_nonce` varbinary(32) NOT NULL,
    `wrapped_dek` varbinary(255) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_encrypted_blob_key_wraps_blob_ring_version` (`encrypted_blob_id`, `keyring_id`, `kek_version`),
    CONSTRAINT `fk_encrypted_blob_key_wraps_blob`
        FOREIGN KEY (`encrypted_blob_id`, `keyring_id`)
        REFERENCES `encrypted_blobs` (`id`, `keyring_id`),
    CONSTRAINT `fk_encrypted_blob_key_wraps_key_version`
        FOREIGN KEY (`keyring_id`, `kek_version`)
        REFERENCES `crypto_key_versions` (`keyring_id`, `key_version`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
