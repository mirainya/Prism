import { describe, expect, it } from 'vitest';

import { getSupportedPlaygroundProtocols } from './protocol';

describe('getSupportedPlaygroundProtocols', () => {
  it('maps configured downstream endpoints to all selectable protocols', () => {
    expect(getSupportedPlaygroundProtocols([
      '/v1/messages',
      '/v1/chat/completions',
      '/v1/responses',
    ])).toEqual(['chat', 'responses', 'anthropic']);
  });

  it('only enables protocols with a configured downstream endpoint', () => {
    expect(getSupportedPlaygroundProtocols(['/v1/chat/completions'])).toEqual(['chat']);
  });

  it('ignores unknown endpoints and handles missing capability data', () => {
    expect(getSupportedPlaygroundProtocols(['/v1/videos', '/unknown'])).toEqual([]);
    expect(getSupportedPlaygroundProtocols(undefined)).toEqual([]);
  });
});
