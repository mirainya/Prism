-- Reason: image generation and image editing are separate Endpoint route operations.
-- Requirement: persist the selected operation on tasks for asynchronous execution and billing audit.
-- Impact: adds a nullable-free, indexed route_operation column to tasks; existing rows remain empty.
-- Deployment: brief metadata locks on tasks while the column and index are added.

SET @prism_schema = DATABASE();
SET @task_route_operation_sql = IF(
    EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = @prism_schema
          AND table_name = 'tasks'
          AND column_name = 'route_operation'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD COLUMN route_operation VARCHAR(40) NOT NULL DEFAULT '''' COMMENT ''路由操作'' AFTER model_code'
);
PREPARE task_route_operation_stmt FROM @task_route_operation_sql;
EXECUTE task_route_operation_stmt;
DEALLOCATE PREPARE task_route_operation_stmt;

SET @task_route_operation_index_sql = IF(
    EXISTS (
        SELECT 1
        FROM information_schema.statistics
        WHERE table_schema = @prism_schema
          AND table_name = 'tasks'
          AND index_name = 'idx_tasks_route_operation'
    ),
    'SELECT 1',
    'ALTER TABLE tasks ADD INDEX idx_tasks_route_operation (route_operation)'
);
PREPARE task_route_operation_index_stmt FROM @task_route_operation_index_sql;
EXECUTE task_route_operation_index_stmt;
DEALLOCATE PREPARE task_route_operation_index_stmt;
