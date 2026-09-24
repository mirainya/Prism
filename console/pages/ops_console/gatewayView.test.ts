import { describe, expect, it } from 'vitest';
import type { UnifiedCatalogProduct, UnifiedRelationLink } from '../../services/unifiedGatewayApi';
import {
  filterGatewayModelRecords,
  filterGatewayModels,
  gatewayModelRuntimeStatus,
  gatewayProductKey,
  groupGatewayModels,
} from './gatewayView';

const product = (patch: Partial<UnifiedCatalogProduct>): UnifiedCatalogProduct => ({
  id: 1,
  product_code: 'deepseek-openai',
  vendor_model: 'deepseek-v4-flash',
  channel_id: 55,
  channel_name: 'DeepSeek',
  product_transport_id: 10,
  channel_transport_id: 20,
  transport_code: 'openai-chat',
  base_url: 'https://example.test',
  protocol: 'openai',
  request_method: 'POST',
  request_path: '/v1/chat/completions',
  task_scope: 'none',
  cancel_mode: 'none',
  source_url_policy: 'fixed',
  adapter_code: 'openai_chat',
  adapter_version: 1,
  offering_id: 30,
  credential_pool_id: 40,
  pool_name: 'main',
  cost_plan_id: 50,
  cost_plan_code: 'cost',
  route_count: 1,
  cost_rate_count: 1,
  commercial_state: 'valid',
  entitled_credential_count: 1,
  ...patch,
});

const relation = (patch: Partial<UnifiedRelationLink>): UnifiedRelationLink => ({
  api_name: 'deepseek-v4', display_name: 'DeepSeek V4', visibility: 'public',
  sku_id: 1, sku_code: 'deepseek-v4', delivery_mode: 'reference', route_id: 2,
  priority: 1, route_weight: 1, offering_id: 30, offering_state: 'active',
  credential_id: 60, credential_code: 'key-1', credential_status: 'active',
  credential_config_version: 1, credential_weight: 1, request_limit: null, task_limit: null,
  credential_pool_id: 40, pool_code: 'main', pool_name: 'Main', pool_status: 'active',
  channel_id: 55, channel_name: 'DeepSeek', channel_status: 'active',
  transport_code: 'openai-chat', product_code: 'deepseek-openai', vendor_model: 'deepseek-v4-flash',
  product_id: 1, product_transport_id: 10, channel_transport_id: 20,
  adapter_code: 'openai_chat', adapter_version: 1, base_url: 'https://example.test',
  protocol: 'openai', request_method: 'POST', request_path: '/v1/chat/completions', task_scope: 'none',
  commercial_valid: true, entitlement_valid: true, secret_identity_active: true,
  execution_granted: true, credential_version_usable: true, breaker_seconds: 0, serving: true,
  ...patch,
});

describe('gateway model presentation', () => {
  it('groups protocol products under one upstream model', () => {
    const products = [
      product({}),
      product({ id: 2, product_code: 'deepseek-anthropic', product_transport_id: 11, offering_id: 31, protocol: 'anthropic', adapter_code: 'anthropic_messages', request_path: '/v1/messages' }),
    ];
    const groups = groupGatewayModels(products, [
      relation({}),
      relation({ product_id: 2, product_code: 'deepseek-anthropic', product_transport_id: 11, offering_id: 31, protocol: 'anthropic', adapter_code: 'anthropic_messages', request_path: '/v1/messages' }),
    ]);
    expect(groups).toHaveLength(1);
    expect(groups[0].products).toHaveLength(2);
    expect(groups[0].protocols).toEqual(['openai', 'anthropic']);
  });

  it('keeps offerings for different key groups separate', () => {
    const first = product({ offering_id: 30, credential_pool_id: 40 });
    const second = product({ offering_id: 31, credential_pool_id: 41 });
    expect(gatewayProductKey(first)).not.toBe(gatewayProductKey(second));
    expect(groupGatewayModels([first, second], [])[0].products).toHaveLength(2);
  });

  it('hides disabled offerings and their relations in the current-model view', () => {
    const currentProduct = product({ offering_state: 'active' });
    const disabledProduct = product({
      id: 2,
      product_transport_id: 11,
      offering_id: 31,
      offering_state: 'disabled',
    });
    const currentRelation = relation({ offering_state: 'active' });
    const disabledRelation = relation({
      product_id: 2,
      product_transport_id: 11,
      offering_id: 31,
      offering_state: 'disabled',
    });

    const current = filterGatewayModelRecords(
      [currentProduct, disabledProduct],
      [currentRelation, disabledRelation],
      false,
    );
    expect(current.products).toEqual([currentProduct]);
    expect(current.relations).toEqual([currentRelation]);

    const all = filterGatewayModelRecords(
      [currentProduct, disabledProduct],
      [currentRelation, disabledRelation],
      true,
    );
    expect(all.products).toHaveLength(2);
    expect(all.relations).toHaveLength(2);
  });

  it('marks fully and partially disabled model groups', () => {
    const currentProduct = product({ offering_state: 'active' });
    const disabledProduct = product({
      id: 2,
      product_transport_id: 11,
      offering_id: 31,
      offering_state: 'disabled',
    });
    expect(gatewayModelRuntimeStatus(groupGatewayModels([currentProduct], [])[0])).toBe('current');
    expect(gatewayModelRuntimeStatus(groupGatewayModels([disabledProduct], [])[0])).toBe('disabled');
    expect(gatewayModelRuntimeStatus(groupGatewayModels([currentProduct, disabledProduct], [])[0])).toBe('mixed');
  });

  it('filters by search and selected credential without changing groups', () => {
    const groups = groupGatewayModels([product({})], [relation({ credential_id: 60 })]);
    expect(filterGatewayModels(groups, 'messages', null)).toHaveLength(0);
    expect(filterGatewayModels(groups, 'chat/completions', null)).toHaveLength(1);
    expect(filterGatewayModels(groups, '', 60)).toHaveLength(1);
    expect(filterGatewayModels(groups, '', 61)).toHaveLength(0);
  });
});
