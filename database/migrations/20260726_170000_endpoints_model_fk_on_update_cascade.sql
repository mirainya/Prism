-- Reason: renaming a model required changing models.code, but fk_endpoints_model defaulted to
--   ON UPDATE RESTRICT, so MySQL rejected the parent-row update with error 1451 whenever the model
--   already had endpoints attached.
-- Requirement: allow renaming a model code from the admin console without manual SQL surgery
--   (needed to give image models the gpt-image- prefix that Sub2API's images endpoint enforces).
-- Impact: replaces the fk_endpoints_model constraint with an ON UPDATE CASCADE variant. No row data
--   changes; updates to models.code now propagate to endpoints.model_code automatically.
--   Delete behaviour stays RESTRICT, so a model with endpoints still cannot be deleted.
-- Deployment: brief metadata lock on `endpoints` while the constraint is swapped.

ALTER TABLE `endpoints` DROP FOREIGN KEY `fk_endpoints_model`;

ALTER TABLE `endpoints` ADD CONSTRAINT `fk_endpoints_model`
  FOREIGN KEY (`model_code`) REFERENCES `models` (`code`) ON UPDATE CASCADE;
