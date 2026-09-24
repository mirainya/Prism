-- Reason: rejected requests were persisted with only a numeric error code,
-- which made validation failures impossible to diagnose from the console.
-- Scope: keep a short, sanitized operator-facing explanation beside the code.
-- Impact: existing access records remain valid; new records populate the field.

ALTER TABLE `api_access_logs`
    ADD COLUMN `error_message` varchar(512) NOT NULL DEFAULT '' AFTER `error_code`;
