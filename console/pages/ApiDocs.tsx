import React, { useState, useEffect, useRef, useCallback } from 'react';
import { Book, Search, Copy, Check, ChevronDown, ChevronRight, Play, Loader2, Zap, MessageSquare, ListChecks, Bell, AlertTriangle, RefreshCw, Braces, FileUp } from 'lucide-react';
import { fetchDocsModels, DocsModel } from '../services/docsApi';
import { fetchGwModels } from '../services/gatewayApi';
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
  requestExample?: string;
  responseExample?: string;
  bodyType?: 'json' | 'multipart';
}

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
const CHAT_COMPLETIONS_PARAMS = [
  { name: 'model', type: 'string', required: true, description: '模型标识，如 gpt-4o, claude-sonnet-4-20250514' },
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
];

const ANTHROPIC_MESSAGES_ENDPOINTS: ApiEndpoint[] = [{
  id: 'ep-anthropic-messages',
  method: 'POST',
  path: '/v1/messages',
  name: '创建消息',
  description: '兼容 Anthropic Messages API。Prism 会按模型已配置的 Transport 原生调用或无损转换，支持文本、图片、文档、工具调用和 SSE。',
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
  ],
  requestExample: JSON.stringify({
    model: 'claude-sonnet-4-20250514',
    max_tokens: 1024,
    messages: [{ role: 'user', content: [{ type: 'text', text: '你好' }] }],
    stream: false,
  }, null, 2),
  responseExample: JSON.stringify({
    id: 'msg_abc123', type: 'message', role: 'assistant', model: 'claude-sonnet-4-20250514',
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

const RESPONSES_ENDPOINTS: ApiEndpoint[] = [
  {
    id: 'ep-responses-create',
    method: 'POST',
    path: '/v1/responses',
    name: '创建响应',
    description: 'OpenAI Responses 兼容入口，支持多模态、函数工具、流式、续话和后台执行。Idempotency-Key 结果保留 24 小时，store=false 也可完整重放。火山 v3 专属字段及未来扩展会原样保留；无法无损转换的字段返回 400。',
    params: RESPONSES_PARAMS,
    requestExample: JSON.stringify({
      model: 'doubao-seed-2-0-pro',
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
      model: 'doubao-seed-2-0-pro',
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

// 兼容接口（图片/视频生成）——卡片渲染与 copyAllDocs 的单一数据源
const COMPAT_ENDPOINTS: ApiEndpoint[] = [
  {
    id: 'ep-images-generations',
    method: 'POST',
    path: '/v1/images/generations',
    name: '图片生成（OpenAI 标准）',
    description: 'OpenAI 标准协议，同步返图。同步渠道直接返回，异步渠道网关内部轮询等待（默认上限 300s），超时返 202 + task_id。任何 OpenAI 图像 SDK 可即插即用。',
    params: [
      { name: 'model', type: 'string', required: true, description: '模型标识' },
      { name: 'prompt', type: 'string', required: true, description: '提示词' },
      { name: 'n', type: 'integer', required: false, description: '生成数量' },
      { name: 'size', type: 'string', required: false, description: '图片尺寸，如 1024x1024' },
      { name: 'quality', type: 'string', required: false, description: '图片质量' },
      { name: 'response_format', type: 'string', required: false, description: 'url | b64_json' },
      { name: 'output_format', type: 'string', required: false, description: '输出格式，如 png/jpeg/webp' },
      { name: 'output_compression', type: 'integer', required: false, description: '压缩率 0-100' },
      { name: 'style', type: 'string', required: false, description: '风格' },
    ],
    requestExample: JSON.stringify({ model: "gpt-image-1", prompt: "a cute corgi wearing sunglasses", n: 1, size: "1024x1024" }, null, 2),
    responseExample: JSON.stringify({ created: 1704067200, data: [{ url: "https://...", revised_prompt: "..." }] }, null, 2),
  },
  {
    id: 'ep-videos-generations',
    method: 'POST',
    path: '/v1/videos/generations',
    name: '视频生成',
    description: '兼容 OpenAI 格式的视频生成',
    params: [
      { name: 'model', type: 'string', required: true, description: '模型标识' },
      { name: 'prompt', type: 'string', required: true, description: '提示词' },
      { name: 'params', type: 'object', required: false, description: '额外参数' },
      { name: 'callback_url', type: 'string', required: false, description: '回调地址' },
    ],
  },
];

// ===== 主组件 =====
const ApiDocs: React.FC = () => {
  const [models, setModels] = useState<DocsModel[]>([]);
  const [search, setSearch] = useState('');
  const [loading, setLoading] = useState(true);
  const [activeSection, setActiveSection] = useState('quickstart');
  const [docsCopied, setDocsCopied] = useState(false);
  const [tryItApi, setTryItApi] = useState<ApiEndpoint | null>(null);
  const contentRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    Promise.all([
      fetchDocsModels().catch(() => []),
      fetchGwModels().catch(() => []),
    ]).then(([caps, gwModels]) => {
      // caps 是老 models 表(现只剩 image/video 能力);chat 模型已迁网关,从 gw 拉取补进来
      const chatDocs: DocsModel[] = gwModels
        .filter(g => g.key_available > 0)
        .map(g => ({
          code: g.model_name,
          name: g.display_name || g.model_name,
          type: 'chat',
          description: '',
          param_schema: null,
          channels: [],
        }));
      setModels([...chatDocs, ...caps]);
      setLoading(false);
    });
  }, []);

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
    const sections = contentRef.current?.querySelectorAll('section[id], div[id^="ep-"], div[id^="cap-"]');
    sections?.forEach(el => observer.observe(el));
    return () => observer.disconnect();
  }, [loading, models]);

  const scrollTo = (id: string) => {
    const el = document.getElementById(id);
    if (el && contentRef.current) {
      contentRef.current.scrollTo({ top: el.offsetTop - 16, behavior: 'smooth' });
    }
  };

  const copyAllDocs = () => {
    const chatModelsLocal = models.filter(m => m.type === 'chat');
    const capModels = models.filter(m => m.type !== 'chat');
    const appendEndpoints = (title: string, intro: string, endpoints: ApiEndpoint[]) => {
      let section = `## ${title}\n\n${intro}\n\n`;
      endpoints.forEach(ep => {
        section += `### ${ep.method} ${ep.path}\n${ep.name}${ep.description ? ' - ' + ep.description : ''}\n\n`;
        if (ep.params.length > 0) {
          section += `| 参数 | 类型 | 必填 | 说明 |\n|------|------|------|------|\n`;
          ep.params.forEach(p => { section += `| ${p.name} | ${p.type} | ${p.required ? '是' : '否'} | ${p.description} |\n`; });
          section += '\n';
        }
        if (ep.requestExample) section += `**请求示例:**\n\`\`\`json\n${ep.requestExample}\n\`\`\`\n\n`;
        if (ep.responseExample) section += `**响应示例:**\n\`\`\`json\n${ep.responseExample}\n\`\`\`\n\n`;
      });
      return section;
    };
    let md = `# API 文档\n\nBase URL: ${window.location.origin}\n认证方式: 请求头 Authorization: YOUR_TOKEN\n\n`;
    md += `## Chat 对话接口\n\n### POST /v1/chat/completions\n对话补全 - 兼容 OpenAI 格式，支持多模态（图片/文件）\n\n| 参数 | 类型 | 必填 | 说明 |\n|------|------|------|------|\n`;
    CHAT_COMPLETIONS_PARAMS.forEach(p => { md += `| ${p.name} | ${p.type} | ${p.required ? '是' : '否'} | ${p.description} |\n`; });
    md += `\n**多模态请求示例 (图片):**\n\`\`\`json\n${JSON.stringify({ model: chatModelsLocal[0]?.code || "gpt-4o", messages: [{ role: "user", content: [{ type: "text", text: "这张图片里有什么？" }, { type: "image_url", image_url: { url: "https://example.com/image.jpg" } }] }], max_tokens: 1000 }, null, 2)}\n\`\`\`\n\n`;
    md += `### GET /v1/models\n获取所有可用模型\n\n当前可用模型: ${models.map(m => m.code).join(', ')}\n\n`;
    md += `### GET /v1/models/:code\n获取单个模型详情\n\n| 参数 | 类型 | 必填 | 说明 |\n|------|------|------|------|\n| code | string | 是 | 模型标识（路径参数） |\n\n`;
    md += `### GET /v1/channels\n获取所有可用渠道列表\n\n`;
	md += appendEndpoints('Anthropic Messages API', '下游使用 Anthropic Messages 协议，模型可通过任一已配置 Transport 执行。', ANTHROPIC_MESSAGES_ENDPOINTS);
    md += appendEndpoints('Responses API', '下游统一使用 /v1/responses。OpenAI 上游调用 /v1/responses；火山方舟调用原生 /api/v3/responses 并保留 v3 扩展；Anthropic 与 Google 由 Prism 转换。', RESPONSES_ENDPOINTS);
    md += appendEndpoints('Files API', '文件按 API Token 隔离，可通过 file_id 用于 Responses 多模态输入。', FILE_ENDPOINTS);
    md += `## 能力接口\n\n### GET /v1/capabilities\n获取所有可用能力接口列表\n\n| 参数 | 类型 | 必填 | 说明 |\n|------|------|------|------|\n| channel | string | 否 | 按渠道类型筛选 |\n| type | string | 否 | 按能力类型筛选 |\n\n当前可用能力: ${capModels.map(m => m.code).join(', ')}\n\n`;
    capModels.forEach(m => {
      md += `### POST /v1/capabilities/${m.code}\n${m.name}${m.description ? ' - ' + m.description : ''}\n\n| 参数 | 类型 | 必填 | 说明 |\n|------|------|------|------|\n| channel | string | 否 | 指定渠道（可选） |\n| callback_url | string | 否 | 回调地址 |\n`;
      if (m.param_schema && typeof m.param_schema === 'object') {
        Object.entries(m.param_schema).forEach(([key, val]: [string, any]) => {
          const t = val.type === 'enum' ? `enum(${(val.options || []).join('|')})` : (val.type || 'string');
          md += `| ${key} | ${t} | ${val.required ? '是' : '否'} | ${val.name || ''} |\n`;
        });
      }
      md += '\n';
    });
    md += `## 兼容接口\n\n`;
    COMPAT_ENDPOINTS.forEach(ep => {
      md += `### ${ep.method} ${ep.path}\n${ep.name}${ep.description ? ' - ' + ep.description : ''}\n\n| 参数 | 类型 | 必填 | 说明 |\n|------|------|------|------|\n`;
      ep.params.forEach(p => { md += `| ${p.name} | ${p.type} | ${p.required ? '是' : '否'} | ${p.description} |\n`; });
      if (ep.requestExample) md += `\n**请求示例:**\n\`\`\`json\n${ep.requestExample}\n\`\`\`\n`;
      if (ep.responseExample) md += `\n**响应示例:**\n\`\`\`json\n${ep.responseExample}\n\`\`\`\n`;
      md += '\n';
    });
    md += `## 任务管理\n\n### GET /v1/tasks/:task_no\n查询任务状态和结果\n\n### POST /v1/tasks/:task_no/cancel\n取消正在处理中的任务\n\n`;
    md += `## 回调通知\n\n提交任务时传入 callback_url，任务完成后系统 POST 结果到该地址。最多重试 3 次。\n\n`;
    md += `## 错误码\n\n| 错误码 | 说明 |\n|--------|------|\n| 0 | 成功 |\n| 400 | 参数错误 |\n| 401 | 未认证/Token无效 |\n| 402 | 余额不足 |\n| 403 | 无权限 |\n| 404 | 资源不存在 |\n| 429 | 请求过于频繁 |\n| 500 | 服务器内部错误 |\n`;
    navigator.clipboard.writeText(md);
    setDocsCopied(true);
    setTimeout(() => setDocsCopied(false), 2000);
  };

  const capabilityEndpoints: ApiEndpoint[] = models
    .filter(m => m.type !== 'chat')
    .filter(m => !search || m.name.includes(search) || m.code.includes(search))
    .map(m => {
      const params = [
        { name: 'channel', type: 'string', required: false, description: '指定渠道（可选）' },
        { name: 'callback_url', type: 'string', required: false, description: '回调地址' },
      ];
      if (m.param_schema && typeof m.param_schema === 'object' && !Array.isArray(m.param_schema)) {
        Object.entries(m.param_schema).forEach(([key, val]: [string, any]) => {
          const typeStr = val.type === 'enum' ? `enum(${(val.options || []).join('|')})` : (val.type || 'string');
          params.push({ name: key, type: typeStr, required: val.required || false, description: val.name || '' });
        });
      }
      // 收集渠道专属参数（与能力级不同的）
      const channelParams: ApiEndpoint['channelParams'] = [];
      m.channels.forEach(ch => {
        if (ch.param_schema && typeof ch.param_schema === 'object') {
          const chParams = Object.entries(ch.param_schema).map(([key, val]: [string, any]) => {
            const typeStr = val.type === 'enum' ? `enum(${(val.options || []).join('|')})` : (val.type || 'string');
            return { name: key, type: typeStr, required: val.required || false, description: val.name || '' };
          });
          if (chParams.length > 0) {
            channelParams.push({ channelName: ch.channel_name, channelType: ch.channel_type, interactionMode: ch.interaction_mode, params: chParams });
          }
        }
      });
      const exampleBody: Record<string, any> = {};
      if (m.param_schema && typeof m.param_schema === 'object') {
        Object.entries(m.param_schema).forEach(([key, val]: [string, any]) => {
          if (val.required) {
            if (val.type === 'enum' && val.options?.length) exampleBody[key] = val.options[0];
            else if (val.type === 'number') exampleBody[key] = 1;
            else exampleBody[key] = `示例${val.name || key}`;
          }
        });
      }
      exampleBody.callback_url = "https://your-domain.com/callback";
      return {
        id: `cap-${m.code}`,
        method: 'POST',
        path: `/v1/capabilities/${m.code}`,
        name: m.name,
        description: m.description || `调用 ${m.name} 能力`,
        params,
        channelParams: channelParams.length > 0 ? channelParams : undefined,
        requestExample: JSON.stringify(exampleBody, null, 2),
        responseExample: JSON.stringify({ code: 0, message: "success", data: { task_no: "task_xxx", status: "pending", capability: m.code } }, null, 2),
      };
    });

  const chatModels = models.filter(m => m.type === 'chat');

  const navGroups: NavGroup[] = [
    { id: 'quickstart', label: '快速开始', icon: <Book size={14} />, items: [] },
    { id: 'chat', label: 'Chat 对话', icon: <MessageSquare size={14} />, items: [
      { id: 'ep-chat-completions', label: '对话补全' },
      { id: 'ep-models', label: '模型列表' },
      { id: 'ep-model-detail', label: '模型详情' },
      { id: 'ep-channels', label: '渠道列表' },
    ]},
	{ id: 'anthropic', label: 'Anthropic Messages', icon: <MessageSquare size={14} />, items: ANTHROPIC_MESSAGES_ENDPOINTS.map(e => ({ id: e.id, label: e.name })) },
    { id: 'responses', label: 'Responses', icon: <Braces size={14} />, items: RESPONSES_ENDPOINTS.map(e => ({ id: e.id, label: e.name })) },
    { id: 'files', label: 'Files', icon: <FileUp size={14} />, items: FILE_ENDPOINTS.map(e => ({ id: e.id, label: e.name })) },
    { id: 'capabilities', label: '能力接口', icon: <Zap size={14} />, items: capabilityEndpoints.map(e => ({ id: e.id, label: e.name })) },
    { id: 'compat', label: '兼容接口', icon: <RefreshCw size={14} />, items: [
      { id: 'ep-images-generations', label: '图片生成' },
      { id: 'ep-videos-generations', label: '视频生成' },
    ]},
    { id: 'tasks', label: '任务管理', icon: <ListChecks size={14} />, items: [
      { id: 'ep-get-task', label: '查询任务' },
      { id: 'ep-cancel-task', label: '取消任务' },
    ]},
    { id: 'callback', label: '回调通知', icon: <Bell size={14} />, items: [] },
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
          <div className="relative mb-3">
            <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" />
            <input value={search} onChange={e => setSearch(e.target.value)} placeholder="搜索接口..." className="w-full pl-8 pr-3 py-2 border border-[var(--border-soft)] rounded-lg text-xs focus:outline-none focus:ring-2 focus:ring-[var(--primary)] bg-[var(--surface-card)] text-[var(--text-primary)]" />
          </div>
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
              <p><strong>步骤:</strong> 1. 创建令牌 → 2. 选择模型/能力 → 3. 发起请求</p>
            </div>
          </div>
        </section>

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
              description: '发送消息获取模型回复，支持流式/非流式、多模态、Tool Use',
              params: CHAT_COMPLETIONS_PARAMS,
              requestExample: JSON.stringify({
                model: chatModels[0]?.code || "gpt-4o",
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
                model: chatModels[0]?.code || "gpt-4o",
                choices: [{ index: 0, message: { role: "assistant", content: "Hello! How can I help you?" }, finish_reason: "stop" }],
                usage: { prompt_tokens: 20, completion_tokens: 10, total_tokens: 30 }
              }, null, 2),
            }} onTryIt={setTryItApi} />

            <EndpointCard api={{
              id: 'ep-models',
              method: 'GET',
              path: '/v1/models',
              name: '模型列表',
              description: '获取所有可用模型（含 Chat 和能力接口）',
              params: [],
              responseExample: JSON.stringify({
                object: "list",
                data: chatModels.slice(0, 3).map(m => ({ id: m.code, object: "model", created: 1704067200, owned_by: m.channels[0]?.channel_type || "unknown", type: "chat" }))
              }, null, 2),
            }} onTryIt={setTryItApi} />

            <EndpointCard api={{
              id: 'ep-model-detail',
              method: 'GET',
              path: '/v1/models/:code',
              name: '模型详情',
              description: '获取单个模型的详细信息',
              params: [{ name: 'code', type: 'string', required: true, description: '模型标识（路径参数）' }],
              responseExample: JSON.stringify({
                id: chatModels[0]?.code || "gpt-4o", object: "model", created: 1704067200, owned_by: "openai", max_tokens: 4096
              }, null, 2),
            }} onTryIt={setTryItApi} />

            <EndpointCard api={{
              id: 'ep-channels',
              method: 'GET',
              path: '/v1/channels',
              name: '渠道列表',
              description: '获取所有可用渠道',
              params: [],
            }} onTryIt={setTryItApi} />
          </div>

          {chatModels.length > 0 && (
            <div className="mt-4 border border-[var(--border-soft)] rounded-xl p-4 bg-[var(--surface-card)]">
              <h3 className="text-sm font-bold text-[var(--text-primary)] mb-3">当前可用模型</h3>
              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2">
                {chatModels.map(m => (
                  <div key={m.code} className="flex items-center gap-2 px-3 py-2 bg-[var(--surface)] rounded-lg">
                    <code className="text-xs font-mono text-[var(--primary)]">{m.code}</code>
                    {m.channels.length > 0 && <span className="text-xs text-[var(--text-secondary)] ml-auto">{m.channels[0].channel_type}</span>}
                  </div>
                ))}
              </div>
            </div>
          )}
        </section>

		<section id="anthropic">
		  <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><MessageSquare size={18} /> Anthropic Messages API</h2>
		  <p className="text-sm text-[var(--text-secondary)] mb-4">兼容 Anthropic Messages 请求、响应与流式事件格式。</p>
		  <div className="space-y-3">
			{ANTHROPIC_MESSAGES_ENDPOINTS.map(ep => <EndpointCard key={ep.id} api={ep} onTryIt={setTryItApi} />)}
		  </div>
		</section>

        {/* Responses API */}
        <section id="responses">
          <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><Braces size={18} /> Responses API</h2>
          <p className="text-sm text-[var(--text-secondary)] mb-2">下游统一调用 OpenAI 兼容的 <code className="text-[var(--primary)]">/v1/responses</code>。</p>
          <p className="text-xs text-[var(--text-secondary)] mb-4">OpenAI 上游使用 <code>/v1/responses</code>；火山方舟使用原生 <code>/api/v3/responses</code>，保留 v3 扩展字段、工具事件和工具用量；Anthropic 与 Google 由 Prism 转换。后台执行、存储、24 小时幂等缓存和公开响应 ID 由 Prism 管理。</p>
          <div className="space-y-3">
            {RESPONSES_ENDPOINTS.map(ep => (
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

        {/* 能力接口 */}
        <section id="capabilities">
          <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><Zap size={18} /> 能力接口</h2>
          <p className="text-sm text-[var(--text-secondary)] mb-4">调用各类 AI 能力，支持异步任务模式</p>
          <div className="space-y-3">
            <EndpointCard api={{
              id: 'ep-list-capabilities',
              method: 'GET',
              path: '/v1/capabilities',
              name: '能力列表',
              description: '获取所有可用的能力接口列表',
              params: [
                { name: 'channel', type: 'string', required: false, description: '按渠道类型筛选' },
                { name: 'type', type: 'string', required: false, description: '按能力类型筛选' },
              ],
            }} onTryIt={setTryItApi} />
          </div>
          {capabilityEndpoints.length === 0
            ? <div className="text-sm text-[var(--text-secondary)] py-8 text-center">暂无能力接口</div>
            : <div className="space-y-3 mt-3">{capabilityEndpoints.map(ep => <EndpointCard key={ep.id} api={ep} onTryIt={setTryItApi} />)}</div>
          }
        </section>

        {/* 兼容接口 */}
        <section id="compat">
          <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><RefreshCw size={18} /> 兼容接口</h2>
          <p className="text-sm text-[var(--text-secondary)] mb-4">兼容 OpenAI 格式的图片/视频生成接口</p>
          <div className="space-y-3">
            {COMPAT_ENDPOINTS.map(ep => (
              <EndpointCard key={ep.id} api={ep} onTryIt={setTryItApi} />
            ))}
          </div>
        </section>

        {/* 任务管理 */}
        <section id="tasks">
          <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><ListChecks size={18} /> 任务管理</h2>
          <div className="space-y-3">
            <EndpointCard api={{
              id: 'ep-get-task',
              method: 'GET',
              path: '/v1/tasks/{task_no}',
              name: '查询任务',
              description: '根据任务编号查询任务状态和结果',
              params: [{ name: 'task_no', type: 'string', required: true, description: '任务编号（路径参数）' }],
              responseExample: JSON.stringify({ code: 0, data: { task_no: "task_xxx", status: "completed", result: { url: "https://..." }, created_at: "2024-01-01T00:00:00Z" } }, null, 2),
            }} onTryIt={setTryItApi} />
            <EndpointCard api={{
              id: 'ep-cancel-task',
              method: 'POST',
              path: '/v1/tasks/{task_no}/cancel',
              name: '取消任务',
              description: '取消一个待处理或进行中的任务',
              params: [{ name: 'task_no', type: 'string', required: true, description: '任务编号（路径参数）' }],
              responseExample: JSON.stringify({ code: 0, message: "success" }, null, 2),
            }} onTryIt={setTryItApi} />
          </div>
        </section>

        {/* 回调通知 */}
        <section id="callback">
          <h2 className="text-lg font-bold text-[var(--text-primary)] mb-3 flex items-center gap-2"><Bell size={18} /> 回调通知</h2>
          <div className="border border-[var(--border-soft)] rounded-xl p-4 bg-[var(--surface-card)] space-y-3">
            <p className="text-sm text-[var(--text-secondary)]">任务完成后，系统会向 <code className="text-[var(--primary)]">callback_url</code> 发送 POST 请求。</p>
            <CodeBlock code={JSON.stringify({ task_no: "task_xxx", status: "completed", capability: "text-to-image", result: { url: "https://..." }, created_at: "2024-01-01T00:00:00Z" }, null, 2)} title="回调 Body 示例" />
            <div className="text-sm text-[var(--text-secondary)] space-y-1">
              <p><strong>status 枚举：</strong></p>
              <ul className="ml-4 space-y-0.5 text-xs">
                <li><code className="text-[var(--primary)]">pending</code> — 等待处理</li>
                <li><code className="text-[var(--primary)]">processing</code> — 处理中</li>
                <li><code className="text-[var(--primary)]">completed</code> — 已完成</li>
                <li><code className="text-[var(--primary)]">failed</code> — 失败</li>
              </ul>
            </div>
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
