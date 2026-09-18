import { describe, expect, it } from 'vitest';

import {
  describeAvailability,
  filterPricingModels,
  formatPricingSKUName,
  formatPublicModelName,
  formatPublicText,
  formatPricingRate,
  groupPricingModels,
  isAvailabilityStale,
} from './Pricing';
import type {
  PublicPricingAvailability,
  PublicPricingCurrency,
  PublicPricingModel,
  PublicPricingSKU,
  PublicRateComponent,
} from '../services/pricingApi';

const currency: PublicPricingCurrency = { code: 'USD', version: 1, fraction_digits: 8 };

const component = (unitScale: number, overrides: Partial<PublicRateComponent> = {}): PublicRateComponent => ({
  id: 1,
  component_code: 'input',
  unit_code: 'token',
  quantity_source: 'usage.input_tokens',
  charge_event: 'call.succeeded',
  unit_price: '0.00000300',
  unit_scale: unitScale,
  quantity_step: '0',
  max_quantity: '1000000',
  ...overrides,
});

describe('formatPricingRate', () => {
  it('shows token rates per million tokens without decimal noise', () => {
    expect(formatPricingRate(component(0), currency)).toBe('USD 3 / 百万 token');
    expect(formatPricingRate(component(0, { unit_price: '0.000005000000000000' }), currency)).toBe('USD 5 / 百万 token');
  });

  it('normalizes an already scaled token rate to the same million-token unit', () => {
    expect(formatPricingRate(component(3, { unit_price: '0.003000' }), currency)).toBe('USD 3 / 百万 token');
  });

  it('keeps video billing units and removes trailing zeroes', () => {
    expect(formatPricingRate(component(0, {
      unit_code: 'second',
      unit_price: '0.430000000000000000',
    }), currency)).toBe('USD 0.43 / 秒');
    expect(formatPricingRate(component(0, {
      unit_code: 'request',
      unit_price: '2.200000000000000000',
    }), currency)).toBe('USD 2.2 / 次');
  });
});

describe('public pricing labels', () => {
  const sku: PublicPricingSKU = {
    id: 1,
    code: 'legacy-deepseek-v4-pro-chat-completions-reference',
    operation: 'chat.completions',
    routes: [{ method: 'POST', path: '/v1/chat/completions' }],
    delivery_mode: 'reference',
    max_results: 1,
    idempotency_mode: 'optional',
    service_tiers: ['standard'],
    currency,
    components: [component(0)],
  };

  it('removes the catalog source from model names and descriptions', () => {
    expect(formatPublicModelName({
      code: 'aicost-seedance-2-5-vid',
      model_code: 'aicost-seedance-2-5-vid',
      name: 'AiCost Seedance 2.5 VID',
    })).toBe('Seedance 2.5 VID');
    expect(formatPublicText('AiCost Seedance 2.5 视频模型')).toBe('Seedance 2.5 视频模型');
  });

  it('uses product language instead of exposing an internal SKU code', () => {
    expect(formatPricingSKUName(sku)).toBe('Chat 对话 · 标准');
    expect(formatPricingSKUName(sku)).not.toContain('legacy');
    expect(formatPricingSKUName(sku)).not.toContain('deepseek-v4-pro');
  });
});

const availability = (overrides: Partial<PublicPricingAvailability> = {}): PublicPricingAvailability => ({
  success_rate: '88.30',
  source: 'upstream',
  window_minutes: 120,
  observed_at: '2026-09-14T10:00:00Z',
  ...overrides,
});

describe('describeAvailability', () => {
  it('names the sample count only for locally measured rates', () => {
    expect(describeAvailability(availability({ source: 'local', samples: 42 }))).toBe('近 2 小时本站实测 42 次调用');
  });

  // The provider publishes no sample count, so the label must not imply one.
  it('says the upstream figure carries no sample count', () => {
    expect(describeAvailability(availability())).toBe('近 2 小时上游报告，未提供样本数');
  });

  it('keeps a window that is not whole hours in minutes', () => {
    expect(describeAvailability(availability({ window_minutes: 90 }))).toBe('近 90 分钟上游报告，未提供样本数');
  });
});

describe('isAvailabilityStale', () => {
  const observedAt = Date.parse('2026-09-14T10:00:00Z');

  it('accepts an observation from a few minutes ago', () => {
    expect(isAvailabilityStale(availability(), observedAt + 5 * 60_000)).toBe(false);
  });

  it('rejects an observation older than the staleness threshold', () => {
    expect(isAvailabilityStale(availability(), observedAt + 31 * 60_000)).toBe(true);
  });

  // An unparseable timestamp must never render as a current figure.
  it('treats an unusable timestamp as stale', () => {
    expect(isAvailabilityStale(availability({ observed_at: '' }), observedAt)).toBe(true);
  });
});

const pricingModel = (overrides: Partial<PublicPricingModel> = {}): PublicPricingModel => ({
  code: 'gpt-5.6-sol',
  model_code: 'gpt-5.6',
  name: 'GPT 5.6 Sol',
  type: 'chat',
  description: 'General LLM',
  visibility: 'public',
  skus: [{
    id: 1,
    code: 'standard-chat',
    operation: 'chat.completions',
    routes: [{ method: 'POST', path: '/v1/chat/completions' }],
    delivery_mode: 'sync',
    max_results: 1,
    idempotency_mode: '',
    service_tiers: ['default'],
    currency,
    components: [component(0)],
  }],
  ...overrides,
});

describe('filterPricingModels', () => {
  const models = [
    pricingModel(),
    pricingModel({ code: 'seedance-2.0', model_code: 'seedance-2.0', name: '豆包视频', type: 'video', description: '视频生成', skus: [] }),
  ];

  it('searches public model and specification labels case-insensitively', () => {
    expect(filterPricingModels(models, 'GPT', 'all').map(model => model.code)).toEqual(['gpt-5.6-sol']);
    expect(filterPricingModels(models, 'Chat 对话', 'all').map(model => model.code)).toEqual(['gpt-5.6-sol']);
    expect(filterPricingModels(models, '视频', 'all').map(model => model.code)).toEqual(['seedance-2.0']);
  });

  it('does not make hidden source names or internal SKU codes searchable', () => {
    const sourced = pricingModel({
      name: 'AiCost GPT 5.6 Sol',
      skus: [{ ...pricingModel().skus[0], code: 'legacy-gpt-5-6-chat-completions-reference' }],
    });
    expect(filterPricingModels([sourced], 'aicost', 'all')).toEqual([]);
    expect(filterPricingModels([sourced], 'legacy-gpt', 'all')).toEqual([]);
  });

  it('filters by model type', () => {
    expect(filterPricingModels(models, '', 'video').map(model => model.code)).toEqual(['seedance-2.0']);
  });
});

describe('groupPricingModels', () => {
  it('uses the fixed product order and keeps server order inside a group', () => {
    const models = [
      pricingModel({ code: 'video-1', type: 'video' }),
      pricingModel({ code: 'chat-1', type: 'chat' }),
      pricingModel({ code: 'chat-2', type: 'chat' }),
      pricingModel({ code: 'unknown-1', type: 'custom' }),
    ];

    const groups = groupPricingModels(models);
    expect(groups.map(group => group.key)).toEqual(['chat', 'video', 'other']);
    expect(groups[0].models.map(model => model.code)).toEqual(['chat-1', 'chat-2']);
  });
});
