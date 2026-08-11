-- Prevent concurrent uploads of the same ready asset for one token.

SET @video_asset_unique_index_sql = IF(
    EXISTS (
        SELECT 1 FROM information_schema.statistics
        WHERE table_schema = DATABASE() AND table_name = 'video_assets' AND index_name = 'uk_video_assets_token_sha256_status'
    ),
    'SELECT 1',
    'ALTER TABLE video_assets ADD UNIQUE INDEX uk_video_assets_token_sha256_status (token_id, sha256, status)'
);
PREPARE video_asset_unique_index_stmt FROM @video_asset_unique_index_sql;
EXECUTE video_asset_unique_index_stmt;
DEALLOCATE PREPARE video_asset_unique_index_stmt;
