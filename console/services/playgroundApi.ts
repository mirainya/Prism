import {
    ChannelOption,
    PlaygroundCapability,
    PlaygroundModelInfo,
    PlaygroundConversation,
    PlaygroundMessage,
    PlaygroundDebugDetail,
    PlaygroundTaskListParams,
    PlaygroundTaskListResponse,
    PlaygroundTaskDetail,
    ConversationTurnRecord,
} from '../types';
import { request, getAuthHeader, API_BASE } from './request';

const normalizePlaygroundChannels = (channels: any[]): ChannelOption[] => {
    const seen = new Set<string>();
    const result: ChannelOption[] = [];

    for (const rawChannel of channels || []) {
        const channel: ChannelOption = {
            channelId: rawChannel.channel_id || 0,
            channelType: rawChannel.channel_type || '',
            channelName: rawChannel.channel_name || '',
            model: rawChannel.model || '',
            routeOperation: rawChannel.route_operation || '',
            price: rawChannel.price || 0,
            interactionMode: rawChannel.interaction_mode || '',
            paramSchema: rawChannel.param_schema || null,
        };
        const selectionId = `${channel.channelType}::${channel.model}::${channel.interactionMode || 'sync'}::${channel.routeOperation || ''}`;
        if (seen.has(selectionId)) continue;
        seen.add(selectionId);
        result.push(channel);
    }

    return result;
};

const normalizeModelOperations = (capability: any) => {
    const operations = (capability.operations || []).map((operation: any) => ({
        id: operation.id,
        path: operation.path || '',
        supportsStream: Boolean(operation.supports_stream),
        paramSchema: operation.param_schema || null,
    }));
    if (operations.length > 0) return operations;

    const fallbackOperation = capability.type === 'image'
        ? 'images.generate'
        : capability.type === 'video'
            ? 'videos.generate'
            : capability.type === 'chat'
                ? 'chat.completions'
                : '';
    return fallbackOperation ? [{ id: fallbackOperation, path: '', supportsStream: false, paramSchema: null }] : [];
};

export const playgroundListModels = async (tokenId: string): Promise<PlaygroundModelInfo[]> => {
    const data = await request<any[]>(`/playground/${tokenId}/capabilities`);
    return (data || [])
        .filter(model => (model.operations || []).some((operation: any) => operation.id === 'chat.completions'))
        .map(model => ({
            id: model.id || model.code,
            owned_by: 'prism',
            max_tokens: model.max_tokens,
            group: model.group,
            supports_stream: model.supports_stream,
            default_stream: model.default_stream,
            supports_tools: model.supports_tools,
            supports_response_format: model.supports_response_format,
            supports_multimodal: model.supports_multimodal,
            thinking: model.thinking,
        }));
};

export const playgroundListCapabilities = async (tokenId: string): Promise<PlaygroundCapability[]> => {
    const json = await request<any[]>(`/playground/${tokenId}/capabilities`);
    return (json || []).map(cap => ({
        id: cap.id || cap.code,
        code: cap.code,
        name: cap.name,
        type: cap.type || 'other',
        description: cap.description || '',
        standardParams: cap.param_schema || cap.standard_params || {},
        operations: normalizeModelOperations(cap),
        channels: normalizePlaygroundChannels(cap.channels || []),
    }));
};

export const playgroundChatCompletions = async (
    tokenId: string,
    body: Record<string, any>,
    signal?: AbortSignal,
    thinkingLevel?: string,
): Promise<Response> => {
    return playgroundProtocolRequest(tokenId, 'chat/completions', body, signal, thinkingLevel);
};

export const playgroundResponses = async (
    tokenId: string,
    body: Record<string, any>,
    signal?: AbortSignal,
    thinkingLevel?: string,
): Promise<Response> => {
    return playgroundProtocolRequest(tokenId, 'responses', body, signal, thinkingLevel);
};

export const playgroundAnthropicMessages = async (
    tokenId: string,
    body: Record<string, any>,
    signal?: AbortSignal,
    thinkingLevel?: string,
): Promise<Response> => {
    return playgroundProtocolRequest(tokenId, 'messages', body, signal, thinkingLevel);
};

const playgroundProtocolRequest = async (
    tokenId: string,
    endpoint: 'chat/completions' | 'responses' | 'messages',
    body: Record<string, any>,
    signal?: AbortSignal,
    thinkingLevel?: string,
): Promise<Response> => {
    // 保留原始 Response，让调用方根据 Content-Type 选择 JSON 或 SSE，并可读取调试响应头。
    return fetch(`${API_BASE}/playground/${tokenId}/${endpoint}`, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            ...getAuthHeader(),
            ...(thinkingLevel ? { 'X-Prism-Thinking-Level': thinkingLevel } : {}),
        },
        body: JSON.stringify(body),
        signal,
    });
};

