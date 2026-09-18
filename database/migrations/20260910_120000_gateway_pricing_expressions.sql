-- Introduce expression-based pricing, SKU variants and declared downstream
-- paths for the v5 operations console.
--
-- Rates keep `unit_price` as the only price fact for `pricing_mode='flat'`.
-- An `expression` rate carries the arithmetic in `pricing_expr` instead; its
-- provable upper bound is verified at publication, not by the database.
-- `cost_group_ratio` scales upstream cost only. User-facing prices stay
-- independent of the selected channel, pool or key.
--
-- MySQL DDL implicitly commits. Every column, index and CHECK is guarded on its
-- own so a dirty migration can resume after any individual ALTER has committed.

SET @prism_schema = DATABASE();

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_credential_pools` ADD COLUMN `cost_group_ratio` decimal(18,8) NOT NULL DEFAULT 1.00000000 AFTER `status`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_credential_pools'
      AND column_name = 'cost_group_ratio'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_credential_pools` ADD CONSTRAINT `ck_gw_credential_pools_cost_group_ratio` CHECK (`cost_group_ratio` > 0)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_credential_pools'
      AND constraint_name = 'ck_gw_credential_pools_cost_group_ratio' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Imported SKUs are the single implicit variant. The adapter manifest names
-- every other variant explicitly.
SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_skus` ADD COLUMN `variant_code` varchar(64) COLLATE utf8mb4_bin NOT NULL DEFAULT ''default'' AFTER `sku_code`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_skus' AND column_name = 'variant_code'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_skus` ADD INDEX `idx_gw_skus_release_variant` (`release_id`, `variant_code`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = @prism_schema AND table_name = 'gw_skus'
      AND index_name = 'idx_gw_skus_release_variant'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD COLUMN `pricing_mode` varchar(16) COLLATE utf8mb4_bin NOT NULL DEFAULT ''flat'' AFTER `unit_price`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_sell_rates' AND column_name = 'pricing_mode'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD COLUMN `pricing_expr` text COLLATE utf8mb4_bin NULL AFTER `pricing_mode`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_sell_rates' AND column_name = 'pricing_expr'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- `max_price` is the provable upper bound produced by the publication-time
-- corner enumeration. Reservation needs a bound before the call runs, so an
-- expression rate without one cannot be published.
SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD COLUMN `max_price` decimal(38,18) NULL AFTER `pricing_expr`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_sell_rates' AND column_name = 'max_price'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD CONSTRAINT `ck_gw_sell_rates_pricing_mode` CHECK (`pricing_mode` IN (''flat'', ''expression''))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_sell_rates'
      AND constraint_name = 'ck_gw_sell_rates_pricing_mode' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- An expression rate must carry an expression, and a flat rate must not. This
-- keeps `unit_price` the single readable price fact for flat rates.
SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD CONSTRAINT `ck_gw_sell_rates_expr_pairing` CHECK ((`pricing_mode` = ''expression'' AND `pricing_expr` IS NOT NULL AND `max_price` IS NOT NULL AND `max_price` >= 0) OR (`pricing_mode` = ''flat'' AND `pricing_expr` IS NULL AND `max_price` IS NULL))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_sell_rates'
      AND constraint_name = 'ck_gw_sell_rates_expr_pairing' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD COLUMN `pricing_mode` varchar(16) COLLATE utf8mb4_bin NOT NULL DEFAULT ''flat'' AFTER `unit_price`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_cost_rates' AND column_name = 'pricing_mode'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD COLUMN `pricing_expr` text COLLATE utf8mb4_bin NULL AFTER `pricing_mode`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_cost_rates' AND column_name = 'pricing_expr'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD COLUMN `max_price` decimal(38,18) NULL AFTER `pricing_expr`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_cost_rates' AND column_name = 'max_price'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD CONSTRAINT `ck_gw_cost_rates_pricing_mode` CHECK (`pricing_mode` IN (''flat'', ''expression''))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_cost_rates'
      AND constraint_name = 'ck_gw_cost_rates_pricing_mode' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD CONSTRAINT `ck_gw_cost_rates_expr_pairing` CHECK ((`pricing_mode` = ''expression'' AND `pricing_expr` IS NOT NULL AND `max_price` IS NOT NULL AND `max_price` >= 0) OR (`pricing_mode` = ''flat'' AND `pricing_expr` IS NULL AND `max_price` IS NULL))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_cost_rates'
      AND constraint_name = 'ck_gw_cost_rates_expr_pairing' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- Downstream paths a SKU answers on. Declaring them as rows keeps the set
-- release-scoped and immutable, instead of hiding it in a JSON column.
CREATE TABLE IF NOT EXISTS `gw_sku_downstream_paths` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `release_id` bigint unsigned NOT NULL,
    `sku_id` bigint unsigned NOT NULL,
    `path` varchar(128) COLLATE utf8mb4_bin NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_sku_downstream_paths` (`release_id`, `sku_id`, `path`),
    UNIQUE KEY `uq_gw_sku_downstream_paths_release_id_id` (`release_id`, `id`),
    CONSTRAINT `fk_gw_sku_downstream_paths_release`
        FOREIGN KEY (`release_id`) REFERENCES `gw_catalog_releases` (`id`),
    CONSTRAINT `fk_gw_sku_downstream_paths_sku`
        FOREIGN KEY (`release_id`, `sku_id`) REFERENCES `gw_skus` (`release_id`, `id`),
    CONSTRAINT `ck_gw_sku_downstream_paths_path`
        CHECK (`path` LIKE '/%')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
