import React, { useEffect, useState } from 'react';
import { RefreshCw, Download, CheckSquare, Square, AlertCircle } from 'lucide-react';
import { Modal } from '../../components/ui/Modal';
import { Select } from '../../components/ui';
import {
  GwChannel, GwChannelKey,
  discoverGwKeyModels, importGwKeyModels, GwUpstreamModel, GwImportItem,
} from '../../services/gatewayApi';

const PROTOCOL_OPTIONS = [
  { value: 'openai', label: 'OpenAI 兼容' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'volcengine', label: '火山方舟' },
  { value: 'google', label: 'Google' },
];

// 渠道新建/编辑弹窗
export const GwChannelModal: React.FC<{
  isOpen: boolean;
  channel: GwChannel | null;
  onClose: () => void;
  onSave: (data: Partial<GwChannel>) => Promise<void>;
}> = ({ isOpen, channel, onClose, onSave }) => {
  const [name, setName] = useState('');
  const [protocol, setProtocol] = useState('openai');
  const [baseUrl, setBaseUrl] = useState('');
  const [extraHeaders, setExtraHeaders] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (isOpen) {
      setName(channel?.name || '');
      setProtocol(channel?.protocol || 'openai');
      setBaseUrl(channel?.base_url || '');
      setExtraHeaders(channel?.extra_headers ? JSON.stringify(channel.extra_headers, null, 2) : '');
      setError('');
    }
  }, [isOpen, channel]);

  const handleSave = async () => {
    if (!name.trim() || !baseUrl.trim()) { setError('名称与 BaseURL 必填'); return; }
    let headers: Record<string, any> | null = null;
    if (extraHeaders.trim()) {
      try { headers = JSON.parse(extraHeaders); }
      catch { setError('额外请求头不是合法 JSON'); return; }
    }
    setSaving(true); setError('');
    try {
      await onSave({ name: name.trim(), protocol, base_url: baseUrl.trim(), extra_headers: headers });
    } catch (e: any) {
      setError(e?.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={isOpen} onClose={onClose} title={channel ? '编辑渠道' : '新建渠道'} width="max-w-lg">
      <div className="modal-form">
       <div className="modal-scroll-body space-y-3">
        {error && <div className="flex items-start gap-2 p-2 bg-red-50 text-red-700 rounded-lg text-xs"><AlertCircle size={14} className="mt-0.5 shrink-0" />{error}</div>}
        <div>
          <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">名称</label>
          <input value={name} onChange={e => setName(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" placeholder="如 Claude-官转" />
        </div>
        <div>
          <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">协议 (一渠道一协议)</label>
          <Select value={protocol} onChange={setProtocol} options={PROTOCOL_OPTIONS} />
        </div>
        <div>
          <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">BaseURL (不带尾斜杠)</label>
          <input value={baseUrl} onChange={e => setBaseUrl(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm font-mono focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" placeholder="https://api.example.com" />
        </div>
        <div>
          <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">额外请求头 (JSON, 可选)</label>
          <textarea value={extraHeaders} onChange={e => setExtraHeaders(e.target.value)} rows={3} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-xs font-mono focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" placeholder='{"x-custom": "value"}' />
        </div>
       </div>
        <div className="modal-footer">
          <button onClick={onClose} className="modal-button modal-button-secondary">取消</button>
          <button onClick={handleSave} disabled={saving} className="modal-button modal-button-primary">{saving ? '保存中...' : '保存'}</button>
        </div>
      </div>
    </Modal>
  );
};

// Key 新建/编辑弹窗
export const GwKeyModal: React.FC<{
  isOpen: boolean;
  channelKey: GwChannelKey | null;
  onClose: () => void;
  onSave: (data: Partial<GwChannelKey>) => Promise<void>;
}> = ({ isOpen, channelKey, onClose, onSave }) => {
  const [name, setName] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [weight, setWeight] = useState(10);
  const [maxConc, setMaxConc] = useState(0);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (isOpen) {
      setName(channelKey?.name || '');
      setApiKey(channelKey?.api_key || '');
      setWeight(channelKey?.weight ?? 10);
      setMaxConc(channelKey?.max_conc ?? 0);
      setError('');
    }
  }, [isOpen, channelKey]);

  const handleSave = async () => {
    if (!apiKey.trim()) { setError('API Key 必填'); return; }
    setSaving(true); setError('');
    try {
      await onSave({ name: name.trim(), api_key: apiKey.trim(), weight, max_conc: maxConc });
    } catch (e: any) {
      setError(e?.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={isOpen} onClose={onClose} title={channelKey ? '编辑 Key' : '添加 Key'} width="max-w-lg">
      <div className="modal-form">
       <div className="modal-scroll-body space-y-3">
        {error && <div className="flex items-start gap-2 p-2 bg-red-50 text-red-700 rounded-lg text-xs"><AlertCircle size={14} className="mt-0.5 shrink-0" />{error}</div>}
        <div>
          <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">名称 (可选)</label>
          <input value={name} onChange={e => setName(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" placeholder="如 主号 / 备号" />
        </div>
        <div>
          <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">API Key</label>
          <input value={apiKey} onChange={e => setApiKey(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm font-mono focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" placeholder="sk-..." />
        </div>
        <div className="modal-grid-responsive grid grid-cols-2 gap-3">
          <div>
            <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">权重</label>
            <input type="number" value={weight} onChange={e => setWeight(Number(e.target.value))} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
          </div>
          <div>
            <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">最大并发 (0=不限)</label>
            <input type="number" value={maxConc} onChange={e => setMaxConc(Number(e.target.value))} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
          </div>
        </div>
       </div>
        <div className="modal-footer">
          <button onClick={onClose} className="modal-button modal-button-secondary">取消</button>
          <button onClick={handleSave} disabled={saving} className="modal-button modal-button-primary">{saving ? '保存中...' : '保存'}</button>
        </div>
      </div>
    </Modal>
  );
};

// 拉取上游模型弹窗: 用该 key 调 /v1/models, 勾选后导入到 gw_abilities
export const GwPullModal: React.FC<{
  isOpen: boolean;
  keyId: number;
  keyName?: string;
  onClose: () => void;
  onImported?: () => void;
}> = ({ isOpen, keyId, keyName, onClose, onImported }) => {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [items, setItems] = useState<GwUpstreamModel[]>([]);
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [aliases, setAliases] = useState<Record<string, string>>({}); // 上游id -> 自定义对外名(默认=上游id)
  const [importing, setImporting] = useState(false);
  const [result, setResult] = useState('');

  const load = async () => {
    if (!keyId) return;
    setLoading(true); setError(''); setResult('');
    try {
      const data = await discoverGwKeyModels(keyId);
      setItems(data);
      const pre = new Set<string>();
      data.forEach(m => { if (!m.imported) pre.add(m.id); });
      setChecked(pre);
    } catch (e: any) {
      setError(e?.message || '拉取失败');
      setItems([]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (isOpen) load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen, keyId]);

  const toggle = (id: string) => setChecked(prev => {
    const next = new Set(prev);
    next.has(id) ? next.delete(id) : next.add(id);
    return next;
  });
  const toggleAll = () => setChecked(checked.size === items.length ? new Set() : new Set(items.map(m => m.id)));

  const handleImport = async () => {
    // 对外名默认=上游id;改了则用自定义名并把上游id落到 vendor_model
    const models: GwImportItem[] = items.filter(m => checked.has(m.id)).map(m => {
      const alias = (aliases[m.id] || '').trim();
      return alias && alias !== m.id
        ? { model_name: alias, vendor_model: m.id }
        : { model_name: m.id };
    });
    if (models.length === 0) { setError('请至少勾选一个模型'); return; }
    setImporting(true); setError('');
    try {
      const res = await importGwKeyModels(keyId, models);
	  setResult(`导入完成: 新增能力 ${res.abilities_added} · 新增 Transport ${res.transports_added} · 新增元数据 ${res.meta_added}`);
      await load();
      onImported?.();
    } catch (e: any) {
      setError(e?.message || '导入失败');
    } finally {
      setImporting(false);
    }
  };

  const allChecked = items.length > 0 && checked.size === items.length;

  return (
    <Modal open={isOpen} onClose={onClose} title={`拉取上游模型${keyName ? ` · ${keyName}` : ''}`} width="max-w-2xl">
      <div className="modal-form">
       <div className="modal-scroll-body space-y-3">
        <div className="flex items-center justify-between">
          <p className="text-xs text-[var(--text-secondary)]">用该 Key 调上游 <code className="px-1 bg-[var(--surface)] rounded">/v1/models</code>，勾选后写入路由能力(gw_abilities)</p>
          <button onClick={load} disabled={loading} className="flex items-center gap-1 text-xs text-[var(--primary)] hover:opacity-80 disabled:opacity-50">
            <RefreshCw size={13} className={loading ? 'animate-spin' : ''} /> 刷新
          </button>
        </div>
        {error && <div className="flex items-start gap-2 p-2 bg-red-50 text-red-700 rounded-lg text-xs"><AlertCircle size={14} className="mt-0.5 shrink-0" />{error}</div>}
        {result && <div className="p-2 bg-green-50 text-green-700 rounded-lg text-xs">{result}</div>}
        {loading ? (
          <div className="py-10 text-center text-sm text-[var(--text-secondary)]"><RefreshCw size={20} className="animate-spin mx-auto mb-2" /> 拉取中...</div>
        ) : items.length === 0 ? (
          <div className="py-10 text-center text-sm text-[var(--text-secondary)]">暂无上游模型</div>
        ) : (
          <>
            <button onClick={toggleAll} className="flex items-center gap-1.5 text-xs font-medium text-[var(--text-primary)] hover:text-[var(--primary)]">
              {allChecked ? <CheckSquare size={15} /> : <Square size={15} />}
              {allChecked ? '取消全选' : '全选'}
              <span className="text-[var(--text-secondary)]">({checked.size}/{items.length})</span>
            </button>
            <div className="space-y-1 pr-1">
              {items.map(m => {
                const isChecked = checked.has(m.id);
                return (
                  <div key={m.id} className={`rounded-lg border transition-colors ${isChecked ? 'border-[var(--primary)]/40 bg-[var(--primary-lighter)]/40' : 'border-[var(--border-soft)] bg-[var(--surface)]'}`}>
                    <div className="flex items-center gap-2 p-2">
                      <button onClick={() => toggle(m.id)} className="shrink-0 text-[var(--primary)]">
                        {isChecked ? <CheckSquare size={16} /> : <Square size={16} className="text-[var(--text-secondary)]" />}
                      </button>
                      <div className="min-w-0 flex-1">
                        <div className="text-sm font-mono text-[var(--text-primary)] truncate">{m.id}</div>
                      </div>
                      {m.imported && <span className="px-1.5 py-0.5 rounded text-[10px] bg-sky-100 text-sky-700 shrink-0">已导入</span>}
                    </div>
                    {isChecked && (
                      <div className="px-2 pb-2 pl-8">
                        <label className="block text-[10px] text-[var(--text-secondary)] mb-0.5">对外名(默认=上游id;想区分同模型时改成别名,上游真名自动落 vendor_model)</label>
                        <input value={aliases[m.id] ?? m.id}
                          onChange={e => setAliases(prev => ({ ...prev, [m.id]: e.target.value }))}
                          className="w-full px-2 py-1 border border-[var(--border-soft)] rounded text-xs font-mono focus:outline-none focus:ring-1 focus:ring-[var(--primary)]"
                          placeholder={m.id} />
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </>
        )}
       </div>
        <div className="modal-footer">
          <button onClick={onClose} className="modal-button modal-button-secondary">关闭</button>
          <button onClick={handleImport} disabled={importing || loading || checked.size === 0} className="modal-button modal-button-primary">
            <Download size={15} className={importing ? 'animate-pulse' : ''} /> 导入选中 ({checked.size})
          </button>
        </div>
      </div>
    </Modal>
  );
};
