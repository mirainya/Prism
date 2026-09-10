import React, { useState } from 'react';
import { Save } from 'lucide-react';
import { Button, Modal } from '../../components/ui';
import {
  createUnifiedChannel,
  updateUnifiedChannel,
  createUnifiedPool,
  updateUnifiedPool,
  type UnifiedChannel,
  type UnifiedPool,
} from '../../services/unifiedChannelApi';
import { ErrorNotice, errorMessage } from './Feedback';
import {
  ConcurrencyField as LimitField,
  configurationInputClass as inputClass,
} from './ConfigurationFields';

export type ChannelEditor =
  | { kind: 'channel'; item?: UnifiedChannel }
  | { kind: 'pool'; channelId: number; item?: UnifiedPool };
export const ChannelDialog: React.FC<{
  editor: ChannelEditor;
  readOnly: boolean;
  onClose: () => void;
  onSaved: () => void;
}> = ({ editor, readOnly, onClose, onSaved }) => {
  const [name, setName] = useState(editor.item?.display_name ?? '');
  const [code, setCode] = useState(
    editor.kind === 'channel'
      ? (editor.item?.channel_code ?? '')
      : (editor.item?.pool_code ?? ''),
  );
  const [enabled, setEnabled] = useState(editor.item?.status !== 'disabled');
  const [requests, setRequests] = useState(
    editor.kind === 'pool'
      ? (editor.item?.request_limit?.toString() ?? '')
      : '',
  );
  const [tasks, setTasks] = useState(
    editor.kind === 'pool' ? (editor.item?.task_limit?.toString() ?? '') : '',
  );
  const [pending, setPending] = useState(false);
  const locked = React.useRef(false);
  const [error, setError] = useState('');
  const parseLimit = (value: string) => (value === '' ? null : Number(value));
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (readOnly || locked.current) return;
    if (!name.trim() || !/^[a-z][a-z0-9_.-]{0,127}$/.test(code)) {
      setError('请填写名称及有效标识');
      return;
    }
    const limits = {
      request_limit: parseLimit(requests),
      task_limit: parseLimit(tasks),
    };
    if (
      Object.values(limits).some(
        (value) =>
          value !== null &&
          (!Number.isSafeInteger(value) || value < 1 || value > 1000000),
      )
    ) {
      setError('并发数须为 1 到 1000000 的整数');
      return;
    }
    locked.current = true;
    setPending(true);
    setError('');
    try {
      if (editor.kind === 'channel') {
        if (editor.item)
          await updateUnifiedChannel(editor.item, {
            display_name: name.trim(),
            status: enabled ? 'active' : 'disabled',
          });
        else
          await createUnifiedChannel({
            channel_code: code,
            display_name: name.trim(),
          });
      } else {
        const data = { display_name: name.trim(), ...limits };
        if (editor.item) await updateUnifiedPool(editor.item, data);
        else
          await createUnifiedPool(editor.channelId, {
            ...data,
            pool_code: code,
          });
      }
      onSaved();
    } catch (err: unknown) {
      setError(errorMessage(err));
    } finally {
      locked.current = false;
      setPending(false);
    }
  };
  return (
    <Modal
      open
      onClose={() => {
        if (!pending) onClose();
      }}
      title={`${editor.item ? '编辑' : '新建'}${editor.kind === 'channel' ? '渠道' : '凭据池'}`}
    >
      <form onSubmit={submit} className="space-y-4">
        {error && <ErrorNotice message={error} />}
        <label className="block space-y-2 text-sm font-semibold">
          <span>名称</span>
          <input
            className={inputClass}
            required
            maxLength={128}
            value={name}
            disabled={pending}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label className="block space-y-2 text-sm font-semibold">
          <span>标识</span>
          <input
            className={`${inputClass} font-mono`}
            required
            pattern="[a-z][a-z0-9_.\-]{0,127}"
            maxLength={128}
            value={code}
            disabled={pending || Boolean(editor.item)}
            onChange={(event) => setCode(event.target.value)}
          />
        </label>
        {editor.kind === 'channel' && editor.item && (
          <label className="flex items-center gap-3 text-sm">
            <input
              type="checkbox"
              checked={enabled}
              disabled={pending}
              onChange={(event) => setEnabled(event.target.checked)}
              className="h-4 w-4 accent-[var(--primary)]"
            />
            启用渠道
          </label>
        )}
        {editor.kind === 'pool' && (
          <div className="grid gap-4 sm:grid-cols-2">
            <LimitField
              label="请求并发"
              value={requests}
              onChange={setRequests}
              pending={pending}
            />
            <LimitField
              label="任务并发"
              value={tasks}
              onChange={setTasks}
              pending={pending}
            />
          </div>
        )}
        <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4">
          <Button
            type="button"
            variant="ghost"
            disabled={pending}
            onClick={onClose}
          >
            取消
          </Button>
          <Button type="submit" loading={pending} disabled={readOnly}>
            <Save size={15} />
            保存
          </Button>
        </div>
      </form>
    </Modal>
  );
};
