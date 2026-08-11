import React, { useEffect, useState } from 'react';
import { VideoChannelKey } from '../../services/videoApi';
import { Modal, Select } from '../../components/ui';

const inputClass = 'w-full px-3 py-2 rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]';
const btnCancel = 'px-4 py-2 text-sm font-bold text-[var(--text-secondary)] bg-[var(--primary-lighter)] rounded-lg hover:bg-gray-200 transition-all';
const btnSave = 'px-5 py-2 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90 disabled:opacity-50 transition-all';

interface KeyModalProps {
  isOpen: boolean;
  channelKey: VideoChannelKey | null;
  onClose: () => void;
  onSave: (data: any) => Promise<void>;
}

export const VideoKeyModal: React.FC<KeyModalProps> = ({ isOpen, channelKey, onClose, onSave }) => {
  const [form, setForm] = useState({ api_key: '', label: '', weight: 1, max_concurrency: 3, status: 'active' });
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!isOpen) return;
    setForm(channelKey
      ? { api_key: '', label: channelKey.label, weight: channelKey.weight, max_concurrency: channelKey.max_concurrency, status: channelKey.status }
      : { api_key: '', label: '', weight: 1, max_concurrency: 3, status: 'active' });
  }, [isOpen, channelKey]);

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    setSaving(true);
    try {
      await onSave(channelKey
        ? { label: form.label, weight: form.weight, max_concurrency: form.max_concurrency, status: form.status }
        : form);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={isOpen} onClose={onClose} title={channelKey ? '编辑 Key' : '添加 Key'} width="max-w-md">
        <form onSubmit={handleSubmit} className="space-y-4">
          {!channelKey && (
            <div>
              <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">API Key</label>
              <input value={form.api_key} onChange={event => setForm(current => ({ ...current, api_key: event.target.value }))} required className={`${inputClass} font-mono`} />
            </div>
          )}
          <div>
            <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">标签</label>
            <input value={form.label} onChange={event => setForm(current => ({ ...current, label: event.target.value }))} placeholder="可选" className={inputClass} />
          </div>
          <div className="grid grid-cols-3 gap-4">
            <div>
              <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">权重</label>
              <input type="number" value={form.weight} onChange={event => setForm(current => ({ ...current, weight: Number(event.target.value) }))} min={0} className={inputClass} />
            </div>
            <div>
              <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">最大并发</label>
              <input type="number" value={form.max_concurrency} onChange={event => setForm(current => ({ ...current, max_concurrency: Number(event.target.value) }))} min={0} className={inputClass} />
            </div>
            <div>
              <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">状态</label>
              <Select value={form.status} onChange={value => setForm(current => ({ ...current, status: value }))}
                options={[{ label: '启用', value: 'active' }, { label: '停用', value: 'inactive' }]} />
            </div>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={onClose} className={btnCancel}>取消</button>
            <button type="submit" disabled={saving} className={btnSave}>{saving ? '保存中...' : '保存'}</button>
          </div>
        </form>
    </Modal>
  );
};
