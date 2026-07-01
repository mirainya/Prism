import { ChatModel, ChatModelChannel } from '../types';
import { request } from './request';

export const fetchChatModels = async (): Promise<ChatModel[]> => {
    const data = await request<any[]>('/admin/chat-models');
    return data.map(m => ({
        id: m.id,
        code: m.code,
        name: m.name,
        provider: m.provider,
        description: m.description,
        features: m.features || [],
        maxTokens: m.max_tokens || 0,
        thinkingConfig: m.thinking_config || null,
        sort: m.sort || 0,
        status: m.status,
        createdAt: m.created_at,
        updatedAt: m.updated_at,
    }));
};

export const getChatModel = async (code: string): Promise<ChatModel> => {
    const m = await request<any>(`/admin/chat-models/${code}`);
    return {
        id: m.id,
        code: m.code,
        name: m.name,
        provider: m.provider,
        description: m.description,
        features: m.features || [],
        maxTokens: m.max_tokens || 0,
        thinkingConfig: m.thinking_config || null,
        sort: m.sort || 0,
        status: m.status,
        createdAt: m.created_at,
        updatedAt: m.updated_at,
    };
};

export const createChatModel = async (data: {
    code: string;
    name: string;
    provider: string;
    description?: string;
    features?: string[];
    max_tokens?: number;
    thinking_config?: any;
    sort?: number;
}): Promise<ChatModel> => {
    const m = await request<any>('/admin/chat-models', {
        method: 'POST',
        body: JSON.stringify(data),
    });
    return {
        id: m.id,
        code: m.code,
        name: m.name,
        provider: m.provider,
        description: m.description,
        features: m.features || [],
        maxTokens: m.max_tokens || 0,
        thinkingConfig: m.thinking_config || null,
        sort: m.sort || 0,
        status: m.status,
        createdAt: m.created_at,
        updatedAt: m.updated_at,
    };
};

export const updateChatModel = async (code: string, data: {
    name?: string;
    provider?: string;
    description?: string;
    features?: string[];
    max_tokens?: number;
    thinking_config?: any;
    sort?: number;
    status?: number;
}): Promise<void> => {
    await request(`/admin/chat-models/${code}`, {
        method: 'PUT',
        body: JSON.stringify(data),
    });
};

export const deleteChatModel = async (code: string): Promise<void> => {
    await request(`/admin/chat-models/${code}`, {method: 'DELETE'});
};

export const reorderChatModels = async (codes: string[]): Promise<void> => {
    await request('/admin/chat-models/reorder', {
        method: 'POST',
        body: JSON.stringify({ codes }),
    });
};

// 快速配置
export const fetchChatModelPresets = async (provider: string): Promise<{ code: string; name: string }[]> => {
    return request<{ code: string; name: string }[]>(`/admin/chat-models/presets?provider=${provider}`);
};

export const quickSetupChatModels = async (data: {
    channel_id: number;
    provider: string;
    models: { code: string; name: string; vendor_model?: string }[];
    price_mode?: string;
    input_price?: number;
    output_price?: number;
    request_path?: string;
}): Promise<{ created: number; skipped: number; mapped: number }> => {
    return request<{ created: number; skipped: number; mapped: number }>('/admin/chat-models/quick-setup', {
        method: 'POST',
        body: JSON.stringify(data),
    });
};

