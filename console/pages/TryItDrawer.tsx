import React, { useState, useEffect } from 'react';
import { Play, Loader2, Copy, Check, Code, List } from 'lucide-react';
import { Drawer, Select } from '../components/ui';

interface ParamDef {
  name: string;
  type: string;
  required: boolean;
  description: string;
}

interface TryItDrawerProps {
  open: boolean;
  onClose: () => void;
  method: string;
  path: string;
  name: string;
  params: ParamDef[];
  bodyType?: 'json' | 'multipart';
  initialJson?: string;
  preferJson?: boolean;
}

export const TryItDrawer: React.FC<TryItDrawerProps> = ({ open, onClose, method, path, name, params, bodyType = 'json', initialJson = '', preferJson = false }) => {
  const [token, setToken] = useState('');
  const [pathValue, setPathValue] = useState(path);
  const [mode, setMode] = useState<'form' | 'json'>('form');
  const [formValues, setFormValues] = useState<Record<string, string>>({});
  const [fileValues, setFileValues] = useState<Record<string, File | null>>({});
  const [jsonBody, setJsonBody] = useState('');
  const [response, setResponse] = useState('');
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    setPathValue(path);
    setFormValues({});
    setFileValues({});
    setJsonBody(initialJson);
    setResponse('');
    setMode(bodyType === 'json' && preferJson ? 'json' : 'form');
  }, [open, path, method, bodyType, initialJson, preferJson]);

  const isPathParameter = (paramName: string) => path.includes(`{${paramName}}`);

  const buildRequestURL = (): string => {
    // 路径参数先替换并编码，剩余 GET 参数再进入 query string。
    const resolvedPath = pathValue.replace(/\{([^}]+)\}/g, (_match, paramName: string) => encodeURIComponent(formValues[paramName] || ''));
    if (method !== 'GET') return `${window.location.origin}${resolvedPath}`;

    const query = new URLSearchParams();
    params.forEach(param => {
      if (isPathParameter(param.name)) return;
      const value = formValues[param.name];
      if (value !== undefined && value !== '') query.set(param.name, value);
    });
    if (!query.size) return `${window.location.origin}${resolvedPath}`;
    return `${window.location.origin}${resolvedPath}${resolvedPath.includes('?') ? '&' : '?'}${query.toString()}`;
  };

  // 表单值 → JSON
  const formToJson = (): string => {
    const body: Record<string, any> = {};
    params.forEach(p => {
      if (p.type === 'file' || isPathParameter(p.name)) return;
      const val = formValues[p.name];
      if (val === undefined || val === '') return;
      if (p.type === 'boolean') body[p.name] = val === 'true';
      else if (p.type === 'number' || p.type === 'integer') body[p.name] = Number(val);
      else if (p.type === 'array' || p.type.startsWith('object') || p.type.includes('[]')) {
        try { body[p.name] = JSON.parse(val); } catch { body[p.name] = val; }
      } else body[p.name] = val;
    });
    return JSON.stringify(body, null, 2);
  };

  // 切换模式
  const switchMode = (newMode: 'form' | 'json') => {
    // 两种编辑模式切换时转换当前值，避免用户已输入内容被重置。
    if (newMode === 'json' && mode === 'form') {
      setJsonBody(formToJson());
    } else if (newMode === 'form' && mode === 'json') {
      try {
        const obj = JSON.parse(jsonBody);
        const vals: Record<string, string> = {};
        Object.entries(obj).forEach(([k, v]) => {
          vals[k] = typeof v === 'object' ? JSON.stringify(v) : String(v);
        });
        setFormValues(vals);
      } catch { /* keep current form values */ }
    }
    setMode(newMode);
  };

  const getBody = (): string => {
    if (method === 'GET' || bodyType === 'multipart') return '';
    return mode === 'json' ? jsonBody : formToJson();
  };

  const send = async () => {
    setLoading(true);
    setResponse('');
    try {
      const url = buildRequestURL();
      const headers: Record<string, string> = { 'Authorization': token };
      const opts: RequestInit = { method, headers };
      if (bodyType === 'multipart' && method !== 'GET') {
        const body = new FormData();
        params.forEach(p => {
          if (p.type === 'file') {
            const file = fileValues[p.name];
            if (file) body.append(p.name, file);
            return;
          }
          const value = formValues[p.name];
          if (value !== undefined && value !== '') body.append(p.name, value);
        });
        opts.body = body;
      } else {
        headers['Content-Type'] = 'application/json';
        const body = getBody();
        if (method !== 'GET' && body && body !== '{}') opts.body = body;
      }
      const res = await fetch(url, opts);
      const text = await res.text();
      try { setResponse(JSON.stringify(JSON.parse(text), null, 2)); } catch { setResponse(text); }
    } catch (e: any) {
      setResponse(`Error: ${e.message}`);
    }
    setLoading(false);
  };

  const curlCmd = (() => {
    if (bodyType === 'multipart') {
      const fields = params.map(p => {
        if (p.type === 'file') return `  -F '${p.name}=@/path/to/file'`;
        const value = formValues[p.name] || (p.required ? `<${p.name}>` : '');
        return value ? `  -F '${p.name}=${value}'` : '';
      }).filter(Boolean);
      return `curl -X ${method} '${window.location.origin}${pathValue}' \\\n  -H 'Authorization: YOUR_TOKEN'${fields.length ? ` \\\n${fields.join(' \\\n')}` : ''}`;
    }
    const body = getBody();
    return `curl -X ${method} '${window.location.origin}${pathValue}' \\\n  -H 'Authorization: YOUR_TOKEN' \\\n  -H 'Content-Type: application/json'${method !== 'GET' && body && body !== '{}' ? ` \\\n  -d '\n${body}\n'` : ''}`;
  })();

  const copyCurl = () => {
    navigator.clipboard.writeText(curlCmd);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  // 获取 enum 选项
  const getEnumOptions = (type: string): string[] => {
    const match = type.match(/^enum\((.+)\)$/);
    return match ? match[1].split('|') : [];
  };

  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={name}
      subtitle={<span className={`inline-flex rounded px-2 py-0.5 text-xs font-bold ${method === 'GET' ? 'bg-green-100 text-green-700' : 'bg-blue-100 text-blue-700'}`}>{method}</span>}
      width="sm:max-w-[480px]"
    >

        {/* 内容 */}
        <div className="flex-1 overflow-y-auto p-5 space-y-4">
          {/* Token */}
          <div>
            <label className="text-xs font-medium text-[var(--text-secondary)] mb-1 block">Token</label>
            <input
              type="password"
              value={token}
              onChange={e => setToken(e.target.value)}
              placeholder="sk-prism-..."
              autoComplete="off"
              className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm font-mono bg-[var(--surface)] text-[var(--text-primary)]"
            />
          </div>

          {/* 路径 */}
          <div>
            <label className="text-xs font-medium text-[var(--text-secondary)] mb-1 block">请求路径</label>
            <input value={pathValue} onChange={e => setPathValue(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm font-mono bg-[var(--surface)] text-[var(--text-primary)]" />
          </div>

          {/* 参数区 */}
          {params.length > 0 && (
            <div>
              <div className="flex items-center justify-between mb-2">
                <label className="text-xs font-medium text-[var(--text-secondary)]">请求参数</label>
                {method !== 'GET' && bodyType === 'json' && <div className="flex items-center gap-1 bg-[var(--surface)] rounded-lg p-0.5">
                  <button onClick={() => switchMode('form')} className={`flex items-center gap-1 px-2 py-1 rounded text-xs font-medium transition-colors ${mode === 'form' ? 'bg-[var(--primary)] text-white' : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}>
                    <List size={12} /> 表单
                  </button>
                  <button onClick={() => switchMode('json')} className={`flex items-center gap-1 px-2 py-1 rounded text-xs font-medium transition-colors ${mode === 'json' ? 'bg-[var(--primary)] text-white' : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}>
                    <Code size={12} /> JSON
                  </button>
                </div>}
              </div>

              {mode === 'form' ? (
                <div className="space-y-3">
                  {params.map(p => {
                    const enumOpts = getEnumOptions(p.type);
                    return (
                      <div key={p.name}>
                        <label className="flex items-center gap-1 text-xs mb-1">
                          <span className="font-mono text-[var(--primary)]">{p.name}</span>
                          {p.required && <span className="text-red-500">*</span>}
                          <span className="text-[var(--text-secondary)] ml-1">{p.type}</span>
                        </label>
                        {p.type === 'file' ? (
                          <input
                            key={`${path}-${p.name}`}
                            type="file"
                            onChange={e => setFileValues({ ...fileValues, [p.name]: e.target.files?.[0] || null })}
                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface)] text-[var(--text-primary)] file:mr-3 file:border-0 file:bg-[var(--primary-lighter)] file:text-[var(--primary)] file:px-3 file:py-1 file:rounded"
                          />
                        ) : enumOpts.length > 0 ? (
                          <Select
                            value={formValues[p.name] || ''}
                            onChange={v => setFormValues({ ...formValues, [p.name]: v })}
                            options={[{ label: '选择...', value: '' }, ...enumOpts.map(o => ({ label: o, value: o }))]}
                          />
                        ) : p.type === 'boolean' ? (
                          <Select
                            value={formValues[p.name] || ''}
                            onChange={v => setFormValues({ ...formValues, [p.name]: v })}
                            options={[{ label: '选择...', value: '' }, { label: 'true', value: 'true' }, { label: 'false', value: 'false' }]}
                          />
                        ) : (p.type === 'array' || p.type.includes('[]') || p.type.startsWith('object')) ? (
                          <textarea
                            value={formValues[p.name] || ''}
                            onChange={e => setFormValues({ ...formValues, [p.name]: e.target.value })}
                            placeholder={p.description || `输入 ${p.type}`}
                            rows={3}
                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-xs font-mono resize-y bg-[var(--surface)] text-[var(--text-primary)]"
                          />
                        ) : (
                          <input
                            type={p.type === 'number' || p.type === 'integer' ? 'number' : 'text'}
                            value={formValues[p.name] || ''}
                            onChange={e => setFormValues({ ...formValues, [p.name]: e.target.value })}
                            placeholder={p.description || p.name}
                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface)] text-[var(--text-primary)]"
                          />
                        )}
                      </div>
                    );
                  })}
                </div>
              ) : bodyType === 'json' ? (
                <textarea
                  value={jsonBody}
                  onChange={e => setJsonBody(e.target.value)}
                  rows={10}
                  className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-xs font-mono resize-y bg-[var(--surface)] text-[var(--text-primary)]"
                  placeholder="Request Body (JSON)"
                />
              ) : null}
            </div>
          )}

          {/* 发送按钮 */}
          <button onClick={send} disabled={loading || !token} className="w-full flex items-center justify-center gap-2 px-4 py-2.5 bg-[var(--primary)] text-white rounded-lg text-sm font-medium hover:opacity-90 disabled:opacity-50 transition-opacity">
            {loading ? <Loader2 size={14} className="animate-spin" /> : <Play size={14} />} 发送请求
          </button>

          {/* 响应 */}
          {response && (
            <div>
              <label className="text-xs font-medium text-[var(--text-secondary)] mb-1 block">响应</label>
              <pre className="bg-gray-900 text-gray-100 rounded-lg p-3 text-xs overflow-x-auto max-h-72 overflow-y-auto"><code>{response}</code></pre>
            </div>
          )}

          {/* curl */}
          <div>
            <div className="flex items-center justify-between mb-1">
              <label className="text-xs font-medium text-[var(--text-secondary)]">cURL</label>
              <button onClick={copyCurl} className="p-1 rounded text-[var(--text-secondary)] hover:text-[var(--primary)]">
                {copied ? <Check size={12} /> : <Copy size={12} />}
              </button>
            </div>
            <pre className="bg-gray-900 text-gray-100 rounded-lg p-3 text-xs overflow-x-auto"><code>{curlCmd}</code></pre>
          </div>
        </div>
    </Drawer>
  );
};
