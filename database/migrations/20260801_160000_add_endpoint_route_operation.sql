-- Reason: generation and edit endpoints must be reusable across channel keys without inferring identity from request paths.
-- Requirement: persist the concrete operation used by Endpoint discovery imports and routing.
-- Impact: adds and backfills one indexed column on endpoints; legacy custom endpoints remain compatible with path inference.
-- Deployment: ALTER and backfill can briefly acquire metadata or row locks on endpoints.

SET @prism_schema = DATABASE();

SET @endpoint_route_operation_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'endpoints' AND column_name = 'route_operation'
    ),
    'SELECT 1',
    'ALTER TABLE endpoints ADD COLUMN route_operation VARCHAR(40) NOT NULL DEFAULT '''' COMMENT ''Endpoint route operation'' AFTER model_code'
);
PREPARE endpoint_route_operation_stmt FROM @endpoint_route_operation_sql;
EXECUTE endpoint_route_operation_stmt;
DEALLOCATE PREPARE endpoint_route_operation_stmt;

UPDATE endpoints
SET route_operation = CASE
    WHEN LOWER(TRIM(TRAILING '/' FROM SUBSTRING_INDEX(request_path, '?', 1))) LIKE '%/images/edits' THEN 'images.edit'
    WHEN LOWER(TRIM(TRAILING '/' FROM SUBSTRING_INDEX(request_path, '?', 1))) LIKE '%/images/generations' THEN 'images.generate'
    ELSE route_operation
END
WHERE route_operation = '';

SET @endpoint_route_operation_index_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = @prism_schema AND table_name = 'endpoints' AND index_name = 'idx_endpoints_route_operation'
    ),
    'SELECT 1',
    'ALTER TABLE endpoints ADD INDEX idx_endpoints_route_operation (route_operation)'
);
PREPARE endpoint_route_operation_index_stmt FROM @endpoint_route_operation_index_sql;
EXECUTE endpoint_route_operation_index_stmt;
DEALLOCATE PREPARE endpoint_route_operation_index_stmt;
