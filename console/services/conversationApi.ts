import { Conversation, ChatMessage, ConversationTurnRecord } from '../types';
import { request } from './request';

export interface ConversationListParams {
    page?: number;
    page_size?: number;
    user_id?: number;
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
    if (params?.user_id) query.append('user_id', String(params.user_id));
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

export interface ConversationTurnsResponse {
    items: ConversationTurnRecord[];
    total: number;
    page: number;
    page_size: number;
}

const mapConversationTurn = (turn: any): ConversationTurnRecord => ({
    id: String(turn.id),
    conversationId: turn.conversation_id,
    sequence: String(turn.sequence),
    callId: turn.call_id,
    requestLogId: turn.request_log_id || undefined,
    model: turn.model,
    providerResponseId: turn.provider_response_id || undefined,
    status: turn.status,
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
});

export const fetchConversationTurns = async (
    conversationId: number,
    page?: number,
    pageSize?: number,
): Promise<ConversationTurnsResponse> => {
    const query = new URLSearchParams();
    if (page) query.append('page', String(page));
    if (pageSize) query.append('page_size', String(pageSize));
    const suffix = query.toString() ? `?${query}` : '';
    const data = await request<any>(`/conversations/${conversationId}/turns${suffix}`);
    return {
        items: (data.items || []).map(mapConversationTurn),
        total: data.total || 0,
        page: data.page || 1,
        page_size: data.page_size || pageSize || 50,
    };
};
