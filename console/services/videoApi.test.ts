import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchVideoTasks } from './videoApi';
import { request } from './request';

vi.mock('./request', () => ({ request: vi.fn() }));

const requestMock = vi.mocked(request);

describe('videoApi', () => {
  beforeEach(() => requestMock.mockReset());

  it('fills pagination fields omitted by the unified video list endpoint', async () => {
    requestMock.mockResolvedValue({
      items: [],
      total: 0,
      snapshot_at: '2026-09-07T00:00:00Z',
    });

    await expect(fetchVideoTasks({ page: 3, page_size: 50 })).resolves.toEqual({
      items: [],
      total: 0,
      page: 3,
      page_size: 50,
      snapshot_at: '2026-09-07T00:00:00Z',
    });
    expect(requestMock).toHaveBeenCalledWith(
      '/admin/video/tasks?page=3&page_size=50',
    );
  });

  it('normalizes invalid pagination and preserves encoded filters', async () => {
    requestMock.mockResolvedValue({
      items: [],
      total: '2',
      page: '2',
      page_size: '10',
    });

    const result = await fetchVideoTasks({
      page: 0,
      page_size: 1000,
      keyword: 'seed & test',
      status: 'tracking',
    });

    expect(result).toMatchObject({ total: 2, page: 2, page_size: 10 });
    const [url] = requestMock.mock.calls[0] as [string];
    const query = new URLSearchParams(url.slice(url.indexOf('?') + 1));
    expect(query.get('page')).toBe('1');
    expect(query.get('page_size')).toBe('100');
    expect(query.get('keyword')).toBe('seed & test');
    expect(query.get('status')).toBe('tracking');
  });
});
