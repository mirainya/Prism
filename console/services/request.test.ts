import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { getAuthHeader, request } from './request';

const fetchMock = vi.fn();
const storage = new Map<string, string>();
const localStorageMock: Storage = {
  get length() {
    return storage.size;
  },
  clear() {
    storage.clear();
  },
  getItem(key) {
    return storage.get(key) ?? null;
  },
  key(index) {
    return Array.from(storage.keys())[index] ?? null;
  },
  removeItem(key) {
    storage.delete(key);
  },
  setItem(key, value) {
    storage.set(key, value);
  },
};

const jsonResponse = (body: unknown, status = 200): Response => ({
  status,
  ok: status >= 200 && status < 300,
  json: vi.fn().mockResolvedValue(body),
} as unknown as Response);

describe('request', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', localStorageMock);
    localStorage.clear();
    fetchMock.mockReset();
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('builds the authorization header from local storage', () => {
    expect(getAuthHeader()).toEqual({});

    localStorage.setItem('prism_token', 'test-token');

    expect(getAuthHeader()).toEqual({ Authorization: 'Bearer test-token' });
  });

  it('adds the API prefix, merges headers, and unwraps response data', async () => {
    localStorage.setItem('prism_token', 'test-token');
    fetchMock.mockResolvedValue(jsonResponse({ data: { id: 7 } }));

    const result = await request<{ id: number }>('/resource', {
      method: 'POST',
      headers: { 'X-Trace-ID': 'trace-1' },
      body: JSON.stringify({ name: 'Prism' }),
    });

    expect(result).toEqual({ id: 7 });
    expect(fetchMock).toHaveBeenCalledWith('/api/resource', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: 'Bearer test-token',
        'X-Trace-ID': 'trace-1',
      },
      body: JSON.stringify({ name: 'Prism' }),
    });
  });

  it('uses the server error message for failed requests', async () => {
    fetchMock.mockResolvedValue(jsonResponse({ message: 'invalid request' }, 400));

    await expect(request('/resource')).rejects.toThrow('invalid request');
  });
});
