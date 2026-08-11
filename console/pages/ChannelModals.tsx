import React, { useEffect, useState } from 'react';
import { Modal } from '../components/ui/Modal';
import { Select } from '../components/ui';
import { Channel, ChannelAccount } from '../types';

// 新建/编辑渠道弹窗
export const ChannelModal: React.FC<{
  isOpen: boolean;
  channel?: Channel | null;
  onClose: () => void;
  onSave: (data: any) => Promise<void>;
}> = ({ isOpen, channel, onClose, onSave }) => {
  const [form, setForm] = useState({ type: '', name: '', base_url: '', config: '{}', image_to_base64: false,
    discoveryEnabled: false, discoveryPath: '/v1/models', generationPath: '/v1/images/generations', editPath: '/v1/images/edits',
    authLocation: 'header', authKey: 'Authorization', authValuePrefix: 'Bearer ', generate: true, edit: true });
  const [loading, setLoading] = useState(false);
  const [jsonError, setJsonError] = useState('');
  const [discoveryError, setDiscoveryError] = useState('');

  useEffect(() => {
    if (channel) {
      const cfg = channel.config || {};
      const { image_to_base64, endpoint_discovery, ...restConfig } = cfg as any;
      const discovery = endpoint_discovery || {};
      setForm({
        type: channel.type,
        name: channel.name,
        base_url: channel.baseUrl,
        config: JSON.stringify(restConfig, null, 2),
        image_to_base64: !!image_to_base64,
        discoveryEnabled: !!discovery.enabled,
        discoveryPath: discovery.discovery_path || '/v1/models',
        generationPath: discovery.generation_path || '/v1/images/generations',
        editPath: discovery.edit_path || '/v1/images/edits',
        authLocation: discovery.auth_location || 'header',
        authKey: discovery.auth_key || 'Authorization',
        authValuePrefix: discovery.auth_value_prefix ?? 'Bearer ',
        generate: (discovery.operations || ['images.generate', 'images.edit']).includes('images.generate'),
        edit: (discovery.operations || ['images.generate', 'images.edit']).includes('images.edit'),
      });
    } else {
      setForm({ type: '', name: '', base_url: '', config: '{}', image_to_base64: false, discoveryEnabled: false, discoveryPath: '/v1/models', generationPath: '/v1/images/generations', editPath: '/v1/images/edits', authLocation: 'header', authKey: 'Authorization', authValuePrefix: 'Bearer ', generate: true, edit: true });
    }
    setJsonError('');
    setDiscoveryError('');
  }, [channel, isOpen]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (form.discoveryEnabled && !form.generate && !form.edit) {
      setDiscoveryError('至少选择一个可导入操作');
      return;
    }
    setDiscoveryError('');
    try {
      JSON.parse(form.config);
      setJsonError('');
    } catch {
      setJsonError('JSON 格式错误');
      return;
    }
    setLoading(true);
    try {
      const config = JSON.parse(form.config);
      if (form.discoveryEnabled) {
        config.endpoint_discovery = {
          enabled: true, adapter: 'openai.images', discovery_path: form.discoveryPath,
          generation_path: form.generationPath, edit_path: form.editPath,
          operations: [form.generate ? 'images.generate' : '', form.edit ? 'images.edit' : ''].filter(Boolean),
          auth_location: form.authLocation, auth_key: form.authKey, auth_value_prefix: form.authValuePrefix,
        };
      } else {
        delete config.endpoint_discovery;
      }
      await onSave({
        type: form.type,
        name: form.name,
        base_url: form.base_url,
        config: { ...config, ...(form.image_to_base64 ? { image_to_base64: true } : {}) }
      });
      onClose();
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal open={isOpen} onClose={onClose} title={channel ? '编辑渠道' : '新建渠道'}>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">渠道标识</label>
            <input
              type="text"
              value={form.type}
              onChange={e => setForm({ ...form, type: e.target.value })}
              disabled={!!channel}
              className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)] disabled:bg-[var(--surface)]"
              placeholder="如: duomi, openai"
              required
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">渠道名称</label>
            <input
              type="text"
              value={form.name}
              onChange={e => setForm({ ...form, name: e.target.value })}
              className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
              placeholder="如: 多米API"
              required
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">基础 URL</label>
            <input
              type="text"
              value={form.base_url}
              onChange={e => setForm({ ...form, base_url: e.target.value })}
              className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
              placeholder="如: https://duomiapi.com"
              required
            />
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="image_to_base64"
              checked={form.image_to_base64}
              onChange={e => setForm({ ...form, image_to_base64: e.target.checked })}
              className="w-4 h-4 rounded border-[var(--border-soft)] text-[var(--primary)] focus:ring-[var(--primary)]"
            />
            <label htmlFor="image_to_base64" className="text-sm text-[var(--text-primary)]">图片转 Base64</label>
            <span className="text-xs text-[var(--text-tertiary)]">上游无法访问图片 URL 时启用</span>
          </div>
          <div className="rounded-lg border border-[var(--border-soft)] p-3 space-y-3">
            <label className="flex items-center gap-2 text-sm font-medium text-[var(--text-primary)]"><input type="checkbox" checked={form.discoveryEnabled} onChange={e => { setForm({ ...form, discoveryEnabled: e.target.checked }); setDiscoveryError(''); }} />启用 Key 级模型发现</label>
            {form.discoveryEnabled && <div className="grid gap-2 md:grid-cols-2">
              <label className="text-xs text-[var(--text-secondary)]">模型列表路径<input value={form.discoveryPath} onChange={e => setForm({ ...form, discoveryPath: e.target.value })} className="mt-1 w-full rounded border border-[var(--border-soft)] px-2 py-1.5 text-sm" /></label>
              <label className="text-xs text-[var(--text-secondary)]">生成路径<input value={form.generationPath} onChange={e => setForm({ ...form, generationPath: e.target.value })} className="mt-1 w-full rounded border border-[var(--border-soft)] px-2 py-1.5 text-sm" /></label>
              <label className="text-xs text-[var(--text-secondary)]">编辑路径<input value={form.editPath} onChange={e => setForm({ ...form, editPath: e.target.value })} className="mt-1 w-full rounded border border-[var(--border-soft)] px-2 py-1.5 text-sm" /></label>
              <div className="text-xs text-[var(--text-secondary)]"><span className="block mb-1">认证位置</span><Select value={form.authLocation} onChange={v => setForm({ ...form, authLocation: v })} options={[{ label: 'Header', value: 'header' }, { label: 'Query', value: 'query' }]} /></div>
              <label className="text-xs text-[var(--text-secondary)]">认证字段<input value={form.authKey} onChange={e => setForm({ ...form, authKey: e.target.value })} className="mt-1 w-full rounded border border-[var(--border-soft)] px-2 py-1.5 text-sm" /></label>
              <label className="text-xs text-[var(--text-secondary)]">认证前缀<input value={form.authValuePrefix} onChange={e => setForm({ ...form, authValuePrefix: e.target.value })} className="mt-1 w-full rounded border border-[var(--border-soft)] px-2 py-1.5 text-sm" /></label>
              <div className="flex items-center gap-4 text-xs text-[var(--text-primary)] md:col-span-2"><span>可导入操作</span><label className="inline-flex items-center gap-1"><input type="checkbox" checked={form.generate} onChange={e => { setForm({ ...form, generate: e.target.checked }); setDiscoveryError(''); }} />生成</label><label className="inline-flex items-center gap-1"><input type="checkbox" checked={form.edit} onChange={e => { setForm({ ...form, edit: e.target.checked }); setDiscoveryError(''); }} />编辑</label></div>
              {discoveryError && <p className="text-xs text-red-500 md:col-span-2">{discoveryError}</p>}
            </div>}
          </div>
          <div>
            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">渠道配置 (JSON)</label>
            <textarea
              value={form.config}
              onChange={e => setForm({ ...form, config: e.target.value })}
              className={`w-full px-3 py-2 border rounded-lg font-mono text-xs focus:outline-none focus:ring-2 focus:ring-[var(--primary)] ${jsonError ? 'border-red-300' : 'border-[var(--border-soft)]'}`}
              placeholder='{"timeout": 30, "retry": 3}'
              rows={3}
            />
            {jsonError && <p className="text-xs text-red-500 mt-1">{jsonError}</p>}
          </div>
          <div className="flex justify-end gap-3 pt-4">
            <button type="button" onClick={onClose} className="px-4 py-2 text-sm font-bold text-[var(--text-secondary)] bg-[var(--primary-lighter)] rounded-lg hover:bg-gray-200 transition-colors">取消</button>
            <button type="submit" disabled={loading} className="px-4 py-2 text-sm font-bold text-white bg-[var(--primary)] rounded-lg hover:opacity-90 disabled:opacity-50 transition-colors">
              {loading ? '保存中...' : '保存'}
            </button>
          </div>
        </form>
    </Modal>
  );
};

