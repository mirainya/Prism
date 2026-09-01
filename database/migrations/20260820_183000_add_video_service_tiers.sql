-- Reason: expose provider-neutral video execution tiers and map them to the
-- H-channel wire flags without adding provider-specific code to Prism.
-- Impact: existing tasks default to standard; only the configured H channel
-- receives priority/VIP request parameters.

ALTER TABLE video_tasks
    ADD COLUMN service_tier VARCHAR(24) NOT NULL DEFAULT 'standard'
    COMMENT '视频执行档位' AFTER task_mode,
    ADD COLUMN provider_metadata JSON NULL
    COMMENT '结构化上游任务元数据' AFTER provider_response;

UPDATE video_channels
SET extra_config = JSON_SET(
    COALESCE(extra_config, JSON_OBJECT()),
	'$.adapter.capabilities.service_tiers', JSON_OBJECT(
		'standard', JSON_OBJECT(),
		'priority', JSON_OBJECT('present_path', 'data.h_channel_priority_queue'),
		'vip', JSON_OBJECT('enabled_path', 'data.h_channel_points_vip.enabled')
	),
	'$.adapter.response.actual_cost_paths', JSON_ARRAY('data.actual_cost', 'actual_cost'),
	'$.adapter.response.unit_cost_paths', JSON_ARRAY('data.unit_cost', 'unit_cost'),
	'$.adapter.response.units_paths', JSON_ARRAY('data.units', 'units'),
	'$.adapter.response.billing_mode_paths', JSON_ARRAY('data.billing_mode', 'billing_mode'),
	'$.adapter.response.billing_tier_paths', JSON_ARRAY('data.billing_tier', 'billing_tier'),
	'$.adapter.response.pricing_source_paths', JSON_ARRAY('data.pricing_source', 'pricing_source'),
	'$.adapter.response.currency_paths', JSON_ARRAY('data.currency', 'currency'),
	'$.adapter.response.queue_status_paths', JSON_ARRAY('data.queue_status', 'queue_status'),
	'$.adapter.response.queue_position_paths', JSON_ARRAY('data.queue_position', 'queue_position'),
	'$.adapter.response.queue_limit_paths', JSON_ARRAY('data.queue_limit', 'queue_limit'),
	'$.adapter.response.priority_queue_paths', JSON_ARRAY('data.priority_queue', 'priority_queue'),
	'$.adapter.response.points_vip_paths', JSON_ARRAY('data.h_channel_points_vip', 'h_channel_points_vip'),
	'$.adapter.response.priority_surcharge_percent_paths', JSON_ARRAY('data.priority_surcharge_percent', 'priority_surcharge_percent'),
    '$.adapter.service_tiers', JSON_OBJECT(
        'standard', JSON_OBJECT('label', '标准队列', 'request_params', JSON_OBJECT()),
        'priority', JSON_OBJECT(
            'label', '优先队列',
            'request_params', JSON_OBJECT('h_channel_priority_queue', TRUE)
        ),
        'vip', JSON_OBJECT(
            'label', '积分 VIP',
            'request_params', JSON_OBJECT('h_channel_points_vip', TRUE)
        )
    ),
    '$.adapter.actions.priority_queue', JSON_OBJECT(
        'enabled', TRUE,
        'method', 'POST',
        'path', '/v1/video-generations/{task_id}/priority-queue',
        'allowed_statuses', JSON_ARRAY('submitted', 'tracking')
    )
)
WHERE name = 'hig逆-Seedance'
  AND adapter_type = 'generic'
  AND base_url LIKE 'https://sub2api.0x0.fan/api%';
