import { DashboardStats, TaskLog, TaskDetail, ChannelRequestLog } from '../types';
import { request } from './request';

export const fetchDashboardStats = async (): Promise<DashboardStats> => {
    return await request<DashboardStats>('/dashboard/stats');
};

// 任务日志
export interface TaskListParams {
  page?: number;
  page_size?: number;
  status?: string;
  capability?: string;
  start_date?: string;
  end_date?: string;
  keyword?: string;
}

export interface TaskListResponse {
  items: TaskLog[];
  total: number;
  page: number;
  page_size: number;
}

export const fetchTaskLogs = async (params?: TaskListParams): Promise<TaskListResponse> => {
  const query = new URLSearchParams();
  if (params?.page) query.append('page', String(params.page));
  if (params?.page_size) query.append('page_size', String(params.page_size));
  if (params?.status) query.append('status', params.status);
  if (params?.capability) query.append('capability', params.capability);
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

// 渠道请求日志
export interface RequestLogListParams {
  page?: number;
  page_size?: number;
  channel_id?: number;
  capability_code?: string;
  request_type?: string;
  task_no?: string;
  start_date?: string;
  end_date?: string;
}

export interface RequestLogListResponse {
  items: ChannelRequestLog[];
  total: number;
  page: number;
  page_size: number;
}

export const fetchRequestLogs = async (params?: RequestLogListParams): Promise<RequestLogListResponse> => {
  const query = new URLSearchParams();
  if (params?.page) query.append('page', String(params.page));
  if (params?.page_size) query.append('page_size', String(params.page_size));
  if (params?.channel_id) query.append('channel_id', String(params.channel_id));
  if (params?.capability_code) query.append('capability_code', params.capability_code);
  if (params?.request_type) query.append('request_type', params.request_type);
  if (params?.task_no) query.append('task_no', params.task_no);
  if (params?.start_date) query.append('start_date', params.start_date);
  if (params?.end_date) query.append('end_date', params.end_date);

  const url = query.toString() ? `/admin/request-logs?${query}` : '/admin/request-logs';
  return await request<RequestLogListResponse>(url);
};

export const fetchRequestLogDetail = async (id: number): Promise<ChannelRequestLog> => {
  return await request<ChannelRequestLog>(`/admin/request-logs/${id}`);
};

export const retryRequestLog = async (id: number): Promise<ChannelRequestLog> => {
    return await request<ChannelRequestLog>(`/admin/request-logs/${id}/retry`, {
        method: 'POST',
    });
};
