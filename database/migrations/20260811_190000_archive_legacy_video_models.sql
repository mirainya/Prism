-- Reason: the dedicated video engine owns video model configuration in video_channels.models.
-- Requirement: remove legacy video definitions from the general capability catalog.
-- Impact: soft-deletes every active video model definition; video channel JSON and task history remain unchanged.
-- Deployment: metadata update only; existing endpoint rows are retained for historical references.

UPDATE `models`
SET `status` = 0,
    `deleted_at` = CURRENT_TIMESTAMP(3),
    `updated_at` = CURRENT_TIMESTAMP(3)
WHERE `type` = 'video'
  AND `deleted_at` IS NULL;
