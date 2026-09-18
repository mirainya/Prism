import React, { useState, useEffect, useRef, useCallback } from 'react';
import { Book, Copy, Check, ChevronDown, ChevronRight, Play, Loader2, MessageSquare, AlertTriangle, Video, Braces, FileUp, Image as ImageIcon, Boxes } from 'lucide-react';
import { fetchDocsModels, fetchDocsVideos, DocsModel, DocsVideosResponse, DocsVideoModelOptions } from '../services/docsApi';
import { TryItDrawer } from './TryItDrawer';

// ===== 代码块组件 =====
const CodeBlock: React.FC<{ code: string; title?: string }> = ({ code, title }) => {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard.writeText(code);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };
  return (
    <div className="relative group">
      {title && <div className="text-xs text-[var(--text-secondary)] mb-1">{title}</div>}
      <pre className="bg-gray-900 text-gray-100 rounded-lg p-4 text-xs overflow-x-auto">
        <code>{code}</code>
      </pre>
      <button onClick={copy} className="absolute top-2 right-2 p-1.5 rounded bg-gray-700 hover:bg-gray-600 text-gray-300 opacity-0 group-hover:opacity-100 transition-opacity">
        {copied ? <Check size={12} /> : <Copy size={12} />}
      </button>
    </div>
  );
};

// ===== 参数表组件 =====
const ParamTable: React.FC<{ params: { name: string; type: string; required: boolean; description: string }[] }> = ({ params }) => (
  <div className="overflow-x-auto -mx-2 px-2">
  <table className="w-full text-sm min-w-[480px]">
    <thead>
      <tr className="bg-[var(--primary-lighter)]">
        <th className="px-3 py-2 text-left font-medium text-[var(--text-secondary)]">参数</th>
        <th className="px-3 py-2 text-left font-medium text-[var(--text-secondary)]">类型</th>
        <th className="px-3 py-2 text-left font-medium text-[var(--text-secondary)]">必填</th>
        <th className="px-3 py-2 text-left font-medium text-[var(--text-secondary)]">说明</th>
      </tr>
    </thead>
    <tbody>
      {params.map(p => (
        <tr key={p.name} className="border-t border-[var(--border-soft)]">
          <td className="px-3 py-2 font-mono text-[var(--primary)] text-xs">{p.name}</td>
          <td className="px-3 py-2 text-[var(--text-secondary)] text-xs">{p.type}</td>
          <td className="px-3 py-2">{p.required ? <span className="text-red-500 text-xs">是</span> : <span className="text-[var(--text-secondary)] text-xs">否</span>}</td>
          <td className="px-3 py-2 text-[var(--text-secondary)] text-xs">{p.description}</td>
        </tr>
      ))}
    </tbody>
  </table>
  </div>
);

// ===== 方法标签 =====
const MethodBadge: React.FC<{ method: string }> = ({ method }) => {
  const colors: Record<string, string> = {
    GET: 'bg-green-100 text-green-700',
    POST: 'bg-blue-100 text-blue-700',
    PUT: 'bg-yellow-100 text-yellow-700',
    DELETE: 'bg-red-100 text-red-700',
  };
  return <span className={`px-2 py-0.5 rounded text-xs font-bold ${colors[method] || 'bg-gray-100 text-gray-700'}`}>{method}</span>;
};

// ===== 接口卡片 =====
interface ApiEndpoint {
  id: string;
  method: string;
  path: string;
  name: string;
  description: string;
  params: { name: string; type: string; required: boolean; description: string }[];
  channelParams?: { channelName: string; channelType: string; interactionMode?: string; params: { name: string; type: string; required: boolean; description: string }[] }[];
  videoModels?: { model: string; options: DocsVideoModelOptions; channelName?: string }[];
  requestExample?: string;
  responseExample?: string;
  bodyType?: 'json' | 'multipart';
}

const videoTaskLabels: Record<string, string> = {
  text: '文生视频',
  first_frame: '首帧',
  first_last_frame: '首尾帧',
  multimodal: '多模态',
  video_edit: '视频编辑',
  video_extension: '视频拓展',
};

const videoRoleLabels: Record<string, string> = {
  first_frame: '首帧',
  last_frame: '尾帧',
  reference_image: '参考图片',
  reference_video: '参考视频',
  reference_audio: '参考音频',
  edit_source: '编辑源视频',
  source_video: '拓展源视频',
};

const formatDurationConstraint = (options: DocsVideoModelOptions) => {
  if (options.duration_options?.length) return `${options.duration_options.join('、')} 秒`;
  if (options.duration_min || options.duration_max) return `${options.duration_min || 1}-${options.duration_max || '不限'} 秒`;
  return '未配置';
};

const formatMultimodalScope = (options: DocsVideoModelOptions) => {
  if (!options.task_types?.includes('multimodal')) return '不支持';
  const roles = options.allowed_roles || [];
  const media = [
    roles.some(role => ['first_frame', 'last_frame', 'reference_image'].includes(role))
      ? `图片${options.max_images ? `（最多 ${options.max_images} 张）` : ''}`
      : '',
    roles.some(role => ['reference_video', 'edit_source', 'source_video'].includes(role))
      ? `视频${options.max_videos ? `（最多 ${options.max_videos} 个）` : ''}`
      : '',
    roles.includes('reference_audio')
      ? `音频${options.max_audios ? `（最多 ${options.max_audios} 个）` : ''}`
      : '',
  ].filter(Boolean);
  if (media.length === 0) return '按素材角色配置';
  return `${media.join('、')}${options.max_media ? `；总计最多 ${options.max_media} 项` : ''}`;
};

