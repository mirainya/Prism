-- Restore user ledger rows omitted when existing accounts were imported.
-- Impact: adds one zero-balance ledger row for each affected billing account.

INSERT INTO `ledger_accounts` (
  `billing_account_id`,
  `currency_code`,
  `currency_version`,
  `balance`,
  `created_at`
)
SELECT
  account.`id`,
  account.`currency_code`,
  account.`currency_version`,
  0,
  CURRENT_TIMESTAMP(3)
FROM `billing_accounts` AS account
LEFT JOIN `ledger_accounts` AS ledger
  ON ledger.`billing_account_id` = account.`id`
 AND ledger.`currency_code` = account.`currency_code`
 AND ledger.`currency_version` = account.`currency_version`
WHERE ledger.`id` IS NULL;
