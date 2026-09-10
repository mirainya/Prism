import { afterEach, expect, it, vi } from 'vitest';
import { request } from './request';
import {
  createManagedCredential,
  fetchManagedCredentials,
  transitionManagedCredential,
  updateManagedCredential,
} from './unifiedCredentialApi';
import type { UnifiedCredential } from './unifiedGatewayApi';

vi.mock('./request', () => ({ request: vi.fn().mockResolvedValue({}) }));
afterEach(() => vi.clearAllMocks());
it('filters the selected pool and cancels stale requests', async () => {
  const controller = new AbortController();
  await fetchManagedCredentials(2, 50, 8, controller.signal);
  expect(request).toHaveBeenCalledWith(
    '/admin/unified-gateway/credentials?page=2&page_size=50&pool_id=8',
    { signal: controller.signal },
  );
});
it('sends secrets only to creation', async () => {
  const fields = { weight: 3, request_limit: null, task_limit: 2 };
  await createManagedCredential(8, {
    ...fields,
    credential_code: 'test',
    secret: 'test-only-secret',
    purposes: ['execution'],
  });
  expect(request).toHaveBeenLastCalledWith(
    '/admin/unified-gateway/pools/8/credentials',
    {
      method: 'POST',
      body: JSON.stringify({
        ...fields,
        credential_code: 'test',
        secret: 'test-only-secret',
        purposes: ['execution'],
      }),
    },
  );
  const item = {
    id: 5,
    config_version: 7,
    status: 'active',
  } as UnifiedCredential;
  await updateManagedCredential(item, fields);
  expect(request).toHaveBeenLastCalledWith(
    '/admin/unified-gateway/credentials/5',
    { method: 'PUT', body: JSON.stringify({ ...fields, expected_version: 7 }) },
  );
  await transitionManagedCredential(item);
  expect(request).toHaveBeenLastCalledWith(
    '/admin/unified-gateway/credentials/5/status',
    {
      method: 'POST',
      body: JSON.stringify({ status: 'draining', expected_version: 7 }),
    },
  );
});
