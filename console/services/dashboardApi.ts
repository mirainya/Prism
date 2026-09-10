import { DashboardStats, TaskLog, TaskDetail } from '../types';
import { request } from './request';

export const fetchDashboardStats = async (): Promise<DashboardStats> => {
    return await request<DashboardStats>('/dashboard/stats');
};

// 任务日志
export interface TaskListParams {
  page?: number;
  page_size?: number;
  snapshot_at?: string;
  status?: string;
  capability?: string;
  token_id?: number;
  start_date?: string;
  end_date?: string;
  keyword?: string;
}

export interface TaskListResponse {
  items: TaskLog[];
  total: number;
  page: number;
  page_size: number;
  snapshot_at: string;
}

export const fetchTaskLogs = async (params?: TaskListParams): Promise<TaskListResponse> => {
  const query = new URLSearchParams();
  if (params?.page) query.append('page', String(params.page));
  if (params?.page_size) query.append('page_size', String(params.page_size));
  if (params?.snapshot_at) query.append('snapshot_at', params.snapshot_at);
  if (params?.status) query.append('status', params.status);
  if (params?.capability) query.append('capability', params.capability);
  if (params?.token_id) query.append('token_id', String(params.token_id));
  if (params?.start_date) query.append('start_date', params.start_date);
  if (params?.end_date) query.append('end_date', params.end_date);
  if (params?.keyword) query.append('keyword', params.keyword);

    const url = query.toString() ? `/tasks?${query}` : '/tasks';
  return await request<TaskListResponse>(url);
};

export const fetchTaskDetail = async (taskNo: string): Promise<TaskDetail> => {
    return await request<TaskDetail>(`/tasks/${taskNo}`);
};

export const fetchLogs = async (): Promise<any[]> => {
  const resp = await fetchTaskLogs({ page: 1, page_size: 20 });
  return resp.items;
};
