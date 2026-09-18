import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  fetchUnifiedChannels,
  updateUnifiedChannel,
  updateUnifiedPool,
  transitionUnifiedPool,
  type UnifiedChannel,
  type UnifiedPool,
} from './unifiedChannelApi';
import { request } from './request';
vi.mock('./request', () => ({ request: vi.fn().mockResolvedValue({}) }));
afterEach(() => vi.clearAllMocks());
describe('unified channel configuration', () => {
  it('encodes filters and passes cancellation', async () => {
    const controller = new AbortController();
    await fetchUnifiedChannels(2, 50, 'a&b', 'active', controller.signal, 'image');
    expect(request).toHaveBeenCalledWith(
      '/admin/unified-gateway/channels?page=2&page_size=50&q=a%26b&status=active&type=image',
      { signal: controller.signal }
    );
  });
  it('keeps the observed channel values in optimistic updates', async () => {
    const channel = {
      id: 2,
      display_name: 'Old',
      status: 'active',
    } as UnifiedChannel;
    await updateUnifiedChannel(channel, {
      display_name: 'New',
      status: 'disabled',
    });
    expect(request).toHaveBeenCalledWith('/admin/unified-gateway/channels/2', {
      method: 'PUT',
      body: JSON.stringify({
        display_name: 'New',
        status: 'disabled',
        expected_display_name: 'Old',
        expected_status: 'active',
      }),
    });
  });
  it('preserves positive and null limits and expected pool version', async () => {
    const pool = { id: 5, config_version: 8 } as UnifiedPool;
    await updateUnifiedPool(pool, {
      display_name: 'Pool',
      request_limit: null,
      task_limit: 1,
    });
    expect(request).toHaveBeenCalledWith('/admin/unified-gateway/pools/5', {
      method: 'PUT',
      body: JSON.stringify({
        display_name: 'Pool',
        request_limit: null,
        task_limit: 1,
        expected_version: 8,
      }),
    });
  });
  it('sends the fixed-point upstream cost group ratio', async () => {
    const pool = { id: 5, config_version: 8 } as UnifiedPool;
    await updateUnifiedPool(pool, {
      display_name: 'Pool',
      request_limit: null,
      task_limit: 1,
      cost_group_ratio: '1.25000000',
    });
    expect(request).toHaveBeenCalledWith('/admin/unified-gateway/pools/5', {
      method: 'PUT',
      body: JSON.stringify({
        display_name: 'Pool',
        request_limit: null,
        task_limit: 1,
        cost_group_ratio: '1.25000000',
        expected_version: 8,
      }),
    });
  });
  it('drains before disabling', async () => {
    const pool = { id: 5, status: 'active', config_version: 8 } as UnifiedPool;
    await transitionUnifiedPool(pool);
    expect(request).toHaveBeenCalledWith(
      '/admin/unified-gateway/pools/5/status',
      {
        method: 'POST',
        body: JSON.stringify({ status: 'draining', expected_version: 8 }),
      }
    );
  });
});
