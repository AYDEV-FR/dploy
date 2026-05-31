import { clearToken, getToken, login } from './auth';
import type {
  AvailableEnvironment,
  EnvironmentStatusResponse,
  ExtendTTLResponse,
  Me,
  RunEnvironmentResponse,
  UIConfig,
  UserEnvironmentsResponse,
} from './types';

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string>),
  };
  if (token && !path.includes('/available') && !path.includes('/ui-config')) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const res = await fetch(path, { ...options, headers });

  if (res.status === 401) {
    clearToken();
    login();
    throw new Error('Authentication required');
  }
  if (res.status === 204) {
    return null as T;
  }
  if (!res.ok) {
    let message = 'Request failed';
    try {
      message = (await res.json()).error || message;
    } catch {
      /* keep default */
    }
    throw new Error(message);
  }
  return res.json() as Promise<T>;
}

export const api = {
  getUIConfig: () => request<UIConfig>('/api/ui-config'),
  getMe: () => request<Me>('/api/me'),
  getAllInstances: () => request<UserEnvironmentsResponse>('/api/admin/instances'),
  getAvailable: () => request<AvailableEnvironment[]>('/api/environments/available'),
  getUserEnvironments: () => request<UserEnvironmentsResponse>('/api/environments'),
  run: (name: string) => request<RunEnvironmentResponse>(`/api/run/${encodeURIComponent(name)}`),
  status: (name: string) =>
    request<EnvironmentStatusResponse>(`/api/run/${encodeURIComponent(name)}/status`),
  extend: (name: string) =>
    request<ExtendTTLResponse>(`/api/run/${encodeURIComponent(name)}/extend`, { method: 'POST' }),
  remove: (name: string) =>
    request<void>(`/api/run/${encodeURIComponent(name)}`, { method: 'DELETE' }),
};
