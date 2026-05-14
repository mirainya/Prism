import { User, UserRole } from '../types';
import { request } from './request';

export const fetchUsers = async (): Promise<User[]> => {
  const data = await request<any[]>('/admin/users');
  return data.map(u => ({
    id: String(u.id),
    username: u.username,
    role: u.role as UserRole,
    balance: Number(u.balance) || 0,
    createdAt: new Date(u.created_at).toISOString().split('T')[0],
  }));
};

export const updateUserRole = async (id: string, role: UserRole): Promise<void> => {
  await request(`/admin/users/${id}/role`, {
    method: 'PUT',
    body: JSON.stringify({ role }),
  });
};

export const updateUserStatus = async (id: string, status: number): Promise<void> => {
  await request(`/admin/users/${id}/status`, {
    method: 'PUT',
    body: JSON.stringify({ status }),
  });
};

export const rechargeUser = async (id: string, amount: number): Promise<void> => {
    await request(`/admin/users/${id}/recharge`, {
        method: 'POST',
        body: JSON.stringify({amount}),
    });
};
