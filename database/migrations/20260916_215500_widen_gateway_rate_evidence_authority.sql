-- Reason: the supported authority value `operator_declared` is 17 characters,
-- while the original column was varchar(16), so operator-entered prices could
-- never be persisted.
-- Scope: rate evidence metadata only; existing values and price rows are unchanged.

ALTER TABLE `gw_rate_evidence`
    MODIFY COLUMN `authority_level` varchar(32) NOT NULL;
