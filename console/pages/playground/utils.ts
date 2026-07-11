import { CapabilityStandardParamSchema } from '../../types';
import { ContentPart, MediaContext, MediaItem, TaskResult } from './types';

export const ACCEPTED_FILE_TYPES = 'image/png,image/jpeg,image/gif,image/webp,application/pdf,text/plain,text/csv,application/vnd.openxmlformats-officedocument.wordprocessingml.document';
export const MAX_FILE_SIZE = 20 * 1024 * 1024;

export const getClipboardFiles = (clipboardData: DataTransfer | null): File[] => {
  if (!clipboardData) return [];

  const files = Array.from(clipboardData.files);
  if (files.length > 0) return files;

  return Array.from(clipboardData.items)
    .filter(item => item.kind === 'file')
    .map(item => item.getAsFile())
    .filter((file): file is File => file !== null);
};

export const FALLBACK_STANDARD_PARAMS: Record<string, CapabilityStandardParamSchema> = {
  prompt: { name: '提示词', type: 'string', required: true },
};

const CHAT_FALLBACK_PARAMS: Record<string, CapabilityStandardParamSchema> = {
  prompt: { name: '提示词', type: 'string', required: true },
  temperature: { name: '温度', type: 'number' },
  max_tokens: { name: '最大Token数', type: 'number' },
};

export const LONG_TEXT_FIELDS = new Set(['prompt', 'negative_prompt']);
export const CONTROL_FIELDS = new Set(['channel', 'model', 'callback_url']);
export const CAPABILITY_PROMPT_KEYS = ['prompt', 'input', 'text', 'description'];
export const CAPABILITY_TYPE_ORDER = ['image', 'video', 'other'];

export const parseJsonField = <T,>(value: string, fieldName: string): T | undefined => {
  if (!value.trim()) return undefined;
  try {
    return JSON.parse(value) as T;
  } catch {
    throw new Error(`${fieldName} JSON 格式错误`);
  }
};

export const getContentText = (content: string | ContentPart[]): string => {
  if (typeof content === 'string') return content;
  return content.filter(p => p.type === 'text').map(p => p.text || '').join('');
};

export const formatFileSize = (bytes: number): string => {
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
};

export const getFileIcon = (contentType: string) => {
  if (contentType === 'application/pdf') return '📄';
  if (contentType.startsWith('text/')) return '📝';
  if (contentType.includes('word') || contentType.includes('document')) return '📃';
  return '📎';
};

export const parseStopSequences = (value: string): string[] | undefined => {
  const items = value.split('\n').map(item => item.trim()).filter(Boolean);
  return items.length > 0 ? items : undefined;
};

export const extractAssistantText = (content: any): string => {
  if (typeof content === 'string') return content;
  if (Array.isArray(content)) {
    return content.map(part => {
      if (typeof part === 'string') return part;
      if (part?.type === 'text' && typeof part.text === 'string') return part.text;
      return '';
    }).join('');
  }
  return '';
};

export const formatTime = (value?: string) => {
  if (!value) return '-';
  return new Date(value).toLocaleString();
};

export const formatJson = (value: any) => {
  if (value === undefined || value === null || value === '') return '暂无';
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
};

const IMAGE_URL_RE = /^https?:\/\/.+\.(png|jpg|jpeg|gif|webp|bmp|svg)(\?.*)?$/i;
const VIDEO_URL_RE = /^https?:\/\/.+\.(mp4|webm|mov|m4v)(\?.*)?$/i;

export const inferMediaType = (url: string): 'image' | 'video' | null => {
  if (IMAGE_URL_RE.test(url)) return 'image';
  if (VIDEO_URL_RE.test(url)) return 'video';
  return null;
};

export const isLikelyMediaKey = (key: string) => {
  const lowerKey = key.toLowerCase();
  return [
    'image', 'images', 'image_url', 'image_urls',
    'video', 'videos', 'video_url', 'video_urls',
    'url', 'urls', 'uri', 'file', 'files', 'output', 'outputs', 'data', 'result', 'results',
  ].some(item => lowerKey.includes(item));
};

export const inferMediaTypeFromHint = (hint?: string | null): 'image' | 'video' | null => {
  if (!hint) return null;
  const lowerHint = hint.toLowerCase();
  if (lowerHint.includes('video')) return 'video';
  if (lowerHint.includes('image')) return 'image';
  return null;
};

