-- Reason: video_channels.passthrough was exposed by the admin API but never read by the video engine.
-- Requirement: keep channel configuration limited to behavior that is implemented at runtime.
-- Impact: removes one unused JSON column; request mapping remains in extra_config.adapter.

ALTER TABLE video_channels
DROP COLUMN passthrough;
