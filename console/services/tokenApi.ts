import { ApiToken } from '../types';
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
    xfsStorage: mapStorageStatus(t.xfs_storage),
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
        xfsStorage: mapStorageStatus(t.xfs_storage),
    };
};

const mapStorageStatus = (value: any): ApiToken['xfsStorage'] => ({
  configured: value?.configured === true,
  keyHint: typeof value?.key_hint === 'string' ? value.key_hint : '',
});

export const createToken = async (
    name: string,
    balance: number
): Promise<{ id: string; key: string; balance: number }> => {
    const data = await request<{ id: number; name: string; key: string; balance: number }>('/tokens', {
    method: 'POST',
        body: JSON.stringify({name, balance}),
  });
  return {
    id: String(data.id),
    key: data.key,
      balance: Number(data.balance) || 0,
  };
};

export const updateToken = async (
    id: string,
    data: { name?: string }
): Promise<void> => {
    await request(`/tokens/${id}`, {
        method: 'PUT',
        body: JSON.stringify(data),
    });
};

export const deleteToken = async (id: string): Promise<void> => {
  await request(`/tokens/${id}`, { method: 'DELETE' });
};

export const bindTokenFileStorage = async (id: string, apiKey: string): Promise<ApiToken['xfsStorage']> => {
  const data = await request<any>(`/tokens/${id}/file-storage`, {
    method: 'PUT',
    body: JSON.stringify({ api_key: apiKey }),
  });
  return mapStorageStatus(data);
};

export const unbindTokenFileStorage = async (id: string): Promise<ApiToken['xfsStorage']> => {
  const data = await request<any>(`/tokens/${id}/file-storage`, { method: 'DELETE' });
  return mapStorageStatus(data);
};

export const rechargeToken = async (id: string, amount: number): Promise<{ id: string; balance: number; totalUsed: number }> => {
  const data = await request<{ id: number; balance: number; total_used: number }>(`/tokens/${id}/recharge`, {
    method: 'POST',
    body: JSON.stringify({amount}),
  });
  return {
    id: String(data.id),
    balance: Number(data.balance) || 0,
    totalUsed: Number(data.total_used) || 0,
  };
};
