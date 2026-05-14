const API_BASE = '/api';

export const getAuthHeader = () => {
  const token = localStorage.getItem('prism_token');
  return token ? { Authorization: `Bearer ${token}` } : {};
};

export const request = async <T>(url: string, options: RequestInit = {}): Promise<T> => {
  const response = await fetch(`${API_BASE}${url}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...getAuthHeader(),
      ...options.headers,
    },
  });

    if (response.status === 401) {
        localStorage.removeItem('prism_token');
        localStorage.removeItem('prism_user');
        window.location.reload();
        throw new Error('Unauthorized');
    }

  const data = await response.json();

  if (!response.ok) {
    throw new Error(data.message || 'Request failed');
  }

  return data.data;
};

export { API_BASE };
