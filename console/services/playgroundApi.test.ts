import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
    playgroundGetConversationTurns,
    playgroundEstimateVideo,
    playgroundListCapabilities,
    playgroundListConversations,
    playgroundListModels,
    playgroundListVideoModels,
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
        operations: [{ id: 'images.generate', path: '/v1/images/generations', supports_stream: false }],
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
        id: 'image.generate',
        code: 'image.generate',
        name: 'Image generation',
        type: 'other',
        description: '',
        standardParams: { prompt: { type: 'string' } },
        operations: [{ id: 'images.generate', path: '/v1/images/generations', supportsStream: false, paramSchema: null }],
        channels: [
          {
            channelId: 3,
            channelType: 'openai',
            channelName: 'Primary',
            model: 'gpt-image',
            routeOperation: '',
            price: 0.25,
            interactionMode: 'sync',
            paramSchema: { size: { type: 'string' } },
          },
        ],
      },
    ]);
    expect(requestMock).toHaveBeenCalledWith('/playground/token-1/capabilities');
  });

  it('keeps operation-specific routes separate while grouping the model', async () => {
    requestMock.mockResolvedValue([
      {
        code: 'gpt-image-2-1.5k',
        name: 'gpt-image-2-1.5k',
        type: 'image',
        operations: [
          { id: 'images.generate', path: '/v1/images/generations', supports_stream: true },
          { id: 'images.edit', path: '/v1/images/edits', supports_stream: false },
        ],
        channels: [
          {
            channel_id: 3,
            channel_type: 'mirainya',
            channel_name: 'MiraiNya',
            model: 'gpt-image-2-1.5k',
            interaction_mode: 'sync',
            route_operation: 'images.generate',
          },
          {
            channel_id: 3,
            channel_type: 'mirainya',
            channel_name: 'MiraiNya',
            model: 'gpt-image-2-1.5k',
            interaction_mode: 'sync',
            route_operation: 'images.edit',
          },
        ],
      },
    ]);

    const capabilities = await playgroundListCapabilities('token-1');

    expect(capabilities).toHaveLength(1);
    expect(capabilities[0].operations.map(operation => operation.id)).toEqual(['images.generate', 'images.edit']);
    expect(capabilities[0].channels).toHaveLength(2);
    expect(capabilities[0].channels[0]).toMatchObject({
      channelId: 3,
      channelType: 'mirainya',
      channelName: 'MiraiNya',
      model: 'gpt-image-2-1.5k',
      routeOperation: 'images.generate',
      interactionMode: 'sync',
    });
    expect(capabilities[0].channels[1].routeOperation).toBe('images.edit');
  });

  it('loads chat models from the unified capability list', async () => {
    requestMock.mockResolvedValue([
      {
        id: 'gpt-4.1',
        code: 'gpt-4.1',
        type: 'chat',
        operations: [{ id: 'chat.completions' }, { id: 'responses.create' }],
        supports_stream: true,
        default_stream: true,
        supports_tools: true,
        group: 'OpenAI',
      },
      {
        id: 'gpt_image2',
        code: 'gpt_image2',
        type: 'image',
        operations: [{ id: 'images.generate' }],
      },
    ]);

    await expect(playgroundListModels('token-1')).resolves.toEqual([
      expect.objectContaining({
        id: 'gpt-4.1',
        owned_by: 'prism',
        supports_stream: true,
        default_stream: true,
        supports_tools: true,
        group: 'OpenAI',
      }),
    ]);
    expect(requestMock).toHaveBeenCalledWith('/playground/token-1/capabilities');
  });

  it('sends video estimate parameters unchanged', async () => {
    requestMock.mockResolvedValue({
      estimated_cost: '1.5', base_cost: '1.25', markup_ratio: '1.2', pricing_mode: 'upstream_estimate',
    });
    const controller = new AbortController();
    const params = {
      model: 'seedance-2.5', prompt: 'test', duration: 5, priority: 4,
      task_mode: 'references',
      content: [{ type: 'video_url' as const, role: 'reference_video' as const, asset_id: 'asset-1', duration_seconds: 4 }],
    };

    await expect(playgroundEstimateVideo('token-1', params, controller.signal)).resolves.toMatchObject({
      estimated_cost: '1.5', pricing_mode: 'upstream_estimate',
    });
    expect(requestMock).toHaveBeenCalledWith('/playground/token-1/videos/estimate', {
      method: 'POST', body: JSON.stringify(params), signal: controller.signal,
    });
  });

  it('loads video models together with configured model options', async () => {
    requestMock.mockResolvedValue({
      models: ['seedance-2.0'],
      model_options: { 'seedance-2.0': { resolutions: ['1080p'] } },
      channels: [{ id: 2, name: '官满血-Seedance', models: ['seedance-2.0'], model_options: {} }],
    });

    await expect(playgroundListVideoModels('token-1')).resolves.toEqual({
      models: ['seedance-2.0'],
      model_options: { 'seedance-2.0': { resolutions: ['1080p'] } },
      channels: [{ id: 2, name: '官满血-Seedance', models: ['seedance-2.0'], model_options: {} }],
    });
    expect(requestMock).toHaveBeenCalledWith('/playground/token-1/videos/models');
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
