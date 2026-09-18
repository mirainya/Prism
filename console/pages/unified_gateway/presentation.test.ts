import { describe, expect, it } from 'vitest';
import { describeRelationBlock, formatDate, statusLabel } from './presentation';

describe('shared gateway presentation', () => {
  it('handles absent and invalid dates', () => {
    expect(formatDate(null)).toBe('-');
    expect(formatDate('invalid')).toBe('-');
  });

  it('formats request states and relation blocks', () => {
    expect(statusLabel('response_recorded')).toBe('响应已记录');
    expect(statusLabel('custom_state')).toBe('custom_state');
    expect(describeRelationBlock('circuit_broken')).toBe('熔断中');
    expect(describeRelationBlock('credential_disabled')).toBe('凭据状态 disabled');
  });
});
