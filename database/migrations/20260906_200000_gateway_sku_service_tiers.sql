-- Declare the service tiers accepted by each immutable video SKU.
-- Existing imported SKUs remain valid with the explicit standard tier.

SET @prism_ddl = (
    SELECT IF(COUNT(*) = 0,
        'ALTER TABLE `gw_skus` ADD COLUMN `service_tiers` JSON NULL AFTER `idempotency_mode`',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'gw_skus' AND column_name = 'service_tiers'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

UPDATE `gw_skus`
SET `service_tiers` = JSON_ARRAY('standard')
WHERE `service_tiers` IS NULL;

-- A dirty run may have added the column but not completed the final MODIFY.
-- Only issue the definition change while it is still nullable or has drifted;
-- a fully applied migration becomes a no-op on retry.
SET @prism_ddl = (
    SELECT IF(COUNT(*) = 1 AND (MAX(is_nullable) = 'YES' OR LOWER(MAX(data_type)) <> 'json'),
        'ALTER TABLE `gw_skus` MODIFY COLUMN `service_tiers` JSON NOT NULL',
        'SELECT 1')
    FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'gw_skus' AND column_name = 'service_tiers'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