// 新建/编辑账号弹窗
export const AccountModal: React.FC<{
  isOpen: boolean;
  channelId: string;
  account?: ChannelAccount | null;
  availableModels?: { code: string; name: string; type?: string }[];
  onClose: () => void;
  onSave: (data: any) => Promise<void>;
}> = ({ isOpen, channelId, account, onClose, onSave }) => {
  const [form, setForm] = useState({ name: '', api_key: '', weight: 10, max_tasks: 0, config: '{}' });
  const [loading, setLoading] = useState(false);
  const [jsonError, setJsonError] = useState('');

  useEffect(() => {
    if (account) {
      setForm({
        name: account.name,
          api_key: '',
        weight: account.weight,
        max_tasks: account.maxTasks || 0,
        config: JSON.stringify(account.config || {}, null, 2)
      });
    } else {
      setForm({ name: '', api_key: '', weight: 10, max_tasks: 0, config: '{}' });
    }
    setJsonError('');
  }, [account, isOpen]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      JSON.parse(form.config);
      setJsonError('');
    } catch {
      setJsonError('JSON 格式错误');
      return;
    }
    setLoading(true);
    try {
      const data: any = {
        channel_id: Number(channelId),
        name: form.name,
        weight: form.weight,
        max_tasks: form.max_tasks,
        config: JSON.parse(form.config),
      };
      if (form.api_key) data.api_key = form.api_key;
      await onSave(data);
      onClose();
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal open={isOpen} onClose={onClose} title={account ? '编辑账号' : '新建账号'}>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">账号名称</label>
            <input
              type="text"
              value={form.name}
              onChange={e => setForm({ ...form, name: e.target.value })}
              className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
              placeholder="如: 主账号"
              required
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">API Key</label>
            <input
                type="text"
              value={form.api_key}
              onChange={e => setForm({ ...form, api_key: e.target.value })}
              className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                placeholder={account ? `当前: ${account.maskedKey}，留空则不修改` : '输入 API Key'}
                required={!account}
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">权重</label>
            <input
              type="number"
              value={form.weight}
              onChange={e => setForm({ ...form, weight: Number(e.target.value) })}
              className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
              min={1}
              max={100}
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">最大并发数</label>
            <input
              type="number"
              value={form.max_tasks}
              onChange={e => setForm({ ...form, max_tasks: Number(e.target.value) })}
              className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
              min={0}
              placeholder="0 表示不限制"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">账号配置 (JSON)</label>
            <textarea
              value={form.config}
              onChange={e => setForm({ ...form, config: e.target.value })}
              className={`w-full px-3 py-2 border rounded-lg font-mono text-xs focus:outline-none focus:ring-2 focus:ring-[var(--primary)] ${jsonError ? 'border-red-300' : 'border-[var(--border-soft)]'}`}
              placeholder='{"extra_headers": {}, "rate_limit": 100}'
              rows={3}
            />
            {jsonError && <p className="text-xs text-red-500 mt-1">{jsonError}</p>}
          </div>
          <div className="flex justify-end gap-3 pt-4">
            <button type="button" onClick={onClose} className="px-4 py-2 text-sm font-bold text-[var(--text-secondary)] bg-[var(--primary-lighter)] rounded-lg hover:bg-gray-200 transition-colors">取消</button>
            <button type="submit" disabled={loading} className="px-4 py-2 text-sm font-bold text-white bg-[var(--primary)] rounded-lg hover:opacity-90 disabled:opacity-50 transition-colors">
              {loading ? '保存中...' : '保存'}
            </button>
          </div>
        </form>
    </Modal>
  );
};
