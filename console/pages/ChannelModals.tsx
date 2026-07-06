import React, { useEffect, useState } from 'react';
import { Modal } from '../components/ui/Modal';
import { Channel, ChannelAccount } from '../types';

// 新建/编辑渠道弹窗
export const ChannelModal: React.FC<{
  isOpen: boolean;
  channel?: Channel | null;
  onClose: () => void;
  onSave: (data: any) => Promise<void>;
}> = ({ isOpen, channel, onClose, onSave }) => {
  const [form, setForm] = useState({ type: '', name: '', base_url: '', config: '{}', image_to_base64: false });
  const [loading, setLoading] = useState(false);
  const [jsonError, setJsonError] = useState('');

  useEffect(() => {
    if (channel) {
      const cfg = channel.config || {};
      const { image_to_base64, ...restConfig } = cfg as any;
      setForm({
        type: channel.type,
        name: channel.name,
        base_url: channel.baseUrl,
        config: JSON.stringify(restConfig, null, 2),
        image_to_base64: !!image_to_base64,
      });
    } else {
      setForm({ type: '', name: '', base_url: '', config: '{}', image_to_base64: false });
    }
    setJsonError('');
  }, [channel, isOpen]);

  if (!isOpen) return null;

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
      await onSave({
        type: form.type,
        name: form.name,
        base_url: form.base_url,
        config: { ...JSON.parse(form.config), ...(form.image_to_base64 ? { image_to_base64: true } : {}) }
      });
      onClose();
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal open={true} onClose={onClose} title={channel ? '编辑渠道' : '新建渠道'}>
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
}> = ({ isOpen, channelId, account, availableModels = [], onClose, onSave }) => {
  const [form, setForm] = useState({ name: '', api_key: '', weight: 10, max_tasks: 0, config: '{}' });
  const [supportedModels, setSupportedModels] = useState<string[]>([]);
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
      setSupportedModels(account.supportedModels || []);
    } else {
      setForm({ name: '', api_key: '', weight: 10, max_tasks: 0, config: '{}' });
      setSupportedModels([]);
    }
    setJsonError('');
  }, [account, isOpen]);

  if (!isOpen) return null;

  const toggleModel = (code: string) => {
    setSupportedModels(prev =>
      prev.includes(code) ? prev.filter(c => c !== code) : [...prev, code]
    );
  };

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
        supported_models: supportedModels,
      };
      if (form.api_key) data.api_key = form.api_key;
      await onSave(data);
      onClose();
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal open={true} onClose={onClose} title={account ? '编辑账号' : '新建账号'}>
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
            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">
              支持模型
              <span className="ml-2 text-xs text-[var(--text-secondary)] font-normal">留空 = 支持全部；勾选后仅该 key 支持的模型会命中此账号</span>
            </label>
            {availableModels.length === 0 ? (
              <p className="text-xs text-[var(--text-secondary)] py-2">暂无模型可选</p>
            ) : (
              <div className="max-h-36 overflow-y-auto flex flex-wrap gap-2 p-2 border border-[var(--border-soft)] rounded-lg">
                {availableModels.map(m => {
                  const checked = supportedModels.includes(m.code);
                  return (
                    <button
                      key={m.code}
                      type="button"
                      onClick={() => toggleModel(m.code)}
                      className={`px-2 py-1 rounded-lg text-xs border transition-colors ${checked
                        ? 'bg-[var(--primary)] text-white border-[var(--primary)]'
                        : 'bg-[var(--surface)] text-[var(--text-secondary)] border-[var(--border-soft)] hover:border-[var(--primary)]'}`}
                      title={m.code}
                    >
                      {m.name || m.code}
                    </button>
                  );
                })}
              </div>
            )}
            {supportedModels.length > 0 && (
              <p className="text-xs text-[var(--text-secondary)] mt-1">已选 {supportedModels.length} 个（白名单模式）</p>
            )}
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
