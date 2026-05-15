import React, { useState, useEffect } from 'react';
import { X, Play, Loader2, Copy, Check, Code, List } from 'lucide-react';
import { ApiToken } from '../types';

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
  tokens: ApiToken[];
}

export const TryItDrawer: React.FC<TryItDrawerProps> = ({ open, onClose, method, path, name, params, tokens }) => {
  const [token, setToken] = useState('');
  const [pathValue, setPathValue] = useState(path);
  const [mode, setMode] = useState<'form' | 'json'>('form');
  const [formValues, setFormValues] = useState<Record<string, string>>({});
  const [jsonBody, setJsonBody] = useState('');
  const [response, setResponse] = useState('');
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (tokens.length > 0 && !token) setToken(tokens[0].key);
  }, [tokens]);

  useEffect(() => {
    setPathValue(path);
    setFormValues({});
    setJsonBody('');
    setResponse('');
  }, [path]);

  // 表单值 → JSON
  const formToJson = (): string => {
    const body: Record<string, any> = {};
    params.forEach(p => {
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
    if (method === 'GET') return '';
    return mode === 'json' ? jsonBody : formToJson();
  };

  const send = async () => {
    setLoading(true);
    setResponse('');
    try {
      const url = `${window.location.origin}${pathValue}`;
      const headers: Record<string, string> = { 'Authorization': token, 'Content-Type': 'application/json' };
      const opts: RequestInit = { method, headers };
      const body = getBody();
      if (method !== 'GET' && body && body !== '{}') opts.body = body;
      const res = await fetch(url, opts);
      const text = await res.text();
      try { setResponse(JSON.stringify(JSON.parse(text), null, 2)); } catch { setResponse(text); }
    } catch (e: any) {
      setResponse(`Error: ${e.message}`);
    }
    setLoading(false);
  };

  const curlCmd = (() => {
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

  if (!open) return null;

  return (
    <>
      {/* 遮罩 */}
      <div className="fixed inset-0 bg-black/30 z-40" onClick={onClose} />
      {/* 抽屉 */}
      <div className="fixed top-0 right-0 h-full w-[480px] max-w-full bg-[var(--surface-card)] shadow-2xl z-50 flex flex-col border-l border-[var(--border-soft)] animate-slide-in-right">
        {/* 头部 */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-[var(--border-soft)]">
          <div>
            <div className="flex items-center gap-2">
              <span className={`px-2 py-0.5 rounded text-xs font-bold ${method === 'GET' ? 'bg-green-100 text-green-700' : 'bg-blue-100 text-blue-700'}`}>{method}</span>
              <span className="text-sm font-bold text-[var(--text-primary)]">{name}</span>
            </div>
          </div>
          <button onClick={onClose} className="p-1.5 hover:bg-[var(--primary-lighter)] rounded-lg text-[var(--text-secondary)]">
            <X size={18} />
          </button>
        </div>

        {/* 内容 */}
        <div className="flex-1 overflow-y-auto p-5 space-y-4">
          {/* Token 选择 */}
          <div>
            <label className="text-xs font-medium text-[var(--text-secondary)] mb-1 block">Token</label>
            <select value={token} onChange={e => setToken(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface)] text-[var(--text-primary)]">
              {tokens.length === 0
                ? <option value="">无可用令牌</option>
                : tokens.map(t => <option key={t.id} value={t.key}>{t.name}</option>)
              }
            </select>
          </div>

          {/* 路径 */}
          <div>
            <label className="text-xs font-medium text-[var(--text-secondary)] mb-1 block">请求路径</label>
            <input value={pathValue} onChange={e => setPathValue(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm font-mono bg-[var(--surface)] text-[var(--text-primary)]" />
          </div>

          {/* 参数区 */}
          {method !== 'GET' && params.length > 0 && (
            <div>
              <div className="flex items-center justify-between mb-2">
                <label className="text-xs font-medium text-[var(--text-secondary)]">请求参数</label>
                <div className="flex items-center gap-1 bg-[var(--surface)] rounded-lg p-0.5">
                  <button onClick={() => switchMode('form')} className={`flex items-center gap-1 px-2 py-1 rounded text-xs font-medium transition-colors ${mode === 'form' ? 'bg-[var(--primary)] text-white' : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}>
                    <List size={12} /> 表单
                  </button>
                  <button onClick={() => switchMode('json')} className={`flex items-center gap-1 px-2 py-1 rounded text-xs font-medium transition-colors ${mode === 'json' ? 'bg-[var(--primary)] text-white' : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}>
                    <Code size={12} /> JSON
                  </button>
                </div>
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
                        {enumOpts.length > 0 ? (
                          <select
                            value={formValues[p.name] || ''}
                            onChange={e => setFormValues({ ...formValues, [p.name]: e.target.value })}
                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface)] text-[var(--text-primary)]"
                          >
                            <option value="">选择...</option>
                            {enumOpts.map(o => <option key={o} value={o}>{o}</option>)}
                          </select>
                        ) : p.type === 'boolean' ? (
                          <select
                            value={formValues[p.name] || ''}
                            onChange={e => setFormValues({ ...formValues, [p.name]: e.target.value })}
                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface)] text-[var(--text-primary)]"
                          >
                            <option value="">选择...</option>
                            <option value="true">true</option>
                            <option value="false">false</option>
                          </select>
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
              ) : (
                <textarea
                  value={jsonBody}
                  onChange={e => setJsonBody(e.target.value)}
                  rows={10}
                  className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-xs font-mono resize-y bg-[var(--surface)] text-[var(--text-primary)]"
                  placeholder="Request Body (JSON)"
                />
              )}
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
      </div>
    </>
  );
};
