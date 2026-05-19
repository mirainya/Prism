import { Conversation, ChatMessage } from '../types';
import { request } from './request';

export interface ConversationListParams {
    page?: number;
    page_size?: number;
    model?: string;
    keyword?: string;
    token_id?: number;
    start_date?: string;
    end_date?: string;
}

export interface ConversationListResponse {
    items: Conversation[];
    total: number;
    page: number;
    page_size: number;
}

export const fetchConversations = async (params?: ConversationListParams): Promise<ConversationListResponse> => {
    const query = new URLSearchParams();
    if (params?.page) query.append('page', String(params.page));
    if (params?.page_size) query.append('page_size', String(params.page_size));
    if (params?.model) query.append('model', params.model);
    if (params?.keyword) query.append('keyword', params.keyword);
    if (params?.token_id) query.append('token_id', String(params.token_id));
    if (params?.start_date) query.append('start_date', params.start_date);
    if (params?.end_date) query.append('end_date', params.end_date);

    const url = query.toString() ? `/conversations?${query}` : '/conversations';
    const data = await request<any>(url);
    return {
        items: data.items.map((c: any) => ({
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

export interface ConversationMessagesResponse {
    items: ChatMessage[];
    total: number;
    page: number;
    page_size: number;
    conversation: Conversation;
}

export const fetchConversationMessages = async (
    conversationId: number,
    page?: number,
    pageSize?: number
): Promise<ConversationMessagesResponse> => {
    const query = new URLSearchParams();
    if (page) query.append('page', String(page));
    if (pageSize) query.append('page_size', String(pageSize));

    const url = query.toString()
        ? `/conversations/${conversationId}/messages?${query}`
        : `/conversations/${conversationId}/messages`;

    const data = await request<any>(url);
    return {
        items: data.items.map((m: any) => ({
            id: m.id,
            conversationId: m.conversation_id,
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
