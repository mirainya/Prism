-- Reason: a legacy database could be adopted without conversations.call_id even though the managed baseline requires it.
-- Requirement: retention and response projection queries need the latest API call associated with each conversation.
-- Impact: adds the missing column and index only on drifted databases; conforming databases are unchanged.
-- Deployment: adding the column or index can briefly acquire a metadata lock on conversations.

SET @prism_schema = DATABASE();

SET @conversation_call_id_sql = IF(
    EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND column_name = 'call_id'
    ),
    'SELECT 1',
    'ALTER TABLE conversations ADD COLUMN call_id VARCHAR(64) NOT NULL DEFAULT '''' COMMENT ''Latest associated API call ID'' AFTER token_id'
);
PREPARE conversation_call_id_stmt FROM @conversation_call_id_sql;
EXECUTE conversation_call_id_stmt;
DEALLOCATE PREPARE conversation_call_id_stmt;

SET @conversation_call_id_index_sql = IF(
    EXISTS (
        SELECT 1
        FROM information_schema.statistics
        WHERE table_schema = @prism_schema
          AND table_name = 'conversations'
          AND index_name = 'idx_conversations_call_id'
    ),
    'SELECT 1',
    'ALTER TABLE conversations ADD INDEX idx_conversations_call_id (call_id)'
);
PREPARE conversation_call_id_index_stmt FROM @conversation_call_id_index_sql;
EXECUTE conversation_call_id_index_stmt;
DEALLOCATE PREPARE conversation_call_id_index_stmt;
