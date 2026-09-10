import React, { useRef, useState } from 'react';
import { Plus } from 'lucide-react';
import { Button, Modal } from '../../components/ui';
import { createUnifiedCatalog } from '../../services/unifiedGatewayApi';
import { configurationInputClass as inputClass } from './ConfigurationFields';
import { ErrorNotice, errorMessage } from './Feedback';

export const CatalogDraftDialog: React.FC<{
  open: boolean;
  onClose: () => void;
  onCreated: () => void;
}> = ({ open, onClose, onCreated }) => {
  const [version, setVersion] = useState('1.0.0');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const locked = useRef(false);
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const value = version.trim();
    if (locked.current || !/^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$/.test(value)) {
      setError('语义版本格式无效');
      return;
    }
    locked.current = true;
    setPending(true);
    setError('');
    try {
      await createUnifiedCatalog({ semantic_version: value });
      onCreated();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      locked.current = false;
      setPending(false);
    }
  };
  return <Modal open={open} onClose={() => !pending && onClose()} title="新建目录草稿">
    <form onSubmit={submit} className="space-y-4">
      {error && <ErrorNotice message={error} />}
      <label className="block space-y-2 text-sm font-semibold"><span>语义版本</span><input autoFocus required maxLength={64} value={version} disabled={pending} onChange={event => setVersion(event.target.value)} className={inputClass} /></label>
      <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="ghost" disabled={pending} onClick={onClose}>取消</Button><Button type="submit" loading={pending}><Plus size={15} />创建</Button></div>
    </form>
  </Modal>;
};
