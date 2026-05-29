import {
    PlaygroundCapability,
    PlaygroundModelInfo,
    PlaygroundConversation,
    PlaygroundMessage,
    PlaygroundDebugDetail,
    PlaygroundTaskListParams,
    PlaygroundTaskListResponse,
    PlaygroundTaskDetail,
} from '../types';
import { request, getAuthHeader, API_BASE } from './request';

export const playgroundListModels = async (tokenId: string): Promise<PlaygroundModelInfo[]> => {
    const data = await request<{object: string; data: PlaygroundModelInfo[]}>(`/playground/${tokenId}/models`);
    return (data as any).data || data || [];
};

export const playgroundListCapabilities = async (tokenId: string): Promise<PlaygroundCapability[]> => {
    const json = await request<any[]>(`/playground/${tokenId}/capabilities`);
    return (json || []).map(cap => ({
        code: cap.code,
        name: cap.name,
        type: cap.type || 'other',
        description: cap.description || '',
        standardParams: cap.param_schema || cap.standard_params || {},
        channels: (cap.channels || []).map((ch: any) => ({
            channelId: ch.channel_id || 0,
            channelType: ch.channel_type || '',
            channelName: ch.channel_name || '',
            model: ch.model || '',
            price: ch.price || 0,
            interactionMode: ch.interaction_mode || '',
            paramSchema: ch.param_schema || null,
        })),
    }));
};

export const playgroundChatCompletions = async (
    tokenId: string,
    body: Record<string, any>,
    signal?: AbortSignal,
): Promise<Response> => {
    return fetch(`${API_BASE}/playground/${tokenId}/chat/completions`, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            ...getAuthHeader(),
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
            lastRequestLogId: c.last_request_log_id,
            lastStatus: c.last_status,
            totalTokens: c.total_tokens,
            messageCount: c.message_count,
            totalCost: Number(c.total_cost) || 0,
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
            requestLogId: m.request_log_id,
            role: m.role,
            content: m.content,
            reasoningContent: m.reasoning_content,
            finishReason: m.finish_reason,
            inputTokens: m.input_tokens,
            outputTokens: m.output_tokens,
            model: m.model,
            channelId: m.channel_id,
            accountId: m.account_id,
            latencyMs: m.latency_ms,
            cost: Number(m.cost) || 0,
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
            lastRequestLogId: data.conversation.last_request_log_id,
            lastStatus: data.conversation.last_status,
            totalTokens: data.conversation.total_tokens,
            messageCount: data.conversation.message_count,
            totalCost: Number(data.conversation.total_cost) || 0,
            status: data.conversation.status,
            createdAt: data.conversation.created_at,
            updatedAt: data.conversation.updated_at,
        },
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
