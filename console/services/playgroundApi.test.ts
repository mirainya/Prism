import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
    playgroundGetConversationTurns,
    playgroundEstimateVideo,
    playgroundHydrateCompletedVideoTasks,
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

  it('loads chat models from the dedicated model endpoint', async () => {
    requestMock.mockResolvedValue({ data: [
      {
        id: 'gpt-4.1',
        owned_by: 'openai',
        supports_stream: true,
        default_stream: true,
        supports_tools: true,
        group: 'OpenAI',
		supported_operations: ['chat.completions', 'responses.create'],
		supported_endpoints: ['/v1/chat/completions', '/v1/responses'],
      },
    ] });

    await expect(playgroundListModels('token-1')).resolves.toEqual([
      expect.objectContaining({
        id: 'gpt-4.1',
        owned_by: 'openai',
        supports_stream: true,
        default_stream: true,
        supports_tools: true,
        group: 'OpenAI',
		supported_operations: ['chat.completions', 'responses.create'],
		supported_endpoints: ['/v1/chat/completions', '/v1/responses'],
      }),
    ]);
    expect(requestMock).toHaveBeenCalledWith('/playground/token-1/models');
  });

  it('normalizes video estimate parameters to the public contract', async () => {
    requestMock.mockResolvedValue({
      estimated_cost: '1.5', base_cost: '1.25', markup_ratio: '1.2', pricing_mode: 'upstream_estimate',
    });
    const controller = new AbortController();
    const params = {
      model: 'video-model', prompt: 'test', duration: 5, params: { web_search: true },
      task_mode: 'references',
      content: [{ type: 'video_url' as const, role: 'reference_video' as const, asset_id: 'asset-1', duration_seconds: 4 }],
    };

    await expect(playgroundEstimateVideo('token-1', params, controller.signal)).resolves.toMatchObject({
      estimated_cost: '1.5', pricing_mode: 'upstream_estimate',
    });
    expect(requestMock).toHaveBeenCalledWith('/playground/token-1/videos/estimate', {
      method: 'POST',
      body: JSON.stringify({
        model: 'video-model',
        prompt: 'test',
        duration: 5,
        references: [{ type: 'video', role: 'reference_video', asset_id: 'asset-1', duration_seconds: 4 }],
        provider_options: { seedance: { web_search: true } },
      }),
      signal: controller.signal,
    });
  });

  it('loads video models together with configured model options', async () => {
    requestMock.mockResolvedValue({
      models: ['seedance-2.0'],
      model_options: { 'seedance-2.0': { resolutions: ['1080p'] } },
    });

    await expect(playgroundListVideoModels('token-1')).resolves.toEqual({
      models: ['seedance-2.0'],
      model_options: {
        'seedance-2.0': {
          resolutions: ['1080p'],
          ratios: [],
          task_types: [],
          allowed_roles: [],
          parameters: [],
          service_tier_options: [],
        },
      },
    });
    expect(requestMock).toHaveBeenCalledWith('/playground/token-1/videos/models');
  });

  it('loads result URLs from completed video task details', async () => {
    requestMock.mockResolvedValueOnce({
      id: 'video-1',
      model: 'seedance-2.0',
      status: 'completed',
      progress: 100,
      prompt: 'ocean',
      result: { video_url: 'https://media.example/video.mp4' },
      created_at: '2026-09-17T00:00:00Z',
    });

    const tasks = await playgroundHydrateCompletedVideoTasks('token-1', [{
      id: 'video-1',
      model: 'seedance-2.0',
      status: 'completed',
      progress: 100,
      prompt: '',
      created_at: '2026-09-17T00:00:00Z',
    }]);

    expect(requestMock).toHaveBeenCalledWith('/playground/token-1/videos/generations/video-1');
    expect(tasks[0]).toMatchObject({
      prompt: 'ocean',
      result: { video_url: 'https://media.example/video.mp4' },
    });
  });

  it('marks completed video details unavailable when the detail request fails', async () => {
    requestMock.mockRejectedValueOnce(new Error('temporary failure'));
    const summary = {
      id: 'video-1',
      model: 'seedance-2.0',
      status: 'completed',
      progress: 100,
      prompt: '',
      created_at: '2026-09-17T00:00:00Z',
    };

    await expect(playgroundHydrateCompletedVideoTasks('token-1', [summary])).resolves.toEqual([{
      ...summary,
      result: { delivery_status: 'detail_error' },
    }]);
  });

  it('retries completed video details until a video URL is available', async () => {
    requestMock.mockResolvedValueOnce({
      id: 'video-1',
      model: 'seedance-2.0',
      status: 'completed',
      progress: 100,
      prompt: 'ocean',
      result: { delivery_status: 'unavailable' },
      created_at: '2026-09-17T00:00:00Z',
    });

    const tasks = await playgroundHydrateCompletedVideoTasks('token-1', [{
      id: 'video-1',
      model: 'seedance-2.0',
      status: 'completed',
      progress: 100,
      prompt: 'ocean',
      result: { delivery_status: 'unavailable' },
      created_at: '2026-09-17T00:00:00Z',
    }]);

    expect(requestMock).toHaveBeenCalledWith('/playground/token-1/videos/generations/video-1');
    expect(tasks[0].result).toEqual({ delivery_status: 'unavailable' });
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
