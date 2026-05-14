import { ApiToken, ChannelPriorityItem } from '../types';
import { request } from './request';

export const fetchTokens = async (): Promise<ApiToken[]> => {
  const data = await request<any[]>('/tokens');
  return data.map(t => ({
    id: String(t.id),
    name: t.name,
    key: t.key,
      balance: Number(t.balance) || 0,
      totalUsed: Number(t.total_used) || 0,
    status: t.status === 1 ? 'active' as const : 'expired' as const,
      channelPriorities: (t.channel_priorities || []).map((p: any) => ({
          capabilityCode: p.capability_code,
          channelId: p.channel_id,
          priority: p.priority,
      })),
  }));
};

export const getToken = async (id: string): Promise<ApiToken> => {
    const t = await request<any>(`/tokens/${id}`);
    return {
        id: String(t.id),
        name: t.name,
        key: t.key,
        balance: Number(t.balance) || 0,
        totalUsed: Number(t.total_used) || 0,
        status: t.status === 1 ? 'active' as const : 'expired' as const,
        channelPriorities: (t.channel_priorities || []).map((p: any) => ({
            capabilityCode: p.capability_code,
            channelId: p.channel_id,
            priority: p.priority,
        })),
    };
};

export const createToken = async (
    name: string,
    balance: number,
    channelPriorities?: ChannelPriorityItem[]
): Promise<{ id: string; key: string; balance: number }> => {
    const body: any = {name, balance};
    if (channelPriorities && channelPriorities.length > 0) {
        body.channel_priorities = channelPriorities.map(p => ({
            capability_code: p.capabilityCode,
            channel_id: p.channelId,
            priority: p.priority,
        }));
    }
    const data = await request<{ id: number; name: string; key: string; balance: number }>('/tokens', {
    method: 'POST',
        body: JSON.stringify(body),
  });
  return {
    id: String(data.id),
    key: data.key,
      balance: Number(data.balance) || 0,
  };
};

export const updateToken = async (
    id: string,
    data: { name?: string; channelPriorities?: ChannelPriorityItem[] }
): Promise<void> => {
    const body: any = {};
    if (data.name) {
        body.name = data.name;
    }
    if (data.channelPriorities !== undefined) {
        body.channel_priorities = data.channelPriorities.map(p => ({
            capability_code: p.capabilityCode,
            channel_id: p.channelId,
            priority: p.priority,
        }));
    }
    await request(`/tokens/${id}`, {
        method: 'PUT',
        body: JSON.stringify(body),
    });
};

export const deleteToken = async (id: string): Promise<void> => {
  await request(`/tokens/${id}`, { method: 'DELETE' });
};

export const rechargeToken = async (id: string, amount: number): Promise<{ id: string; balance: number }> => {
  const data = await request<{ id: number; balance: number }>(`/tokens/${id}/recharge`, {
    method: 'POST',
    body: JSON.stringify({amount}),
  });
  return {
    id: String(data.id),
    balance: Number(data.balance) || 0,
  };
};
