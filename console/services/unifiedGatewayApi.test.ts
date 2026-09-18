import { beforeEach, describe, expect, it, vi } from 'vitest';
import { request } from './request';
import { createUnifiedActiveProduct, fetchUnifiedCallDetail, fetchUnifiedCalls, fetchUnifiedCatalog, fetchUnifiedCatalogDiscoveries, fetchUnifiedCatalogDiscoveryItems, fetchUnifiedCatalogModelEntries, fetchUnifiedCatalogPriceCandidates, fetchUnifiedCatalogSources, fetchUnifiedChannelRelations, fetchUnifiedCredentials, fetchUnifiedRequestLogPayloads, fetchUnifiedRequestLogs, type UnifiedActiveProductCreate } from './unifiedGatewayApi';

vi.mock('./request', () => ({ request: vi.fn() }));
beforeEach(() => vi.mocked(request).mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }));

describe('unified gateway pagination', () => {
  for (const [name, load] of [
    ['calls', fetchUnifiedCalls], ['catalog', fetchUnifiedCatalog],
    ['credentials', fetchUnifiedCredentials],
  ] as const) {
    it(`sends page size and cancellation for ${name}`, async () => {
      const controller = new AbortController();
      await load(3, 50, controller.signal);
      expect(request).toHaveBeenCalledWith(`/admin/unified-gateway/${name}?page=3&page_size=50`, { signal: controller.signal });
    });
  }
  it('paginates attempts within the selected call', async () => {
    await fetchUnifiedCallDetail(7, 2, 10);
    expect(request).toHaveBeenCalledWith('/admin/unified-gateway/calls/7?page=2&page_size=10', { signal: undefined });
  });
  it('loads model entries within one release', async () => {
    const controller = new AbortController();
    await fetchUnifiedCatalogModelEntries(12, 2, 50, controller.signal);
    expect(request).toHaveBeenCalledWith('/admin/unified-gateway/catalog/12/model-entries?page=2&page_size=50', { signal: controller.signal });
  });
  it('encodes model filters before server pagination', async () => {
    const controller = new AbortController();
    await fetchUnifiedCatalogModelEntries(12, 3, 20, { q: 'image & edit', type: 'image', status: 'inactive' }, controller.signal);
    expect(request).toHaveBeenCalledWith('/admin/unified-gateway/catalog/12/model-entries?page=3&page_size=20&q=image+%26+edit&type=image&status=inactive', { signal: controller.signal });
  });
  it('paginates request logs independently and forwards cancellation', async () => {
    const controller = new AbortController();
    await fetchUnifiedRequestLogs(7, 3, 50, controller.signal);
    expect(request).toHaveBeenCalledWith('/admin/unified-gateway/calls/7/requests?page=3&page_size=50', { signal: controller.signal });
  });
  it('loads one request payload only after it is selected', async () => {
    const controller = new AbortController();
    await fetchUnifiedRequestLogPayloads(7, 19, controller.signal);
    expect(request).toHaveBeenCalledWith('/admin/unified-gateway/calls/7/requests/19/payloads', { signal: controller.signal });
  });
  it('loads relations for the selected channel and forwards cancellation', async () => {
    const controller = new AbortController();
    await fetchUnifiedChannelRelations(23, controller.signal);
    expect(request).toHaveBeenCalledWith('/admin/unified-gateway/channels/23/relations', { signal: controller.signal });
  });
  it('creates an upstream product through the direct active-catalog endpoint', async () => {
    const payload = { expected_active_release_id: 2, expected_config_version: 3 } as UnifiedActiveProductCreate;
    await createUnifiedActiveProduct(payload);
    expect(request).toHaveBeenCalledWith('/admin/unified-gateway/catalog-changes/product-create', {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  });
  it('paginates catalog sources and discovery evidence', async () => {
    const controller = new AbortController();
    await fetchUnifiedCatalogSources(2, 50, 'active', controller.signal);
    expect(request).toHaveBeenLastCalledWith('/admin/unified-gateway/catalog-sources?page=2&page_size=50&status=active', { signal: controller.signal });
    await fetchUnifiedCatalogDiscoveries(7, 3, 20, controller.signal);
    expect(request).toHaveBeenLastCalledWith('/admin/unified-gateway/catalog/7/discoveries?page=3&page_size=20', { signal: controller.signal });
    await fetchUnifiedCatalogDiscoveryItems(31, 4, 10, controller.signal);
    expect(request).toHaveBeenLastCalledWith('/admin/unified-gateway/catalog-discoveries/31/items?page=4&page_size=10', { signal: controller.signal });
    await fetchUnifiedCatalogPriceCandidates(7, 5, 50, 'pending', controller.signal);
    expect(request).toHaveBeenLastCalledWith('/admin/unified-gateway/catalog/7/price-candidates?page=5&page_size=50&state=pending', { signal: controller.signal });
  });
});
