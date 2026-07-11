-- Reason: Track whether a task's account concurrency slot has been released.
-- Requirement: Keep cancellation, failure, and finalization slot release idempotent.
-- Impact: Adds one boolean column to tasks; existing rows use the false default.

ALTER TABLE `tasks`
    ADD COLUMN `account_slot_released` BOOLEAN DEFAULT FALSE
    COMMENT 'Whether the account concurrency slot has been released';