export interface PlaygroundUploadResult {
    url: string;
    thUrl?: string;
    filename: string;
    size: number;
    contentType: string;
}

export const playgroundUploadFile = async (
    tokenId: string,
    file: File,
): Promise<PlaygroundUploadResult> => {
    const formData = new FormData();
    formData.append('file', file);

    const response = await fetch(`${API_BASE}/playground/${tokenId}/upload`, {
        method: 'POST',
        headers: getAuthHeader(),
        body: formData,
    });

    const data = await response.json();
    if (data.code !== 0) {
        throw new Error(data.message || '上传失败');
    }
    return data.data;
};

export interface PlaygroundConversationListParams {
    page?: number;
    page_size?: number;
    model?: string;
    keyword?: string;
    start_date?: string;
    end_date?: string;
}

export interface PlaygroundConversationListResponse {
    items: PlaygroundConversation[];
    total: number;
    page: number;
    page_size: number;
}

export interface PlaygroundConversationMessagesResponse {
    items: PlaygroundMessage[];
    total: number;
    page: number;
    page_size: number;
    conversation: PlaygroundConversation;
}

export const playgroundListConversations = async (
    tokenId: string,
    params?: PlaygroundConversationListParams,
): Promise<PlaygroundConversationListResponse> => {
    // API 的 snake_case 在服务边界转换，页面组件只使用 camelCase 类型。
    const query = new URLSearchParams();
    if (params?.page) query.append('page', String(params.page));
    if (params?.page_size) query.append('page_size', String(params.page_size));
    if (params?.model) query.append('model', params.model);
    if (params?.keyword) query.append('keyword', params.keyword);
    if (params?.start_date) query.append('start_date', params.start_date);
    if (params?.end_date) query.append('end_date', params.end_date);
    const url = query.toString()
        ? `/playground/${tokenId}/conversations?${query}`
        : `/playground/${tokenId}/conversations`;
    const data = await request<any>(url);
    return {
        items: (data.items || []).map((c: any) => ({
            id: c.id,
            userId: c.user_id,
            tokenId: c.token_id,
            title: c.title,
            model: c.model,
            systemPrompt: c.system_prompt,
            lastCallId: c.last_call_id,
            lastRequestLogId: c.last_request_log_id,
            lastStatus: c.last_status,
            totalTokens: c.total_tokens,
            messageCount: c.message_count,
            totalCost: c.total_cost ?? '0',
            status: c.status,
            createdAt: c.created_at,
            updatedAt: c.updated_at,
        })),
        total: data.total,
        page: data.page,
        page_size: data.page_size,
    };
};

export const playgroundGetConversationMessages = async (
    tokenId: string,
    conversationId: number,
    page?: number,
    pageSize?: number,
): Promise<PlaygroundConversationMessagesResponse> => {
    const query = new URLSearchParams();
    if (page) query.append('page', String(page));
    if (pageSize) query.append('page_size', String(pageSize));
    const url = query.toString()
        ? `/playground/${tokenId}/conversations/${conversationId}/messages?${query}`
        : `/playground/${tokenId}/conversations/${conversationId}/messages`;
    const data = await request<any>(url);
    return {
        items: (data.items || []).map((m: any) => ({
            id: m.id,
            conversationId: m.conversation_id,
            callId: m.call_id || undefined,
            callStatus: m.call_status || undefined,
            requestLogId: m.request_log_id,
            role: m.role,
            content: m.content,
            attachments: m.attachments,
            reasoningContent: m.reasoning_content,
            finishReason: m.finish_reason,
            inputTokens: m.input_tokens,
            outputTokens: m.output_tokens,
            model: m.model,
            channelId: m.channel_id,
            accountId: m.account_id,
            latencyMs: m.latency_ms,
            cost: m.cost ?? '0',
            createdAt: m.created_at,
        })),
        total: data.total,
        page: data.page,
        page_size: data.page_size,
        conversation: {
            id: data.conversation.id,
            userId: data.conversation.user_id,
            tokenId: data.conversation.token_id,
            title: data.conversation.title,
            model: data.conversation.model,
            systemPrompt: data.conversation.system_prompt,
            lastCallId: data.conversation.last_call_id,
            lastRequestLogId: data.conversation.last_request_log_id,
            lastStatus: data.conversation.last_status,
            totalTokens: data.conversation.total_tokens,
            messageCount: data.conversation.message_count,
            totalCost: data.conversation.total_cost ?? '0',
            status: data.conversation.status,
            createdAt: data.conversation.created_at,
            updatedAt: data.conversation.updated_at,
        },
    };
};

export interface PlaygroundConversationTurnsResponse {
    items: ConversationTurnRecord[];
    total: number;
    page: number;
    page_size: number;
}