const VideoModelCapabilities: React.FC<{ models: { model: string; options: DocsVideoModelOptions; channelName?: string }[] }> = ({ models }) => (
  <div className="space-y-2">
    <div className="flex items-center justify-between gap-3">
      <h4 className="text-xs font-bold text-[var(--text-secondary)]">模型能力</h4>
      <span className="text-[11px] text-[var(--text-tertiary)]">共 {models.length} 个模型</span>
    </div>
    <div className="space-y-2">
      {models.map(({ model, options, channelName }, index) => (
        <details key={`${channelName || 'channel'}:${model}:${index}`} className="group rounded-lg border border-[var(--border-soft)] bg-[var(--surface)]" open={index === 0}>
          <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2.5 text-sm font-semibold text-[var(--text-primary)] [&::-webkit-details-marker]:hidden">
            <ChevronRight size={14} className="shrink-0 transition-transform group-open:rotate-90" />
            <code className="min-w-0 flex-1 truncate text-xs text-[var(--primary)]">{model}</code>
            {channelName ? <span className="max-w-[14rem] truncate text-[11px] font-normal text-[var(--text-tertiary)]">{channelName}</span> : null}
            <span className="hidden shrink-0 text-[11px] font-normal text-[var(--text-tertiary)] sm:inline">{formatDurationConstraint(options)}</span>
          </summary>
          <div className="grid grid-cols-1 gap-3 border-t border-[var(--border-soft)] px-3 py-3 text-xs sm:grid-cols-2 lg:grid-cols-3">
            <div>
              <div className="mb-1 text-[var(--text-tertiary)]">任务类型</div>
              <div className="text-[var(--text-secondary)]">{options.task_types?.map(value => videoTaskLabels[value] || value).join('、') || '未配置'}</div>
            </div>
            <div>
              <div className="mb-1 text-[var(--text-tertiary)]">多模态范围</div>
              <div className="text-[var(--text-secondary)]">{formatMultimodalScope(options)}</div>
            </div>
            <div>
              <div className="mb-1 text-[var(--text-tertiary)]">时长</div>
              <div className="text-[var(--text-secondary)]">{formatDurationConstraint(options)}</div>
            </div>
            <div>
              <div className="mb-1 text-[var(--text-tertiary)]">分辨率</div>
              <div className="text-[var(--text-secondary)]">{options.resolutions?.join('、') || '未配置'}</div>
            </div>
            <div>
              <div className="mb-1 text-[var(--text-tertiary)]">画面比例</div>
              <div className="text-[var(--text-secondary)]">{options.ratios?.join('、') || '未配置'}</div>
            </div>
            <div>
              <div className="mb-1 text-[var(--text-tertiary)]">素材</div>
              <div className="text-[var(--text-secondary)]">
                {options.allowed_roles?.map(value => videoRoleLabels[value] || value).join('、') || '无'}
                {options.max_media ? `，最多 ${options.max_media} 项` : ''}
              </div>
            </div>
            <div>
              <div className="mb-1 text-[var(--text-tertiary)]">音频与档位</div>
              <div className="text-[var(--text-secondary)]">
                {options.allow_generated_audio === false ? '不支持生成音频' : '支持生成音频'}
                {options.service_tiers?.length ? `；${options.service_tiers.join('、')}` : ''}
              </div>
            </div>
            {options.parameters?.length ? (
              <div className="sm:col-span-2 lg:col-span-3">
                <div className="mb-1 text-[var(--text-tertiary)]">渠道参数</div>
                <div className="space-y-1 text-[var(--text-secondary)]">
                  {options.parameters.map(parameter => (
                    <div key={parameter.name} className="flex flex-wrap gap-x-2 gap-y-1">
                      <code className="text-[var(--primary)]">{parameter.name}</code>
                      <span>{parameter.label}</span>
                      {parameter.min !== undefined || parameter.max !== undefined ? <span>范围 {parameter.min ?? '-'}-{parameter.max ?? '不限'}</span> : null}
                      {parameter.options?.length ? <span>可选 {parameter.options.map(option => option.label || String(option.value)).join('、')}</span> : null}
                    </div>
                  ))}
                </div>
              </div>
            ) : null}
          </div>
        </details>
      ))}
    </div>
  </div>
);

// ===== 可复制路径 =====
const CopyablePath: React.FC<{ path: string }> = ({ path }) => {
  const [copied, setCopied] = useState(false);
  const copy = (e: React.MouseEvent) => {
    e.stopPropagation();
    navigator.clipboard.writeText(`${window.location.origin}${path}`);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };
  return (
    <span className="flex items-center gap-1 group/path">
      <code className="text-sm font-mono text-[var(--text-primary)]">{path}</code>
      <button onClick={copy} className="p-0.5 rounded text-[var(--text-secondary)] opacity-0 group-hover/path:opacity-100 hover:text-[var(--primary)] transition-opacity" title="复制完整 URL">
        {copied ? <Check size={11} /> : <Copy size={11} />}
      </button>
    </span>
  );
};

const EndpointCard: React.FC<{ api: ApiEndpoint; onTryIt: (api: ApiEndpoint) => void }> = ({ api, onTryIt }) => {
  const [expanded, setExpanded] = useState(false);

  const handleTryIt = (e: React.MouseEvent) => {
    e.stopPropagation();
    onTryIt(api);
  };

  return (
    <div id={api.id} className="border border-[var(--border-soft)] rounded-xl overflow-hidden bg-[var(--surface-card)]">
      <div className="flex items-start sm:items-center gap-2 sm:gap-3 px-4 py-3 cursor-pointer hover:bg-[var(--primary-lighter)] transition-colors flex-wrap" onClick={() => setExpanded(!expanded)}>
        <div className="flex items-center gap-2 sm:gap-3 min-w-0 flex-1">
          {expanded ? <ChevronDown size={14} className="flex-shrink-0" /> : <ChevronRight size={14} className="flex-shrink-0" />}
          <MethodBadge method={api.method} />
          <CopyablePath path={api.path} />
        </div>
        <div className="flex items-center gap-2 ml-auto">
          <span className="text-sm text-[var(--text-secondary)] hidden sm:inline">{api.name}</span>
          <button
            onClick={handleTryIt}
            className="flex items-center gap-1 px-2.5 py-1 rounded-lg text-xs font-medium border transition-colors shrink-0 border-[var(--primary)] text-[var(--primary)] hover:bg-[var(--primary-lighter)]"
          >
            <Play size={10} /> 试用
          </button>
        </div>
      </div>
      {expanded && (
        <div className="border-t border-[var(--border-soft)] p-4 space-y-4">
          <p className="text-sm text-[var(--text-secondary)]">{api.description}</p>
          {api.params.length > 0 && <ParamTable params={api.params} />}
          {api.videoModels && api.videoModels.length > 0 && <VideoModelCapabilities models={api.videoModels} />}
          {api.channelParams && api.channelParams.length > 0 && (
            <div className="space-y-3">
              <h4 className="text-xs font-bold text-[var(--text-secondary)]">渠道专属参数</h4>
              {api.channelParams.map(ch => {
                const modeLabel = ch.interactionMode === 'poll' ? '异步轮询' : ch.interactionMode === 'callback' ? '异步回调' : ch.interactionMode === 'sync' ? '同步' : '';
                return (
                  <div key={ch.channelName} className="border border-[var(--border-soft)] rounded-lg p-3">
                    <div className="text-xs font-medium text-[var(--text-primary)] mb-2">
                      {ch.channelName} <span className="text-[var(--text-tertiary)]">({ch.channelType})</span>
                      {modeLabel && <span className={`ml-2 px-1.5 py-0.5 rounded text-[10px] ${ch.interactionMode === 'sync' ? 'bg-green-100 text-green-700' : 'bg-amber-100 text-amber-700'}`}>{modeLabel}</span>}
                    </div>
                    <ParamTable params={ch.params} />
                  </div>
                );
              })}
            </div>
          )}
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            {api.requestExample && <CodeBlock code={api.requestExample} title="请求示例" />}
            {api.responseExample && <CodeBlock code={api.responseExample} title="响应示例" />}
          </div>
        </div>
      )}
    </div>
  );
};

// ===== 导航分组 =====
interface NavGroup {
  id: string;
  label: string;
  icon: React.ReactNode;
  items: { id: string; label: string }[];
}

// ===== Chat 接口参数定义 =====
// 示例里的模型名原来是写死的（gpt-4o / claude-sonnet-4 / doubao-seed-2-0-pro）——
// 这个部署里很可能一个都不存在，照着示例发请求必然 404。`GET /api/docs/models`
// 本来就返回当前生效目录里可执行的模型，只是前端一直没调。现在按下游协议挑一个
// 真名填进示例；取不到才退回占位符。
const modelDisplayName = (model: DocsModel) => model.code || model.model_code || model.id;

