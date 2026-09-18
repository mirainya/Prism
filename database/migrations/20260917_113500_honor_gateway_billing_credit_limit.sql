-- Make the account credit limit usable by gateway authorization and settlement.
-- Impact: replaces the billing account amount constraint; no balance data changes.

SET @prism_schema = DATABASE();

ALTER TABLE `billing_accounts`
  DROP CHECK `ck_billing_accounts_amounts`;

ALTER TABLE `billing_accounts`
  ADD CONSTRAINT `ck_billing_accounts_amounts`
  CHECK (
    `held_amount` >= 0
    AND `credit_limit` >= 0
    AND (`posted_balance` + `credit_limit` >= 0 OR `status` = 'frozen')
  );