export const playgroundGetConversationTurns = async (
    tokenId: string,
    conversationId: number,
    page?: number,
    pageSize?: number,
): Promise<PlaygroundConversationTurnsResponse> => {
    const query = new URLSearchParams();
    if (page) query.append('page', String(page));
    if (pageSize) query.append('page_size', String(pageSize));
    const suffix = query.toString() ? `?${query}` : '';
    const data = await request<any>(`/playground/${tokenId}/conversations/${conversationId}/turns${suffix}`);
    return {
        items: (data.items || []).map((turn: any) => ({
            id: String(turn.id),
            conversationId: turn.conversation_id,
            sequence: String(turn.sequence),
            callId: turn.call_id,
            requestLogId: turn.request_log_id || undefined,
            model: turn.model,
            providerResponseId: turn.provider_response_id || undefined,
            status: turn.status,
            contextMode: turn.context_mode || 'legacy',
            inputTokens: turn.input_tokens || 0,
            outputTokens: turn.output_tokens || 0,
            totalTokens: turn.total_tokens || 0,
            cost: turn.cost ?? '0',
            latencyMs: turn.latency_ms || 0,
            finishReason: turn.finish_reason || undefined,
            errorType: turn.error_type || undefined,
            errorCode: turn.error_code || undefined,
            errorMessage: turn.error_message || undefined,
            createdAt: turn.created_at,
            items: (turn.items || []).map((item: any) => ({
                id: String(item.id),
                direction: item.direction,
                ordinal: item.ordinal,
                canonical: item.canonical || {},
            })),
        })),
        total: data.total || 0,
        page: data.page || 1,
        page_size: data.page_size || pageSize || 50,
    };
};

export const playgroundGetDebug = async (
    tokenId: string,
    requestLogId: number,
): Promise<PlaygroundDebugDetail> => {
    const data = await request<any>(`/playground/${tokenId}/debug/${requestLogId}`);
    return {
        conversationId: data.conversation_id,
        requestLogId: data.request_log_id,
        channelId: data.channel_id,
        accountId: data.account_id,
        channelName: data.channel_name,
        channelType: data.channel_type,
        modelCode: data.model_code,
        vendorModel: data.vendor_model,
        requestPath: data.request_path,
        isStream: data.is_stream,
        latencyMs: data.duration_ms,
        statusCode: data.status_code,
        errorMessage: data.error_message,
        finishReason: data.finish_reason,
        contextMode: data.context_mode,
        providerResponseId: data.provider_response_id,
        responsePreview: data.response_preview,
        requestHeaders: data.request_headers,
        requestBody: data.request_body,
        responseBody: data.response_body,
        status: data.error_message ? 'failed' : 'completed',
        usage: {
            prompt_tokens: data.usage_prompt_tokens || 0,
            completion_tokens: data.usage_completion_tokens || 0,
            total_tokens: data.usage_total_tokens || 0,
        },
    };
};

export const playgroundInvokeCapability = async (
    tokenId: string,
    capability: string,
    params: Record<string, any>,
): Promise<any> => {
    return request(`/playground/${tokenId}/capabilities/${capability}`, {
        method: 'POST',
        body: JSON.stringify(params),
    });
};

export const playgroundListTasks = async (
    tokenId: string,
    params?: PlaygroundTaskListParams,
): Promise<PlaygroundTaskListResponse> => {
    const query = new URLSearchParams();
    if (params?.page) query.append('page', String(params.page));
    if (params?.page_size) query.append('page_size', String(params.page_size));
    if (params?.snapshot_at) query.append('snapshot_at', params.snapshot_at);
    if (params?.status) query.append('status', params.status);
    if (params?.capability) query.append('capability', params.capability);
    if (params?.keyword) query.append('keyword', params.keyword);
    const url = query.toString()
        ? `/playground/${tokenId}/tasks?${query}`
        : `/playground/${tokenId}/tasks`;
    const data = await request<any>(url);
    return {
        items: (data.items || []).map((item: any) => ({
            id: item.id,
            taskNo: item.task_no,
            capability: item.capability,
            capabilityName: item.capability_name,
            channel: item.channel,
            status: item.status,
            progress: item.progress || 0,
            cost: Number(item.cost) || 0,
            refunded: Boolean(item.refunded),
            error: item.error,
            createdAt: item.created_at,
            completedAt: item.completed_at,
        })),
        total: data.total || 0,
        page: data.page || 1,
        page_size: data.page_size || params?.page_size || 20,
        snapshot_at: data.snapshot_at,
    };
};

export const playgroundGetTask = async (tokenId: string, taskNo: string): Promise<PlaygroundTaskDetail> => {
    const data = await request<any>(`/playground/${tokenId}/tasks/${taskNo}`);
    return {
        taskId: data.task_id,
        taskNo: data.task_no,
        status: data.status,
        progress: data.progress || 0,
        result: data.result,
        error: data.error || '',
        cost: Number(data.cost) || 0,
        rawParams: data.raw_params,
        mappedParams: data.mapped_params,
        vendorResponse: data.vendor_response,
        vendorTaskId: data.vendor_task_id,
        createdAt: data.created_at,
        startedAt: data.started_at,
        completedAt: data.completed_at,
    };
};