// transports 是该模型在目录里实际可用的上游 Transport，用它判断哪条下游协议示例
// 该拿哪个模型：走不了 anthropic_messages 的模型，放进 /v1/messages 示例只会误导。
export const pickDocsModel = (models: DocsModel[], transports: string[], fallback: string): string => {
  const matched = models.find(model => (model.transports || []).some(code => transports.includes(code)));
  return matched ? modelDisplayName(matched) : models[0] ? modelDisplayName(models[0]) : fallback;
};

const buildChatParams = (models: DocsModel[]) => {
  const names = models.slice(0, 3).map(modelDisplayName).filter(Boolean);
  return [
    { name: 'model', type: 'string', required: true, description: names.length ? `模型标识，当前可用：${names.join('、')}${models.length > names.length ? ' 等' : ''}` : '模型标识；当前目录里没有可用模型' },
    ...CHAT_COMMON_PARAMS,
  ];
};

const CHAT_COMMON_PARAMS = [
  { name: 'messages', type: 'array', required: true, description: '消息数组，每条包含 role(system/user/assistant/tool) 和 content' },
  { name: 'stream', type: 'boolean', required: false, description: '是否流式返回，默认 false' },
  { name: 'temperature', type: 'number', required: false, description: '温度 (0-2)，值越高越随机' },
  { name: 'max_tokens', type: 'integer', required: false, description: '最大输出 token 数' },
  { name: 'top_p', type: 'number', required: false, description: '核采样 (0-1)' },
  { name: 'frequency_penalty', type: 'number', required: false, description: '频率惩罚 (-2~2)' },
  { name: 'presence_penalty', type: 'number', required: false, description: '存在惩罚 (-2~2)' },
  { name: 'stop', type: 'string[]', required: false, description: '停止序列' },
  { name: 'tools', type: 'array', required: false, description: '工具定义数组，每个包含 type:"function" 和 function 对象' },
  { name: 'tool_choice', type: 'string|object', required: false, description: '"auto"/"none"/"required" 或 {type:"function",function:{name:"..."}}' },
  { name: 'response_format', type: 'object', required: false, description: '{type:"text"|"json_object"|"json_schema"}' },
  { name: 'seed', type: 'integer', required: false, description: '随机种子，相同 seed 尽量返回相同结果' },
  { name: 'user', type: 'string', required: false, description: '终端用户标识' },
  { name: 'conversation_id', type: 'integer|string', required: false, description: '续接当前 Token 所属的 Prism 对话；也可使用 X-Prism-Conversation-ID 请求头' },
];

const buildAnthropicEndpoints = (model: string): ApiEndpoint[] => [{
  id: 'ep-anthropic-messages',
  method: 'POST',
  path: '/v1/messages',
  name: '创建消息',
  description: '兼容 Anthropic Messages API。Prism 会按模型已配置的 Transport 原生调用或无损转换，支持文本、图片、文档、工具调用和 SSE，并自动保存成功、失败和中断的对话轮次。',
  params: [
    { name: 'model', type: 'string', required: true, description: '模型标识' },
    { name: 'max_tokens', type: 'integer', required: true, description: '最大输出 token 数' },
    { name: 'messages', type: 'array', required: true, description: 'Anthropic user/assistant 消息数组' },
    { name: 'system', type: 'string|array', required: false, description: '系统提示词或内容块数组' },
    { name: 'stream', type: 'boolean', required: false, description: '是否返回 Anthropic SSE 事件流' },
    { name: 'tools', type: 'array', required: false, description: '工具定义，使用 input_schema' },
    { name: 'tool_choice', type: 'object', required: false, description: 'auto、any、tool 或 none' },
    { name: 'temperature', type: 'number', required: false, description: '采样温度' },
    { name: 'top_p', type: 'number', required: false, description: '核采样参数' },
    { name: 'stop_sequences', type: 'string[]', required: false, description: '停止序列' },
    { name: 'metadata', type: 'object', required: false, description: '请求元数据' },
    { name: 'X-Prism-Conversation-ID (header)', type: 'integer', required: false, description: '续接当前 Token 所属的 Prism 对话' },
  ],
  requestExample: JSON.stringify({
    model,
    max_tokens: 1024,
    messages: [{ role: 'user', content: [{ type: 'text', text: '你好' }] }],
    stream: false,
  }, null, 2),
  responseExample: JSON.stringify({
    id: 'msg_abc123', type: 'message', role: 'assistant', model,
    content: [{ type: 'text', text: '你好！' }], stop_reason: 'end_turn',
    usage: { input_tokens: 8, output_tokens: 4 },
  }, null, 2),
}];

const RESPONSES_PARAMS = [
  { name: 'model', type: 'string', required: true, description: '模型标识' },
  { name: 'input', type: 'string|array', required: true, description: '文本或输入项数组，支持 input_text、input_image、input_audio、input_video、input_file 和工具结果' },
  { name: 'instructions', type: 'string', required: false, description: '系统指令' },
  { name: 'stream', type: 'boolean', required: false, description: '是否返回 Responses SSE 事件流' },
  { name: 'store', type: 'boolean', required: false, description: '是否保存响应，默认 true' },
  { name: 'background', type: 'boolean', required: false, description: '由 Prism 后台执行；不能与 stream 同时启用' },
  { name: 'previous_response_id', type: 'string', required: false, description: 'Prism 返回的上一条 resp_ ID' },
  { name: 'tools', type: 'array', required: false, description: '函数或上游托管工具；file_search 暂不支持' },
  { name: 'tool_choice', type: 'string|object', required: false, description: '工具选择策略' },
  { name: 'parallel_tool_calls', type: 'boolean', required: false, description: '是否允许并行工具调用' },
  { name: 'max_output_tokens', type: 'integer', required: false, description: '最大输出 token 数' },
  { name: 'max_tool_calls', type: 'integer', required: false, description: '最大托管工具调用次数' },
  { name: 'temperature', type: 'number', required: false, description: '采样温度' },
  { name: 'top_p', type: 'number', required: false, description: '核采样参数' },
  { name: 'top_logprobs', type: 'integer', required: false, description: '返回的候选 token 数，范围 0-20' },
  { name: 'reasoning', type: 'object', required: false, description: '推理配置' },
  { name: 'thinking', type: 'object', required: false, description: '火山方舟思考模式配置' },
  { name: 'caching', type: 'object', required: false, description: '火山方舟缓存配置' },
  { name: 'text', type: 'object', required: false, description: '文本输出及结构化输出配置' },
  { name: 'conversation', type: 'string|object', required: false, description: '上游会话配置' },
  { name: 'X-Prism-Conversation-ID (header)', type: 'integer', required: false, description: '续接当前 Token 所属的 Prism 对话；不改变原生 conversation 字段' },
  { name: 'prompt', type: 'object', required: false, description: '提示模板配置' },
  { name: 'stream_options', type: 'object', required: false, description: '流式输出配置' },
  { name: 'context_management', type: 'array', required: false, description: '上下文管理配置' },
  { name: 'metadata', type: 'object', required: false, description: '自定义元数据；火山 Responses v3 不支持' },
  { name: 'include', type: 'string[]', required: false, description: '附加输出字段' },
  { name: 'truncation', type: 'string', required: false, description: '上下文截断策略' },
  { name: 'service_tier', type: 'string', required: false, description: '服务等级' },
  { name: 'prompt_cache_key', type: 'string', required: false, description: '提示缓存键' },
  { name: 'prompt_cache_retention', type: 'string', required: false, description: '提示缓存保留策略' },
  { name: 'safety_identifier', type: 'string', required: false, description: '安全标识' },
  { name: 'user', type: 'string', required: false, description: '终端用户标识' },
  { name: 'expire_at', type: 'integer', required: false, description: '火山方舟响应过期时间（Unix 秒）' },
  { name: 'session', type: 'object', required: false, description: '火山方舟会话配置' },
];

