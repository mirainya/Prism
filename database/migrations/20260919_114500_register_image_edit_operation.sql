-- Reason: image-edit downstream paths were configurable before the unified
-- operation registry declared their public route, so executable edit aliases
-- were omitted from the public catalog.
-- Scope: register the canonical image-edit operation and route only.
-- Impact: existing POST /v1/images/edits declarations become discoverable;
-- no catalog SKU, product, route weight, credential, or price is changed.

INSERT INTO `gw_operation_contracts` (
  `operation_code`,
  `contract_version`,
  `status`,
  `created_at`
)
VALUES (
  'images.edit',
  1,
  'active',
  UTC_TIMESTAMP(3)
)
ON DUPLICATE KEY UPDATE
  `status` = 'active';

SET @prism_images_edit_contract_id = (
  SELECT `id`
  FROM `gw_operation_contracts`
  WHERE `operation_code` = 'images.edit'
    AND `contract_version` = 1
  LIMIT 1
);

INSERT INTO `gw_operation_routes` (
  `operation_contract_id`,
  `http_method`,
  `route_template`,
  `created_at`
)
VALUES (
  @prism_images_edit_contract_id,
  'POST',
  '/v1/images/edits',
  UTC_TIMESTAMP(3)
)
ON DUPLICATE KEY UPDATE
  `operation_contract_id` = @prism_images_edit_contract_id;
