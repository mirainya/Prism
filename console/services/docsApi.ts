import { request } from './request';

export interface DocsModel {
  code: string;
  name: string;
  type: string;
  description: string;
  param_schema: Record<string, any> | null;
  channels: { channel_type: string; channel_name: string; model: string; price: number }[];
}

export const fetchDocsModels = async (): Promise<DocsModel[]> => {
  return request<DocsModel[]>('/docs/models');
};

export interface TryApiRequest {
  method: string;
  path: string;
  headers: Record<string, string>;
  body?: string;
}

export const buildApiUrl = (path: string): string => {
  return `${window.location.origin}${path}`;
};
