import { describe, expect, it } from 'vitest';
import type { UnifiedAdapterManifest } from '../../services/unifiedGatewayApi';
import {
  buildModelOnboardInput,
  onboardingManifestChoices,
  type NewModelOnboardingValues,
  validateNewModelOnboarding,
} from './onboardingForm';

const baseValues = (patch: Partial<NewModelOnboardingValues> = {}): NewModelOnboardingValues => ({
  releaseId: 7,
  configVersion: 3,
  channelId: 11,
  channelCode: 'aicost',
  poolId: 12,
  poolCode: 'seedance-wholesale',
  adapterCode: 'generic',
  adapterVersion: 1,
  adapterCapability: '',
  apiName: 'seedance2.5-md-480p',
  displayName: 'Seedance 2.5 MD 480p',
  vendorModel: 'seedance2.5-md-480p',
  baseURL: 'https://api.example.com/',
  requestPath: '/v1/videos/generations',
  downstreamPath: '/v1/videos/generations',
  variantCode: 'default',
  capabilityConstraints: { adapter: { profile: 'json_task_v1' } },
  billingUnit: 'request',
  billingMaxQuantity: '',
  sellPrice: '1.25',
  costPrice: '0.80',
  uniqueSuffix: 'test1',
  ...patch,
});

describe('model onboarding form', () => {
  it('uses adapter manifest paths and variants', () => {
    const manifest: UnifiedAdapterManifest = {
      adapter: 'seedance',
      adapter_version: 1,
      protocol: 'seedance',
      has_manifest: true,
      downstream_paths: ['/v1/videos/generations'],
      variants: [
        { code: 'official', display: '官方', models: [], param_hints: {} },
        { code: 'seedance25', display: 'Seedance 2.5', models: [], param_hints: {} },
      ],
    };
    expect(onboardingManifestChoices('seedance', manifest)).toEqual({
      downstreamPaths: ['/v1/videos/generations'],
      variants: [
        { code: 'official', label: '官方' },
        { code: 'seedance25', label: 'Seedance 2.5' },
      ],
    });
  });

  it('provides the generic video fallback', () => {
    expect(onboardingManifestChoices('generic', {
      adapter: 'generic', adapter_version: 1, protocol: 'video_generation', has_manifest: false,
    })).toEqual({
      downstreamPaths: ['/v1/videos/generations'],
      variants: [{ code: 'default', label: '默认' }],
    });
  });

  it('builds a complete task-based onboarding request billed by second', () => {
    const payload = buildModelOnboardInput(baseValues({
      billingUnit: 'second',
      billingMaxQuantity: '12',
    }));
    expect(payload.sku).toMatchObject({
      model_code: 'seedance2.5-md-480p',
      api_name: 'seedance2.5-md-480p',
      operation_code: 'videos.generate',
      route_template: '/v1/videos/generations',
      variant_code: 'default',
      capability_tags: ['video'],
    });
    expect(payload.product).toMatchObject({
      channel_id: 11,
      credential_pool_id: 12,
      adapter_code: 'generic',
      task_scope: 'task',
      upstream_scope_kind: 'credential_pool',
      upstream_scope_key: 'seedance-wholesale',
      base_url: 'https://api.example.com',
    });
    expect(payload.product.actions.map((item) => item.action_code)).toEqual(['submit', 'query']);
    expect(payload.sell_rate).toMatchObject({
      unit_code: 'second',
      unit_price: '1.25',
      component_code: 'duration',
      quantity_source: 'request.seconds',
      max_quantity: '12',
      pricing_mode: 'flat',
    });
    expect(payload.cost_rate).toMatchObject({
      unit_code: 'second',
      unit_price: '0.80',
      component_code: 'duration',
      quantity_source: 'request.seconds',
      max_quantity: '12',
      pricing_mode: 'flat',
    });
    expect(payload.product.product_code.length).toBeLessThanOrEqual(128);
  });

  it('builds a synchronous model billed by request with a fixed quantity bound', () => {
    const payload = buildModelOnboardInput(baseValues({
      adapterCode: 'openai_chat',
      apiName: 'gpt-new',
      displayName: 'GPT New',
      vendorModel: 'gpt-new-upstream',
      requestPath: '/v1/chat/completions',
      downstreamPath: '/v1/chat/completions',
      capabilityConstraints: {},
      billingUnit: 'request',
      billingMaxQuantity: '86400',
    }));
    expect(payload.sku.operation_code).toBe('chat.completions');
    expect(payload.sku.capability_tags).toEqual(['llm']);
    expect(payload.product.task_scope).toBe('request');
    expect(payload.product.actions).toEqual([]);
    expect(payload.sell_rate).toMatchObject({
      unit_code: 'request',
      component_code: 'request',
      quantity_source: 'one',
      max_quantity: '1',
      pricing_mode: 'flat',
    });
    expect(payload.cost_rate).toMatchObject({
      unit_code: 'request',
      unit_price: '0.80',
      component_code: 'request',
      quantity_source: 'one',
      max_quantity: '1',
      pricing_mode: 'flat',
    });
  });

  it('builds a synchronous image model with the routable image capability', () => {
    const payload = buildModelOnboardInput(baseValues({
      adapterCode: 'openai_images',
      adapterCapability: 'image',
      apiName: 'gpt-image-public',
      displayName: 'GPT Image',
      vendorModel: 'gpt-image-1',
      requestPath: '/v1/images/generations',
      downstreamPath: '/v1/images/generations',
      capabilityConstraints: { sizes: ['1024x1024'] },
    }));

    expect(payload.sku.capability_tags).toEqual(['image_generation']);
    expect(payload.product.task_scope).toBe('request');
    expect(payload.product.actions).toEqual([]);
  });

  it('builds an asynchronous image model with submit and query actions', () => {
    const payload = buildModelOnboardInput(baseValues({
      adapterCode: 'openai_images',
      adapterCapability: 'image',
      apiName: 'gpt-image-async',
      displayName: 'GPT Image Async',
      vendorModel: 'gpt-image-2',
      requestPath: '/v1/images/generations?async=true',
      downstreamPath: '/v1/images/generations',
      capabilityConstraints: {
        async_image: {
          profile: 'json_image_task_v1',
          submit: { method: 'POST', path: '/v1/images/generations?async=true' },
          poll: { method: 'GET', path: '/v1/tasks/{task_id}' },
        },
      },
    }));

    expect(payload.sku.capability_tags).toEqual(['image_generation']);
    expect(payload.product.task_scope).toBe('task');
    expect(payload.product.actions.map((item) => item.action_code)).toEqual(['submit', 'query']);
  });

  it('requires a positive cost price', () => {
    const values = baseValues({ costPrice: '' });
    expect(validateNewModelOnboarding(values)).toContain('成本');
    expect(() => buildModelOnboardInput(values)).toThrow('成本');
  });

  it('rejects invalid second billing bounds and non-video models', () => {
    for (const billingMaxQuantity of ['', '0', '1.5', '86401']) {
      expect(validateNewModelOnboarding(baseValues({
        billingUnit: 'second',
        billingMaxQuantity,
      }))).toContain('1-86400');
    }
    expect(validateNewModelOnboarding(baseValues({
      adapterCode: 'openai_chat',
      adapterCapability: '',
      requestPath: '/v1/chat/completions',
      downstreamPath: '/v1/chat/completions',
      billingUnit: 'second',
      billingMaxQuantity: '60',
    }))).toContain('视频模型');
  });

  it('accepts actual AICost public model names', () => {
    for (const apiName of ['seedance2.5-9图', 'seedance2.5-10图', 'seedance2.0-480p-100%']) {
      expect(validateNewModelOnboarding(baseValues({ apiName }))).toBe('');
    }
  });

  it('rejects a missing billing unit, invalid public identity, and prices', () => {
    expect(validateNewModelOnboarding(baseValues({ billingUnit: '' }))).toContain('计费单位');
    expect(validateNewModelOnboarding(baseValues({ apiName: '模型' }))).toContain('公开调用名');
    expect(validateNewModelOnboarding(baseValues({ apiName: 'seedance2.5 图' }))).toContain('公开调用名');
    expect(validateNewModelOnboarding(baseValues({ apiName: `a${'图'.repeat(43)}` }))).toContain('公开调用名');
    expect(validateNewModelOnboarding(baseValues({ sellPrice: '0' }))).toContain('售价');
    expect(validateNewModelOnboarding(baseValues({ costPrice: '-1' }))).toContain('成本');
  });
});
