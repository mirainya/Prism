import React, {useState, useEffect, useRef, useMemo, useCallback} from 'react';
import {
  Play, Send, Bot, Loader2, Square, Trash2,
  Zap, AlertCircle, User as UserIcon, Eye,
  ChevronDown, ChevronRight, Brain, History, Bug,
  Clock3, CheckCircle2, XCircle, Radio,
  SlidersHorizontal, PanelLeft, FileJson, Plus, Image as ImageIcon, Link2, Search,
  Paperclip, X, FileText, File as FileIcon, Upload
} from 'lucide-react';
import {
  fetchTokens,
  playgroundListModels,
  playgroundListCapabilities,
  playgroundChatCompletions,
  playgroundInvokeCapability,
  playgroundGetTask,
  playgroundListTasks,
  playgroundListConversations,
  playgroundGetConversationMessages,
  playgroundGetDebug,
  playgroundUploadFile,
} from '../services/api';
import {
  ApiToken,
  PlaygroundModelInfo,
  PlaygroundConversation,
  PlaygroundDebugDetail,
  PlaygroundMessage,
  PlaygroundCapability,
  CapabilityStandardParamSchema,
} from '../types';

const ThinkingBlock: React.FC<{ content: string }> = ({ content }) => {
  const [expanded, setExpanded] = useState(false);
  return (
    <div className="border-b border-gray-200">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center gap-1.5 px-4 py-2 text-xs text-gray-500 hover:text-gray-700 transition-colors"
      >
        <Brain size={12} />
        <span>思考过程</span>
        {expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
      </button>
      {expanded && (
        <div className="px-4 pb-3 text-xs text-gray-500 whitespace-pre-wrap bg-gray-50 max-h-60 overflow-y-auto">
          {content}
        </div>
      )}
    </div>
  );
};

interface ContentPart {
  type: 'text' | 'image_url' | 'file_url';
  text?: string;
  image_url?: { url: string; detail?: string };
  file_url?: { url: string; content_type?: string };
}

interface Attachment {
  id: string;
  file: File;
  preview?: string;
  uploading: boolean;
  uploaded: boolean;
  url?: string;
  thUrl?: string;
  contentType: string;
  error?: string;
}

interface ChatMessage {
  role: 'system' | 'user' | 'assistant';
  content: string | ContentPart[];
  reasoningContent?: string;
  requestLogId?: number;
  finishReason?: string;
  status?: 'streaming' | 'completed' | 'failed' | 'aborted';
}

interface ChatState {
  messages: ChatMessage[];
  isStreaming: boolean;
  usage: { input: number; output: number; total?: number; cost?: number } | null;
  latencyMs: number | null;
  statusText: string;
}

const parseJsonField = <T,>(value: string, fieldName: string): T | undefined => {
  if (!value.trim()) return undefined;
  try {
    return JSON.parse(value) as T;
  } catch {
    throw new Error(`${fieldName} JSON 格式错误`);
  }
};

const getContentText = (content: string | ContentPart[]): string => {
  if (typeof content === 'string') return content;
  return content.filter(p => p.type === 'text').map(p => p.text || '').join('');
};

const ACCEPTED_FILE_TYPES = 'image/png,image/jpeg,image/gif,image/webp,application/pdf,text/plain,text/csv,application/vnd.openxmlformats-officedocument.wordprocessingml.document';
const MAX_FILE_SIZE = 20 * 1024 * 1024;

const formatFileSize = (bytes: number): string => {
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
};

const getFileIcon = (contentType: string) => {
  if (contentType === 'application/pdf') return '📄';
  if (contentType.startsWith('text/')) return '📝';
  if (contentType.includes('word') || contentType.includes('document')) return '📃';
  return '📎';
};

const parseStopSequences = (value: string): string[] | undefined => {
  const items = value.split('\n').map(item => item.trim()).filter(Boolean);
  return items.length > 0 ? items : undefined;
};

const extractAssistantText = (content: any): string => {
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

const formatTime = (value?: string) => {
  if (!value) return '-';
  return new Date(value).toLocaleString();
};

const formatJson = (value: any) => {
  if (value === undefined || value === null || value === '') return '暂无';
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
};

const StatusBadge: React.FC<{ status?: string }> = ({ status }) => {
  const config = {
    streaming: { label: '流式接收中', className: 'bg-blue-100 text-blue-700', icon: <Radio size={12} /> },
    completed: { label: '已完成', className: 'bg-green-100 text-green-700', icon: <CheckCircle2 size={12} /> },
    success: { label: '已完成', className: 'bg-green-100 text-green-700', icon: <CheckCircle2 size={12} /> },
    failed: { label: '失败', className: 'bg-red-100 text-red-700', icon: <XCircle size={12} /> },
    aborted: { label: '已中断', className: 'bg-amber-100 text-amber-700', icon: <Square size={12} /> },
    pending: { label: '等待中', className: 'bg-gray-100 text-gray-700', icon: <Clock3 size={12} /> },
    processing: { label: '处理中', className: 'bg-indigo-100 text-indigo-700', icon: <Loader2 size={12} className="animate-spin" /> },
    running: { label: '处理中', className: 'bg-indigo-100 text-indigo-700', icon: <Loader2 size={12} className="animate-spin" /> },
  } as const;
  const item = config[(status || 'pending') as keyof typeof config] || config.pending;
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-1 rounded-full text-xs ${item.className}`}>
      {item.icon}
      {item.label}
    </span>
  );
};

const HistoryPanel: React.FC<{
  items: PlaygroundConversation[];
  selectedConversationId?: number;
  currentModel?: string;
  onSelect: (conversation: PlaygroundConversation) => void;
  onCreateNew: () => void;
  loading: boolean;
}> = ({ items, selectedConversationId, currentModel, onSelect, onCreateNew, loading }) => {
  return (
    <div className="w-72 flex-shrink-0 bg-white rounded-xl border border-gray-200 overflow-hidden flex flex-col">
      <div className="px-4 py-3 border-b border-gray-100 flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 text-sm font-semibold text-gray-700">
          <History size={16} /> 历史会话
        </div>
        <button
          type="button"
          onClick={onCreateNew}
          className="inline-flex items-center gap-1 rounded-lg border border-indigo-200 bg-indigo-50 px-2 py-1 text-xs font-medium text-indigo-700 hover:bg-indigo-100"
        >
          <Plus size={12} /> 新会话
        </button>
      </div>
      <div className="flex-1 overflow-y-auto">
        {loading ? (
          <div className="p-4 text-sm text-gray-400 flex items-center gap-2"><Loader2 size={14} className="animate-spin" /> 加载中...</div>
        ) : items.length === 0 ? (
          <div className="p-4 text-sm text-gray-400">还没有历史会话</div>
        ) : items.map(item => {
          const modelMatched = currentModel && item.model === currentModel;
          return (
            <button
              key={item.id}
              onClick={() => onSelect(item)}
              className={`w-full text-left px-4 py-3 border-b border-gray-100 hover:bg-gray-50 transition-colors ${selectedConversationId === item.id ? 'bg-indigo-50' : ''}`}
            >
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <div className="text-sm font-medium text-gray-800 truncate">{item.title || `会话 #${item.id}`}</div>
                  <div className="mt-1 flex items-center gap-2 flex-wrap">
                    <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-[11px] ${modelMatched ? 'bg-indigo-100 text-indigo-700' : 'bg-gray-100 text-gray-600'}`}>{item.model}</span>
                    <span className="text-[11px] text-gray-400">{item.messageCount} 条消息</span>
                  </div>
                </div>
                <StatusBadge status={item.lastStatus || 'pending'} />
              </div>
              <div className="mt-2 text-[11px] text-gray-400 flex items-center justify-between gap-2">
                <span className="truncate">会话 #{item.id}</span>
                <span>{formatTime(item.updatedAt)}</span>
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
};

const DetailSection: React.FC<{
  title: string;
  icon: React.ReactNode;
  content: string;
}> = ({ title, icon, content }) => {
  const [expanded, setExpanded] = useState(false);

  return (
    <div className="rounded-lg border border-gray-200 overflow-hidden bg-white">
      <button
        type="button"
        onClick={() => setExpanded(prev => !prev)}
        className="w-full flex items-center justify-between px-3 py-2 bg-gray-50 text-xs font-semibold text-gray-600"
      >
        <span className="flex items-center gap-2">{icon} {title}</span>
        {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
      </button>
      {expanded && (
        <pre className="p-3 text-xs overflow-auto max-h-72 bg-gray-50 border-t border-gray-200">{content}</pre>
      )}
    </div>
  );
};

const DebugPanel: React.FC<{
  debugDetail: PlaygroundDebugDetail | null;
  lastPayload: Record<string, any> | null;
  compact?: boolean;
  showAllDetails?: boolean;
  onExpandFull?: () => void;
  currentConversationMeta?: PlaygroundConversation | null;
}> = ({ debugDetail, lastPayload, compact = false, showAllDetails = false, onExpandFull, currentConversationMeta }) => {
  const summaryRows = [
    { label: '日志 ID', value: debugDetail?.requestLogId || currentConversationMeta?.lastRequestLogId || '-' },
    { label: '会话 ID', value: debugDetail?.conversationId || currentConversationMeta?.id || '-' },
    { label: '渠道', value: `${debugDetail?.channelName || '-'}${debugDetail?.channelType ? ` (${debugDetail.channelType})` : ''}` },
    { label: '模型', value: debugDetail?.modelCode || '-' },
    { label: '供应商模型', value: debugDetail?.vendorModel || '-' },
    { label: '请求路径', value: debugDetail?.requestPath || '-' },
    { label: '模式', value: debugDetail?.isStream ? '流式' : '非流式' },
    { label: '耗时', value: debugDetail?.latencyMs ? `${(debugDetail.latencyMs / 1000).toFixed(2)}s` : '-' },
    { label: 'Finish Reason', value: debugDetail?.finishReason || '-' },
  ];

  return (
    <div className={`bg-white rounded-xl border border-gray-200 overflow-hidden flex flex-col min-w-0 ${compact ? 'w-full h-full' : 'w-[24rem] flex-shrink-0'}`}>
      <div className="px-4 py-3 border-b border-gray-100 flex items-center gap-2 text-sm font-semibold text-gray-700">
        <Bug size={16} /> 调试面板
      </div>
      <div className="flex-1 overflow-y-auto p-4 space-y-4">
        <div className="rounded-lg border border-gray-200 p-3 space-y-3 text-sm">
          <div className="flex items-center justify-between">
            <span className="text-gray-500">状态</span>
            <StatusBadge status={debugDetail?.status || 'pending'} />
          </div>
          {summaryRows.map(row => (
            <div key={row.label} className="flex items-start justify-between gap-3">
              <span className="text-gray-500 text-sm">{row.label}</span>
              <span className="text-right text-sm break-all font-mono">{row.value}</span>
            </div>
          ))}
        </div>

        {debugDetail?.errorMessage && (
          <div className="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 whitespace-pre-wrap">
            {debugDetail.errorMessage}
          </div>
        )}

        <div>
          <div className="text-xs font-semibold text-gray-600 mb-2">响应摘要</div>
          <div className="rounded-lg border border-gray-200 p-3 text-sm text-gray-700 whitespace-pre-wrap min-h-20">
            {debugDetail?.responsePreview || '暂无'}
          </div>
        </div>

        {debugDetail?.usage && (
          <div className="rounded-lg border border-gray-200 p-3 text-sm space-y-2">
            <div className="text-xs font-semibold text-gray-600">Usage</div>
            <div className="flex items-center justify-between"><span className="text-gray-500">Prompt</span><span>{debugDetail.usage.prompt_tokens}</span></div>
            <div className="flex items-center justify-between"><span className="text-gray-500">Completion</span><span>{debugDetail.usage.completion_tokens}</span></div>
            <div className="flex items-center justify-between"><span className="text-gray-500">Total</span><span>{debugDetail.usage.total_tokens}</span></div>
          </div>
        )}

        {showAllDetails ? (
          <>
            <div className="flex items-center justify-between gap-2">
              <div className="text-xs font-semibold text-gray-600">完整调试详情</div>
            </div>
            <div>
              <div className="text-xs font-semibold text-gray-600 mb-2">前端参数</div>
              <pre className="bg-gray-50 border border-gray-200 rounded-lg p-3 text-xs overflow-auto max-h-72">{formatJson(lastPayload)}</pre>
            </div>
            <div>
              <div className="text-xs font-semibold text-gray-600 mb-2">上游请求体</div>
              <pre className="bg-gray-50 border border-gray-200 rounded-lg p-3 text-xs overflow-auto max-h-72">{formatJson(debugDetail?.requestBody)}</pre>
            </div>
            <div>
              <div className="text-xs font-semibold text-gray-600 mb-2">请求头摘要</div>
              <pre className="bg-gray-50 border border-gray-200 rounded-lg p-3 text-xs overflow-auto max-h-56">{formatJson(debugDetail?.requestHeaders)}</pre>
            </div>
            <div>
              <div className="text-xs font-semibold text-gray-600 mb-2">响应体 / 错误详情</div>
              <pre className="bg-gray-50 border border-gray-200 rounded-lg p-3 text-xs overflow-auto max-h-[32rem]">{formatJson(debugDetail?.responseBody)}</pre>
            </div>
          </>
        ) : (
          <>
            <button
              type="button"
              onClick={() => onExpandFull?.()}
              className="w-full inline-flex items-center justify-center gap-2 px-3 py-2 rounded-lg border border-gray-200 text-sm text-gray-600 hover:bg-gray-50"
              disabled={!debugDetail}
            >
              <Eye size={14} /> 查看完整调试
            </button>
            <DetailSection title="前端参数" icon={<PanelLeft size={14} />} content={formatJson(lastPayload)} />
            <DetailSection title="上游请求体" icon={<FileJson size={14} />} content={formatJson(debugDetail?.requestBody)} />
            <DetailSection title="请求头摘要" icon={<FileJson size={14} />} content={formatJson(debugDetail?.requestHeaders)} />
            <DetailSection title="响应体 / 错误详情" icon={<FileJson size={14} />} content={formatJson(debugDetail?.responseBody)} />
          </>
        )}
      </div>
    </div>
  );
};

type MediaItem = {
  type: 'image' | 'video';
  url: string;
  label: string;
};

type MediaContext = {
  capabilityType?: PlaygroundCapability['type'];
};

const IMAGE_URL_RE = /^https?:\/\/.+\.(png|jpg|jpeg|gif|webp|bmp|svg)(\?.*)?$/i;
const VIDEO_URL_RE = /^https?:\/\/.+\.(mp4|webm|mov|m4v)(\?.*)?$/i;

const inferMediaType = (url: string): 'image' | 'video' | null => {
  if (IMAGE_URL_RE.test(url)) return 'image';
  if (VIDEO_URL_RE.test(url)) return 'video';
  return null;
};

const isLikelyMediaKey = (key: string) => {
  const lowerKey = key.toLowerCase();
  return [
    'image', 'images', 'image_url', 'image_urls',
    'video', 'videos', 'video_url', 'video_urls',
    'url', 'urls', 'uri', 'file', 'files', 'output', 'outputs', 'data', 'result', 'results',
  ].some(item => lowerKey.includes(item));
};

const inferMediaTypeFromHint = (hint?: string | null): 'image' | 'video' | null => {
  if (!hint) return null;
  const lowerHint = hint.toLowerCase();
  if (lowerHint.includes('video')) return 'video';
  if (lowerHint.includes('image')) return 'image';
  return null;
};

const inferMediaTypeFromContext = (key: string, container?: Record<string, any> | null): 'image' | 'video' | null => {
  const keyHint = inferMediaTypeFromHint(key);
  if (keyHint) return keyHint;
  if (!container || typeof container !== 'object') return null;

  const contextHints = [
    container.type,
    container.media_type,
    container.mediaType,
    container.mime_type,
    container.mimeType,
    container.content_type,
    container.contentType,
    container.file_type,
    container.fileType,
    container.resource_type,
    container.resourceType,
  ];

  for (const hint of contextHints) {
    if (typeof hint !== 'string') continue;
    const inferred = inferMediaTypeFromHint(hint);
    if (inferred) return inferred;
  }

  return null;
};

const extractMediaItems = (value: any, path = 'result', results: MediaItem[] = [], context: MediaContext = {}): MediaItem[] => {
  if (!value) return results;

  if (typeof value === 'string') {
    const mediaType = inferMediaType(value);
    if (mediaType) {
      results.push({ type: mediaType, url: value, label: path });
    }
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
        if (mediaType) {
          results.push({ type: mediaType, url: item, label: nextPath });
          return;
        }
        if (isLikelyMediaKey(key) && /^https?:\/\//i.test(item)) {
          const inferredType = inferMediaTypeFromContext(key, value)
            || (key.toLowerCase().includes('url') && context.capabilityType === 'video' ? 'video' : null)
            || (key.toLowerCase().includes('url') && context.capabilityType === 'image' ? 'image' : null);
          if (inferredType) {
            results.push({ type: inferredType, url: item, label: nextPath });
            return;
          }
        }
      }
      extractMediaItems(item, nextPath, results, context);
    });
  }

  return results.filter((item, index, array) => array.findIndex(current => current.url === item.url) === index);
};

const extractLinkItems = (value: any, path = 'result', results: Array<{ label: string; url: string }> = []): Array<{ label: string; url: string }> => {
  if (!value) return results;
  if (typeof value === 'string') {
    if (/^https?:\/\//i.test(value)) {
      results.push({ label: path, url: value });
    }
    return results;
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) => extractLinkItems(item, `${path}[${index}]`, results));
    return results;
  }
  if (typeof value === 'object') {
    Object.entries(value).forEach(([key, item]) => {
      extractLinkItems(item, `${path}.${key}`, results);
    });
  }
  return results.filter((item, index, array) => array.findIndex(current => current.url === item.url) === index);
};

const buildResultSummary = (value: any) => {
  if (value === null || value === undefined) return '暂无结果';
  if (typeof value === 'string') return value.slice(0, 160) || '字符串结果';
  if (Array.isArray(value)) return `数组结果，共 ${value.length} 项`;
  if (typeof value === 'object') {
    const keys = Object.keys(value);
    return keys.length > 0 ? `对象结果，字段：${keys.slice(0, 6).join('、')}${keys.length > 6 ? '…' : ''}` : '空对象结果';
  }
  return String(value);
};

const CapabilityResultCard: React.FC<{ task: TaskResult }> = ({ task }) => {
  const mediaItems = useMemo(
    () => extractMediaItems(task.result, 'result', [], { capabilityType: task.capabilityType }),
    [task.result, task.capabilityType],
  );
  const imageItems = useMemo(() => mediaItems.filter(item => item.type === 'image'), [mediaItems]);
  const videoItems = useMemo(() => mediaItems.filter(item => item.type === 'video'), [mediaItems]);
  const linkItems = useMemo(() => extractLinkItems(task.result).filter(item => !mediaItems.some(media => media.url === item.url)), [task.result, mediaItems]);
  const summary = useMemo(() => buildResultSummary(task.result), [task.result]);

  return (
    <div className="space-y-3">
      <div className="rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
        {summary}
      </div>

      {imageItems.length > 0 && (
        <div>
          <div className="mb-2 flex items-center gap-2 text-xs font-semibold text-gray-600">
            <ImageIcon size={14} /> 图片结果
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            {imageItems.map((item, index) => (
              <a
                key={`${item.url}-${index}`}
                href={item.url}
                target="_blank"
                rel="noreferrer"
                className="group overflow-hidden rounded-xl border border-gray-200 bg-gray-50 hover:border-indigo-300"
              >
                <img src={item.url} alt={`result-${index}`} className="h-56 w-full object-cover bg-gray-100" />
                <div className="space-y-1 border-t border-gray-200 px-3 py-2 text-xs text-gray-600">
                  <div className="truncate font-medium">{item.label}</div>
                  <div className="truncate">{item.url}</div>
                </div>
              </a>
            ))}
          </div>
        </div>
      )}

      {videoItems.length > 0 && (
        <div>
          <div className="mb-2 flex items-center gap-2 text-xs font-semibold text-gray-600">
            <Play size={14} /> 视频结果
          </div>
          <div className="grid grid-cols-1 gap-3">
            {videoItems.map((item, index) => (
              <div key={`${item.url}-${index}`} className="overflow-hidden rounded-xl border border-gray-200 bg-gray-50">
                <video src={item.url} controls className="h-72 w-full bg-black" preload="metadata" />
                <div className="space-y-1 border-t border-gray-200 px-3 py-2 text-xs text-gray-600">
                  <div className="truncate font-medium">{item.label}</div>
                  <a href={item.url} target="_blank" rel="noreferrer" className="block truncate text-indigo-700 hover:underline">{item.url}</a>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {linkItems.length > 0 && (
        <div>
          <div className="mb-2 flex items-center gap-2 text-xs font-semibold text-gray-600">
            <Link2 size={14} /> 结果链接
          </div>
          <div className="space-y-2">
            {linkItems.map(item => (
              <a
                key={item.url}
                href={item.url}
                target="_blank"
                rel="noreferrer"
                className="flex items-center justify-between gap-3 rounded-lg border border-gray-200 px-3 py-2 text-sm text-indigo-700 hover:bg-indigo-50"
              >
                <span className="truncate">{item.label}</span>
                <span className="truncate text-xs text-gray-500">{item.url}</span>
              </a>
            ))}
          </div>
        </div>
      )}

      <details className="rounded-lg border border-gray-200 bg-gray-50">
        <summary className="cursor-pointer select-none px-3 py-2 text-xs font-semibold text-gray-600">原始结果 JSON</summary>
        <pre className="max-h-72 overflow-auto border-t border-gray-200 p-3 text-xs">{JSON.stringify(task.result, null, 2)}</pre>
      </details>
    </div>
  );
};

const ChatTab: React.FC<{ tokenId: string }> = ({ tokenId }) => {
  const [models, setModels] = useState<PlaygroundModelInfo[]>([]);
  const [selectedModel, setSelectedModel] = useState('');
  const [systemPrompt, setSystemPrompt] = useState('');
  const [temperature, setTemperature] = useState(0.7);
  const [maxTokens, setMaxTokens] = useState(200000);
  const [topP, setTopP] = useState(1);
  const [presencePenalty, setPresencePenalty] = useState(0);
  const [frequencyPenalty, setFrequencyPenalty] = useState(0);
  const [stop, setStop] = useState('');
  const [seed, setSeed] = useState('');
  const [userValue, setUserValue] = useState('');
  const [conversationId, setConversationId] = useState('');
  const [responseFormatText, setResponseFormatText] = useState('');
  const [toolsText, setToolsText] = useState('');
  const [toolChoiceText, setToolChoiceText] = useState('');
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [showHistoryDrawer, setShowHistoryDrawer] = useState(false);
  const [showAdvancedDrawer, setShowAdvancedDrawer] = useState(false);
  const [showDebugDrawer, setShowDebugDrawer] = useState(false);
  const [showSystemPrompt, setShowSystemPrompt] = useState(false);
  const [showFullDebug, setShowFullDebug] = useState(false);
  const [pendingModel, setPendingModel] = useState<string | null>(null);
  const [stream, setStream] = useState(false);
  const [input, setInput] = useState('');
  const [chat, setChat] = useState<ChatState>({ messages: [], isStreaming: false, usage: null, latencyMs: null, statusText: '等待发送' });
  const [error, setError] = useState('');
  const [historyItems, setHistoryItems] = useState<PlaygroundConversation[]>([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [selectedConversationId, setSelectedConversationId] = useState<number | undefined>();
  const [currentConversationMeta, setCurrentConversationMeta] = useState<PlaygroundConversation | null>(null);
  const [debugDetail, setDebugDetail] = useState<PlaygroundDebugDetail | null>(null);
  const [lastPayload, setLastPayload] = useState<Record<string, any> | null>(null);
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [isDragging, setIsDragging] = useState(false);
  const dragCounterRef = useRef(0);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const abortRef = useRef<AbortController | null>(null);
  const streamFlushTimerRef = useRef<number | null>(null);
  const lastAutoScrollAtRef = useRef(0);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const selectedModelInfo = useMemo(() => models.find(model => model.id === selectedModel), [models, selectedModel]);
  const streamDisabled = selectedModelInfo?.supports_stream === false;
  const effectiveConversationId = selectedConversationId ? String(selectedConversationId) : conversationId.trim();
  const conversationModel = currentConversationMeta?.model;
  const hasConversationMessages = chat.messages.some(msg => msg.role === 'user' || msg.role === 'assistant');
  const modelChangedOnConversation = Boolean(selectedConversationId && conversationModel && selectedModel && conversationModel !== selectedModel && hasConversationMessages);

  const loadHistory = async () => {
    if (!tokenId) return;
    setHistoryLoading(true);
    try {
      const result = await playgroundListConversations(tokenId, { page: 1, page_size: 30 });
      setHistoryItems(result.items || []);
      setCurrentConversationMeta(prev => prev ? (result.items.find(item => item.id === prev.id) || prev) : prev);
    } catch {
      setHistoryItems([]);
    } finally {
      setHistoryLoading(false);
    }
  };

  useEffect(() => {
    if (!tokenId) return;
    playgroundListModels(tokenId)
      .then(m => {
        setModels(m);
        if (m.length > 0) {
          setSelectedModel(prev => (prev && m.some(item => item.id === prev) ? prev : m[0].id));
        }
      })
      .catch(() => setModels([]));
    loadHistory();
  }, [tokenId]);

  useEffect(() => {
    if (!selectedModelInfo) return;
    if (selectedModelInfo.supports_stream === false) {
      setStream(false);
      return;
    }
    setStream(Boolean(selectedModelInfo.default_stream));
  }, [selectedModelInfo?.id, selectedModelInfo?.supports_stream, selectedModelInfo?.default_stream]);

  useEffect(() => {
    const now = Date.now();
    const behavior: ScrollBehavior = now - lastAutoScrollAtRef.current < 150 ? 'auto' : 'smooth';
    messagesEndRef.current?.scrollIntoView({ behavior });
    lastAutoScrollAtRef.current = now;
  }, [chat.messages]);

  useEffect(() => () => {
    if (streamFlushTimerRef.current !== null) {
      window.clearTimeout(streamFlushTimerRef.current);
    }
  }, []);

  const applyConversationMessages = async (conversation: PlaygroundConversation) => {
    setSelectedConversationId(conversation.id);
    setConversationId(String(conversation.id));
    setSelectedModel(conversation.model);
    setCurrentConversationMeta(conversation);
    setPendingModel(null);
    setShowHistoryDrawer(false);
    try {
      const result = await playgroundGetConversationMessages(tokenId, conversation.id);
      const loadedMessages: ChatMessage[] = result.items.map((msg: PlaygroundMessage) => ({
        role: msg.role,
        content: msg.content,
        reasoningContent: msg.reasoningContent,
        requestLogId: msg.requestLogId,
        finishReason: msg.finishReason,
        status: 'completed',
      }));
      const fallbackRequestLogId = result.conversation.lastRequestLogId
        || [...result.items].reverse().find((msg: PlaygroundMessage) => typeof msg.requestLogId === 'number' && msg.requestLogId > 0)?.requestLogId;
      const debug = fallbackRequestLogId ? await playgroundGetDebug(tokenId, fallbackRequestLogId).catch(() => null) : null;
      setChat({
        messages: loadedMessages,
        isStreaming: false,
        usage: null,
        latencyMs: null,
        statusText: '已载入历史会话',
      });
      setCurrentConversationMeta({
        ...result.conversation,
        lastRequestLogId: fallbackRequestLogId,
      });
      setDebugDetail(debug);
      setError('');
    } catch (err: any) {
      setError(err.message || '加载历史会话失败');
    }
  };

  const handleSend = async () => {
    const hasText = input.trim().length > 0;
    const readyAttachments = attachments.filter(a => a.uploaded && a.url);
    const hasAttachments = readyAttachments.length > 0;
    if ((!hasText && !hasAttachments) || chat.isStreaming || !selectedModel) return;

    // 检查是否有正在上传的附件
    if (attachments.some(a => a.uploading)) {
      setError('请等待文件上传完成');
      return;
    }
    // 检查是否有上传失败的附件
    if (attachments.some(a => a.error)) {
      setError('有文件上传失败，请移除后重试');
      return;
    }

    setError('');

    if (modelChangedOnConversation) {
      setError('当前模型与已加载会话模型不一致，请先确认切换策略。');
      return;
    }

    let responseFormat: any;
    let tools: any;
    let toolChoice: any;
    try {
      responseFormat = parseJsonField(responseFormatText, 'response_format');
      tools = parseJsonField(toolsText, 'tools');
      toolChoice = parseJsonField(toolChoiceText, 'tool_choice');
    } catch (err: any) {
      setError(err.message || '高级参数格式错误');
      return;
    }

    // 构造 content：有附件时用数组格式，否则用纯文本
    let userContent: string | ContentPart[];
    if (hasAttachments) {
      const parts: ContentPart[] = [];
      if (hasText) {
        parts.push({ type: 'text', text: input.trim() });
      }
      for (const att of readyAttachments) {
        if (att.contentType.startsWith('image/')) {
          parts.push({ type: 'image_url', image_url: { url: att.url! } });
        } else {
          parts.push({ type: 'file_url', file_url: { url: att.url!, content_type: att.contentType } });
        }
      }
      userContent = parts;
    } else {
      userContent = input.trim();
    }

    const userMsg: ChatMessage = { role: 'user', content: userContent, status: 'completed' };
    const hasConversation = Boolean(effectiveConversationId);
    const allMessages: ChatMessage[] = hasConversation
      ? [...chat.messages, userMsg]
      : [...chat.messages.filter(msg => msg.role !== 'system'), userMsg];
    const incrementalMessages = hasConversation ? [userMsg] : allMessages.map(m => ({ role: m.role, content: m.content }));
    const requestMessages = systemPrompt.trim() && !hasConversation
      ? [{ role: 'system', content: systemPrompt.trim() }, ...incrementalMessages]
      : incrementalMessages;

    const payload: Record<string, any> = {
      model: selectedModel,
      messages: requestMessages,
      temperature,
      max_tokens: maxTokens,
      top_p: topP,
      presence_penalty: presencePenalty,
      frequency_penalty: frequencyPenalty,
      stop: parseStopSequences(stop),
      stream,
      seed: seed.trim() ? Number(seed) : undefined,
      user: userValue.trim() || undefined,
      conversation_id: effectiveConversationId || undefined,
      response_format: responseFormat,
      tools,
      tool_choice: toolChoice,
    };
    setLastPayload(payload);

    setChat(prev => ({
      ...prev,
      messages: [...prev.messages.filter(msg => msg.role !== 'system'), userMsg, { role: 'assistant', content: '', status: 'streaming' }],
      isStreaming: stream,
      usage: null,
      latencyMs: null,
      statusText: stream ? '正在流式接收...' : '正在请求...',
    }));
    setInput('');
    // 清空附件（释放预览 URL）
    attachments.forEach(a => { if (a.preview) URL.revokeObjectURL(a.preview); });
    setAttachments([]);

    const startTime = Date.now();
    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const res = await playgroundChatCompletions(tokenId, payload, controller.signal);
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.message || `请求失败 (${res.status})`);
      }

      let assistantContent = '';
      let reasoningContent = '';
      let usage: ChatState['usage'] = null;
      let finalStatus: ChatMessage['status'] = 'completed';
      let debug: PlaygroundDebugDetail | null = null;
      let finishReason = '';

      if (stream) {
        if (!res.body) throw new Error('无响应流');
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';

        const flushStreamMessage = () => {
          if (streamFlushTimerRef.current !== null) {
            return;
          }
          streamFlushTimerRef.current = window.setTimeout(() => {
            streamFlushTimerRef.current = null;
            setChat(prev => {
              const msgs = [...prev.messages];
              msgs[msgs.length - 1] = {
                role: 'assistant',
                content: assistantContent,
                reasoningContent: reasoningContent || undefined,
                status: 'streaming',
                finishReason,
              };
              return { ...prev, messages: msgs, statusText: '正在流式接收...' };
            });
          }, 80);
        };

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const events = buffer.split('\n\n');
          buffer = events.pop() || '';

          for (const eventBlock of events) {
            const lines = eventBlock.split('\n');
            let eventName = 'message';
            const dataLines: string[] = [];
            for (const line of lines) {
              if (line.startsWith('event: ')) eventName = line.slice(7).trim();
              if (line.startsWith('data: ')) dataLines.push(line.slice(6));
            }
            const data = dataLines.join('\n');
            if (!data || data === '[DONE]') continue;

            if (eventName === 'prism-debug') {
              debug = JSON.parse(data) as PlaygroundDebugDetail;
              setDebugDetail(debug);
              continue;
            }

            try {
              const parsed = JSON.parse(data);
              const delta = parsed.choices?.[0]?.delta;
              if (delta?.content) assistantContent += delta.content;
              if (delta?.reasoning_content) reasoningContent += delta.reasoning_content;
              if (parsed.choices?.[0]?.finish_reason) finishReason = parsed.choices[0].finish_reason;
              if (parsed.usage) {
                usage = {
                  input: parsed.usage.prompt_tokens || 0,
                  output: parsed.usage.completion_tokens || 0,
                  total: parsed.usage.total_tokens || 0,
                };
              }
              flushStreamMessage();
            } catch {
              // ignore chunk parse errors
            }
          }
        }

        if (streamFlushTimerRef.current !== null) {
          window.clearTimeout(streamFlushTimerRef.current);
          streamFlushTimerRef.current = null;
        }
      } else {
        const parsed = await res.json();
        assistantContent = extractAssistantText(parsed.choices?.[0]?.message?.content);
        reasoningContent = parsed.choices?.[0]?.message?.reasoning_content || '';
        finishReason = parsed.choices?.[0]?.finish_reason || parsed.debug?.finishReason || '';
        if (parsed.usage) {
          usage = {
            input: parsed.usage.prompt_tokens || 0,
            output: parsed.usage.completion_tokens || 0,
            total: parsed.usage.total_tokens || 0,
          };
        }
        debug = parsed.debug || null;
        setDebugDetail(debug);
        if (parsed.conversation_id) {
          setConversationId(parsed.conversation_id);
          setSelectedConversationId(Number(parsed.conversation_id));
        }
      }

      setChat(prev => {
        const msgs = [...prev.messages];
        if (assistantContent || reasoningContent) {
          msgs[msgs.length - 1] = {
            role: 'assistant',
            content: assistantContent,
            reasoningContent: reasoningContent || undefined,
            status: finalStatus,
            finishReason,
            requestLogId: debug?.requestLogId,
          };
        } else {
          msgs[msgs.length - 1] = {
            role: 'assistant',
            content: '',
            reasoningContent: reasoningContent || undefined,
            status: finalStatus,
            finishReason,
            requestLogId: debug?.requestLogId,
          };
        }
        return {
          ...prev,
          messages: msgs,
          isStreaming: false,
          usage,
          latencyMs: Date.now() - startTime,
          statusText: finalStatus === 'completed' ? '已完成' : '请求结束',
        };
      });

      if (debug?.conversationId) {
        setConversationId(String(debug.conversationId));
        setSelectedConversationId(debug.conversationId);
        setHistoryItems(prev => {
          const existing = prev.find(item => item.id === debug.conversationId);
          const updatedItem: PlaygroundConversation = existing ? {
            ...existing,
            model: selectedModel,
            systemPrompt,
            lastRequestLogId: debug.requestLogId,
            lastStatus: debug.status || 'completed',
            updatedAt: new Date().toISOString(),
            messageCount: Math.max(existing.messageCount, chat.messages.length + 2),
          } : {
            id: debug.conversationId,
            userId: 0,
            tokenId: Number(tokenId),
            title: input.trim().slice(0, 20) || `会话 #${debug.conversationId}`,
            model: selectedModel,
            systemPrompt,
            lastRequestLogId: debug.requestLogId,
            lastStatus: debug.status || 'completed',
            totalTokens: usage?.total || 0,
            messageCount: chat.messages.length + 2,
            totalCost: 0,
            status: 1,
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
          };
          setCurrentConversationMeta(updatedItem);
          return existing
            ? prev.map(item => item.id === debug.conversationId ? updatedItem : item)
            : [updatedItem, ...prev];
        });
      } else {
        void loadHistory();
      }
    } catch (err: any) {
      const aborted = err.name === 'AbortError';
      setError(aborted ? '请求已中断' : (err.message || '请求失败'));
      setChat(prev => {
        const msgs = [...prev.messages];
        if (msgs.length > 0) {
          msgs[msgs.length - 1] = {
            ...msgs[msgs.length - 1],
            status: aborted ? 'aborted' : 'failed',
          };
        }
        return {
          ...prev,
          messages: msgs,
          isStreaming: false,
          statusText: aborted ? '已手动中断' : '请求失败',
        };
      });
    } finally {
      abortRef.current = null;
    }
  };

  const handleStop = () => {
    abortRef.current?.abort();
  };

  const resetConversationState = (statusText = '等待发送') => {
    setChat({ messages: [], isStreaming: false, usage: null, latencyMs: null, statusText });
    setError('');
    setDebugDetail(null);
    setConversationId('');
    setSelectedConversationId(undefined);
    setCurrentConversationMeta(null);
    setPendingModel(null);
    setInput('');
    setShowFullDebug(false);
  };

  const handleClear = () => {
    resetConversationState('等待发送');
  };

  const handleCreateNewConversation = () => {
    resetConversationState('已开启新会话');
    setShowHistoryDrawer(false);
  };

  const handleModelSelect = (nextModel: string) => {
    setSelectedModel(nextModel);
    if (selectedConversationId && hasConversationMessages && conversationModel && nextModel !== conversationModel) {
      setPendingModel(nextModel);
      setError('你已切换到新模型。请新建会话，或清空当前会话后再发送。');
      return;
    }
    setPendingModel(null);
  };

  const startFreshConversationWithModel = () => {
    if (!pendingModel) return;
    setSelectedModel(pendingModel);
    resetConversationState('已切换模型，请开始新会话');
    setPendingModel(null);
  };

  const addFiles = useCallback((files: FileList | File[]) => {
    const newAttachments: Attachment[] = [];
    for (const file of Array.from(files)) {
      if (file.size > MAX_FILE_SIZE) {
        setError(`文件 ${file.name} 超过 20MB 限制`);
        continue;
      }
      if (!ACCEPTED_FILE_TYPES.split(',').includes(file.type)) {
        setError(`不支持的文件类型: ${file.type || file.name}`);
        continue;
      }
      const att: Attachment = {
        id: crypto.randomUUID(),
        file,
        preview: file.type.startsWith('image/') ? URL.createObjectURL(file) : undefined,
        uploading: true,
        uploaded: false,
        contentType: file.type,
      };
      newAttachments.push(att);
    }
    if (newAttachments.length === 0) return;
    setAttachments(prev => [...prev, ...newAttachments]);

    // 立即上传
    for (const att of newAttachments) {
      playgroundUploadFile(tokenId, att.file)
        .then(result => {
          setAttachments(prev => prev.map(a =>
            a.id === att.id ? { ...a, uploading: false, uploaded: true, url: result.url, thUrl: result.thUrl } : a
          ));
        })
        .catch(err => {
          setAttachments(prev => prev.map(a =>
            a.id === att.id ? { ...a, uploading: false, error: err.message || '上传失败' } : a
          ));
        });
    }
  }, [tokenId]);

  const removeAttachment = useCallback((id: string) => {
    setAttachments(prev => {
      const att = prev.find(a => a.id === id);
      if (att?.preview) URL.revokeObjectURL(att.preview);
      return prev.filter(a => a.id !== id);
    });
  }, []);

  const handlePaste = useCallback((e: React.ClipboardEvent) => {
    const files = e.clipboardData?.files;
    if (files && files.length > 0) {
      e.preventDefault();
      addFiles(files);
    }
  }, [addFiles]);

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounterRef.current = 0;
    setIsDragging(false);
    const files = e.dataTransfer?.files;
    if (files && files.length > 0) {
      addFiles(files);
    }
  }, [addFiles]);

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  }, []);

  const handleDragEnter = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounterRef.current++;
    if (e.dataTransfer?.types?.includes('Files')) {
      setIsDragging(true);
    }
  }, []);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounterRef.current--;
    if (dragCounterRef.current === 0) {
      setIsDragging(false);
    }
  }, []);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  return (
    <div className="relative h-[calc(100vh-220px)] overflow-hidden">
      {(showHistoryDrawer || showAdvancedDrawer || showDebugDrawer) && (
        <div
          className="absolute inset-0 z-20 bg-black/20"
          onClick={() => {
            setShowHistoryDrawer(false);
            setShowAdvancedDrawer(false);
            setShowDebugDrawer(false);
          }}
        />
      )}

      {showHistoryDrawer && (
        <div className="absolute inset-y-0 left-0 z-30">
          <HistoryPanel
            items={historyItems}
            selectedConversationId={selectedConversationId}
            currentModel={selectedModel}
            onSelect={applyConversationMessages}
            onCreateNew={handleCreateNewConversation}
            loading={historyLoading}
          />
        </div>
      )}

      {showAdvancedDrawer && (
        <div className="absolute inset-y-0 right-0 z-30 w-[24rem] bg-white rounded-xl border border-gray-200 overflow-y-auto p-4 space-y-4 shadow-2xl">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 text-sm font-semibold text-gray-700">
              <SlidersHorizontal size={16} /> 参数设置
            </div>
            <button type="button" onClick={() => setShowAdvancedDrawer(false)} className="text-xs text-gray-400 hover:text-gray-600">关闭</button>
          </div>

          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">Temperature: {temperature}</label>
            <input type="range" min="0" max="2" step="0.1" value={temperature} onChange={e => setTemperature(Number(e.target.value))} className="w-full accent-indigo-600" />
          </div>

          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">Max Tokens</label>
            <input type="number" min={1} max={200000} value={maxTokens} onChange={e => setMaxTokens(Number(e.target.value))} className="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>

          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">Top P</label>
            <input type="number" min={0} max={1} step="0.1" value={topP} onChange={e => setTopP(Number(e.target.value))} className="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>

          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">Presence Penalty</label>
            <input type="number" min={-2} max={2} step="0.1" value={presencePenalty} onChange={e => setPresencePenalty(Number(e.target.value))} className="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>

          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">Frequency Penalty</label>
            <input type="number" min={-2} max={2} step="0.1" value={frequencyPenalty} onChange={e => setFrequencyPenalty(Number(e.target.value))} className="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>

          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">Stop（每行一个）</label>
            <textarea value={stop} onChange={e => setStop(e.target.value)} rows={3} className="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm resize-none focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>

          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">Seed</label>
            <input type="number" value={seed} onChange={e => setSeed(e.target.value)} className="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>

          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">User</label>
            <input type="text" value={userValue} onChange={e => setUserValue(e.target.value)} className="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>

          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">Conversation ID</label>
            <input type="text" value={effectiveConversationId} onChange={e => setConversationId(e.target.value)} className="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500" />
          </div>

          <div className="border border-gray-200 rounded-xl overflow-hidden">
            <button type="button" onClick={() => setShowAdvanced(prev => !prev)} className="w-full flex items-center justify-between px-3 py-2 bg-gray-50 text-xs font-medium text-gray-600">
              <span>高级参数</span>
              {showAdvanced ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
            </button>
            {showAdvanced && (
              <div className="p-3 space-y-3 border-t border-gray-200">
                <div>
                  <label className="block text-xs font-medium text-gray-500 mb-1">response_format (JSON)</label>
                  <textarea value={responseFormatText} onChange={e => setResponseFormatText(e.target.value)} rows={4} className="w-full px-3 py-2 border border-gray-200 rounded-lg text-xs font-mono resize-none focus:outline-none focus:ring-2 focus:ring-indigo-500" placeholder='{"type":"json_object"}' />
                </div>
                <div>
                  <label className="block text-xs font-medium text-gray-500 mb-1">tools (JSON)</label>
                  <textarea value={toolsText} onChange={e => setToolsText(e.target.value)} rows={5} className="w-full px-3 py-2 border border-gray-200 rounded-lg text-xs font-mono resize-none focus:outline-none focus:ring-2 focus:ring-indigo-500" />
                </div>
                <div>
                  <label className="block text-xs font-medium text-gray-500 mb-1">tool_choice (JSON)</label>
                  <textarea value={toolChoiceText} onChange={e => setToolChoiceText(e.target.value)} rows={4} className="w-full px-3 py-2 border border-gray-200 rounded-lg text-xs font-mono resize-none focus:outline-none focus:ring-2 focus:ring-indigo-500" />
                </div>
              </div>
            )}
          </div>

          <button onClick={handleClear} className="w-full flex items-center justify-center gap-2 px-3 py-2 text-sm text-gray-500 hover:text-red-500 hover:bg-red-50 rounded-lg transition-colors">
            <Trash2 size={14} /> 清空对话
          </button>
        </div>
      )}

      {showDebugDrawer && (
        <div className="absolute inset-y-0 right-0 z-30 w-[28rem] shadow-2xl">
          <DebugPanel debugDetail={debugDetail} lastPayload={lastPayload} compact showAllDetails={showFullDebug} onExpandFull={() => setShowFullDebug(true)} currentConversationMeta={currentConversationMeta} />
        </div>
      )}

      <div className="h-full flex gap-4">
        <div className="flex-1 flex flex-col bg-white rounded-xl border border-gray-200 overflow-hidden min-w-0">
          <div className="px-4 py-2 border-b border-gray-100 flex items-center justify-between gap-3">
            <div className="flex items-center gap-2 text-sm font-medium text-gray-700 min-w-0">
              <Bot size={15} /> 当前对话
              <StatusBadge status={chat.isStreaming ? 'streaming' : debugDetail?.status || 'pending'} />
            </div>
            <div className="flex items-center gap-1.5 flex-wrap justify-end">
              <button type="button" onClick={handleCreateNewConversation} className="inline-flex items-center gap-1.5 px-2 py-1 rounded-lg border border-indigo-200 bg-indigo-50 text-[11px] text-indigo-700 hover:bg-indigo-100">
                <Plus size={12} /> 新会话
              </button>
              <button type="button" onClick={() => setShowHistoryDrawer(true)} className="inline-flex items-center gap-1.5 px-2 py-1 rounded-lg border border-gray-200 text-[11px] text-gray-600 hover:bg-gray-50">
                <History size={12} /> 历史
              </button>
              <button type="button" onClick={() => setShowAdvancedDrawer(true)} className="inline-flex items-center gap-1.5 px-2 py-1 rounded-lg border border-gray-200 text-[11px] text-gray-600 hover:bg-gray-50">
                <SlidersHorizontal size={12} /> 参数
              </button>
              <button type="button" onClick={() => setShowSystemPrompt(prev => !prev)} className={`inline-flex items-center gap-1.5 px-2 py-1 rounded-lg border text-[11px] ${showSystemPrompt || systemPrompt.trim() ? 'border-indigo-200 bg-indigo-50 text-indigo-700' : 'border-gray-200 text-gray-600 hover:bg-gray-50'}`}>
                <ChevronDown size={12} className={`transition-transform ${showSystemPrompt ? 'rotate-180' : ''}`} /> Prompt
              </button>
              <button type="button" onClick={() => { setShowFullDebug(true); setShowDebugDrawer(true); }} className="inline-flex items-center gap-1.5 px-2 py-1 rounded-lg border border-gray-200 text-[11px] text-gray-600 hover:bg-gray-50 xl:hidden">
                <Bug size={12} /> 调试
              </button>
            </div>
          </div>

          <div className="px-4 py-1.5 border-b border-gray-100 space-y-1.5">
            <div className="grid grid-cols-1 lg:grid-cols-[minmax(0,1fr)_220px] gap-3 items-end">
              <div>
                <label className="block text-[11px] font-medium text-gray-500 mb-1">模型</label>
                <select
                  value={selectedModel}
                  onChange={e => handleModelSelect(e.target.value)}
                  className={`w-full h-10 px-3 border rounded-lg text-sm bg-white focus:outline-none focus:ring-2 focus:ring-indigo-500 ${modelChangedOnConversation ? 'border-amber-300 bg-amber-50' : 'border-gray-200'}`}
                >
                  {models.map(m => <option key={m.id} value={m.id}>{m.id}</option>)}
                </select>
              </div>
              <div>
                <label className="block text-[11px] font-medium text-gray-500 mb-1">流式输出</label>
                <label className={`h-10 flex items-center justify-between gap-3 rounded-lg border px-3 ${streamDisabled ? 'border-gray-100 bg-gray-50 text-gray-400' : 'border-gray-200 bg-white text-gray-700'}`}>
                  <span className="text-sm font-medium">Stream</span>
                  <button
                    type="button"
                    role="switch"
                    aria-checked={stream}
                    aria-label="切换 Stream"
                    disabled={streamDisabled}
                    onClick={() => !streamDisabled && setStream(prev => !prev)}
                    className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${streamDisabled ? 'bg-gray-200 cursor-not-allowed' : stream ? 'bg-indigo-600' : 'bg-gray-300'}`}
                  >
                    <span
                      className={`inline-block h-5 w-5 transform rounded-full bg-white shadow-sm transition-transform ${stream ? 'translate-x-5' : 'translate-x-1'}`}
                    />
                  </button>
                </label>
              </div>
            </div>
            <div className={`rounded-lg border px-3 py-1 text-[11px] ${streamDisabled ? 'border-amber-200 bg-amber-50 text-amber-700' : 'border-gray-200 bg-gray-50 text-gray-600'}`}>
              {streamDisabled ? '当前模型渠道声明为不支持流式，已自动关闭 stream。' : `当前模型默认 stream: ${selectedModelInfo?.default_stream ? '开启' : '关闭'}`}
            </div>
          </div>

          {(showSystemPrompt || systemPrompt.trim()) && (
            <div className="px-4 py-1.5 border-b border-gray-100">
              <label className="block text-[11px] font-medium text-gray-500 mb-1">System Prompt</label>
              <textarea value={systemPrompt} onChange={e => setSystemPrompt(e.target.value)} rows={2} className="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm resize-none focus:outline-none focus:ring-2 focus:ring-indigo-500" />
            </div>
          )}

          <div className="px-4 py-1 border-b border-gray-100 text-[11px] text-gray-500 flex items-center gap-3 flex-wrap">
            <span>{chat.statusText}</span>
            {conversationModel ? <span>会话模型 {conversationModel}</span> : null}
            {chat.latencyMs ? <span>耗时 {(chat.latencyMs / 1000).toFixed(2)}s</span> : null}
            {chat.usage ? <span>Tokens {chat.usage.input}/{chat.usage.output}/{chat.usage.total || (chat.usage.input + chat.usage.output)}</span> : null}
            {effectiveConversationId ? <span>会话 #{effectiveConversationId}</span> : null}
            {currentConversationMeta?.lastRequestLogId ? <span>日志 #{currentConversationMeta.lastRequestLogId}</span> : null}
          </div>
          {modelChangedOnConversation && (
            <div className="px-4 py-2 border-b border-amber-100 bg-amber-50 text-[11px] text-amber-700 flex items-center justify-between gap-3 flex-wrap">
              <span>当前会话绑定模型 {conversationModel}，你已切换到 {selectedModel}。为避免混入不同模型，请开启新会话后再发送。</span>
              <button type="button" onClick={startFreshConversationWithModel} className="px-2.5 py-1 rounded-md bg-amber-100 hover:bg-amber-200 text-amber-800 font-medium">
                新建会话
              </button>
            </div>
          )}

          <div className="flex-1 overflow-y-auto p-4 space-y-4 min-h-0">
            {chat.messages.length === 0 && (
              <div className="flex flex-col items-center justify-center h-full text-gray-300">
                <Bot size={56} strokeWidth={1} />
                <p className="mt-3 text-sm">选择模型，开始对话</p>
              </div>
            )}

            {chat.messages.map((msg, i) => (
              <div key={i} className={`flex gap-3 ${msg.role === 'user' ? 'justify-end' : ''}`}>
                {msg.role === 'assistant' && (
                  <div className="w-8 h-8 rounded-lg bg-indigo-100 flex items-center justify-center flex-shrink-0">
                    <Bot size={16} className="text-indigo-600" />
                  </div>
                )}
                <div className={`max-w-[85%] rounded-2xl text-sm ${msg.role === 'user' ? 'px-4 py-3 bg-indigo-600 text-white rounded-br-md' : 'bg-gray-100 text-gray-800 rounded-bl-md'}`}>
                  {msg.role === 'assistant' && msg.reasoningContent && <ThinkingBlock content={msg.reasoningContent} />}
                  <div className={msg.role === 'assistant' ? 'px-4 py-3 whitespace-pre-wrap' : 'whitespace-pre-wrap'}>
                    {typeof msg.content === 'string' ? (
                      msg.content || (msg.status === 'streaming' ? <Loader2 size={16} className="animate-spin text-gray-400" /> : null)
                    ) : (
                      <div className="space-y-2">
                        {msg.content.map((part, pi) => {
                          if (part.type === 'text' && part.text) {
                            return <span key={pi} className="whitespace-pre-wrap">{part.text}</span>;
                          }
                          if (part.type === 'image_url' && part.image_url) {
                            return (
                              <a key={pi} href={part.image_url.url} target="_blank" rel="noreferrer" className="block">
                                <img
                                  src={part.image_url.url}
                                  alt=""
                                  className={`max-w-[280px] max-h-[200px] rounded-xl object-cover shadow-sm hover:shadow-md transition-shadow ${msg.role === 'user' ? 'border border-white/20' : 'border border-gray-200'}`}
                                />
                              </a>
                            );
                          }
                          if (part.type === 'file_url' && part.file_url) {
                            const fileName = decodeURIComponent(part.file_url.url.split('/').pop() || '文件');
                            const ct = part.file_url.content_type || '';
                            return (
                              <a
                                key={pi}
                                href={part.file_url.url}
                                target="_blank"
                                rel="noreferrer"
                                className={`inline-flex items-center gap-2 px-3 py-2 rounded-xl text-xs transition-colors ${msg.role === 'user' ? 'bg-white/10 hover:bg-white/20 border border-white/10' : 'bg-white hover:bg-gray-50 border border-gray-200 shadow-sm'}`}
                              >
                                <span className="text-base">{getFileIcon(ct)}</span>
                                <span className="truncate max-w-[160px] font-medium">{fileName}</span>
                              </a>
                            );
                          }
                          return null;
                        })}
                      </div>
                    )}
                  </div>
                  {msg.role === 'assistant' && (
                    <div className="px-4 pb-3 flex items-center gap-3 text-xs text-gray-500 flex-wrap">
                      <StatusBadge status={msg.status || 'completed'} />
                      {msg.finishReason ? <span>finish: {msg.finishReason}</span> : null}
                      {msg.requestLogId ? <span>log #{msg.requestLogId}</span> : null}
                    </div>
                  )}
                </div>
                {msg.role === 'user' && (
                  <div className="w-8 h-8 rounded-lg bg-gray-200 flex items-center justify-center flex-shrink-0">
                    <UserIcon size={16} className="text-gray-600" />
                  </div>
                )}
              </div>
            ))}
            <div ref={messagesEndRef} />
          </div>

          {error && (
            <div className="mx-4 mb-2 px-3 py-2 bg-red-50 text-red-600 text-sm rounded-lg flex items-center gap-2">
              <AlertCircle size={14} /> {error}
            </div>
          )}

          <div className="border-t border-gray-100 p-4 relative" onDrop={handleDrop} onDragOver={handleDragOver} onDragEnter={handleDragEnter} onDragLeave={handleDragLeave}>
            <input
              ref={fileInputRef}
              type="file"
              accept={ACCEPTED_FILE_TYPES}
              multiple
              className="hidden"
              onChange={e => { if (e.target.files) addFiles(e.target.files); e.target.value = ''; }}
            />

            {/* 拖拽遮罩 */}
            {isDragging && (
              <div className="absolute inset-0 z-10 bg-indigo-50/80 backdrop-blur-sm border-2 border-dashed border-indigo-400 rounded-xl flex items-center justify-center pointer-events-none">
                <div className="flex flex-col items-center gap-2 text-indigo-600">
                  <Upload size={32} className="animate-bounce" />
                  <span className="text-sm font-medium">松开以添加文件</span>
                </div>
              </div>
            )}

            {/* 统一输入容器 */}
            <div className={`border rounded-2xl transition-all ${isDragging ? 'border-indigo-400 ring-2 ring-indigo-200' : 'border-gray-200 focus-within:border-indigo-400 focus-within:ring-2 focus-within:ring-indigo-100'}`}>
              {/* 附件预览区 */}
              {attachments.length > 0 && (
                <div className="px-3 pt-3 pb-2 flex flex-wrap gap-2 border-b border-gray-100">
                  {attachments.map(att => (
                    <div key={att.id} className="relative group animate-in fade-in zoom-in-95 duration-200">
                      {att.preview ? (
                        /* 图片附件 */
                        <div className="relative w-16 h-16 rounded-xl overflow-hidden border border-gray-200 bg-gray-50">
                          <img src={att.preview} alt="" className="w-full h-full object-cover" />
                          {att.uploading && (
                            <div className="absolute inset-0 bg-black/40 flex items-center justify-center">
                              <Loader2 size={16} className="animate-spin text-white" />
                            </div>
                          )}
                          {att.error && (
                            <div className="absolute inset-0 bg-red-500/60 flex items-center justify-center">
                              <XCircle size={16} className="text-white" />
                            </div>
                          )}
                          {att.uploaded && (
                            <div className="absolute bottom-0.5 right-0.5 w-4 h-4 bg-emerald-500 rounded-full flex items-center justify-center">
                              <CheckCircle2 size={10} className="text-white" />
                            </div>
                          )}
                          <button
                            onClick={() => removeAttachment(att.id)}
                            className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-all shadow-sm"
                          >
                            <X size={10} />
                          </button>
                        </div>
                      ) : (
                        /* 文件附件 */
                        <div className="relative flex items-center gap-2.5 pl-2.5 pr-7 py-2 rounded-xl border border-gray-200 bg-gray-50 hover:bg-gray-100 transition-colors max-w-[200px]">
                          <div className="w-8 h-8 rounded-lg bg-indigo-50 flex items-center justify-center flex-shrink-0 text-base">
                            {getFileIcon(att.contentType)}
                          </div>
                          <div className="min-w-0 flex-1">
                            <div className="text-xs font-medium text-gray-700 truncate">{att.file.name}</div>
                            <div className="text-[10px] text-gray-400 mt-0.5 flex items-center gap-1">
                              {att.uploading && <><Loader2 size={8} className="animate-spin text-indigo-500" /><span className="text-indigo-500">上传中</span></>}
                              {att.uploaded && <><CheckCircle2 size={8} className="text-emerald-500" /><span>{formatFileSize(att.file.size)}</span></>}
                              {att.error && <><XCircle size={8} className="text-red-500" /><span className="text-red-500 truncate">{att.error}</span></>}
                            </div>
                          </div>
                          <button
                            onClick={() => removeAttachment(att.id)}
                            className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-all shadow-sm"
                          >
                            <X size={10} />
                          </button>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}

              {/* 输入行 */}
              <div className="flex items-end gap-1.5 p-2">
                <button
                  onClick={() => fileInputRef.current?.click()}
                  disabled={chat.isStreaming}
                  className="p-2 text-gray-400 hover:text-indigo-600 hover:bg-indigo-50 rounded-lg disabled:opacity-40 transition-all flex-shrink-0 mb-0.5"
                  title="添加附件"
                >
                  <Paperclip size={18} />
                </button>
                <textarea
                  value={input}
                  onChange={e => setInput(e.target.value)}
                  onKeyDown={handleKeyDown}
                  onPaste={handlePaste}
                  placeholder="输入消息... (Enter 发送, Shift+Enter 换行, 可粘贴/拖拽文件)"
                  rows={2}
                  className="flex-1 px-2 py-2.5 text-sm resize-none focus:outline-none bg-transparent max-h-32"
                  disabled={chat.isStreaming}
                />
                {chat.isStreaming ? (
                  <button onClick={handleStop} className="px-4 py-2.5 bg-red-500 text-white rounded-xl hover:bg-red-600 transition-colors flex-shrink-0">
                    <Square size={18} />
                  </button>
                ) : (
                  <button
                    onClick={handleSend}
                    disabled={(!input.trim() && attachments.filter(a => a.uploaded).length === 0) || !selectedModel}
                    className="px-4 py-2.5 bg-indigo-600 text-white rounded-xl hover:bg-indigo-700 disabled:opacity-30 disabled:cursor-not-allowed transition-colors flex-shrink-0 flex items-center gap-1.5"
                  >
                    <Send size={16} />
                    <span className="text-sm font-medium">发送</span>
                  </button>
                )}
              </div>
            </div>
          </div>
        </div>

        <div className="hidden xl:flex xl:w-[24rem] xl:flex-shrink-0 xl:self-stretch min-w-0">
          <DebugPanel debugDetail={debugDetail} lastPayload={lastPayload} compact onExpandFull={() => { setShowFullDebug(true); setShowDebugDrawer(true); }} currentConversationMeta={currentConversationMeta} />
        </div>
      </div>
    </div>
  );
};

interface TaskResult {
  taskNo: string;
  status: string;
  progress: number;
  result: any;
  error: string;
  cost: number;
  capability?: string;
  capabilityName?: string;
  capabilityType?: PlaygroundCapability['type'];
  channel?: string;
  refunded?: boolean;
  params?: Record<string, any>;
  rawParams?: Record<string, any>;
  mappedParams?: Record<string, any>;
  vendorResponse?: any;
  vendorTaskId?: string;
  createdAt?: string;
  startedAt?: string;
  completedAt?: string;
}

const FALLBACK_STANDARD_PARAMS: Record<string, CapabilityStandardParamSchema> = {
  prompt: { name: '提示词', type: 'string', required: true },
};

const LONG_TEXT_FIELDS = new Set(['prompt', 'negative_prompt']);
const CONTROL_FIELDS = new Set(['channel', 'model', 'callback_url']);
const CAPABILITY_PROMPT_KEYS = ['prompt', 'input', 'text', 'description'];

const isUploadableField = (key: string): boolean => {
  if (CONTROL_FIELDS.has(key)) return false;
  const k = key.toLowerCase();
  return ['image', 'url', 'file', 'ref_image'].some(p => k.includes(p));
};

const truncateText = (value: string, maxLength = 180) => {
  const normalized = value.replace(/\s+/g, ' ').trim();
  if (normalized.length <= maxLength) return normalized;
  return `${normalized.slice(0, maxLength).trim()}...`;
};

const extractFirstMeaningfulString = (value: any): string => {
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

const extractCapabilityPrompt = (task: TaskResult) => {
  const candidates = [
    task.params,
    task.rawParams,
    task.mappedParams,
  ];

  for (const source of candidates) {
    if (!source || typeof source !== 'object') continue;
    for (const key of CAPABILITY_PROMPT_KEYS) {
      const value = source[key];
      if (typeof value === 'string' && value.trim()) {
        return value.trim();
      }
    }
  }

  for (const source of candidates) {
    const fallback = extractFirstMeaningfulString(source);
    if (fallback) return fallback;
  }

  return '';
};

const getCapabilityPromptPreview = (task: TaskResult) => {
  const prompt = extractCapabilityPrompt(task);
  if (prompt) return truncateText(prompt);
  return `调用任务 ${task.taskNo}`;
};

const normalizeCapabilityValue = (schema: CapabilityStandardParamSchema, value: string) => {
  if (schema.type === 'number') {
    const trimmed = value.trim();
    if (!trimmed) return undefined;
    const parsed = Number(trimmed);
    return Number.isFinite(parsed) ? parsed : undefined;
  }

  if (schema.type === 'array') {
    const items = value
      .split(/\r?\n/)
      .map(item => item.trim())
      .filter(Boolean);
    return items.length > 0 ? items : undefined;
  }

  const trimmed = value.trim();
  return trimmed ? trimmed : undefined;
};

const extractCapabilitySchema = (cap?: PlaygroundCapability | null) => {
  const schema = cap?.standardParams && Object.keys(cap.standardParams).length > 0
    ? cap.standardParams
    : FALLBACK_STANDARD_PARAMS;
  return Object.entries(schema).filter(([key]) => !CONTROL_FIELDS.has(key));
};

const extractCapabilityModel = (task: TaskResult) => {
  const candidates = [
    task.mappedParams?.model,
    task.rawParams?.model,
    task.vendorResponse?.model,
    task.result?.model,
  ];

  for (const value of candidates) {
    if (typeof value === 'string' && value.trim()) {
      return value.trim();
    }
  }

  if (task.channel) return '自动选择';
  return '未知';
};

const getCapabilityTaskStatus = (status?: string) => {
  if (!status) return 'pending';
  if (status === 'success') return 'success';
  if (status === 'completed') return 'completed';
  if (status === 'failed') return 'failed';
  if (status === 'cancelled') return 'aborted';
  if (status === 'processing' || status === 'running') return status;
  return 'pending';
};

const CapabilityDebugPanel: React.FC<{ task: TaskResult | null }> = ({ task }) => {
  if (!task) {
    return (
      <div className="bg-white rounded-xl border border-gray-200 overflow-hidden flex flex-col min-w-0 w-full h-full">
        <div className="px-4 py-3 border-b border-gray-100 flex items-center gap-2 text-sm font-semibold text-gray-700">
          <Bug size={16} /> 能力调试详情
        </div>
        <div className="flex-1 flex items-center justify-center p-5 text-gray-400 text-sm text-center">
          选择一个任务后，可在这里查看调试详情
        </div>
      </div>
    );
  }

  const resolvedModel = extractCapabilityModel(task);
  const fullPrompt = extractCapabilityPrompt(task);
  const hasResult = ['completed', 'success'].includes(task.status) && task.result;

  return (
    <div className="bg-white rounded-xl border border-gray-200 overflow-hidden flex flex-col min-w-0 w-full h-full">
      <div className="px-4 py-3 border-b border-gray-100 flex items-center gap-2 text-sm font-semibold text-gray-700">
        <Bug size={16} /> 能力调试详情
      </div>
      <div className="flex-1 overflow-y-auto p-4 space-y-4">
        <div className="rounded-lg border border-gray-200 p-3 space-y-3 text-sm">
          <div className="flex items-center justify-between">
            <span className="text-gray-500">状态</span>
            <StatusBadge status={getCapabilityTaskStatus(task.status)} />
          </div>
          <div className="flex items-start justify-between gap-3">
            <span className="text-gray-500">任务号</span>
            <span className="text-right font-mono break-all">{task.taskNo}</span>
          </div>
          <div className="flex items-start justify-between gap-3">
            <span className="text-gray-500">能力</span>
            <span className="text-right break-all">{task.capabilityName || task.capability || '-'}</span>
          </div>
          <div className="flex items-start justify-between gap-3">
            <span className="text-gray-500">渠道</span>
            <span className="text-right break-all">{task.channel || '自动选择'}</span>
          </div>
          <div className="flex items-start justify-between gap-3">
            <span className="text-gray-500">实际模型</span>
            <span className="text-right font-mono break-all">{resolvedModel}</span>
          </div>
          <div className="flex items-start justify-between gap-3">
            <span className="text-gray-500">供应商任务 ID</span>
            <span className="text-right font-mono break-all">{task.vendorTaskId || '-'}</span>
          </div>
          <div className="flex items-start justify-between gap-3">
            <span className="text-gray-500">进度</span>
            <span>{task.progress || 0}%</span>
          </div>
          <div className="flex items-start justify-between gap-3">
            <span className="text-gray-500">费用</span>
            <span>¥{Number(task.cost || 0).toFixed(4)}</span>
          </div>
          <div className="flex items-start justify-between gap-3">
            <span className="text-gray-500">创建时间</span>
            <span>{formatTime(task.createdAt)}</span>
          </div>
          <div className="flex items-start justify-between gap-3">
            <span className="text-gray-500">开始时间</span>
            <span>{formatTime(task.startedAt)}</span>
          </div>
          <div className="flex items-start justify-between gap-3">
            <span className="text-gray-500">完成时间</span>
            <span>{formatTime(task.completedAt)}</span>
          </div>
        </div>

        {task.error && (
          <div className="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 whitespace-pre-wrap">
            {task.error}
          </div>
        )}

        {hasResult ? <CapabilityResultCard task={task} /> : null}
        <DetailSection title="完整提示词" icon={<PanelLeft size={14} />} content={fullPrompt || '-'} />
        <DetailSection title="前端提交参数" icon={<PanelLeft size={14} />} content={formatJson(task.params)} />
        <DetailSection title="后端原始参数" icon={<FileJson size={14} />} content={formatJson(task.rawParams)} />
        <DetailSection title="后端映射参数" icon={<FileJson size={14} />} content={formatJson(task.mappedParams)} />
        <DetailSection title="标准结果" icon={<FileJson size={14} />} content={formatJson(task.result)} />
        <DetailSection title="供应商原始响应" icon={<FileJson size={14} />} content={formatJson(task.vendorResponse)} />
      </div>
    </div>
  );
};

const CAPABILITY_TYPE_ORDER = ['image', 'video', 'chat', 'other'];

const getCapabilityTypeBadgeClass = (type?: string) => {
  switch (type) {
    case 'image':
      return 'bg-pink-100 text-pink-700';
    case 'video':
      return 'bg-violet-100 text-violet-700';
    case 'chat':
      return 'bg-sky-100 text-sky-700';
    default:
      return 'bg-gray-100 text-gray-700';
  }
};

const CapabilityTab: React.FC<{ tokenId: string }> = ({ tokenId }) => {
  const [capabilities, setCapabilities] = useState<PlaygroundCapability[]>([]);
  const [selectedCap, setSelectedCap] = useState('');
  const [showCapabilityPicker, setShowCapabilityPicker] = useState(false);
  const [capabilitySearch, setCapabilitySearch] = useState('');
  const [capabilityTypeFilter, setCapabilityTypeFilter] = useState('');
  const [expandedCapabilityGroups, setExpandedCapabilityGroups] = useState<Set<string>>(new Set());
  const [params, setParams] = useState<Record<string, string>>({ prompt: '' });
  const [showAdvancedParams, setShowAdvancedParams] = useState(false);
  const [tasks, setTasks] = useState<TaskResult[]>([]);
  const [selectedTaskNo, setSelectedTaskNo] = useState<string>('');
  const [taskFilter, setTaskFilter] = useState<'all' | 'current'>('current');
  const [taskSearch, setTaskSearch] = useState('');
  const [showDebugDrawer, setShowDebugDrawer] = useState(false);
  const [showParamPanel, setShowParamPanel] = useState(true);
  const [hasTouchedParamPanel, setHasTouchedParamPanel] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState('');
  const pollTimers = useRef<Record<string, ReturnType<typeof setInterval>>>({});
  const activeTokenRef = useRef(tokenId);
  const capabilityPickerRef = useRef<HTMLDivElement | null>(null);
  const capFileInputRef = useRef<HTMLInputElement>(null);
  const [capUploadingField, setCapUploadingField] = useState<string | null>(null);
  const [capAttachments, setCapAttachments] = useState<Record<string, Attachment[]>>({});

  const handleFieldUpload = useCallback((key: string, file: File) => {
    if (!tokenId) return;
    const att: Attachment = {
      id: crypto.randomUUID(),
      file,
      preview: file.type.startsWith('image/') ? URL.createObjectURL(file) : undefined,
      uploading: true,
      uploaded: false,
      contentType: file.type,
    };
    setCapAttachments(prev => ({ ...prev, [key]: [...(prev[key] || []), att] }));

    playgroundUploadFile(tokenId, file)
      .then(result => {
        let url = result.url;
        if (url && !url.startsWith('http://') && !url.startsWith('https://')) {
          url = 'https://' + url;
        }
        setCapAttachments(prev => ({
          ...prev,
          [key]: (prev[key] || []).map(a => a.id === att.id ? { ...a, uploading: false, uploaded: true, url } : a),
        }));
      })
      .catch(err => {
        setCapAttachments(prev => ({
          ...prev,
          [key]: (prev[key] || []).map(a => a.id === att.id ? { ...a, uploading: false, error: err.message || '上传失败' } : a),
        }));
      });
  }, [tokenId]);

  const removeCapAttachment = useCallback((key: string, id: string) => {
    setCapAttachments(prev => {
      const list = prev[key] || [];
      const att = list.find(a => a.id === id);
      if (att?.preview) URL.revokeObjectURL(att.preview);
      return { ...prev, [key]: list.filter(a => a.id !== id) };
    });
  }, []);

  const triggerFieldUpload = useCallback((key: string) => {
    setCapUploadingField(key);
    setTimeout(() => capFileInputRef.current?.click(), 0);
  }, []);

  const mergeTask = (prev: TaskResult[], nextTask: TaskResult) => {
    const index = prev.findIndex(task => task.taskNo === nextTask.taskNo);
    if (index === -1) {
      return [nextTask, ...prev];
    }
    const merged = [...prev];
    merged[index] = {
      ...merged[index],
      ...nextTask,
      params: nextTask.params ?? merged[index].params,
      rawParams: nextTask.rawParams ?? merged[index].rawParams,
      mappedParams: nextTask.mappedParams ?? merged[index].mappedParams,
      vendorResponse: nextTask.vendorResponse ?? merged[index].vendorResponse,
      vendorTaskId: nextTask.vendorTaskId ?? merged[index].vendorTaskId,
      capabilityType: nextTask.capabilityType ?? merged[index].capabilityType,
      createdAt: nextTask.createdAt ?? merged[index].createdAt,
      startedAt: nextTask.startedAt ?? merged[index].startedAt,
      completedAt: nextTask.completedAt ?? merged[index].completedAt,
      result: nextTask.result ?? merged[index].result,
    };
    return merged;
  };

  const clearAllPolling = () => {
    Object.values(pollTimers.current).forEach(clearInterval);
    pollTimers.current = {};
  };

  useEffect(() => {
    activeTokenRef.current = tokenId;
  }, [tokenId]);

  useEffect(() => {
    if (!tokenId) return;
    playgroundListCapabilities(tokenId).then(caps => {
      setCapabilities(caps);
      setSelectedCap(prev => prev && caps.some(cap => cap.code === prev) ? prev : (caps[0]?.code || ''));
    }).catch(() => setCapabilities([]));
  }, [tokenId]);

  useEffect(() => () => {
    clearAllPolling();
  }, []);

  const currentCap = capabilities.find(c => c.code === selectedCap);
  const currentSchemaEntries = useMemo(() => extractCapabilitySchema(currentCap), [currentCap]);
  const hasExplicitSchema = Boolean(currentCap?.standardParams && Object.keys(currentCap.standardParams).length > 0);
  const capabilityTypes = useMemo(() => {
    const types = new Set<string>(capabilities.map(cap => cap.type || 'other'));
    return CAPABILITY_TYPE_ORDER.filter(type => types.has(type)).concat(
      Array.from(types).filter(type => !CAPABILITY_TYPE_ORDER.includes(type as typeof CAPABILITY_TYPE_ORDER[number])).sort(),
    );
  }, [capabilities]);
  const filteredCapabilities = useMemo(() => {
    const keyword = capabilitySearch.trim().toLowerCase();
    return capabilities.filter(cap => {
      const matchesType = !capabilityTypeFilter || (cap.type || 'other') === capabilityTypeFilter;
      const matchesKeyword = !keyword || [cap.name, cap.code, cap.description].some(field => String(field || '').toLowerCase().includes(keyword));
      return matchesType && matchesKeyword;
    });
  }, [capabilities, capabilitySearch, capabilityTypeFilter]);
  const groupedCapabilities = useMemo(() => {
    const groups = new Map<string, PlaygroundCapability[]>();
    filteredCapabilities.forEach(cap => {
      const type = cap.type || 'other';
      const existing = groups.get(type) || [];
      existing.push(cap);
      groups.set(type, existing);
    });

    const orderedTypes = CAPABILITY_TYPE_ORDER.filter(type => groups.has(type)).concat(
      Array.from(groups.keys()).filter(type => !CAPABILITY_TYPE_ORDER.includes(type)).sort(),
    );

    return orderedTypes.map(type => ({
      type,
      items: (groups.get(type) || []).sort((a, b) => a.name.localeCompare(b.name)),
    }));
  }, [filteredCapabilities]);
  const capabilityFilteredTasks = useMemo(() => {
    if (taskFilter === 'current' && selectedCap) {
      return tasks.filter(task => task.capability === selectedCap);
    }
    return tasks;
  }, [tasks, taskFilter, selectedCap]);
  const filteredTasks = useMemo(() => {
    const keyword = taskSearch.trim().toLowerCase();
    if (!keyword) return capabilityFilteredTasks;
    return capabilityFilteredTasks.filter(task => {
      const promptPreview = getCapabilityPromptPreview(task).toLowerCase();
      const capabilityLabel = String(task.capabilityName || task.capability || '').toLowerCase();
      const taskNo = String(task.taskNo || '').toLowerCase();
      return taskNo.includes(keyword) || promptPreview.includes(keyword) || capabilityLabel.includes(keyword);
    });
  }, [capabilityFilteredTasks, taskSearch]);
  const selectedTask = useMemo(() => filteredTasks.find(task => task.taskNo === selectedTaskNo) || filteredTasks[0] || null, [filteredTasks, selectedTaskNo]);
  const requiredSchemaEntries = useMemo(() => currentSchemaEntries.filter(([, schema]) => schema.required), [currentSchemaEntries]);
  const completedRequiredCount = useMemo(
    () => requiredSchemaEntries.filter(([key]) => String(params[key] || '').trim()).length,
    [params, requiredSchemaEntries],
  );
  const missingRequiredFields = useMemo(
    () => requiredSchemaEntries.filter(([key]) => !String(params[key] || '').trim()).map(([, schema]) => schema.name),
    [requiredSchemaEntries, params],
  );
  const isSubmitDisabled = isSubmitting || !selectedCap || missingRequiredFields.length > 0;

  const hydrateTask = async (taskNo: string) => {
    const detail = await playgroundGetTask(tokenId, taskNo);
    setTasks(prev => mergeTask(prev, {
      taskNo: detail.taskNo,
      status: detail.status,
      progress: detail.progress || 0,
      result: detail.result,
      error: detail.error || '',
      cost: detail.cost || 0,
      rawParams: detail.rawParams,
      mappedParams: detail.mappedParams,
      vendorResponse: detail.vendorResponse,
      vendorTaskId: detail.vendorTaskId,
      createdAt: detail.createdAt,
      startedAt: detail.startedAt,
      completedAt: detail.completedAt,
    }));
    return detail;
  };

  const startPolling = (taskNo: string) => {
    if (pollTimers.current[taskNo]) return;
    pollTimers.current[taskNo] = setInterval(async () => {
      try {
        const detail = await hydrateTask(taskNo);
        if (['completed', 'success', 'failed', 'cancelled'].includes(detail.status)) {
          clearInterval(pollTimers.current[taskNo]);
          delete pollTimers.current[taskNo];
        }
      } catch {
        clearInterval(pollTimers.current[taskNo]);
        delete pollTimers.current[taskNo];
      }
    }, 3000);
  };

  const loadCapabilityHistory = async () => {
    if (!tokenId) return;
    const currentTokenId = tokenId;
    const data = await playgroundListTasks(currentTokenId, { page: 1, page_size: 20 });
    const historyTasks: TaskResult[] = data.items.map(item => ({
      taskNo: item.taskNo,
      status: item.status,
      progress: item.progress || 0,
      result: null,
      error: item.error || '',
      cost: item.cost || 0,
      capability: item.capability,
      capabilityName: item.capabilityName,
      capabilityType: capabilities.find(cap => cap.code === item.capability)?.type,
      channel: item.channel,
      refunded: item.refunded,
      createdAt: item.createdAt,
      completedAt: item.completedAt,
    }));
    setTasks(historyTasks);
    setSelectedTaskNo(prev => {
      if (prev && historyTasks.some(task => task.taskNo === prev)) return prev;
      return historyTasks[0]?.taskNo || '';
    });

    const detailTargets = historyTasks.slice(0, 5);
    await Promise.allSettled(detailTargets.map(task => playgroundGetTask(currentTokenId, task.taskNo).then(detail => {
      if (currentTokenId !== activeTokenRef.current) return;
      setTasks(prev => mergeTask(prev, {
        taskNo: detail.taskNo,
        status: detail.status,
        progress: detail.progress || 0,
        result: detail.result,
        error: detail.error || '',
        cost: detail.cost || 0,
        rawParams: detail.rawParams,
        mappedParams: detail.mappedParams,
        vendorResponse: detail.vendorResponse,
        vendorTaskId: detail.vendorTaskId,
        createdAt: detail.createdAt,
        startedAt: detail.startedAt,
        completedAt: detail.completedAt,
      }));
    })));

    historyTasks.forEach(task => {
      if (['pending', 'processing', 'running'].includes(task.status)) {
        startPolling(task.taskNo);
      }
    });
  };

  useEffect(() => {
    clearAllPolling();
    setTasks([]);
    setSelectedTaskNo('');
    setShowDebugDrawer(false);
    setHasTouchedParamPanel(false);
    setShowParamPanel(true);
    if (!tokenId) return;
    loadCapabilityHistory().catch(() => setTasks([]));
  }, [tokenId]);

  useEffect(() => {
    if (!selectedTaskNo || !tokenId) return;
    hydrateTask(selectedTaskNo)
      .then(detail => {
        if (['pending', 'processing', 'running'].includes(detail.status)) {
          startPolling(detail.taskNo);
        }
      })
      .catch(() => undefined);
  }, [selectedTaskNo, tokenId]);

  useEffect(() => {
    if (!showCapabilityPicker) return;

    const handlePointerDownOutside = (event: MouseEvent) => {
      if (!capabilityPickerRef.current?.contains(event.target as Node)) {
        setShowCapabilityPicker(false);
      }
    };

    document.addEventListener('mousedown', handlePointerDownOutside);
    return () => document.removeEventListener('mousedown', handlePointerDownOutside);
  }, [showCapabilityPicker]);

  useEffect(() => {
    setTaskFilter('current');
    setShowAdvancedParams(false);
    setShowCapabilityPicker(false);
    setCapabilitySearch('');
    setCapabilityTypeFilter('');
    if (currentCap?.type) {
      setExpandedCapabilityGroups(new Set([currentCap.type]));
    }
    setParams(prev => {
      const next: Record<string, string> = {};
      if (prev.channel) {
        next.channel = prev.channel;
      }
      currentSchemaEntries.forEach(([key]) => {
        if (typeof prev[key] === 'string') {
          next[key] = prev[key];
        }
      });
      if (!currentSchemaEntries.some(([key]) => key === 'prompt') && !next.prompt) {
        next.prompt = '';
      }
      return next;
    });
  }, [selectedCap, currentSchemaEntries, currentCap?.type]);

  useEffect(() => {
    if (hasTouchedParamPanel) return;
    setShowParamPanel(!(tasks.length > 0 || !!selectedTaskNo));
  }, [tasks.length, selectedTaskNo, hasTouchedParamPanel, selectedCap]);

  const resetCapabilityFilters = () => {
    setCapabilitySearch('');
    setCapabilityTypeFilter('');
    if (currentCap?.type) {
      setExpandedCapabilityGroups(new Set([currentCap.type]));
    } else {
      setExpandedCapabilityGroups(new Set());
    }
  };

  const toggleCapabilityGroup = (type: string) => {
    setExpandedCapabilityGroups(prev => {
      const next = new Set(prev);
      if (next.has(type)) {
        next.delete(type);
      } else {
        next.add(type);
      }
      return next;
    });
  };

  const handleSelectCapability = (capabilityCode: string) => {
    setSelectedCap(capabilityCode);
    setShowCapabilityPicker(false);
  };

  const handleSubmit = async () => {
    if (!selectedCap || isSubmitting) return;
    // 检查是否有正在上传的附件
    const anyUploading = (Object.values(capAttachments) as Attachment[][]).some(list => list.some(a => a.uploading));
    if (anyUploading) {
      setError('请等待文件上传完成');
      return;
    }
    setError('');
    setIsSubmitting(true);
    try {
      const requestParams = Object.entries(params).reduce<Record<string, any>>((acc, [key, value]) => {
        if (key === 'channel') {
          if (value) acc[key] = value;
          return acc;
        }

        const schema = currentCap?.standardParams?.[key] || FALLBACK_STANDARD_PARAMS[key];
        const stringValue = String(value || '');
        if (!schema) {
          const trimmed = stringValue.trim();
          if (trimmed) acc[key] = trimmed;
          return acc;
        }

        const normalized = normalizeCapabilityValue(schema, stringValue);
        if (normalized !== undefined) {
          acc[key] = normalized;
        }
        return acc;
      }, {});

      // 将附件 URL 合并到请求参数
      for (const [key, list] of Object.entries(capAttachments) as [string, Attachment[]][]) {
        const urls = list.filter(a => a.uploaded && a.url).map(a => a.url!);
        if (urls.length === 0) continue;
        const cap = capabilities.find(c => c.code === selectedCap);
        const schema = cap?.standardParams?.[key] || FALLBACK_STANDARD_PARAMS[key];
        if (schema?.type === 'array' || key === 'image_urls') {
          const existing = Array.isArray(requestParams[key]) ? requestParams[key] as string[] : [];
          requestParams[key] = [...existing, ...urls];
        } else {
          requestParams[key] = urls[urls.length - 1];
        }
      }
      const res = await playgroundInvokeCapability(tokenId, selectedCap, requestParams);
      const taskNo = res.data?.task_id || res.data?.task_no || res.task_id || '';
      if (taskNo) {
        const newTask: TaskResult = {
          taskNo,
          status: 'processing',
          progress: 0,
          result: null,
          error: '',
          cost: 0,
          capability: selectedCap,
          capabilityName: currentCap?.name,
          capabilityType: currentCap?.type,
          params: requestParams,
          createdAt: new Date().toISOString(),
        };
        setTasks(prev => mergeTask(prev, newTask));
        setSelectedTaskNo(taskNo);
        startPolling(taskNo);
      } else {
        const syncTaskNo = `sync-${Date.now()}`;
        setTasks(prev => mergeTask(prev, {
          taskNo: syncTaskNo,
          status: 'completed',
          progress: 100,
          result: res.data || res,
          error: '',
          cost: 0,
          capability: selectedCap,
          capabilityName: currentCap?.name,
          capabilityType: currentCap?.type,
          params: requestParams,
          createdAt: new Date().toISOString(),
        }));
        setSelectedTaskNo(syncTaskNo);
      }
      // 清理附件预览
      (Object.values(capAttachments) as Attachment[][]).flat().forEach(a => { if (a.preview) URL.revokeObjectURL(a.preview); });
      setCapAttachments({});
    } catch (err: any) {
      setError(err.message || '调用失败');
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div className="relative h-[calc(100vh-220px)] overflow-hidden">
      <input
        ref={capFileInputRef}
        type="file"
        accept={ACCEPTED_FILE_TYPES}
        className="hidden"
        onChange={e => {
          const file = e.target.files?.[0];
          if (file && capUploadingField) {
            handleFieldUpload(capUploadingField, file);
          }
          e.target.value = '';
          setCapUploadingField(null);
        }}
      />
      {showDebugDrawer && (
        <>
          <div className="absolute inset-0 z-20 bg-black/20 xl:hidden" onClick={() => setShowDebugDrawer(false)} />
          <div className="absolute inset-y-0 right-0 z-30 w-full max-w-[28rem] p-2 xl:hidden">
            <div className="relative h-full">
              <button
                type="button"
                onClick={() => setShowDebugDrawer(false)}
                className="absolute right-3 top-3 z-10 px-2 py-1 rounded-md bg-white/90 border border-gray-200 text-xs text-gray-500 hover:text-gray-700"
              >
                关闭
              </button>
              <CapabilityDebugPanel task={selectedTask} />
            </div>
          </div>
        </>
      )}

      <div className="h-full flex gap-4">
        <div className="flex-1 flex flex-col bg-white rounded-xl border border-gray-200 overflow-hidden min-w-0">
          <div className="px-4 py-2 border-b border-gray-100 flex items-center justify-between gap-3">
            <div className="flex items-center gap-2 text-sm font-medium text-gray-700 min-w-0">
              <Zap size={15} /> 能力任务
              <StatusBadge status={selectedTask ? getCapabilityTaskStatus(selectedTask.status) : 'pending'} />
            </div>
            <div className="flex items-center gap-2 flex-wrap justify-end">
              <span className="text-[11px] text-gray-500">{filteredTasks.length > 0 ? `筛选后 ${filteredTasks.length} 条` : '提交任务后会显示在这里'}</span>
              <div className="flex items-center gap-1 bg-gray-100 rounded-xl p-1">
                <button
                  type="button"
                  onClick={() => setTaskFilter('all')}
                  className={`px-3 py-1 rounded-lg text-[11px] font-medium transition-all ${taskFilter === 'all' ? 'bg-white text-indigo-700 shadow-sm' : 'text-gray-500 hover:text-gray-700'}`}
                >
                  全部
                </button>
                <button
                  type="button"
                  onClick={() => setTaskFilter('current')}
                  className={`px-3 py-1 rounded-lg text-[11px] font-medium transition-all ${taskFilter === 'current' ? 'bg-white text-indigo-700 shadow-sm' : 'text-gray-500 hover:text-gray-700'}`}
                >
                  当前能力
                </button>
              </div>
              <button
                type="button"
                onClick={() => setShowDebugDrawer(true)}
                className="inline-flex items-center gap-1.5 px-2 py-1 rounded-lg border border-gray-200 text-[11px] text-gray-600 hover:bg-gray-50 xl:hidden"
                disabled={!selectedTask}
              >
                <Bug size={12} /> 详情
              </button>
            </div>
          </div>

          <div ref={capabilityPickerRef} className="px-4 py-1.5 border-b border-gray-100 space-y-3">
            <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1.2fr)_220px_minmax(0,1fr)] gap-3 items-start">
              <div >
                <label className="block text-[11px] font-medium text-gray-500 mb-1">能力</label>
                <button
                  type="button"
                  onClick={() => {
                    if (!showCapabilityPicker && currentCap?.type) {
                      setExpandedCapabilityGroups(prev => {
                        const next = new Set(prev);
                        next.add(currentCap.type);
                        return next;
                      });
                    }
                    setShowCapabilityPicker(prev => !prev);
                  }}
                  className="w-full h-10 rounded-xl border border-gray-200 bg-white px-3 text-left hover:border-indigo-200 hover:bg-indigo-50/30 transition-colors"
                >
                  <div className="flex h-full items-center justify-between gap-3">
                    <div className="min-w-0 flex-1 overflow-hidden">
                      <div className="flex items-center gap-2 min-w-0 whitespace-nowrap overflow-hidden">
                        <span className="min-w-0 overflow-hidden whitespace-nowrap text-clip text-sm font-medium text-gray-900">{currentCap?.name || '请选择能力'}</span>
                        {currentCap?.code ? <code className="shrink-0 text-[11px] px-2 py-0.5 rounded bg-gray-100 text-gray-600">{currentCap.code}</code> : null}
                        {currentCap?.type ? <span className={`shrink-0 text-[11px] px-2 py-0.5 rounded ${getCapabilityTypeBadgeClass(currentCap.type)}`}>{currentCap.type}</span> : null}
                      </div>
                    </div>
                    <div className="text-gray-400 flex-shrink-0">
                      {showCapabilityPicker ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                    </div>
                  </div>
                </button>
              </div>
              <div>
                <label className="block text-[11px] font-medium text-gray-500 mb-1">渠道</label>
                <select value={params.channel || ''} onChange={e => setParams(prev => ({ ...prev, channel: e.target.value }))} className="w-full h-10 px-3 border border-gray-200 rounded-lg text-sm bg-white focus:outline-none focus:ring-2 focus:ring-indigo-500">
                  <option value="">自动选择</option>
                  {currentCap?.channels?.map((ch: any, idx: number) => <option key={idx} value={ch.channel_type}>{ch.channel_name} ({ch.model})</option>)}
                </select>
              </div>
              <div>
                <label className="block text-[11px] font-medium text-gray-500 mb-1">搜索任务</label>
                <div className="relative">
                  <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
                  <input
                    type="text"
                    value={taskSearch}
                    onChange={e => setTaskSearch(e.target.value)}
                    placeholder="搜索 taskNo / prompt / 能力名"
                    className="w-full h-10 pl-9 pr-3 border border-gray-200 rounded-lg text-sm bg-white focus:outline-none focus:ring-2 focus:ring-indigo-500"
                  />
                </div>
              </div>
            </div>

            {showCapabilityPicker && (
              <div  className="rounded-2xl border border-gray-200 bg-gray-50 p-3 space-y-3">
                <div className="flex flex-col lg:flex-row gap-3">
                  <div className="relative flex-1">
                    <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
                    <input
                      type="text"
                      value={capabilitySearch}
                      onChange={e => setCapabilitySearch(e.target.value)}
                      placeholder="搜索能力名 / code / 描述"
                      className="w-full h-10 pl-9 pr-3 border border-gray-200 rounded-lg text-sm bg-white focus:outline-none focus:ring-2 focus:ring-indigo-500"
                    />
                  </div>
                  <div className="lg:w-48">
                    <select
                      value={capabilityTypeFilter}
                      onChange={e => {
                        const nextType = e.target.value;
                        setCapabilityTypeFilter(nextType);
                        if (nextType) {
                          setExpandedCapabilityGroups(prev => new Set(prev).add(nextType));
                        }
                      }}
                      className="w-full h-10 px-3 border border-gray-200 rounded-lg text-sm bg-white focus:outline-none focus:ring-2 focus:ring-indigo-500"
                    >
                      <option value="">全部类型</option>
                      {capabilityTypes.map(type => <option key={type} value={type}>{type}</option>)}
                    </select>
                  </div>
                  <button
                    type="button"
                    onClick={resetCapabilityFilters}
                    className="h-10 px-4 border border-gray-200 rounded-lg text-sm text-gray-700 bg-white hover:bg-gray-100 transition-colors"
                  >
                    重置筛选
                  </button>
                </div>

                {capabilities.length === 0 ? (
                  <div className="rounded-xl border border-dashed border-gray-200 bg-white px-4 py-8 text-center text-sm text-gray-500">
                    当前 token 下暂无可用能力
                  </div>
                ) : groupedCapabilities.length === 0 ? (
                  <div className="rounded-xl border border-dashed border-gray-200 bg-white px-4 py-8 text-center text-sm text-gray-500">
                    无匹配能力，试试调整搜索词或类型筛选
                  </div>
                ) : (
                  <div className="space-y-3 max-h-80 overflow-y-auto pr-1">
                    {groupedCapabilities.map(group => {
                      const expanded = expandedCapabilityGroups.has(group.type);
                      return (
                        <div key={group.type} className="rounded-2xl border border-gray-200 bg-white overflow-hidden">
                          <button
                            type="button"
                            onClick={() => toggleCapabilityGroup(group.type)}
                            className="w-full px-4 py-3 flex items-center justify-between gap-3 hover:bg-gray-50 transition-colors"
                          >
                            <div className="min-w-0 text-left">
                              <div className="flex items-center gap-2 flex-wrap">
                                <span className={`text-sm font-medium uppercase px-2 py-0.5 rounded ${getCapabilityTypeBadgeClass(group.type)}`}>{group.type}</span>
                                <span className="text-[11px] px-2 py-0.5 rounded bg-gray-100 text-gray-500">{group.items.length} 个能力</span>
                              </div>
                              <div className="text-xs text-gray-500 mt-1">按类型浏览并快速切换能力</div>
                            </div>
                            <div className="text-gray-400 flex-shrink-0">
                              {expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                            </div>
                          </button>

                          {expanded && (
                            <div className="border-t border-gray-100 bg-gray-50 p-3 space-y-2">
                              {group.items.map(cap => {
                                const isSelected = cap.code === selectedCap;
                                return (
                                  <button
                                    key={cap.code}
                                    type="button"
                                    onClick={() => handleSelectCapability(cap.code)}
                                    className={`w-full rounded-xl border px-3 py-3 text-left transition-colors ${isSelected ? 'border-indigo-200 bg-indigo-50' : 'border-gray-200 bg-white hover:border-gray-300 hover:bg-gray-50'}`}
                                  >
                                    <div className="flex items-start justify-between gap-3">
                                      <div className="min-w-0 space-y-1.5">
                                        <div className="flex items-center gap-2 flex-wrap">
                                          <span className="text-sm font-medium text-gray-900">{cap.name}</span>
                                          <code className="text-[11px] px-2 py-0.5 rounded bg-gray-100 text-gray-600">{cap.code}</code>
                                          <span className={`text-[11px] px-2 py-0.5 rounded ${getCapabilityTypeBadgeClass(cap.type)}`}>{cap.type}</span>
                                          {isSelected ? <span className="text-[11px] px-2 py-0.5 rounded bg-emerald-100 text-emerald-700">当前</span> : null}
                                        </div>
                                        <div className="text-xs text-gray-500 line-clamp-2">{cap.description || '暂无能力描述'}</div>
                                      </div>
                                      <div className="text-[11px] text-gray-500 rounded-lg bg-gray-100 px-2.5 py-1 flex-shrink-0">
                                        渠道 {cap.channels?.length || 0}
                                      </div>
                                    </div>
                                  </button>
                                );
                              })}
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            )}
          </div>

          <div className="px-4 py-1 border-b border-gray-100 text-[11px] text-gray-500 flex items-center gap-3 flex-wrap">
            <span>{selectedTask ? `当前任务 #${selectedTask.taskNo}` : '等待提交任务'}</span>
            {selectedTask?.capabilityName || selectedTask?.capability ? <span>能力 {selectedTask.capabilityName || selectedTask.capability}</span> : null}
            {selectedTask ? <span>模型 {extractCapabilityModel(selectedTask)}</span> : null}
            {selectedTask ? <span>渠道 {selectedTask.channel || '自动选择'}</span> : null}
            {selectedTask ? <span>费用 ¥{Number(selectedTask.cost || 0).toFixed(4)}</span> : null}
          </div>

          <div className="flex-1 overflow-y-auto p-4 space-y-4 min-h-0">
            {filteredTasks.length === 0 ? (
              <div className="flex flex-col items-center justify-center h-full text-gray-300">
                <Zap size={56} strokeWidth={1} />
                <p className="mt-3 text-sm">{taskSearch.trim() ? '没有匹配到任务，试试换个关键词' : (taskFilter === 'current' ? '当前能力下暂无任务，试试切到“全部”查看历史任务' : '选择能力，在底部输入 Prompt 开始调用')}</p>
              </div>
            ) : (
              filteredTasks.map(task => {
                const promptPreview = getCapabilityPromptPreview(task);
                const resolvedModel = extractCapabilityModel(task);
                const taskStatus = getCapabilityTaskStatus(task.status);
                const isProcessing = ['pending', 'processing', 'running'].includes(task.status);
                const isSuccess = ['completed', 'success'].includes(task.status) && !!task.result;
                const isSelected = selectedTaskNo === task.taskNo;

                return (
                  <button
                    key={task.taskNo}
                    type="button"
                    onClick={() => {
                      setSelectedTaskNo(task.taskNo);
                      if (window.innerWidth < 1280) {
                        setShowDebugDrawer(true);
                      }
                    }}
                    className={`w-full text-left rounded-2xl border overflow-hidden transition-colors ${isSelected ? 'border-indigo-200 ring-2 ring-indigo-100' : 'border-gray-200 hover:border-gray-300'}`}
                  >
                    <div className="px-4 py-4 bg-gradient-to-b from-white to-gray-50 space-y-3">
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0 flex-1 space-y-3">
                          <div className="flex items-center gap-2 flex-wrap">
                            <StatusBadge status={taskStatus} />
                            <span className="text-sm font-medium text-gray-800">{task.capabilityName || task.capability || '能力任务'}</span>
                            <span className="text-xs text-gray-400">{task.taskNo}</span>
                          </div>

                          <div className="grid gap-2 text-xs text-gray-500 sm:grid-cols-2 xl:grid-cols-4">
                            <span>渠道：{task.channel || '自动选择'}</span>
                            <span>实际模型：{resolvedModel}</span>
                            <span>进度：{task.progress || 0}%</span>
                            <span>时间：{formatTime(task.createdAt)}</span>
                          </div>

                          <div className="rounded-xl bg-indigo-600 text-white px-4 py-3 text-sm whitespace-pre-wrap break-words">
                            {promptPreview}
                          </div>
                        </div>

                        <div className="flex items-center gap-2 text-gray-400 flex-shrink-0">
                          <UserIcon size={16} />
                          {isSelected ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                        </div>
                      </div>

                      <div>
                        {isProcessing ? (
                          <div className="rounded-xl bg-gray-50 border border-gray-200 p-4 space-y-3">
                            <div className="flex items-center gap-2 text-gray-600 text-sm">
                              <Loader2 size={16} className="animate-spin" />
                              <span>任务正在处理中，请稍候...</span>
                            </div>
                            <div className="w-full bg-white rounded-full h-1.5">
                              <div className="bg-indigo-500 h-1.5 rounded-full transition-all duration-500" style={{ width: `${Math.max(task.progress, 8)}%` }} />
                            </div>
                          </div>
                        ) : task.status === 'failed' ? (
                          <div className="rounded-xl bg-red-50 border border-red-200 p-4 text-sm text-red-600 whitespace-pre-wrap">{task.error || '任务失败'}</div>
                        ) : isSuccess ? (
                          <div className="rounded-xl bg-emerald-50 border border-emerald-200 px-4 py-3 text-sm text-emerald-800">
                            {buildResultSummary(task.result)}
                          </div>
                        ) : (
                          <div className="rounded-xl bg-gray-50 border border-gray-200 p-4 text-sm text-gray-400">暂无结果</div>
                        )}
                      </div>

                      <div className="flex items-center gap-3 text-xs text-gray-500 flex-wrap">
                        <span>费用 ¥{Number(task.cost || 0).toFixed(4)}</span>
                        {task.vendorTaskId ? <span>vendor #{task.vendorTaskId}</span> : null}
                        {extractCapabilityPrompt(task) ? <span>提示词长度 {extractCapabilityPrompt(task).length}</span> : null}
                      </div>
                    </div>
                  </button>
                );
              })
            )}
          </div>

          {error && (
            <div className="mx-4 mb-2 px-3 py-2 bg-red-50 text-red-600 text-sm rounded-lg flex items-center gap-2">
              <AlertCircle size={14} /> {error}
            </div>
          )}

          <div className="border-t border-gray-100 bg-white">
            <div className="px-4 py-3 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
              <button
                type="button"
                onClick={() => {
                  setHasTouchedParamPanel(true);
                  setShowParamPanel(prev => !prev);
                }}
                className="flex-1 text-left rounded-2xl border border-gray-200 bg-gray-50 px-4 py-3 hover:bg-gray-100 transition-colors"
              >
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0 space-y-1.5">
                    <div className="flex items-center gap-2 text-sm font-medium text-gray-700">
                      <SlidersHorizontal size={15} />
                      <span>参数面板</span>
                      <span className="text-xs text-gray-400">{currentCap?.name || '未选择能力'}</span>
                    </div>
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-gray-500">
                      <span>字段 {currentSchemaEntries.length}</span>
                      <span>必填 {completedRequiredCount}/{requiredSchemaEntries.length || 0}</span>
                      {!hasExplicitSchema ? <span>fallback 模式</span> : null}
                      {missingRequiredFields.length > 0 ? <span>待补充：{missingRequiredFields.join('、')}</span> : <span>可直接提交</span>}
                    </div>
                  </div>
                  <div className="flex items-center gap-1 text-xs text-indigo-600 flex-shrink-0">
                    <span>{showParamPanel ? '收起' : '展开'}</span>
                    {showParamPanel ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                  </div>
                </div>
              </button>

              <button
                onClick={handleSubmit}
                disabled={isSubmitDisabled}
                className="px-4 py-3 bg-indigo-600 text-white rounded-xl hover:bg-indigo-700 disabled:opacity-40 disabled:cursor-not-allowed transition-colors h-[52px] flex items-center justify-center gap-2 min-w-[120px] md:self-stretch"
              >
                {isSubmitting ? <><Loader2 size={16} className="animate-spin" /> 提交中</> : <><Send size={18} /> 提交</>}
              </button>
            </div>

            {showParamPanel && (
              <div className="px-4 pb-4 space-y-3">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                  {currentSchemaEntries.map(([key, schema]) => {
                    const value = params[key] || '';
                    const commonClassName = 'w-full px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500';
                    const label = `${schema.name}${schema.required ? ' *' : ''}`;
                    const uploadable = isUploadableField(key);
                    const fieldAttachments = capAttachments[key] || [];

                    // 附件卡片预览区（可上传字段共用）
                    const attachmentCards = uploadable && fieldAttachments.length > 0 ? (
                      <div className="flex flex-wrap gap-2 mt-2">
                        {fieldAttachments.map(att => (
                          <div key={att.id} className="relative group">
                            {att.preview ? (
                              <div className="relative w-16 h-16 rounded-xl overflow-hidden border border-gray-200 bg-gray-50">
                                <img src={att.preview} alt="" className="w-full h-full object-cover" />
                                {att.uploading && (
                                  <div className="absolute inset-0 bg-black/40 flex items-center justify-center">
                                    <Loader2 size={16} className="animate-spin text-white" />
                                  </div>
                                )}
                                {att.error && (
                                  <div className="absolute inset-0 bg-red-500/60 flex items-center justify-center">
                                    <XCircle size={16} className="text-white" />
                                  </div>
                                )}
                                {att.uploaded && (
                                  <div className="absolute bottom-0.5 right-0.5 w-4 h-4 bg-emerald-500 rounded-full flex items-center justify-center">
                                    <CheckCircle2 size={10} className="text-white" />
                                  </div>
                                )}
                                <button
                                  onClick={() => removeCapAttachment(key, att.id)}
                                  className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-all shadow-sm"
                                >
                                  <X size={10} />
                                </button>
                              </div>
                            ) : (
                              <div className="relative flex items-center gap-2 pl-2 pr-6 py-1.5 rounded-xl border border-gray-200 bg-gray-50 max-w-[180px]">
                                <div className="w-7 h-7 rounded-lg bg-indigo-50 flex items-center justify-center flex-shrink-0 text-sm">
                                  {getFileIcon(att.contentType)}
                                </div>
                                <div className="min-w-0 flex-1">
                                  <div className="text-[11px] font-medium text-gray-700 truncate">{att.file.name}</div>
                                  <div className="text-[10px] text-gray-400 flex items-center gap-1">
                                    {att.uploading && <><Loader2 size={8} className="animate-spin text-indigo-500" /><span className="text-indigo-500">上传中</span></>}
                                    {att.uploaded && <><CheckCircle2 size={8} className="text-emerald-500" /><span>{formatFileSize(att.file.size)}</span></>}
                                    {att.error && <><XCircle size={8} className="text-red-500" /><span className="text-red-500 truncate">{att.error}</span></>}
                                  </div>
                                </div>
                                <button
                                  onClick={() => removeCapAttachment(key, att.id)}
                                  className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-all shadow-sm"
                                >
                                  <X size={10} />
                                </button>
                              </div>
                            )}
                          </div>
                        ))}
                      </div>
                    ) : null;

                    // 上传按钮
                    const uploadArea = uploadable ? (
                      <button
                        type="button"
                        onClick={() => triggerFieldUpload(key)}
                        disabled={isSubmitting}
                        className="mt-2 w-full flex items-center justify-center gap-2 px-3 py-2 border-2 border-dashed border-gray-200 rounded-xl text-xs text-gray-400 hover:text-indigo-600 hover:border-indigo-300 hover:bg-indigo-50/50 transition-all disabled:opacity-40"
                      >
                        <Upload size={14} />
                        <span>点击上传文件</span>
                      </button>
                    ) : null;

                    if (schema.type === 'enum') {
                      return (
                        <div key={key}>
                          <label className="block text-[11px] font-medium text-gray-500 mb-1">{label}</label>
                          <select
                            value={value}
                            onChange={e => setParams(prev => ({ ...prev, [key]: e.target.value }))}
                            className={commonClassName}
                            disabled={isSubmitting}
                          >
                            <option value="">请选择</option>
                            {(schema.enumValues || []).map(option => (
                              <option key={option} value={option}>{option}</option>
                            ))}
                          </select>
                        </div>
                      );
                    }

                    if (schema.type === 'array' && uploadable) {
                      return (
                        <div key={key} className="md:col-span-2">
                          <label className="block text-[11px] font-medium text-gray-500 mb-1">{label}</label>
                          {attachmentCards}
                          {uploadArea}
                        </div>
                      );
                    }

                    if (schema.type === 'array') {
                      return (
                        <div key={key} className="md:col-span-2">
                          <label className="block text-[11px] font-medium text-gray-500 mb-1">{label}</label>
                          <textarea
                            value={value}
                            onChange={e => setParams(prev => ({ ...prev, [key]: e.target.value }))}
                            placeholder="每行一个值"
                            rows={3}
                            className={`${commonClassName} resize-none`}
                            disabled={isSubmitting}
                          />
                        </div>
                      );
                    }

                    if (schema.type === 'string' && LONG_TEXT_FIELDS.has(key)) {
                      return (
                        <div key={key} className="md:col-span-2">
                          <label className="block text-[11px] font-medium text-gray-500 mb-1">{label}</label>
                          <textarea
                            value={value}
                            onChange={e => setParams(prev => ({ ...prev, [key]: e.target.value }))}
                            placeholder={`请输入${schema.name}`}
                            rows={key === 'prompt' ? 4 : 3}
                            className={`${commonClassName} resize-none`}
                            disabled={isSubmitting}
                          />
                        </div>
                      );
                    }

                    if (uploadable) {
                      return (
                        <div key={key} className="md:col-span-2">
                          <label className="block text-[11px] font-medium text-gray-500 mb-1">{label}</label>
                          {attachmentCards}
                          {fieldAttachments.length === 0 && uploadArea}
                        </div>
                      );
                    }

                    return (
                      <div key={key}>
                        <label className="block text-[11px] font-medium text-gray-500 mb-1">{label}</label>
                        <input
                          type={schema.type === 'number' ? 'number' : 'text'}
                          value={value}
                          onChange={e => setParams(prev => ({ ...prev, [key]: e.target.value }))}
                          placeholder={`请输入${schema.name}`}
                          className={commonClassName}
                          disabled={isSubmitting}
                        />
                      </div>
                    );
                  })}
                </div>

                {!hasExplicitSchema && (
                  <div className="rounded-lg border border-dashed border-gray-200 bg-gray-50 px-3 py-2 text-xs text-gray-500">
                    当前能力尚未配置 standard_params，已回退为基础 prompt 输入。
                    <button
                      type="button"
                      onClick={() => setShowAdvancedParams(prev => !prev)}
                      className="ml-2 text-indigo-600 hover:text-indigo-700"
                    >
                      {showAdvancedParams ? '收起高级参数' : '展开高级参数'}
                    </button>
                  </div>
                )}

                {!hasExplicitSchema && showAdvancedParams && (
                  <div>
                    <label className="block text-[11px] font-medium text-gray-500 mb-1">高级 image_urls</label>
                    {(capAttachments['image_urls'] || []).length > 0 && (
                      <div className="flex flex-wrap gap-2 mb-2">
                        {(capAttachments['image_urls'] || []).map(att => (
                          <div key={att.id} className="relative group">
                            {att.preview ? (
                              <div className="relative w-16 h-16 rounded-xl overflow-hidden border border-gray-200 bg-gray-50">
                                <img src={att.preview} alt="" className="w-full h-full object-cover" />
                                {att.uploading && (
                                  <div className="absolute inset-0 bg-black/40 flex items-center justify-center">
                                    <Loader2 size={16} className="animate-spin text-white" />
                                  </div>
                                )}
                                {att.uploaded && (
                                  <div className="absolute bottom-0.5 right-0.5 w-4 h-4 bg-emerald-500 rounded-full flex items-center justify-center">
                                    <CheckCircle2 size={10} className="text-white" />
                                  </div>
                                )}
                                <button
                                  onClick={() => removeCapAttachment('image_urls', att.id)}
                                  className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-all shadow-sm"
                                >
                                  <X size={10} />
                                </button>
                              </div>
                            ) : (
                              <div className="relative flex items-center gap-2 pl-2 pr-6 py-1.5 rounded-xl border border-gray-200 bg-gray-50 max-w-[180px]">
                                <div className="w-7 h-7 rounded-lg bg-indigo-50 flex items-center justify-center flex-shrink-0 text-sm">
                                  {getFileIcon(att.contentType)}
                                </div>
                                <div className="min-w-0 flex-1">
                                  <div className="text-[11px] font-medium text-gray-700 truncate">{att.file.name}</div>
                                  <div className="text-[10px] text-gray-400 flex items-center gap-1">
                                    {att.uploading && <><Loader2 size={8} className="animate-spin text-indigo-500" /><span className="text-indigo-500">上传中</span></>}
                                    {att.uploaded && <><CheckCircle2 size={8} className="text-emerald-500" /><span>{formatFileSize(att.file.size)}</span></>}
                                  </div>
                                </div>
                                <button
                                  onClick={() => removeCapAttachment('image_urls', att.id)}
                                  className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-all shadow-sm"
                                >
                                  <X size={10} />
                                </button>
                              </div>
                            )}
                          </div>
                        ))}
                      </div>
                    )}
                    <button
                      type="button"
                      onClick={() => triggerFieldUpload('image_urls')}
                      disabled={isSubmitting}
                      className="w-full flex items-center justify-center gap-2 px-3 py-2 border-2 border-dashed border-gray-200 rounded-xl text-xs text-gray-400 hover:text-indigo-600 hover:border-indigo-300 hover:bg-indigo-50/50 transition-all disabled:opacity-40"
                    >
                      <Upload size={14} />
                      <span>点击上传图片</span>
                    </button>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>

        <div className="hidden xl:flex xl:w-[24rem] xl:flex-shrink-0 xl:self-stretch min-w-0">
          <CapabilityDebugPanel task={selectedTask} />
        </div>
      </div>
    </div>
  );
};

type TabType = 'chat' | 'capability';

const Playground: React.FC = () => {
  const [activeTab, setActiveTab] = useState<TabType>('chat');
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [selectedTokenId, setSelectedTokenId] = useState('');
  const [isLoadingTokens, setIsLoadingTokens] = useState(true);

  useEffect(() => {
    setIsLoadingTokens(true);
    fetchTokens()
      .then(list => {
        const active = list.filter(t => t.status === 'active');
        setTokens(active);
        if (active.length > 0 && !selectedTokenId) {
          setSelectedTokenId(active[0].id);
        }
      })
      .catch(() => setTokens([]))
      .finally(() => setIsLoadingTokens(false));
  }, []);

  const tabs = [
    { key: 'chat' as TabType, label: 'Chat 调试', icon: <Bot size={16} /> },
    { key: 'capability' as TabType, label: '能力调用', icon: <Zap size={16} /> },
  ];

  return (
    <div className="h-[calc(100vh-8rem)] flex flex-col">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-1 bg-gray-100 rounded-xl p-1">
          {tabs.map(tab => (
            <button
              key={tab.key}
              onClick={() => setActiveTab(tab.key)}
              className={`flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium transition-all ${activeTab === tab.key ? 'bg-white text-indigo-700 shadow-sm' : 'text-gray-500 hover:text-gray-700'}`}
            >
              {tab.icon} {tab.label}
            </button>
          ))}
        </div>

        <div className="flex items-center gap-3">
          <label className="text-sm text-gray-500">令牌：</label>
          {isLoadingTokens ? (
            <div className="flex items-center gap-2 text-sm text-gray-400"><Loader2 size={14} className="animate-spin" /> 加载中...</div>
          ) : tokens.length === 0 ? (
            <span className="text-sm text-red-500">暂无可用令牌，请先创建</span>
          ) : (
            <select value={selectedTokenId} onChange={e => setSelectedTokenId(e.target.value)} className="px-3 py-1.5 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500">
              {tokens.map(t => <option key={t.id} value={t.id}>{t.name} (余额: ¥{t.balance.toFixed(2)})</option>)}
            </select>
          )}
        </div>
      </div>

      <div className="flex-1 min-h-0">
        {!selectedTokenId ? (
          <div className="h-full flex items-center justify-center text-gray-400">
            <div className="text-center">
              <Play size={48} className="mx-auto mb-3 opacity-30" />
              <p>请先选择一个令牌开始试用</p>
            </div>
          </div>
        ) : activeTab === 'chat' ? (
          <ChatTab tokenId={selectedTokenId} />
        ) : (
          <CapabilityTab tokenId={selectedTokenId} />
        )}
      </div>
    </div>
  );
};

export default Playground;
