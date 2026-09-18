-- Change reason: existing LLM SKUs were imported with only their native
-- downstream path, so the playground could not offer all supported public
-- protocols even though the gateway adapters can translate among them.
-- Scope: every release whose SKU belongs to an LLM operation contract. The
-- migration only adds the three canonical public LLM paths and is retry-safe.

INSERT IGNORE INTO `gw_sku_downstream_paths` (`release_id`, `sku_id`, `path`, `created_at`)
SELECT sku.`release_id`, sku.`id`, alias_route.`route_template`, UTC_TIMESTAMP(3)
FROM `gw_skus` sku
JOIN `gw_model_operations` model_operation
  ON model_operation.`release_id` = sku.`release_id`
 AND model_operation.`id` = sku.`model_operation_id`
JOIN `gw_operation_contracts` native_contract
  ON native_contract.`id` = model_operation.`operation_contract_id`
 AND native_contract.`status` = 'active'
JOIN `gw_operation_contracts` alias_contract
  ON alias_contract.`status` = 'active'
 AND alias_contract.`operation_code` IN ('chat.completions', 'responses.create', 'messages.create')
JOIN `gw_operation_routes` alias_route
  ON alias_route.`operation_contract_id` = alias_contract.`id`
 AND alias_route.`http_method` = 'POST'
 AND (
      (alias_contract.`operation_code` = 'chat.completions' AND alias_route.`route_template` = '/v1/chat/completions')
   OR (alias_contract.`operation_code` = 'responses.create' AND alias_route.`route_template` = '/v1/responses')
   OR (alias_contract.`operation_code` = 'messages.create' AND alias_route.`route_template` = '/v1/messages')
 )
WHERE native_contract.`operation_code` IN ('chat.completions', 'responses.create', 'messages.create');
