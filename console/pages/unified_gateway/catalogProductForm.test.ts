import { describe, expect, it } from 'vitest';
import {
  buildCatalogAllowedHosts,
  catalogDefaultRequestPath,
  catalogRequestMethods,
  catalogTaskPolicy,
  genericCatalogTemplate,
  parseCatalogConstraints,
} from './catalogProductForm';

describe('catalog product form', () => {
  it('adds and deduplicates the upstream host', () => {
    expect(buildCatalogAllowedHosts('https://API.Example.com/v1', 'api.example.com, https://cdn.example.com')).toEqual([
      { protocol: 'https', host: 'api.example.com', port: 443 },
      { protocol: 'https', host: 'cdn.example.com', port: 443 },
    ]);
  });

  it('rejects non-origin result host values', () => {
    expect(() => buildCatalogAllowedHosts('https://api.example.com', 'https://cdn.example.com/file.mp4')).toThrow('结果来源域名格式无效');
  });

  it('pins generic submit transport to formal fields', () => {
    const template = genericCatalogTemplate('POST', '/tasks');
    const value = parseCatalogConstraints(template, 'generic', 'PATCH', '/jobs') as { adapter: { submit: Record<string, unknown> } };
    expect(value.adapter.submit).toMatchObject({ enabled: true, method: 'PATCH', path: '/jobs' });
  });

  it('exposes extended methods only for generic declarations', () => {
    expect(catalogRequestMethods('generic')).toContain('PATCH');
    expect(catalogRequestMethods('seedance')).toEqual(['POST']);
    expect(catalogRequestMethods('openai_chat')).toEqual(['POST']);
  });

  it('uses only task semantics for executable video adapters', () => {
    expect(catalogTaskPolicy('generic')).toEqual({ taskScope: 'task', cancelMode: 'none' });
    expect(catalogTaskPolicy('seedance')).toEqual({ taskScope: 'task', cancelMode: 'none' });
    expect(catalogTaskPolicy('openai_chat')).toEqual({ taskScope: 'request', cancelMode: 'none' });
  });

  it('selects the deployed adapter request path', () => {
    expect(catalogDefaultRequestPath('seedance')).toBe('/api/v3/contents/generations/tasks');
    expect(catalogDefaultRequestPath('openai_responses')).toBe('/v1/responses');
    expect(catalogDefaultRequestPath('unknown')).toBe('/');
  });
});
