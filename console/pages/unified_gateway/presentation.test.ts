import { describe, expect, it } from 'vitest';
import type { UnifiedGatewayOverview } from '../../services/unifiedGatewayApi';
import { formatDate, gatewayChecks, gatewayStatus } from './presentation';

const overview: UnifiedGatewayOverview = {
  state: 'target_configured', ready_for_cutover: false,
  runtime: { active_release_id: 1, release_state_version: 1, deployment_id: 0, deployment_status: '' },
  target: { channels: 4, models: 13, credentials: 9, catalog_releases: 1, calls: 0, sell_rates: 0, cost_rates: 0, currencies: 0 },
  legacy: { channels: 6, abilities: 14 },
};

describe('unified gateway status', () => {
  it('does not treat configured tables or a release pointer as readiness', () => {
    expect(gatewayStatus(overview)).toEqual({ ready: false, label: '迁移未完成' });
    expect(gatewayStatus({ ...overview, legacy: { channels: 0, abilities: 0 } }).ready).toBe(false);
  });
  it('requires both runtime proof and a completed cutover check', () => {
    expect(gatewayStatus({ ...overview, ready_for_cutover: true }).ready).toBe(false);
    expect(gatewayStatus({ ...overview, ready_for_cutover: true, runtime_ready: true }).ready).toBe(false);
    expect(gatewayStatus({ ...overview, legacy: { channels: 0, abilities: 0 }, ready_for_cutover: true, runtime_ready: true }).ready).toBe(true);
    expect(gatewayStatus({ ...overview, ready_for_cutover: true, runtime_ready: true }, true).ready).toBe(false);
  });
  it('handles initial and unknown states without a false success', () => {
    expect(gatewayStatus(null).ready).toBe(false);
    expect(gatewayStatus({ ...overview, state: 'future_state' }).ready).toBe(false);
  });
  it('exposes missing prices and currency', () => {
    expect(gatewayChecks(overview).filter(item => item.ok).map(item => item.label)).toEqual(['活动目录']);
  });
  it('handles absent and invalid dates', () => {
    expect(formatDate(null)).toBe('-');
    expect(formatDate('invalid')).toBe('-');
  });
});
