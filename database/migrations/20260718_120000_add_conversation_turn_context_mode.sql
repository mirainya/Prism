-- Reason: distinguish incremental conversation input from unmatched full-history snapshots.
-- Requirement: keep existing turn records readable without pretending their association is known.
-- Impact: adds context classification to conversation turns and pending projection entries; no message content is changed.

SET @prism_schema = DATABASE();

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversation_turns'
          AND column_name = 'context_mode'
    ),
    'SELECT 1',
    'ALTER TABLE conversation_turns ADD COLUMN context_mode VARCHAR(24) NOT NULL DEFAULT ''legacy'' AFTER status'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;

SET @prism_ddl = IF(
    EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversation_projection_outbox'
          AND column_name = 'context_mode'
    ),
    'SELECT 1',
    'ALTER TABLE conversation_projection_outbox ADD COLUMN context_mode VARCHAR(24) NOT NULL DEFAULT '''' AFTER input_prepared'
);
PREPARE prism_stmt FROM @prism_ddl;
EXECUTE prism_stmt;
DEALLOCATE PREPARE prism_stmt;