const buildResponsesEndpoints = (model: string): ApiEndpoint[] => [
  {
    id: 'ep-responses-create',
    method: 'POST',
    path: '/v1/responses',
    name: '创建响应',
    description: 'OpenAI Responses 兼容入口，支持多模态、函数工具、流式、续话和后台执行，并自动保存成功、失败和中断的对话轮次。JSON 请求体上限 32 MiB，大文件应先上传到 /v1/files 并使用 file_id。Idempotency-Key 结果保留 24 小时；store=false 不保存 Responses 资源正文，但仍保存 Conversation 投影。火山 v3 专属字段及未来扩展会原样保留；无法无损转换的字段返回 400。',
    params: RESPONSES_PARAMS,
    requestExample: JSON.stringify({
      model,
      input: [{
        role: 'user',
        content: [
          { type: 'input_text', text: '描述这张图片' },
          { type: 'input_image', image_url: 'https://example.com/image.jpg' },
        ],
      }],
      stream: false,
      store: true,
    }, null, 2),
    responseExample: JSON.stringify({
      id: 'resp_abc123',
      object: 'response',
      status: 'completed',
      model,
      output: [{ type: 'message', role: 'assistant', content: [{ type: 'output_text', text: '图片描述...' }] }],
      usage: { input_tokens: 120, output_tokens: 32, total_tokens: 152 },
    }, null, 2),
  },
  {
    id: 'ep-responses-get',
    method: 'GET',
    path: '/v1/responses/{response_id}',
    name: '查询响应',
    description: '查询当前 Token 创建且已保存的响应。',
    params: [{ name: 'response_id', type: 'string', required: true, description: '响应 ID（路径参数）' }],
  },
  {
    id: 'ep-responses-delete',
    method: 'DELETE',
    path: '/v1/responses/{response_id}',
    name: '删除响应',
    description: '删除当前 Token 创建的响应记录。',
    params: [{ name: 'response_id', type: 'string', required: true, description: '响应 ID（路径参数）' }],
  },
  {
    id: 'ep-responses-cancel',
    method: 'POST',
    path: '/v1/responses/{response_id}/cancel',
    name: '取消后台响应',
    description: '取消 queued 或 in_progress 状态的后台响应。',
    params: [{ name: 'response_id', type: 'string', required: true, description: '响应 ID（路径参数）' }],
  },
  {
    id: 'ep-responses-input-items',
    method: 'GET',
    path: '/v1/responses/{response_id}/input_items',
    name: '输入项列表',
    description: '分页读取已保存响应的输入项。',
    params: [
      { name: 'response_id', type: 'string', required: true, description: '响应 ID（路径参数）' },
      { name: 'limit', type: 'integer', required: false, description: '返回数量，1-100，默认 20' },
      { name: 'order', type: 'string', required: false, description: 'asc 或 desc' },
      { name: 'after', type: 'string', required: false, description: '分页游标' },
    ],
  },
];

const FILE_ENDPOINTS: ApiEndpoint[] = [
  {
    id: 'ep-files-upload',
    method: 'POST',
    path: '/v1/files',
    name: '上传文件',
    description: '使用 multipart/form-data 上传文件。单文件上限 64 MiB，文件归当前 API Token 所有。',
    bodyType: 'multipart',
    params: [
      { name: 'file', type: 'file', required: true, description: '要上传的文件' },
      { name: 'purpose', type: 'string', required: false, description: 'assistants、batch、evals、fine-tune、user_data 或 vision；默认 assistants' },
    ],
    responseExample: JSON.stringify({ id: 'file_abc123', object: 'file', bytes: 1024, filename: 'image.png', purpose: 'vision', status: 'processed' }, null, 2),
  },
  {
    id: 'ep-files-list',
    method: 'GET',
    path: '/v1/files',
    name: '文件列表',
    description: '分页列出当前 Token 上传的文件。',
    params: [
      { name: 'purpose', type: 'string', required: false, description: '按用途筛选' },
      { name: 'limit', type: 'integer', required: false, description: '返回数量，1-10000' },
      { name: 'order', type: 'string', required: false, description: 'asc 或 desc' },
      { name: 'after', type: 'string', required: false, description: '分页游标' },
    ],
  },
  {
    id: 'ep-files-get',
    method: 'GET',
    path: '/v1/files/{file_id}',
    name: '文件信息',
    description: '获取当前 Token 所有的文件元数据。',
    params: [{ name: 'file_id', type: 'string', required: true, description: '文件 ID（路径参数）' }],
  },
  {
    id: 'ep-files-content',
    method: 'GET',
    path: '/v1/files/{file_id}/content',
    name: '下载文件',
    description: '下载当前 Token 所有的文件内容。',
    params: [{ name: 'file_id', type: 'string', required: true, description: '文件 ID（路径参数）' }],
  },
  {
    id: 'ep-files-delete',
    method: 'DELETE',
    path: '/v1/files/{file_id}',
    name: '删除文件',
    description: '删除当前 Token 所有的文件。',
    params: [{ name: 'file_id', type: 'string', required: true, description: '文件 ID（路径参数）' }],
  },
];

