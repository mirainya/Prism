import { describe, expect, it } from 'vitest';
import { ChannelCapability } from '../../types';
import { getEndpointRouteState } from './CapabilityEndpointList';

const endpoint = (bindings: Array<{status: number; accountStatus: number}> = [], status = 1) => ({
    status,
    accountBindings: bindings.map((binding, index) => ({
        id: String(index + 1),
        endpointId: 'endpoint-1',
        accountId: String(index + 1),
        priority: 0,
        weight: 10,
        accountName: `Key ${index + 1}`,
        ...binding,
    })),
} as ChannelCapability);

describe('getEndpointRouteState', () => {
    it('requires capability, channel, endpoint and at least one key to be enabled', () => {
        expect(getEndpointRouteState(endpoint([{status: 1, accountStatus: 1}]), 1, 1).available).toBe(true);
        expect(getEndpointRouteState(endpoint([{status: 0, accountStatus: 1}]), 1, 1).label).toBe('无可用 Key');
        expect(getEndpointRouteState(endpoint([{status: 1, accountStatus: 0}]), 1, 1).label).toBe('无可用 Key');
        expect(getEndpointRouteState(endpoint([{status: 1, accountStatus: 1}]), 0, 1).label).toBe('能力已禁用');
        expect(getEndpointRouteState(endpoint([{status: 1, accountStatus: 1}]), 1, 0).label).toBe('渠道已禁用');
        expect(getEndpointRouteState(endpoint([], 1), 1, 1).label).toBe('未绑定 Key');
    });
});
