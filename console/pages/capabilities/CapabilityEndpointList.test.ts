import { describe, expect, it } from 'vitest';
import { Channel, ChannelCapability } from '../../types';
import { getEndpointOperationLabel, getEndpointOriginPresentation, getEndpointRouteState, groupEndpointModelRoutes, summarizeEndpointModelRoutes } from './CapabilityEndpointList';

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

describe('getEndpointOriginPresentation', () => {
    it('shows the persisted Key discovery source', () => {
        const value = getEndpointOriginPresentation({
            ...endpoint(),
            originType: 'key_discovery',
            originAccountId: '24',
            originSnapshot: {accountId: 24, accountName: 'gpt-image-2'},
        } as ChannelCapability);
        expect(value).toMatchObject({label: 'Key 发现', detail: 'gpt-image-2'});
    });

    it('marks migrated source data as inferred instead of claiming discovery', () => {
        const value = getEndpointOriginPresentation({
            ...endpoint(),
            originType: 'legacy_inferred',
            originAccountId: '24',
            originSnapshot: {accountId: 24, accountName: 'gpt-image-2', inferred: true},
        } as ChannelCapability);
        expect(value).toMatchObject({label: '历史推断', detail: 'gpt-image-2'});
    });
});

describe('groupEndpointModelRoutes', () => {
    it('shows multiple operations on one physical endpoint', () => {
        const value = {
            ...endpoint(),
            routeOperation: 'images.generate',
            supportedOperations: ['images.generate', 'images.edit'],
        } as ChannelCapability;
        expect(getEndpointOperationLabel(value)).toBe('生成 / 编辑');
    });

    it('groups generate and edit endpoints for the same channel and upstream model', () => {
        const endpoints = [
            {...endpoint([{status: 1, accountStatus: 1}]), id: '2', channelId: '10', model: 'gpt-image-1', routeOperation: 'images.edit'},
            {...endpoint([{status: 1, accountStatus: 1}]), id: '1', channelId: '10', model: 'gpt-image-1', routeOperation: 'images.generate'},
            {...endpoint([{status: 1, accountStatus: 1}]), id: '3', channelId: '11', model: 'gpt-image-1', routeOperation: 'images.generate'},
        ] as ChannelCapability[];

        const groups = groupEndpointModelRoutes(endpoints);
        expect(groups).toHaveLength(2);
        expect(groups[0].endpoints.map(item => item.routeOperation)).toEqual(['images.generate', 'images.edit']);
    });

    it('keeps legacy endpoints without an upstream model separate', () => {
        const endpoints = [
            {...endpoint(), id: '1', channelId: '10', model: '', name: 'legacy 1'},
            {...endpoint(), id: '2', channelId: '10', model: '', name: 'legacy 2'},
        ] as ChannelCapability[];
        expect(groupEndpointModelRoutes(endpoints)).toHaveLength(2);
    });

    it('summarizes model routes, operations, unique keys and unavailable operations', () => {
        const endpoints = [
            {...endpoint([{status: 1, accountStatus: 1}]), id: '1', channelId: '10', model: 'gpt-image-1'},
            {...endpoint([{status: 0, accountStatus: 1}]), id: '2', channelId: '10', model: 'gpt-image-1'},
        ] as ChannelCapability[];
        const channels = new Map([['10', {id: '10', status: 1} as Channel]]);
        expect(summarizeEndpointModelRoutes(endpoints, 1, channels)).toEqual({
            modelRouteCount: 1,
            operationCount: 2,
            keyCount: 1,
            unavailableCount: 1,
        });
    });
});
