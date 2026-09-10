-- Add explicit pricing components for catalog billing. Existing price facts are
-- preserved without guessing their quantity source or contractual upper bound.
-- Incomplete rates cannot pass publication/readiness validation.
--
-- MySQL DDL implicitly commits. Every column, index and CHECK is guarded on its
-- own so a dirty migration can resume after any individual ALTER has committed.

SET @prism_schema = DATABASE();

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD COLUMN `component_code` varchar(32) COLLATE utf8mb4_bin NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_sell_rates' AND column_name = 'component_code'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD COLUMN `quantity_source` varchar(32) COLLATE utf8mb4_bin NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_sell_rates' AND column_name = 'quantity_source'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD COLUMN `charge_event` varchar(32) COLLATE utf8mb4_bin NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_sell_rates' AND column_name = 'charge_event'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD COLUMN `unit_scale` tinyint unsigned NOT NULL DEFAULT 0',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_sell_rates' AND column_name = 'unit_scale'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD COLUMN `quantity_step` decimal(38,18) NOT NULL DEFAULT 0',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_sell_rates' AND column_name = 'quantity_step'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD COLUMN `max_quantity` decimal(38,18) NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_sell_rates' AND column_name = 'max_quantity'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD UNIQUE KEY `uq_gw_sell_rates_component` (`release_id`, `sku_id`, `component_code`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = @prism_schema AND table_name = 'gw_sell_rates' AND index_name = 'uq_gw_sell_rates_component'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- The component index has the same leading foreign-key columns, so create it
-- before removing the old identity index used by the SKU foreign key.
SET @prism_ddl = (
    SELECT IF(COUNT(*) > 0,
        'ALTER TABLE `gw_sell_rates` DROP INDEX `uq_gw_sell_rates_release_sku`',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = @prism_schema
      AND table_name = 'gw_sell_rates'
      AND index_name = 'uq_gw_sell_rates_release_sku'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD UNIQUE KEY `uq_gw_sell_rates_meter` (`release_id`, `sku_id`, `charge_event`, `quantity_source`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = @prism_schema AND table_name = 'gw_sell_rates' AND index_name = 'uq_gw_sell_rates_meter'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD CONSTRAINT `ck_gw_sell_rates_unit_scale` CHECK (`unit_scale` <= 12)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_sell_rates'
      AND constraint_name = 'ck_gw_sell_rates_unit_scale' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD CONSTRAINT `ck_gw_sell_rates_quantity_step` CHECK (`quantity_step` >= 0)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_sell_rates'
      AND constraint_name = 'ck_gw_sell_rates_quantity_step' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD CONSTRAINT `ck_gw_sell_rates_max_quantity` CHECK (`max_quantity` > 0)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_sell_rates'
      AND constraint_name = 'ck_gw_sell_rates_max_quantity' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD CONSTRAINT `ck_gw_sell_rates_charge_event` CHECK (`charge_event` IN (''call.succeeded'', ''call.failed'', ''call.cancelled'', ''provider.accepted'', ''delivery.ready'', ''delivery.failed''))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_sell_rates'
      AND constraint_name = 'ck_gw_sell_rates_charge_event' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_sell_rates` ADD CONSTRAINT `ck_gw_sell_rates_quantity_source` CHECK (`quantity_source` IN (''one'', ''usage.input_tokens'', ''usage.output_tokens'', ''usage.uncached_input_tokens'', ''usage.cached_input_tokens'', ''request.seconds'', ''result.seconds'', ''request.images'', ''result.images'', ''result.videos'', ''result.megapixels''))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_sell_rates'
      AND constraint_name = 'ck_gw_sell_rates_quantity_source' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD COLUMN `component_code` varchar(32) COLLATE utf8mb4_bin NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_cost_rates' AND column_name = 'component_code'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD COLUMN `quantity_source` varchar(32) COLLATE utf8mb4_bin NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_cost_rates' AND column_name = 'quantity_source'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD COLUMN `charge_event` varchar(32) COLLATE utf8mb4_bin NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_cost_rates' AND column_name = 'charge_event'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD COLUMN `unit_scale` tinyint unsigned NOT NULL DEFAULT 0',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_cost_rates' AND column_name = 'unit_scale'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD COLUMN `quantity_step` decimal(38,18) NOT NULL DEFAULT 0',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_cost_rates' AND column_name = 'quantity_step'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD COLUMN `max_quantity` decimal(38,18) NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = @prism_schema AND table_name = 'gw_cost_rates' AND column_name = 'max_quantity'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD UNIQUE KEY `uq_gw_cost_rates_component` (`release_id`, `cost_plan_id`, `component_code`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = @prism_schema AND table_name = 'gw_cost_rates' AND index_name = 'uq_gw_cost_rates_component'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

-- As above, retain foreign-key coverage through the new component index
-- before removing the legacy plan/unit identity.
SET @prism_ddl = (
    SELECT IF(COUNT(*) > 0,
        'ALTER TABLE `gw_cost_rates` DROP INDEX `uq_gw_cost_rates_plan_unit`',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = @prism_schema AND table_name = 'gw_cost_rates' AND index_name = 'uq_gw_cost_rates_plan_unit'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD UNIQUE KEY `uq_gw_cost_rates_meter` (`release_id`, `cost_plan_id`, `charge_event`, `quantity_source`)',
        'SELECT 1')
    FROM information_schema.statistics
    WHERE table_schema = @prism_schema AND table_name = 'gw_cost_rates' AND index_name = 'uq_gw_cost_rates_meter'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD CONSTRAINT `ck_gw_cost_rates_unit_scale` CHECK (`unit_scale` <= 12)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_cost_rates'
      AND constraint_name = 'ck_gw_cost_rates_unit_scale' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD CONSTRAINT `ck_gw_cost_rates_quantity_step` CHECK (`quantity_step` >= 0)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_cost_rates'
      AND constraint_name = 'ck_gw_cost_rates_quantity_step' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD CONSTRAINT `ck_gw_cost_rates_max_quantity` CHECK (`max_quantity` > 0)',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_cost_rates'
      AND constraint_name = 'ck_gw_cost_rates_max_quantity' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD CONSTRAINT `ck_gw_cost_rates_charge_event` CHECK (`charge_event` IN (''call.succeeded'', ''call.failed'', ''call.cancelled'', ''provider.accepted'', ''delivery.ready'', ''delivery.failed''))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_cost_rates'
      AND constraint_name = 'ck_gw_cost_rates_charge_event' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_cost_rates` ADD CONSTRAINT `ck_gw_cost_rates_quantity_source` CHECK (`quantity_source` IN (''one'', ''usage.input_tokens'', ''usage.output_tokens'', ''usage.uncached_input_tokens'', ''usage.cached_input_tokens'', ''request.seconds'', ''result.seconds'', ''request.images'', ''result.images'', ''result.videos'', ''result.megapixels''))',
        'SELECT 1')
    FROM information_schema.table_constraints
    WHERE constraint_schema = @prism_schema AND table_name = 'gw_cost_rates'
      AND constraint_name = 'ck_gw_cost_rates_quantity_source' AND constraint_type = 'CHECK'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
