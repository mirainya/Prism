import { User, UserRole } from '../types';
import { request } from './request';

export const login = async (username: string, password: string): Promise<{ user: User; token: string }> => {
  const data = await request<{ token: string; user: { id: number; username: string; role: string; balance: number } }>('/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  });

  localStorage.setItem('prism_token', data.token);

  return {
    user: {
      id: String(data.user.id),
      username: data.user.username,
      role: data.user.role as UserRole,
      balance: Number(data.user.balance) || 0,
      createdAt: new Date().toISOString().split('T')[0],
    },
    token: data.token,
  };
};

export const register = async (username: string, password: string): Promise<{ user: User }> => {
  const data = await request<{ id: number; username: string; role: string }>('/auth/register', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  });

  return {
    user: {
      id: String(data.id),
      username: data.username,
      role: data.role as UserRole,
      balance: 0,
      createdAt: new Date().toISOString().split('T')[0],
    },
  };
};

export const logout = async () => {
  try {
    await request('/auth/logout', { method: 'POST' });
  } catch {
    // ignore
  }
  localStorage.removeItem('prism_token');
  localStorage.removeItem('prism_user');
};

export const getCurrentUser = async (): Promise<User> => {
  const data = await request<{ id: number; username: string; role: string; balance: number }>('/user/me');
  return {
    id: String(data.id),
    username: data.username,
    role: data.role as UserRole,
    balance: Number(data.balance) || 0,
    createdAt: new Date().toISOString().split('T')[0],
  };
};

export const changePassword = async (oldPassword: string, newPassword: string): Promise<void> => {
    await request('/user/password', {
        method: 'PUT',
        body: JSON.stringify({old_password: oldPassword, new_password: newPassword}),
    });
};
