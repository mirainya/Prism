export interface CatalogAllowedHost {
  protocol: 'http' | 'https';
  host: string;
  port: number;
}

const httpProtocols = new Set(['http:', 'https:']);

export const isVideoCatalogAdapter = (adapterCode: string) => adapterCode === 'generic' || adapterCode === 'seedance';

export const catalogRequestMethods = (adapterCode: string) => adapterCode === 'generic'
  ? ['POST', 'GET', 'PUT', 'PATCH', 'DELETE']
  : ['POST'];

export const catalogTaskPolicy = (adapterCode: string) => isVideoCatalogAdapter(adapterCode)
  ? { taskScope: 'task', cancelMode: 'none' }
  : { taskScope: 'request', cancelMode: 'none' };

const catalogRequestPaths: Record<string, string> = {
  anthropic_messages: '/v1/messages',
  generic: '/tasks',
  google_generate_content: '/v1beta/models/{model}:generateContent',
  openai_chat: '/v1/chat/completions',
  openai_images: '/v1/images/generations',
  openai_responses: '/v1/responses',
  seedance: '/api/v3/contents/generations/tasks',
  volcengine_responses_v3: '/api/v3/responses',
};

export const catalogDefaultRequestPath = (adapterCode: string) => catalogRequestPaths[adapterCode] || '/';

export const genericCatalogTemplate = (method: string, path: string) => JSON.stringify({
  adapter: {
    profile: 'json_task_v1',
    auth_location: 'header',
    auth_key: 'Authorization',
    auth_prefix: 'Bearer ',
    submit: { enabled: true, method, path },
    poll: { enabled: true, method: 'GET', path: `${path.replace(/\/$/, '')}/{task_id}` },
    request: {
      fields: {
        model: 'model', prompt: 'prompt', resolution: 'resolution', ratio: 'ratio',
        duration: 'duration', audio: 'generate_audio', task_mode: 'task_mode',
      },
      params_mode: 'merge_missing',
    },
    response: {
      task_id_paths: ['data.id', 'id'],
      status_paths: ['data.status', 'status'],
      video_url_paths: ['data.result.video_url', 'data.video_url', 'result.video_url', 'video_url'],
      error_paths: ['data.error.message', 'data.message', 'error.message', 'message'],
      status_map: {
        queued: 'submitted', pending: 'submitted', running: 'tracking', processing: 'tracking',
        succeeded: 'completed', completed: 'completed', failed: 'failed', canceled: 'cancelled',
      },
      submit_default_status: 'submitted',
      poll_default_status: 'tracking',
      unknown_status: 'tracking',
    },
  },
}, null, 2);

export const parseCatalogConstraints = (source: string, adapterCode: string, method: string, path: string) => {
  let value: unknown;
  try {
    value = JSON.parse(source);
  } catch {
    throw new Error('能力与适配声明不是有效 JSON');
  }
  if (!isObject(value)) throw new Error('能力与适配声明必须是 JSON 对象');

  if (adapterCode === 'generic') {
    if (!isObject(value.adapter)) throw new Error('通用视频适配器缺少 adapter 声明');
    const submit = isObject(value.adapter.submit) ? value.adapter.submit : {};
    value = {
      ...value,
      adapter: {
        ...value.adapter,
        submit: { ...submit, enabled: true, method, path },
      },
    };
  }

  if (new TextEncoder().encode(JSON.stringify(value)).length > 16384) {
    throw new Error('能力与适配声明不能超过 16 KiB');
  }
  return value;
};

export const buildCatalogAllowedHosts = (baseURL: string, resultHosts: string): CatalogAllowedHost[] => {
  const base = parseHTTPURL(baseURL, false, '上游地址');
  const values = [toAllowedHost(base)];
  for (const entry of resultHosts.split(/[\n,]+/).map(value => value.trim()).filter(Boolean)) {
    values.push(toAllowedHost(parseHTTPURL(entry.includes('://') ? entry : `https://${entry}`, true, '结果来源域名')));
  }

  const seen = new Set<string>();
  const result = values.filter(value => {
    const key = `${value.protocol}\0${value.host}\0${value.port}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
  if (result.length > 32) throw new Error('允许主机不能超过 32 个');
  return result;
};

const parseHTTPURL = (source: string, originOnly: boolean, label: string) => {
  let parsed: URL;
  try {
    parsed = new URL(source.trim());
  } catch {
    throw new Error(`${label}格式无效`);
  }
  if (!httpProtocols.has(parsed.protocol) || !parsed.hostname || parsed.username || parsed.password || parsed.search || parsed.hash || (originOnly && parsed.pathname !== '/')) {
    throw new Error(`${label}格式无效`);
  }
  return parsed;
};

const toAllowedHost = (parsed: URL): CatalogAllowedHost => {
  const protocol = parsed.protocol.slice(0, -1) as 'http' | 'https';
  const host = parsed.hostname.toLowerCase().replace(/^\[|\]$/g, '').replace(/\.$/, '');
  const port = Number(parsed.port || (protocol === 'https' ? 443 : 80));
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('主机端口格式无效');
  return { protocol, host, port };
};

const isObject = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
