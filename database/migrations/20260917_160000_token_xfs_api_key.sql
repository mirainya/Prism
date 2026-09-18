-- Reason: allow each Prism API token to choose its XFileStorage account.
-- Scope: the token stores the current setting; each call fixes the setting used by that task.
-- Impact: replacing or clearing a token setting affects new tasks only.

ALTER TABLE `tokens`
    ADD COLUMN `xfs_api_key` varchar(512) NOT NULL DEFAULT '' AFTER `user_id`;

ALTER TABLE `gw_api_calls`
    ADD COLUMN `xfs_api_key` varchar(512) NOT NULL DEFAULT '' AFTER `delivery_mode`;

ALTER TABLE `gw_media_assets`
    ADD COLUMN `attempt_id` bigint unsigned NULL AFTER `token_id`,
    ADD KEY `idx_gw_media_assets_attempt` (`attempt_id`),
    ADD CONSTRAINT `fk_gw_media_assets_attempt`
        FOREIGN KEY (`attempt_id`) REFERENCES `gw_api_call_attempts` (`id`);
