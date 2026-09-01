import { request } from './request';

export interface DocsModel {
  code: string;
  name: string;
  type: string;
  description: string;
  param_schema: Record<string, any> | null;
  channels: { channel_type: string; channel_name: string; model: string; price: number; interaction_mode?: string; param_schema?: Record<string, any> }[];
}

export const fetchDocsModels = async (): Promise<DocsModel[]> => {
  return request<DocsModel[]>('/docs/models');
};

export interface DocsVideoParameterOption {
  label: string;
  value: string | number | boolean;
  adds_resolutions?: string[];
}

export interface DocsVideoParameter {
  name: string;
  label: string;
  type: string;
  default?: string | number | boolean;
  min?: number;
  max?: number;
  options?: DocsVideoParameterOption[];
  task_modes?: string[];
  conflicts_with?: string[];
}

export interface DocsVideoModelOptions {
  resolutions?: string[];
  ratios?: string[];
  duration_min?: number;
  duration_max?: number;
  duration_max_with_video_reference?: number;
  duration_options?: number[];
  task_types?: string[];
  service_tiers?: string[];
  require_visual_media_with_audio?: boolean;
  allow_generated_audio?: boolean;
  allowed_roles?: string[];
  max_images?: number;
  max_videos?: number;
  max_audios?: number;
  max_media?: number;
  media_duration_min?: number;
  media_duration_max?: number;
  max_video_duration_total?: number;
  max_audio_duration_total?: number;
  parameters?: DocsVideoParameter[];
  allow_local_cancel?: boolean;
  cancel_statuses?: string[];
}

export interface DocsVideoChannel {
  id: number;
  name: string;
  models: string[];
  model_options: Record<string, DocsVideoModelOptions>;
}

export interface DocsVideosResponse {
  models: string[];
  model_options: Record<string, DocsVideoModelOptions>;
  channels: DocsVideoChannel[];
}

export const fetchDocsVideos = async (): Promise<DocsVideosResponse> => {
  return request<DocsVideosResponse>('/docs/videos');
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