// 图像两个入口此前在文档里完全缺失，而它们是真实存在且带一堆约束的：
// n 上限 10、partial_images 必须配 stream=true、input_fidelity 必须有图输入、
// /v1/images/generations 带了 image_urls 会自动转成 images.edit 计费。
const buildImageEndpoints = (model: string): ApiEndpoint[] => {
  const shared = [
    { name: 'n', type: 'integer', required: false, description: '生成张数，1-10，默认 1' },
    { name: 'size', type: 'string', required: false, description: '输出尺寸，如 1024x1024；具体可用值以所选模型能力为准' },
    { name: 'aspect_ratio', type: 'string', required: false, description: '画面比例，与 size 二选一，取决于上游支持' },
    { name: 'quality', type: 'string', required: false, description: '画质档位，以所选模型能力为准' },
    { name: 'response_format', type: 'string', required: false, description: 'url 或 b64_json，默认 url' },
    { name: 'output_format', type: 'string', required: false, description: '输出文件格式，如 png、jpeg、webp' },
    { name: 'output_compression', type: 'integer', required: false, description: '压缩质量 0-100' },
    { name: 'moderation', type: 'string', required: false, description: '内容审核档位' },
    { name: 'style', type: 'string', required: false, description: '风格参数' },
    { name: 'background', type: 'string', required: false, description: '背景处理，如 transparent' },
    { name: 'stream', type: 'boolean', required: false, description: '是否以 SSE 流式返回（OpenAI 标准的 partial image 事件）' },
    { name: 'partial_images', type: 'integer', required: false, description: '流式中间帧数量，0-3，必须同时 stream=true' },
    { name: 'user', type: 'string', required: false, description: '终端用户标识' },
  ];
  return [
    {
      id: 'ep-images-generations',
      method: 'POST',
      path: '/v1/images/generations',
      name: '图像生成',
      description: '兼容 OpenAI Images API。异步上游由 Prism 自动轮询后同步返图，调用方无需自己查任务。带上 image_urls 时按图生图（images.edit）执行与计费。',
      params: [
        { name: 'model', type: 'string', required: true, description: '模型标识' },
        { name: 'prompt', type: 'string', required: true, description: '文本提示词' },
        { name: 'image_urls', type: 'string[]', required: false, description: '参考图 URL 或 data URI；给了就转为图生图' },
        { name: 'input_fidelity', type: 'string', required: false, description: '参考图保真度；没有图像输入时提交会 400' },
        ...shared,
      ],
      requestExample: JSON.stringify({ model, prompt: '赛博朋克风格的城市夜景', n: 1, size: '1024x1024', response_format: 'url' }, null, 2),
      responseExample: JSON.stringify({ created: 1704067200, data: [{ url: 'https://example.com/generated.png', revised_prompt: '...' }] }, null, 2),
    },
    {
      id: 'ep-images-edits',
      method: 'POST',
      path: '/v1/images/edits',
      name: '图像编辑',
      description: '以 multipart/form-data 上传原图做图生图。字段名固定为 image，可重复多次传多张；其余参数与生成接口同名，均以表单字段提交。',
      bodyType: 'multipart',
      params: [
        { name: 'image', type: 'file', required: true, description: '原图文件，可重复该字段上传多张' },
        { name: 'model', type: 'string', required: true, description: '模型标识' },
        { name: 'prompt', type: 'string', required: true, description: '编辑指令' },
        { name: 'input_fidelity', type: 'string', required: false, description: '原图保真度' },
        ...shared,
      ],
      requestExample: `curl -X POST ${window.location.origin}/v1/images/edits \\\n  -H "Authorization: YOUR_TOKEN" \\\n  -F "model=${model}" \\\n  -F "prompt=把背景换成雪山" \\\n  -F "image=@./source.png"`,
      responseExample: JSON.stringify({ created: 1704067200, data: [{ url: 'https://example.com/edited.png' }] }, null, 2),
    },
  ];
};

const buildVideoDocEndpoints = (docs: DocsVideosResponse): ApiEndpoint[] => {
  const modelOptions = docs.model_options || {};
  const videoModels = (docs.models || []).map(model => ({ model, options: modelOptions[model] || {} }));
  const roleLabels = Array.from(new Set(videoModels.flatMap(({ options }) => options.allowed_roles || [])))
    .map(role => videoRoleLabels[role] || role);
  const params = [
    { name: 'model', type: 'string', required: true, description: '公开模型标识，具体能力和限制见下方“模型能力”列表' },
    { name: 'prompt', type: 'string', required: false, description: '文本提示词；纯素材任务可省略' },
    { name: 'duration', type: 'integer', required: false, description: '视频时长（秒），按所选模型的能力限制' },
    { name: 'resolution', type: 'string', required: false, description: '输出分辨率，按所选模型的能力限制' },
    { name: 'aspect_ratio', type: 'string', required: false, description: '画面比例；未配置比例的模型不要提交此字段' },
    { name: 'generate_audio', type: 'boolean', required: false, description: '是否生成音频；以模型能力为准，部分模型固定禁止' },
    { name: 'service_tier', type: 'string', required: false, description: '执行档位；具体可用值以所选模型能力为准，未填写时使用 standard' },
    { name: 'references', type: 'array', required: false, description: `素材引用数组。每项为 {type, role, asset_id|url, duration_seconds}；可用角色以当前模型能力为准${roleLabels.length ? `，当前已配置：${roleLabels.join('、')}` : ''}，未声明的角色不要提交` },
    { name: 'provider_options', type: 'object', required: false, description: '官方协议扩展；provider_options.seedance 支持 camera_fixed、return_last_frame、web_search' },
    { name: 'callback_url', type: 'string', required: false, description: '任务完成后的回调地址' },
  ];
  const example = videoModels[0];
  const exampleModel = example?.model || docs.models?.[0] || 'your-video-model';
  const exampleOptions = example?.options || modelOptions[exampleModel] || {};
  const exampleRole = exampleOptions.allowed_roles?.includes('reference_image')
    ? 'reference_image'
    : exampleOptions.allowed_roles?.[0];
  const exampleType = exampleRole?.includes('audio') ? 'audio' : exampleRole?.includes('video') ? 'video' : 'image';
  const generationEndpoint: ApiEndpoint = {
    id: 'ep-videos-generations',
    method: 'POST',
    path: '/v1/videos/generations',
    name: '视频生成（Prism V1）',
    description: 'Prism 统一视频协议。模型能力由当前发布目录动态展示，系统自动选路；任务类型由 references[].role 推断。',
    params,
    videoModels,
    requestExample: JSON.stringify({
      model: exampleModel,
      prompt: '镜头缓慢推进，城市夜景亮起灯光',
      duration: exampleOptions.duration_options?.[0] || exampleOptions.duration_min || 5,
      resolution: exampleOptions.resolutions?.[0] || '1080p',
      ...(exampleOptions.ratios?.length ? { aspect_ratio: exampleOptions.ratios[0] } : {}),
      generate_audio: exampleOptions.allow_generated_audio !== false,
      service_tier: exampleOptions.service_tiers?.[0] || 'standard',
      ...(exampleRole ? { references: [{ type: exampleType, role: exampleRole, asset_id: 'asset_xxx' }] } : {}),
    }, null, 2),
    responseExample: JSON.stringify({ id: 'video_xxx', status: 'queued', service_tier: 'standard' }, null, 2),
  };
  return [generationEndpoint,
    {
      id: 'ep-videos-queue', method: 'GET', path: '/v1/videos/queue', name: '视频队列',
      description: '查询当前 Token 的活动视频任务和队列摘要。', params: [],
      responseExample: JSON.stringify({ active_count: 1, queued_count: 1, running_count: 0, items: [] }, null, 2),
    },
    {
      id: 'ep-video-queue', method: 'GET', path: '/v1/videos/generations/{id}/queue', name: '任务队列状态',
      description: '查询单个视频任务的实时队列信息。', params: [{ name: 'id', type: 'string', required: true, description: '任务 ID' }],
    },
  ];
};

