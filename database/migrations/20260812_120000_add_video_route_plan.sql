-- Reason: preserve the selected public-to-upstream model mapping and video route across asynchronous execution.
-- Requirement: store the upstream model and immutable route snapshot on each video task.
-- Impact: adds two task columns and initializes the upstream model for historical tasks.

SET @prism_schema = DATABASE();

SET @video_vendor_model_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'video_tasks' AND column_name = 'vendor_model'
    ),
    'SELECT 1',
    'ALTER TABLE video_tasks ADD COLUMN vendor_model VARCHAR(120) NOT NULL DEFAULT '''' AFTER model'
);
PREPARE video_vendor_model_stmt FROM @video_vendor_model_sql;
EXECUTE video_vendor_model_stmt;
DEALLOCATE PREPARE video_vendor_model_stmt;

UPDATE video_tasks
SET vendor_model = model
WHERE vendor_model = '';

SET @video_route_plan_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = @prism_schema AND table_name = 'video_tasks' AND column_name = 'route_plan'
    ),
    'SELECT 1',
    'ALTER TABLE video_tasks ADD COLUMN route_plan JSON NULL AFTER adapter_type'
);
PREPARE video_route_plan_stmt FROM @video_route_plan_sql;
EXECUTE video_route_plan_stmt;
DEALLOCATE PREPARE video_route_plan_stmt;
