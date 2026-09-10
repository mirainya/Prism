-- Preserve control-plane run state changes and their machine-readable reasons.
-- This makes catalog discovery failures auditable and allows a failed run to
-- be retried without deleting its immutable source binding or request logs.

CREATE TABLE IF NOT EXISTS `gw_control_plane_run_events` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `control_plane_run_id` bigint unsigned NOT NULL,
    `event_seq` bigint unsigned NOT NULL,
    `old_state` varchar(16) NULL,
    `new_state` varchar(16) NOT NULL,
    `reason_code` varchar(128) NOT NULL,
    `created_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_control_plane_run_events_seq` (`control_plane_run_id`, `event_seq`),
    CONSTRAINT `fk_gw_control_plane_run_events_run`
        FOREIGN KEY (`control_plane_run_id`) REFERENCES `gw_control_plane_runs` (`id`),
    CONSTRAINT `ck_gw_control_plane_run_events_old_state`
        CHECK (`old_state` IS NULL OR `old_state` IN ('scheduled', 'running', 'completed', 'failed', 'manual_review')),
    CONSTRAINT `ck_gw_control_plane_run_events_new_state`
        CHECK (`new_state` IN ('scheduled', 'running', 'completed', 'failed', 'manual_review'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO `gw_control_plane_run_events`
    (`control_plane_run_id`, `event_seq`, `old_state`, `new_state`, `reason_code`, `created_at`)
SELECT `id`, `state_version`, NULL, `state`, 'state_backfill', `updated_at`
FROM `gw_control_plane_runs`;