export const inferMediaTypeFromContext = (key: string, container?: Record<string, any> | null): 'image' | 'video' | null => {
  const keyHint = inferMediaTypeFromHint(key);
  if (keyHint) return keyHint;
  if (!container || typeof container !== 'object') return null;

  const contextHints = [
    container.type, container.media_type, container.mediaType,
    container.mime_type, container.mimeType, container.content_type,
    container.contentType, container.file_type, container.fileType,
    container.resource_type, container.resourceType,
  ];

  for (const hint of contextHints) {
    if (typeof hint !== 'string') continue;
    const inferred = inferMediaTypeFromHint(hint);
    if (inferred) return inferred;
  }
  return null;
};

const BASE64_IMAGE_PREFIXES = ['data:image/', '/9j/', 'iVBOR', 'R0lGO', 'UklGR'];
const BASE64_KEY_HINTS = ['b64', 'base64', 'b64_json'];

export const isBase64Image = (key: string, value: string): string | null => {
  if (value.startsWith('data:image/')) return value;
  if (value.length < 100) return null;
  const lowerKey = key.toLowerCase();
  const keyMatch = BASE64_KEY_HINTS.some(h => lowerKey.includes(h));
  if (!keyMatch && !BASE64_IMAGE_PREFIXES.some(p => value.startsWith(p))) return null;
  if (value.startsWith('/9j/')) return `data:image/jpeg;base64,${value}`;
  if (value.startsWith('iVBOR')) return `data:image/png;base64,${value}`;
  if (value.startsWith('R0lGO')) return `data:image/gif;base64,${value}`;
  if (value.startsWith('UklGR')) return `data:image/webp;base64,${value}`;
  if (keyMatch) return `data:image/png;base64,${value}`;
  return null;
};

export const extractMediaItems = (value: any, path = 'result', results: MediaItem[] = [], context: MediaContext = {}): MediaItem[] => {
  if (!value) return results;
  if (typeof value === 'string') {
    const mediaType = inferMediaType(value);
    if (mediaType) results.push({ type: mediaType, url: value, label: path });
    return results;
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) => extractMediaItems(item, `${path}[${index}]`, results, context));
    return results;
  }
  if (typeof value === 'object') {
    Object.entries(value).forEach(([key, item]) => {
      const nextPath = `${path}.${key}`;
      if (typeof item === 'string') {
        const mediaType = inferMediaType(item);
        if (mediaType) { results.push({ type: mediaType, url: item, label: nextPath }); return; }
        const b64Url = isBase64Image(key, item);
        if (b64Url) { results.push({ type: 'image', url: b64Url, label: nextPath }); return; }
        if (isLikelyMediaKey(key) && /^https?:\/\//i.test(item)) {
          const inferredType = inferMediaTypeFromContext(key, value)
            || (key.toLowerCase().includes('url') && context.capabilityType === 'video' ? 'video' : null)
            || (key.toLowerCase().includes('url') && context.capabilityType === 'image' ? 'image' : null);
          if (inferredType) { results.push({ type: inferredType, url: item, label: nextPath }); return; }
        }
      }
      extractMediaItems(item, nextPath, results, context);
    });
  }
  return results.filter((item, index, array) => array.findIndex(current => current.url === item.url) === index);
};

export const extractLinkItems = (value: any, path = 'result', results: Array<{ label: string; url: string }> = []): Array<{ label: string; url: string }> => {
  if (!value) return results;
  if (typeof value === 'string') {
    if (/^https?:\/\//i.test(value)) results.push({ label: path, url: value });
    return results;
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) => extractLinkItems(item, `${path}[${index}]`, results));
    return results;
  }
  if (typeof value === 'object') {
    Object.entries(value).forEach(([key, item]) => extractLinkItems(item, `${path}.${key}`, results));
  }
  return results.filter((item, index, array) => array.findIndex(current => current.url === item.url) === index);
};

export const buildResultSummary = (value: any) => {
  if (value === null || value === undefined) return '暂无结果';
  if (typeof value === 'string') return value.slice(0, 160) || '字符串结果';
  if (Array.isArray(value)) return `数组结果，共 ${value.length} 项`;
  if (typeof value === 'object') {
    const keys = Object.keys(value);
    return keys.length > 0 ? `对象结果，字段：${keys.slice(0, 6).join('、')}${keys.length > 6 ? '…' : ''}` : '空对象结果';
  }
  return String(value);
};