export const playgroundCancelTask = async (tokenId: string, taskNo: string): Promise<void> => {
    await request(`/playground/${tokenId}/tasks/${taskNo}/cancel`, { method: 'POST' });
};

// --- Video Playground API ---

export interface VideoCreateParams {
    model: string;
    prompt: string;
    channel_id?: number;
    resolution?: string;
    ratio?: string;
    duration?: number;
    generate_audio?: boolean;
    task_mode?: string;
    content?: VideoContentItem[];
    priority?: number;
}

export interface VideoEstimate {
    estimated_cost: string;
    base_cost: string;
    markup_ratio: string;
    pricing_mode: 'fixed' | 'upstream_estimate';
}

export interface VideoContentItem {
    type: 'image_url' | 'video_url' | 'audio_url';
    role: 'first_frame' | 'last_frame' | 'reference_image' | 'reference_video' | 'reference_audio';
    url?: string;
    asset_id?: string;
    duration_seconds?: number;
}

export interface VideoAsset {
    id: string;
    kind: 'image' | 'video' | 'audio';
    content_type: string;
    size_bytes: number;
    status: string;
    storage_path: string;
    expires_at: string;
}

export interface VideoTask {
    id: string;
    model: string;
    status: string;
    progress: number;
    prompt: string;
    resolution?: string;
    ratio?: string;
    duration?: number;
    result?: { video_url?: string; thumbnail_url?: string; duration?: number };
    error_message?: string;
    created_at: string;
    completed_at?: string;
}

export interface PlaygroundVideoModelOptions {
    resolutions?: string[];
    task_types?: Array<'text' | 'first_frame' | 'first_last_frame' | 'multimodal'>;
}

export interface PlaygroundVideoChannelOption {
    id: number;
    name: string;
    models: string[];
    model_options: Record<string, PlaygroundVideoModelOptions>;
}

export interface PlaygroundVideoModelsResponse {
    models: string[];
    model_options?: Record<string, PlaygroundVideoModelOptions>;
    channels?: PlaygroundVideoChannelOption[];
}

export const playgroundListVideoModels = async (tokenId: string): Promise<PlaygroundVideoModelsResponse> => {
    const data = await request<PlaygroundVideoModelsResponse>(`/playground/${tokenId}/videos/models`);
    return {
        models: data.models || [],
        model_options: data.model_options || {},
        channels: data.channels || [],
    };
};

export const playgroundCreateVideo = async (tokenId: string, params: VideoCreateParams): Promise<{ id: string; status: string }> => {
    return await request<{ id: string; status: string }>(`/playground/${tokenId}/videos/generations`, {
        method: 'POST',
        body: JSON.stringify(params),
    });
};

export const playgroundEstimateVideo = async (
    tokenId: string,
    params: VideoCreateParams,
    signal?: AbortSignal,
): Promise<VideoEstimate> => {
    return await request<VideoEstimate>(`/playground/${tokenId}/videos/estimate`, {
        method: 'POST',
        body: JSON.stringify(params),
        signal,
    });
};

export const playgroundUploadVideoAsset = async (
    tokenId: string,
    file: File,
    kind: VideoAsset['kind'],
    durationSeconds?: number,
): Promise<VideoAsset> => {
    const formData = new FormData();
    formData.append('file', file);
    formData.append('kind', kind);
    if (durationSeconds && durationSeconds > 0) {
        formData.append('duration_seconds', String(durationSeconds));
    }

    const response = await fetch(`${API_BASE}/playground/${tokenId}/videos/assets`, {
        method: 'POST',
        headers: getAuthHeader(),
        body: formData,
    });
    const payload = await response.json();
    if (!response.ok || payload.code !== 0) {
        throw new Error(payload.message || '素材上传失败');
    }
    return payload.data;
};

export const playgroundListVideos = async (tokenId: string): Promise<{ items: VideoTask[]; total: number }> => {
    const data = await request<{ items: VideoTask[]; total: number }>(`/playground/${tokenId}/videos/generations`);
    return { items: data.items || [], total: data.total || 0 };
};

export const playgroundGetVideo = async (tokenId: string, taskId: string): Promise<VideoTask> => {
    return await request<VideoTask>(`/playground/${tokenId}/videos/generations/${taskId}`);
};

export const playgroundCancelVideo = async (tokenId: string, taskId: string): Promise<void> => {
    await request(`/playground/${tokenId}/videos/generations/${taskId}/cancel`, { method: 'POST' });
};
