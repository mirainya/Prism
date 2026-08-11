-- Normalize image capability parameters for legacy, discovered, and imported endpoints.
-- Keep provider-specific fields, but expose the canonical image request fields and
-- remove obsolete size choices that are not accepted by current image routes.

UPDATE `models`
SET `param_schema` = JSON_MERGE_PATCH(
  COALESCE(`param_schema`, JSON_OBJECT()),
  JSON_OBJECT(
    'prompt', JSON_OBJECT('name', 'prompt', 'type', 'string', 'required', TRUE),
    'image_urls', JSON_OBJECT(
      'name', 'image_urls', 'type', 'array', 'required', FALSE,
      'description', 'reference image URLs or data URLs; non-empty values enable image editing'
    ),
    'aspect_ratio', JSON_OBJECT(
      'name', 'aspect_ratio', 'type', 'enum', 'required', FALSE,
      'options', JSON_ARRAY('1:1', '16:9', '9:16', '4:3', '3:4', '3:2', '2:3')
    ),
    'size', JSON_OBJECT(
      'name', 'size', 'type', 'enum', 'required', FALSE, 'default', '1024x1024',
      'options', JSON_ARRAY('1024x1024', '1536x1024', '1024x1536', 'auto')
    ),
    'n', JSON_OBJECT('name', 'n', 'type', 'number', 'required', FALSE, 'default', 1),
    'quality', JSON_OBJECT(
      'name', 'quality', 'type', 'enum', 'required', FALSE, 'default', 'auto',
      'options', JSON_ARRAY('auto', 'high', 'medium', 'low')
    ),
    'response_format', JSON_OBJECT(
      'name', 'response_format', 'type', 'enum', 'required', FALSE, 'default', 'url',
      'options', JSON_ARRAY('url', 'b64_json')
    ),
    'output_format', JSON_OBJECT(
      'name', 'output_format', 'type', 'enum', 'required', FALSE, 'default', 'png',
      'options', JSON_ARRAY('png', 'jpeg', 'webp')
    ),
    'output_compression', JSON_OBJECT(
      'name', 'output_compression', 'type', 'number', 'required', FALSE,
      'description', 'output compression, 0-100'
    ),
    'moderation', JSON_OBJECT('name', 'moderation', 'type', 'string', 'required', FALSE),
    'style', JSON_OBJECT('name', 'style', 'type', 'string', 'required', FALSE)
  )
),
`updated_at` = CURRENT_TIMESTAMP(3)
WHERE `type` = 'image' AND `deleted_at` IS NULL;

UPDATE `endpoints` AS e
JOIN `models` AS m ON m.`code` = e.`model_code` AND m.`deleted_at` IS NULL
SET e.`param_schema` = JSON_MERGE_PATCH(
  COALESCE(e.`param_schema`, JSON_OBJECT()),
  JSON_OBJECT(
    'prompt', JSON_OBJECT('name', 'prompt', 'type', 'string', 'required', TRUE),
    'image_urls', JSON_OBJECT(
      'name', 'image_urls', 'type', 'array', 'required', FALSE,
      'description', 'reference image URLs or data URLs; non-empty values enable image editing'
    ),
    'aspect_ratio', JSON_OBJECT(
      'name', 'aspect_ratio', 'type', 'enum', 'required', FALSE,
      'options', JSON_ARRAY('1:1', '16:9', '9:16', '4:3', '3:4', '3:2', '2:3')
    ),
    'size', JSON_OBJECT(
      'name', 'size', 'type', 'enum', 'required', FALSE, 'default', '1024x1024',
      'options', JSON_ARRAY('1024x1024', '1536x1024', '1024x1536', 'auto')
    ),
    'n', JSON_OBJECT('name', 'n', 'type', 'number', 'required', FALSE, 'default', 1),
    'quality', JSON_OBJECT(
      'name', 'quality', 'type', 'enum', 'required', FALSE, 'default', 'auto',
      'options', JSON_ARRAY('auto', 'high', 'medium', 'low')
    ),
    'response_format', JSON_OBJECT(
      'name', 'response_format', 'type', 'enum', 'required', FALSE, 'default', 'url',
      'options', JSON_ARRAY('url', 'b64_json')
    ),
    'output_format', JSON_OBJECT(
      'name', 'output_format', 'type', 'enum', 'required', FALSE, 'default', 'png',
      'options', JSON_ARRAY('png', 'jpeg', 'webp')
    ),
    'output_compression', JSON_OBJECT(
      'name', 'output_compression', 'type', 'number', 'required', FALSE,
      'description', 'output compression, 0-100'
    ),
    'moderation', JSON_OBJECT('name', 'moderation', 'type', 'string', 'required', FALSE),
    'style', JSON_OBJECT('name', 'style', 'type', 'string', 'required', FALSE)
  )
),
e.`updated_at` = CURRENT_TIMESTAMP(3)
WHERE m.`type` = 'image' AND e.`deleted_at` IS NULL;
