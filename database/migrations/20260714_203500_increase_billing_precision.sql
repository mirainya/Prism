-- Reason: the unified call and billing ledgers keep eight decimal places, but
-- legacy balance, task, and conversation columns still truncate to 4-6 places.
-- Requirement: reservation, settlement, refund, and displayed usage must use
-- the same precision end to end.
-- Impact: widens monetary columns without changing existing numeric values.

-- Historical schemas allowed NULL on some defaulted monetary columns. Normalize
-- those rows before enforcing the non-null invariant used by billing code.
UPDATE users SET balance = 0 WHERE balance IS NULL;
UPDATE tokens SET balance = 0 WHERE balance IS NULL;
UPDATE tokens SET total_used = 0 WHERE total_used IS NULL;
UPDATE tasks SET cost = 0 WHERE cost IS NULL;
UPDATE messages SET cost = 0 WHERE cost IS NULL;
UPDATE billing_logs SET amount = 0 WHERE amount IS NULL;

ALTER TABLE users
    MODIFY COLUMN balance DECIMAL(20,8) NOT NULL DEFAULT 0 COMMENT '账户余额';

ALTER TABLE tokens
    MODIFY COLUMN balance DECIMAL(20,8) NOT NULL DEFAULT 0 COMMENT '剩余额度',
    MODIFY COLUMN total_used DECIMAL(20,8) NOT NULL DEFAULT 0 COMMENT '已使用额度';

ALTER TABLE tasks
    MODIFY COLUMN cost DECIMAL(20,8) NOT NULL DEFAULT 0 COMMENT '费用';

ALTER TABLE messages
    MODIFY COLUMN cost DECIMAL(20,8) NOT NULL DEFAULT 0 COMMENT '费用';

ALTER TABLE billing_logs
    MODIFY COLUMN amount DECIMAL(20,8) NOT NULL DEFAULT 0 COMMENT '金额';
