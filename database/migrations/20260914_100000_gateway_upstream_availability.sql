-- Cache upstream-reported availability so it can be shown to end users.
-- This is observation, not catalog content: it carries no release_id, never
-- enters a release content hash and is refreshed in place by a background
-- worker. Prices and capabilities stay inside the release; how often a model
-- currently succeeds does not, because it changes far faster than a release
-- may be republished.

CREATE TABLE IF NOT EXISTS `gw_upstream_availability` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `catalog_source_id` bigint unsigned NOT NULL,
    `model_code` varchar(128) NOT NULL,
    `category` varchar(32) NOT NULL,
    -- Reported as a percentage (0..100) exactly as the provider states it.
    -- Storing the provider's own scale avoids a silent conversion between the
    -- upstream figure and the number shown to the user.
    `success_rate` decimal(5,2) NOT NULL,
    `average_completion_seconds` decimal(10,1) NOT NULL DEFAULT 0,
    `window_minutes` int unsigned NOT NULL,
    -- The provider publishes no sample count, so a rate of 100% may rest on a
    -- single call. Local aggregation supplies a count; this source cannot.
    `observed_at` datetime(3) NOT NULL,
    `updated_at` datetime(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_gw_upstream_availability_source_model` (`catalog_source_id`, `model_code`),
    KEY `idx_gw_upstream_availability_model` (`model_code`),
    CONSTRAINT `fk_gw_upstream_availability_source`
        FOREIGN KEY (`catalog_source_id`) REFERENCES `gw_catalog_sources` (`id`),
    CONSTRAINT `ck_gw_upstream_availability_rate`
        CHECK (`success_rate` BETWEEN 0 AND 100),
    CONSTRAINT `ck_gw_upstream_availability_window`
        CHECK (`window_minutes` > 0),
    CONSTRAINT `ck_gw_upstream_availability_completion`
        CHECK (`average_completion_seconds` >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
