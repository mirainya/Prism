import { request } from './request';

export interface DocsModel {
  id: string;
  code: string;
  model_code: string;
  object: string;
  name: string;
  type: string;
  types: string[];
  description: string;
  visibility: string;
  features: string[];
  operations: {
    id: string;
    method: string;
    path: string;
    supports_stream: boolean;
    skus: {
      id: number;
      code: string;
      delivery_mode: string;
      max_results: number;
      idempotency_mode: string;
      service_tiers: string[];
    }[];
  }[];
  channels: {
    channel_id: number;
    channel_type: string;
    channel_name: string;
    model: string;
    protocol: string;
    interaction_mode: string;
    route_operation: string;
  }[];
  transports: string[];
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
}

export interface DocsVideosResponse {
  models: string[];
  model_options: Record<string, DocsVideoModelOptions>;
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