// ===== 主组件 =====
const ApiDocs: React.FC = () => {
  const [videoDocs, setVideoDocs] = useState<DocsVideosResponse>({ models: [], model_options: {} });
  const [models, setModels] = useState<DocsModel[]>([]);
  const [loading, setLoading] = useState(true);
  const [activeSection, setActiveSection] = useState('quickstart');
  const [docsCopied, setDocsCopied] = useState(false);
  const [tryItApi, setTryItApi] = useState<ApiEndpoint | null>(null);
  const contentRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    // 两个接口各自容错：模型目录挂了不该让整页文档打不开。
    Promise.all([
      fetchDocsVideos().catch(() => ({ models: [], model_options: {} } as DocsVideosResponse)),
      fetchDocsModels().catch(() => [] as DocsModel[]),
    ]).then(([videos, list]) => {
      setVideoDocs(videos);
      setModels(Array.isArray(list) ? list : []);
      setLoading(false);
    });
  }, []);

  const chatModel = pickDocsModel(models, ['openai_chat'], 'your-chat-model');
  const anthropicModel = pickDocsModel(models, ['anthropic_messages'], 'your-anthropic-model');
  const responsesModel = pickDocsModel(models, ['openai_responses', 'volcengine_responses_v3'], 'your-responses-model');
  const imageModel = pickDocsModel(models.filter(model => (model.types || [model.type]).includes('image')), ['openai_images'], 'your-image-model');
  const chatParams = buildChatParams(models);
  const anthropicEndpoints = buildAnthropicEndpoints(anthropicModel);
  const responsesEndpoints = buildResponsesEndpoints(responsesModel);
  const imageEndpoints = buildImageEndpoints(imageModel);
  const videoEndpoints = buildVideoDocEndpoints(videoDocs);

  // IntersectionObserver for active nav tracking
  useEffect(() => {
    if (loading) return;
    const observer = new IntersectionObserver(
      entries => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setActiveSection(entry.target.id);
            break;
          }
        }
      },
      { root: contentRef.current, threshold: 0.15, rootMargin: '-10% 0px -70% 0px' }
    );
    const sections = contentRef.current?.querySelectorAll('section[id], div[id^="ep-"]');
    sections?.forEach(el => observer.observe(el));
    return () => observer.disconnect();
  }, [loading, videoDocs, models]);

  const scrollTo = (id: string) => {
    const el = document.getElementById(id);
    if (el && contentRef.current) {
      contentRef.current.scrollTo({ top: el.offsetTop - 16, behavior: 'smooth' });
    }
  };

  const copyAllDocs = () => {
    const appendEndpoints = (title: string, intro: string, endpoints: ApiEndpoint[]) => {
      let section = `## ${title}\n\n${intro}\n\n`;
      endpoints.forEach(ep => {
        section += `### ${ep.method} ${ep.path}\n${ep.name}${ep.description ? ' - ' + ep.description : ''}\n\n`;
        if (ep.params.length > 0) {
          section += `| 参数 | 类型 | 必填 | 说明 |\n|------|------|------|------|\n`;
          ep.params.forEach(p => { section += `| ${p.name} | ${p.type} | ${p.required ? '是' : '否'} | ${p.description} |\n`; });
          section += '\n';
        }
        if (ep.videoModels?.length) {
          section += `**模型能力（按渠道）:**\n\n| 渠道 | 模型 | 任务 | 多模态范围 | 时长 | 分辨率 | 比例 | 素材角色 |\n|------|------|------|------|------|------|------|------|\n`;
          ep.videoModels.forEach(({ model, options, channelName }) => {
            const roles = (options.allowed_roles || []).map(value => videoRoleLabels[value] || value).join('、') || '-';
            section += `| ${channelName || '-'} | ${model} | ${(options.task_types || []).map(value => videoTaskLabels[value] || value).join('、') || '-'} | ${formatMultimodalScope(options)} | ${formatDurationConstraint(options)} | ${(options.resolutions || []).join('、') || '-'} | ${(options.ratios || []).join('、') || '-'} | ${roles} |\n`;
          });
          section += '\n';
        }
        // 图像编辑的示例是 curl（multipart 没法用 JSON 表达），按内容选代码块语言。
        if (ep.requestExample) section += `**请求示例:**\n\`\`\`${ep.requestExample.trimStart().startsWith('{') ? 'json' : 'bash'}\n${ep.requestExample}\n\`\`\`\n\n`;
        if (ep.responseExample) section += `**响应示例:**\n\`\`\`json\n${ep.responseExample}\n\`\`\`\n\n`;
      });
      return section;
    };
    let md = `# API 文档\n\nBase URL: ${window.location.origin}\n认证方式: 请求头 Authorization: YOUR_TOKEN\n\n`;
    if (models.length) {
      md += `## 可用模型\n\n| 模型 | 类型 | Transport |\n|------|------|------|\n`;
      models.forEach(model => { md += `| ${modelDisplayName(model)} | ${(model.types || [model.type]).filter(Boolean).join('、') || '-'} | ${(model.transports || []).join('、') || '-'} |\n`; });
      md += '\n';
    }
    md += `## Chat 对话接口\n\n### POST /v1/chat/completions\n对话补全 - 兼容 OpenAI 格式，支持多模态（图片/文件）\n\n| 参数 | 类型 | 必填 | 说明 |\n|------|------|------|------|\n`;
    chatParams.forEach(p => { md += `| ${p.name} | ${p.type} | ${p.required ? '是' : '否'} | ${p.description} |\n`; });
    md += `\n**多模态请求示例 (图片):**\n\`\`\`json\n${JSON.stringify({ model: chatModel, messages: [{ role: "user", content: [{ type: "text", text: "这张图片里有什么？" }, { type: "image_url", image_url: { url: "https://example.com/image.jpg" } }] }], max_tokens: 1000 }, null, 2)}\n\`\`\`\n\n`;
    md += `### GET /v1/models\n获取当前令牌可用的全部模型，返回类型、介绍、协议端点与近期成功率。无有效样本时不返回 availability。\n\n`;
    md += `### GET /v1/models/:code\n获取单个模型详情，字段与列表项一致。\n\n| 参数 | 类型 | 必填 | 说明 |\n|------|------|------|------|\n| code | string | 是 | 模型标识（路径参数） |\n\n`;
    md += appendEndpoints('Anthropic Messages API', '下游使用 Anthropic Messages 协议，模型可通过任一已配置 Transport 执行。', anthropicEndpoints);
    md += appendEndpoints('Responses API', '下游统一使用 /v1/responses。OpenAI 上游调用 /v1/responses；火山方舟调用原生 /api/v3/responses 并保留 v3 扩展；Anthropic 与 Google 由 Prism 转换。', responsesEndpoints);
    md += appendEndpoints('Images API', '兼容 OpenAI Images。异步上游由 Prism 轮询后同步返图；/v1/images/edits 使用 multipart 上传原图。', imageEndpoints);
    md += appendEndpoints('Files API', '文件按 API Token 隔离，可通过 file_id 用于 Responses 多模态输入。', FILE_ENDPOINTS);
    md += appendEndpoints('Video API', '统一视频生成与队列查询。', videoEndpoints);
    md += `## 错误码\n\n| 错误码 | 说明 |\n|--------|------|\n| 0 | 成功 |\n| 400 | 参数错误 |\n| 401 | 未认证/Token无效 |\n| 402 | 余额不足 |\n| 403 | 无权限 |\n| 404 | 资源不存在 |\n| 429 | 请求过于频繁 |\n| 500 | 服务器内部错误 |\n`;
    navigator.clipboard.writeText(md);
    setDocsCopied(true);
    setTimeout(() => setDocsCopied(false), 2000);
  };

  const navGroups: NavGroup[] = [
    { id: 'quickstart', label: '快速开始', icon: <Book size={14} />, items: [] },
    ...(models.length ? [{ id: 'models', label: '可用模型', icon: <Boxes size={14} />, items: [] }] : []),
    { id: 'chat', label: 'Chat 对话', icon: <MessageSquare size={14} />, items: [
      { id: 'ep-chat-completions', label: '对话补全' },
      { id: 'ep-models', label: '模型列表' },
      { id: 'ep-model-detail', label: '模型详情' },
    ]},
    { id: 'anthropic', label: 'Anthropic Messages', icon: <MessageSquare size={14} />, items: anthropicEndpoints.map(e => ({ id: e.id, label: e.name })) },
    { id: 'responses', label: 'Responses', icon: <Braces size={14} />, items: responsesEndpoints.map(e => ({ id: e.id, label: e.name })) },
    { id: 'images', label: 'Images', icon: <ImageIcon size={14} />, items: imageEndpoints.map(e => ({ id: e.id, label: e.name })) },
    { id: 'files', label: 'Files', icon: <FileUp size={14} />, items: FILE_ENDPOINTS.map(e => ({ id: e.id, label: e.name })) },
    { id: 'video', label: 'Video', icon: <Video size={14} />, items: videoEndpoints.map(e => ({ id: e.id, label: e.name })) },
    { id: 'errors', label: '错误码', icon: <AlertTriangle size={14} />, items: [] },
  ];

  const isActive = useCallback((id: string) => activeSection === id, [activeSection]);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 size={24} className="animate-spin text-[var(--primary)]" />
      </div>
    );
  }

  return (
    <div className="flex h-[calc(100dvh-6rem)] gap-4">
      {/* 左侧导航 */}
      <aside className="w-56 shrink-0 overflow-y-auto no-scrollbar hidden md:block">
        <div className="sticky top-0 space-y-1">
          <button onClick={copyAllDocs} className="flex items-center gap-2 w-full px-3 py-2 mb-2 text-xs font-medium border border-[var(--border-soft)] rounded-lg hover:bg-[var(--primary-lighter)] transition-colors text-[var(--text-secondary)]">
            {docsCopied ? <><Check size={12} className="text-green-500" /> 已复制</> : <><Copy size={12} /> 一键复制全部文档</>}
          </button>
          {navGroups.map(g => (
            <div key={g.id}>
              <button
                onClick={() => scrollTo(g.id)}
                className={`flex items-center gap-2 w-full px-3 py-2 text-sm font-medium rounded-lg transition-colors border-l-2 ${isActive(g.id) ? 'border-[var(--primary)] text-[var(--primary)] bg-[var(--primary-lighter)]' : 'border-transparent text-[var(--text-primary)] hover:bg-[var(--primary-lighter)]'}`}
              >
                {g.icon} {g.label}
              </button>
              {g.items.length > 0 && (
                <div className="ml-6 space-y-0.5">
                  {g.items.map(item => (
                    <button
                      key={item.id}
                      onClick={() => scrollTo(item.id)}
                      className={`block w-full text-left px-2 py-1 text-xs rounded border-l-2 truncate transition-colors ${isActive(item.id) ? 'border-[var(--primary)] text-[var(--primary)] font-medium' : 'border-transparent text-[var(--text-secondary)] hover:text-[var(--primary)]'}`}
                    >
                      {item.label}
                    </button>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      </aside>

      {/* 右侧内容 */}
      <main ref={contentRef} className="flex-1 overflow-y-auto space-y-6 pr-2">
        {/* 快速开始 */}
        <section id="quickstart">
          <div className="bg-gradient-to-r from-indigo-500 to-purple-600 rounded-2xl p-6 text-white">
            <div className="flex items-center gap-3 mb-4">
              <Book size={24} />
              <h2 className="text-lg font-bold">快速开始</h2>
            </div>
            <div className="space-y-2 text-sm text-indigo-100">
              <p><strong>Base URL:</strong> <code className="bg-white/20 px-2 py-0.5 rounded">{window.location.origin}</code></p>
              <p><strong>认证方式:</strong> 请求头 <code className="bg-white/20 px-2 py-0.5 rounded">Authorization: YOUR_TOKEN</code></p>
              <p><strong>步骤:</strong> 1. 创建令牌 → 2. 选择模型 → 3. 发起请求</p>
            </div>
          </div>
        </section>

        {/* 可用模型 —— 来自 GET /api/docs/models，即当前生效目录里真的能执行的那些 */}
        {models.length > 0 && (
          <section id="models">
            <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><Boxes size={18} /> 可用模型</h2>
            <p className="text-sm text-[var(--text-secondary)] mb-4">下表来自当前生效的模型套餐版本，共 {models.length} 个。下文示例里的模型名也取自这里。</p>
            <div className="border border-[var(--border-soft)] rounded-xl overflow-hidden bg-[var(--surface-card)] overflow-x-auto">
              <table className="w-full text-sm min-w-[560px]">
                <thead>
                  <tr className="bg-[var(--surface)]">
                    <th className="px-4 py-3 text-left font-medium text-[var(--text-secondary)]">模型</th>
                    <th className="px-4 py-3 text-left font-medium text-[var(--text-secondary)]">类型</th>
                    <th className="px-4 py-3 text-left font-medium text-[var(--text-secondary)]">可用 Transport</th>
                  </tr>
                </thead>
                <tbody>
                  {models.map(model => (
                    <tr key={model.id} className="border-t border-[var(--border-soft)]">
                      <td className="px-4 py-3"><code className="text-xs text-[var(--primary)]">{modelDisplayName(model)}</code>{model.name && <span className="ml-2 text-xs text-[var(--text-secondary)]">{model.name}</span>}</td>
                      <td className="px-4 py-3 text-xs text-[var(--text-secondary)]">{(model.types || [model.type]).filter(Boolean).join('、') || '-'}</td>
                      <td className="px-4 py-3 text-xs text-[var(--text-secondary)]">{(model.transports || []).join('、') || '-'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        )}

        {/* Chat 对话接口 */}
        <section id="chat">
          <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><MessageSquare size={18} /> Chat 对话接口</h2>
          <p className="text-sm text-[var(--text-secondary)] mb-4">兼容 OpenAI Chat Completions API 格式</p>
          <div className="space-y-3">
            <EndpointCard api={{
              id: 'ep-chat-completions',
              method: 'POST',
              path: '/v1/chat/completions',
              name: '对话补全',
              description: '发送消息获取模型回复，支持流式/非流式、多模态、Tool Use，并自动保存成功、失败和中断的对话轮次',
              params: chatParams,
              requestExample: JSON.stringify({
                model: chatModel,
                messages: [
                  { role: "system", content: "You are a helpful assistant." },
                  { role: "user", content: [
                    { type: "text", text: "这张图片里有什么？" },
                    { type: "image_url", image_url: { url: "https://example.com/image.jpg" } }
                  ]}
                ],
                stream: false,
                temperature: 0.7,
                max_tokens: 1000
              }, null, 2),
              responseExample: JSON.stringify({
                id: "chatcmpl-abc123",
                object: "chat.completion",
                created: 1704067200,
                model: chatModel,
                choices: [{ index: 0, message: { role: "assistant", content: "Hello! How can I help you?" }, finish_reason: "stop" }],
                usage: { prompt_tokens: 20, completion_tokens: 10, total_tokens: 30 }
              }, null, 2),
            }} onTryIt={setTryItApi} />

            <EndpointCard api={{
              id: 'ep-models',
              method: 'GET',
              path: '/v1/models',
              name: '模型列表',
              description: '获取当前令牌可用的全部模型，含类型、介绍、协议端点与近期成功率',
              params: [],
              responseExample: JSON.stringify({
                object: "list",
                data: [{
                  id: chatModel,
                  object: "model",
                  owned_by: "prism",
                  name: chatModel,
                  model_code: chatModel,
                  description: "模型介绍",
                  type: "chat",
                  types: ["chat"],
                  native_transports: ["openai"],
                  supported_operations: ["chat.completions"],
                  supported_endpoints: ["/v1/chat/completions"],
                  availability: {
                    success_rate: "99.5",
                    source: "upstream",
                    window_minutes: 120,
                    observed_at: "2026-09-16T07:00:00Z"
                  }
                }]
              }, null, 2),
            }} onTryIt={setTryItApi} />

            <EndpointCard api={{
              id: 'ep-model-detail',
              method: 'GET',
              path: '/v1/models/:code',
              name: '模型详情',
              description: '获取单个模型详情，字段与列表项一致',
              params: [{ name: 'code', type: 'string', required: true, description: '模型标识（路径参数）' }],
              responseExample: JSON.stringify({
                id: chatModel,
                object: "model",
                owned_by: "prism",
                description: "模型介绍",
                type: "chat",
                native_transports: ["openai"],
                supported_endpoints: ["/v1/chat/completions"]
              }, null, 2),
            }} onTryIt={setTryItApi} />
          </div>
        </section>

		<section id="anthropic">
		  <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><MessageSquare size={18} /> Anthropic Messages API</h2>
		  <p className="text-sm text-[var(--text-secondary)] mb-4">兼容 Anthropic Messages 请求、响应与流式事件格式。</p>
		  <div className="space-y-3">
			{anthropicEndpoints.map(ep => <EndpointCard key={ep.id} api={ep} onTryIt={setTryItApi} />)}
		  </div>
		</section>

        {/* Responses API */}
        <section id="responses">
          <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><Braces size={18} /> Responses API</h2>
          <p className="text-sm text-[var(--text-secondary)] mb-2">下游统一调用 OpenAI 兼容的 <code className="text-[var(--primary)]">/v1/responses</code>。</p>
          <p className="text-xs text-[var(--text-secondary)] mb-4">OpenAI 上游使用 <code>/v1/responses</code>；火山方舟使用原生 <code>/api/v3/responses</code>，保留 v3 扩展字段、工具事件和工具用量；Anthropic 与 Google 由 Prism 转换。后台执行、存储、24 小时幂等缓存和公开响应 ID 由 Prism 管理。</p>
          <div className="space-y-3">
            {responsesEndpoints.map(ep => (
              <EndpointCard key={ep.id} api={ep} onTryIt={setTryItApi} />
            ))}
          </div>
        </section>

        {/* Images API */}
        <section id="images">
          <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><ImageIcon size={18} /> Images API</h2>
          <p className="text-sm text-[var(--text-secondary)] mb-4">兼容 OpenAI Images API。上游是异步任务制的渠道由 Prism 轮询后同步返图，调用方不需要自己查任务；<code className="text-[var(--primary)]">/v1/images/generations</code> 带上 <code>image_urls</code> 即按图生图执行与计费。</p>
          <div className="space-y-3">
            {imageEndpoints.map(ep => (
              <EndpointCard key={ep.id} api={ep} onTryIt={setTryItApi} />
            ))}
          </div>
        </section>

        {/* Files API */}
        <section id="files">
          <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><FileUp size={18} /> Files API</h2>
          <p className="text-sm text-[var(--text-secondary)] mb-4">上传并管理多模态输入文件。文件按 API Token 隔离，可在 Responses 输入中通过 <code className="text-[var(--primary)]">file_id</code> 引用。</p>
          <div className="space-y-3">
            {FILE_ENDPOINTS.map(ep => (
              <EndpointCard key={ep.id} api={ep} onTryIt={setTryItApi} />
            ))}
          </div>
        </section>

        <section id="video">
          <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><Video size={18} /> Video API</h2>
          <p className="text-sm text-[var(--text-secondary)] mb-4">统一视频生成与队列查询</p>
          <div className="space-y-3">
            {videoEndpoints.map(ep => (
              <EndpointCard key={ep.id} api={ep} onTryIt={setTryItApi} />
            ))}
          </div>
        </section>

        {/* 错误码 */}
        <section id="errors">
          <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><AlertTriangle size={18} /> 错误码</h2>
          <div className="border border-[var(--border-soft)] rounded-xl overflow-hidden bg-[var(--surface-card)]">
            <table className="w-full text-sm">
              <thead>
                <tr className="bg-[var(--surface)]">
                  <th className="px-4 py-3 text-left font-medium text-[var(--text-secondary)]">错误码</th>
                  <th className="px-4 py-3 text-left font-medium text-[var(--text-secondary)]">说明</th>
                </tr>
              </thead>
              <tbody>
                {[
                  { code: 0, desc: '成功' },
                  { code: 400, desc: '参数错误' },
                  { code: 401, desc: '未认证 / Token 无效' },
                  { code: 402, desc: '余额不足' },
                  { code: 403, desc: '无权限' },
                  { code: 404, desc: '资源不存在' },
                  { code: 429, desc: '请求过于频繁' },
                  { code: 500, desc: '服务器内部错误' },
                ].map(e => (
                  <tr key={e.code} className="border-t border-[var(--border-soft)]">
                    <td className="px-4 py-3"><code className={`px-2 py-0.5 rounded text-xs ${e.code === 0 ? 'bg-green-100 text-green-700' : 'bg-red-100 text-red-700'}`}>{e.code}</code></td>
                    <td className="px-4 py-3 text-[var(--text-secondary)]">{e.desc}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      </main>

      <TryItDrawer
        open={!!tryItApi}
        onClose={() => setTryItApi(null)}
        method={tryItApi?.method || 'GET'}
        path={tryItApi?.path || ''}
        name={tryItApi?.name || ''}
        params={tryItApi?.params || []}
        bodyType={tryItApi?.bodyType || 'json'}
      />
    </div>
  );
};

export default ApiDocs;