export const truncateText = (value: string, maxLength = 180) => {
  const normalized = value.replace(/\s+/g, ' ').trim();
  if (normalized.length <= maxLength) return normalized;
  return `${normalized.slice(0, maxLength).trim()}...`;
};

export const extractFirstMeaningfulString = (value: any): string => {
  if (typeof value === 'string') {
    const normalized = value.trim();
    return normalized.length >= 12 ? normalized : '';
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const found = extractFirstMeaningfulString(item);
      if (found) return found;
    }
    return '';
  }
  if (value && typeof value === 'object') {
    for (const [key, nested] of Object.entries(value)) {
      if (['model', 'channel', 'callback_url'].includes(key)) continue;
      const found = extractFirstMeaningfulString(nested);
      if (found) return found;
    }
  }
  return '';
};

export const extractCapabilityPrompt = (task: TaskResult) => {
  const candidates = [task.params, task.rawParams, task.mappedParams];
  for (const source of candidates) {
    if (!source || typeof source !== 'object') continue;
    for (const key of CAPABILITY_PROMPT_KEYS) {
      const value = source[key];
      if (typeof value === 'string' && value.trim()) return value.trim();
    }
  }
  for (const source of candidates) {
    const fallback = extractFirstMeaningfulString(source);
    if (fallback) return fallback;
  }
  return '';
};

export const getCapabilityPromptPreview = (task: TaskResult) => {
  const prompt = extractCapabilityPrompt(task);
  if (prompt) return truncateText(prompt);
  return `调用任务 ${task.taskNo}`;
};

export const normalizeCapabilityValue = (schema: CapabilityStandardParamSchema, value: string) => {
  if (schema.type === 'number') {
    const trimmed = value.trim();
    if (!trimmed) return undefined;
    const parsed = Number(trimmed);
    return Number.isFinite(parsed) ? parsed : undefined;
  }
  if (schema.type === 'array') {
    const items = value.split(/\r?\n/).map(item => item.trim()).filter(Boolean);
    return items.length > 0 ? items : undefined;
  }
  const trimmed = value.trim();
  return trimmed ? trimmed : undefined;
};

export const extractCapabilitySchema = (cap?: import('../../types').PlaygroundCapability | null) => {
  let schema: Record<string, CapabilityStandardParamSchema>;
  if (cap?.standardParams && Object.keys(cap.standardParams).length > 0) {
    schema = cap.standardParams;
  } else if (cap?.type === 'chat') {
    schema = CHAT_FALLBACK_PARAMS;
  } else {
    schema = FALLBACK_STANDARD_PARAMS;
  }
  return Object.entries(schema).filter(([key]) => !CONTROL_FIELDS.has(key));
};

export const extractCapabilityModel = (task: TaskResult) => {
  const candidates = [
    task.mappedParams?.model, task.rawParams?.model,
    task.vendorResponse?.model, task.result?.model,
  ];
  for (const value of candidates) {
    if (typeof value === 'string' && value.trim()) return value.trim();
  }
  if (task.channel) return '自动选择';
  return '未知';
};

export const getCapabilityTaskStatus = (status?: string) => {
  if (!status) return 'pending';
  if (status === 'success') return 'success';
  if (status === 'completed') return 'completed';
  if (status === 'failed') return 'failed';
  if (status === 'cancelled') return 'aborted';
  if (status === 'processing' || status === 'running') return status;
  return 'pending';
};

export const getCapabilityTypeBadgeClass = (type?: string) => {
  switch (type) {
    case 'image': return 'bg-pink-100 text-pink-700';
    case 'video': return 'bg-violet-100 text-violet-700';
    case 'chat': return 'bg-sky-100 text-sky-700';
    default: return 'bg-[var(--primary-lighter)] text-[var(--text-primary)]';
  }
};

export const isUploadableField = (key: string): boolean => {
  if (CONTROL_FIELDS.has(key)) return false;
  const k = key.toLowerCase();
  return ['image', 'url', 'file', 'ref_image'].some(p => k.includes(p));
};
