import { describe, expect, it } from 'vitest';
import {
  formatCredentialDisplayName,
  formatOpsDate,
  formatServiceSpecName,
  formatSKUDeliveryLabel,
  formatSKUDisplayName,
  formatSKUOperationLabel,
  formatSKUServiceSummary,
  formatSKUServiceTiers,
  formatSKUVariantLabel,
  opsStatusLabel,
  opsStatusVariant,
  shortDigest,
} from './presentation';

describe('ops console presentation helpers', () => {
  it('maps operational states to concise labels and variants', () => {
    expect(opsStatusLabel('published')).toBe('已发布');
    expect(opsStatusVariant('published')).toBe('success');
    expect(opsStatusLabel('failed')).toBe('失败');
    expect(opsStatusVariant('failed')).toBe('error');
    expect(opsStatusVariant('draft')).toBe('warning');
  });

  it('shortens digests without changing short values', () => {
    expect(shortDigest('abcdef')).toBe('abcdef');
    expect(shortDigest('abcdefghijklmnop')).toBe('abcdefghijkl');
    expect(shortDigest(null)).toBe('-');
  });

  it('formats valid dates and preserves invalid values', () => {
    expect(formatOpsDate('2026-09-11T08:30:00Z')).toMatch(/^2026\/09\/11/);
    expect(formatOpsDate('not-a-date')).toBe('not-a-date');
    expect(formatOpsDate(null)).toBe('-');
  });

  it('replaces generated migration credential codes with readable names', () => {
    expect(formatCredentialDisplayName('legacy-credential-gw-channel-key-28', '国模', 0)).toBe('国模 · API Key 1');
    expect(formatCredentialDisplayName('legacy-credential-gw-channel-key-29', '', 1)).toBe('上游 · API Key 2');
    expect(formatCredentialDisplayName('team-openai-primary', 'OpenAI', 0)).toBe('team-openai-primary');
  });

  it('formats SKU operations, delivery modes, variants and service tiers', () => {
    expect(formatSKUOperationLabel('videos.generate')).toBe('视频生成');
    expect(formatSKUDeliveryLabel('managed_copy')).toBe('托管副本');
    expect(formatSKUVariantLabel('default')).toBe('');
    expect(formatSKUVariantLabel('h_channel')).toBe('H 渠道');
    expect(formatSKUServiceTiers(['priority', 'standard', 'vip', 'standard'])).toBe('标准 / 优先 / VIP');
  });

  it('keeps generated SKU codes out of the operator-facing name', () => {
    const migratedSKU = {
      sku_code: 'legacy-seedance-2.5-videos-generate-reference',
      variant_code: 'default',
      delivery_mode: 'reference',
      service_tiers: ['priority', 'standard', 'vip'],
      model_code: 'seedance-2.5',
      api_name: 'seedance-2.5',
      display_name: 'seedance-2.5',
      operation_code: 'videos.generate',
    };

    expect(formatSKUDisplayName(migratedSKU)).toBe('视频生成');
    expect(formatSKUServiceSummary(migratedSKU)).toBe('标准 / 优先 / VIP · 直链');
    expect(formatSKUDisplayName({ ...migratedSKU, display_name: '快速生成' })).toBe('快速生成 · 视频生成');
    expect(formatSKUServiceSummary({ ...migratedSKU, variant_code: 'official' })).toContain('变体：官方渠道');
  });

  it('supports the service specification alias without exposing migration codes', () => {
    expect(formatServiceSpecName({
      display_name: 'legacy-deepseek-v4-pro-chat-completions-reference',
      model_code: 'legacy-deepseek-v4-pro',
      api_name: '',
      operation_code: 'videos.generate',
      variant_code: 'default',
    })).toBe('视频生成');
  });

});
