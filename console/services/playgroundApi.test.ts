import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  playgroundGetConversationTurns,
  playgroundListCapabilities,
  playgroundListConversations,
} from './playgroundApi';
import { request } from './request';

vi.mock('./request', () => ({
  API_BASE: '/api',
  getAuthHeader: vi.fn(() => ({})),
  request: vi.fn(),
}));

const requestMock = vi.mocked(request);

describe('playgroundApi', () => {
  beforeEach(() => {
    requestMock.mockReset();
  });

  it('normalizes capabilities and channel parameter overrides', async () => {
    requestMock.mockResolvedValue([
      {
        code: 'image.generate',
        name: 'Image generation',
        param_schema: { prompt: { type: 'string' } },
        channels: [
          {
            channel_id: 3,
            channel_type: 'openai',
            channel_name: 'Primary',
            model: 'gpt-image',
            price: 0.25,
            interaction_mode: 'sync',
            param_schema: { size: { type: 'string' } },
          },
        ],
      },
    ]);

    await expect(playgroundListCapabilities('token-1')).resolves.toEqual([
      {
        code: 'image.generate',
        name: 'Image generation',
        type: 'other',
        description: '',
        standardParams: { prompt: { type: 'string' } },
        channels: [
          {
            channelId: 3,
            channelType: 'openai',
            channelName: 'Primary',
            model: 'gpt-image',
            price: 0.25,
            interactionMode: 'sync',
            paramSchema: { size: { type: 'string' } },
          },
        ],
      },
    ]);
    expect(requestMock).toHaveBeenCalledWith('/playground/token-1/capabilities');
  });

  it('builds conversation queries and maps API fields', async () => {
    requestMock.mockResolvedValue({
      items: [
        {
          id: 10,
          user_id: 2,
          token_id: 4,
          title: 'Test conversation',
          model: 'gpt-test',
          system_prompt: 'Be concise',
          last_call_id: 'call_1',
          total_tokens: 12,
          message_count: 2,
          total_cost: null,
          status: 'active',
          created_at: '2026-07-18T00:00:00Z',
          updated_at: '2026-07-18T00:01:00Z',
        },
      ],
      total: 1,
      page: 2,
      page_size: 10,
    });

    const result = await playgroundListConversations('token-1', {
      page: 2,
      page_size: 10,
      model: 'gpt-test',
      keyword: 'hello world',
    });

    expect(requestMock).toHaveBeenCalledWith(
      '/playground/token-1/conversations?page=2&page_size=10&model=gpt-test&keyword=hello+world',
    );
    expect(result.items[0]).toMatchObject({
      id: 10,
      userId: 2,
      tokenId: 4,
      systemPrompt: 'Be concise',
      lastCallId: 'call_1',
      totalTokens: 12,
      messageCount: 2,
      totalCost: '0',
    });
  });

  it('maps conversation turns with stable defaults', async () => {
    requestMock.mockResolvedValue({
      items: [
        {
          id: 8,
          conversation_id: 10,
          sequence: 3,
          call_id: 'call_3',
          model: 'gpt-test',
          status: 'completed',
          input_tokens: 4,
          output_tokens: 6,
          total_tokens: 10,
          cost: null,
          created_at: '2026-07-18T00:02:00Z',
          items: [
            { id: 9, direction: 'output', ordinal: 0, canonical: { type: 'message' } },
          ],
        },
      ],
    });

    const result = await playgroundGetConversationTurns('token-1', 10);

    expect(result).toMatchObject({
      total: 0,
      page: 1,
      page_size: 50,
      items: [
        {
          id: '8',
          sequence: '3',
          contextMode: 'legacy',
          cost: '0',
          latencyMs: 0,
          items: [{ id: '9', direction: 'output', ordinal: 0 }],
        },
      ],
    });
  });
});
