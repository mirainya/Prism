-- Reason: API token secrets are now returned once and authenticated only by hash.
-- Requirement: remove legacy plaintext token storage and retain only a last-four display hint.
-- Impact: existing token authentication is unchanged; plaintext token recovery is permanently removed.
-- Deployment: stop every old Prism instance before applying this migration.
-- An old binary's AutoMigrate would recreate plain_key and resume plaintext writes.

SET @prism_schema = DATABASE();

SET @token_plain_key_cleanup_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'tokens' AND column_name = 'plain_key'
    ) AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'tokens' AND column_name = 'key_hint'
    ),
    'UPDATE tokens SET key_hint = CASE WHEN plain_key IS NOT NULL AND plain_key <> '''' THEN CONCAT(''****'', RIGHT(plain_key, 4)) WHEN CHAR_LENGTH(key_hint) = 8 AND LEFT(key_hint, 4) = ''****'' THEN key_hint ELSE ''****'' END',
    'SELECT 1'
);
PREPARE token_plain_key_cleanup_stmt FROM @token_plain_key_cleanup_sql;
EXECUTE token_plain_key_cleanup_stmt;
DEALLOCATE PREPARE token_plain_key_cleanup_stmt;

SET @token_plain_key_drop_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'tokens' AND column_name = 'plain_key'
    ),
    'ALTER TABLE tokens DROP COLUMN plain_key',
    'SELECT 1'
);
PREPARE token_plain_key_drop_stmt FROM @token_plain_key_drop_sql;
EXECUTE token_plain_key_drop_stmt;
DEALLOCATE PREPARE token_plain_key_drop_stmt;
