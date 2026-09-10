import { afterEach, describe, expect, it, vi } from 'vitest';
import { request } from './request';
import {
  fetchCatalogPriceCandidates,
  reviewCatalogDiscovery,
  scheduleCatalogDiscovery,
  transitionCatalogSource,
  type UnifiedCatalogDiscovery,
  type UnifiedCatalogSource,
} from './unifiedCatalogSourceApi';

vi.mock('./request', () => ({ request: vi.fn().mockResolvedValue({}) }));

afterEach(() => vi.clearAllMocks());

describe('unified catalog source API', () => {
  it('schedules a source inside the selected immutable draft', async () => {
    await scheduleCatalogDiscovery(7, 9);
    expect(request).toHaveBeenCalledWith(
      '/admin/unified-gateway/catalog/7/catalog-sources/9/discover',
      { method: 'POST', body: '{}' },
    );
  });

  it('uses the observed source version while draining', async () => {
    await transitionCatalogSource({
      id: 9,
      status: 'active',
      state_version: 4,
    } as UnifiedCatalogSource);
    expect(request).toHaveBeenCalledWith(
      '/admin/unified-gateway/catalog-sources/9/status',
      {
        method: 'POST',
        body: JSON.stringify({
          status: 'draining',
          expected_version: 4,
          reason_code: 'admin_transition',
        }),
      },
    );
  });

  it('reviews the exact snapshot version', async () => {
    await reviewCatalogDiscovery(
      { snapshot_id: 12, review_version: 3 } as UnifiedCatalogDiscovery,
      'accepted',
    );
    expect(request).toHaveBeenCalledWith(
      '/admin/unified-gateway/catalog-discoveries/12/review',
      {
        method: 'POST',
        body: JSON.stringify({
          decision: 'accepted',
          reason_code: 'admin_accepted',
          expected_version: 3,
        }),
      },
    );
  });

  it('encodes price candidate filters', async () => {
    const controller = new AbortController();
    await fetchCatalogPriceCandidates(5, 2, 50, 'pending & new', controller.signal);
    expect(request).toHaveBeenCalledWith(
      '/admin/unified-gateway/catalog/5/price-candidates?page=2&page_size=50&state=pending%20%26%20new',
      { signal: controller.signal },
    );
  });
});