// 渠道映射
export const fetchChatModelChannels = async (
    modelCode?: string,
    channelId?: number
): Promise<ChatModelChannel[]> => {
    const params = new URLSearchParams();
    if (modelCode) params.append('model_code', modelCode);
    if (channelId) params.append('channel_id', channelId.toString());
    const query = params.toString();
    const data = await request<any[]>(`/admin/chat-model-channels${query ? '?' + query : ''}`);
    return data.map(mc => ({
        id: mc.id,
        modelCode: mc.model_code,
        channelId: mc.channel_id,
        vendorModel: mc.vendor_model,
        protocol: mc.protocol,
        priority: mc.priority,
        priceMode: mc.price_mode,
        inputPrice: mc.input_price,
        outputPrice: mc.output_price,
        requestPath: mc.request_path,
        timeout: mc.timeout,
        supportsStream: mc.supports_stream ?? undefined,
        defaultStream: mc.default_stream ?? undefined,
        extraHeaders: mc.extra_headers || {},
        extraConfig: mc.extra_config || {},
        status: mc.status,
        createdAt: mc.created_at,
        updatedAt: mc.updated_at,
        chatModel: mc.chat_model ? {
            id: mc.chat_model.id,
            code: mc.chat_model.code,
            name: mc.chat_model.name,
            provider: mc.chat_model.provider,
            description: mc.chat_model.description,
            status: mc.chat_model.status,
            createdAt: mc.chat_model.created_at,
            updatedAt: mc.chat_model.updated_at,
        } : undefined,
        channel: mc.channel,
    }));
};

export const getChatModelChannel = async (id: number): Promise<ChatModelChannel> => {
    const mc = await request<any>(`/admin/chat-model-channels/${id}`);
    return {
        id: mc.id,
        modelCode: mc.model_code,
        channelId: mc.channel_id,
        vendorModel: mc.vendor_model,
        protocol: mc.protocol,
        priority: mc.priority,
        priceMode: mc.price_mode,
        inputPrice: mc.input_price,
        outputPrice: mc.output_price,
        requestPath: mc.request_path,
        timeout: mc.timeout,
        supportsStream: mc.supports_stream ?? undefined,
        defaultStream: mc.default_stream ?? undefined,
        extraHeaders: mc.extra_headers || {},
        extraConfig: mc.extra_config || {},
        status: mc.status,
        createdAt: mc.created_at,
        updatedAt: mc.updated_at,
        chatModel: mc.chat_model ? {
            id: mc.chat_model.id,
            code: mc.chat_model.code,
            name: mc.chat_model.name,
            provider: mc.chat_model.provider,
            description: mc.chat_model.description,
            status: mc.chat_model.status,
            createdAt: mc.chat_model.created_at,
            updatedAt: mc.chat_model.updated_at,
        } : undefined,
        channel: mc.channel,
    };
};

export const createChatModelChannel = async (data: {
    model_code: string;
    channel_id: number;
    vendor_model: string;
    protocol?: string;
    priority?: number;
    price_mode?: string;
    input_price?: number;
    output_price?: number;
    request_path?: string;
    timeout?: number;
    supports_stream?: boolean;
    default_stream?: boolean;
    extra_headers?: Record<string, string>;
    extra_config?: Record<string, any>;
}): Promise<ChatModelChannel> => {
    const mc = await request<any>('/admin/chat-model-channels', {
        method: 'POST',
        body: JSON.stringify(data),
    });
    return {
        id: mc.id,
        modelCode: mc.model_code,
        channelId: mc.channel_id,
        vendorModel: mc.vendor_model,
        protocol: mc.protocol,
        priority: mc.priority,
        priceMode: mc.price_mode,
        inputPrice: mc.input_price,
        outputPrice: mc.output_price,
        requestPath: mc.request_path,
        timeout: mc.timeout,
        supportsStream: mc.supports_stream ?? undefined,
        defaultStream: mc.default_stream ?? undefined,
        extraHeaders: mc.extra_headers || {},
        extraConfig: mc.extra_config || {},
        status: mc.status,
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
    };
};

export const updateChatModelChannel = async (
    id: number,
    data: {
        vendor_model?: string;
        protocol?: string;
        priority?: number;
        price_mode?: string;
        input_price?: number;
        output_price?: number;
        request_path?: string;
        timeout?: number;
        supports_stream?: boolean;
        default_stream?: boolean;
        extra_headers?: Record<string, string>;
        extra_config?: Record<string, any>;
        status?: number;
    }
): Promise<void> => {
    await request(`/admin/chat-model-channels/${id}`, {
    method: 'PUT',
        body: JSON.stringify(data),
  });
};

export const deleteChatModelChannel = async (id: number): Promise<void> => {
    await request(`/admin/chat-model-channels/${id}`, {method: 'DELETE'});
};
