import React, { useEffect, useRef, useState } from 'react';
import { Plus } from 'lucide-react';
import { Button, Modal } from '../../components/ui';
import { createUnifiedDeployment } from '../../services/unifiedGatewayApi';
import { ErrorNotice, errorMessage } from './Feedback';

const inputClass = 'w-full min-w-0 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 py-2.5 text-sm text-[var(--text-primary)] outline-none focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--focus-ring)]';

export const DeploymentDialog: React.FC<{ open: boolean; nextGeneration: number; onClose: () => void; onCreated: () => void }> = ({ open, nextGeneration, onClose, onCreated }) => {
  const [generation, setGeneration] = useState(nextGeneration);
  const [version, setVersion] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const locked = useRef(false);
  useEffect(() => {
    if (open) { setGeneration(nextGeneration); setVersion(''); setError(''); }
  }, [open, nextGeneration]);

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (locked.current) return;
    if (!Number.isSafeInteger(generation) || generation < 1 || !version.trim()) {
      setError('请填写有效代次和语义版本');
      return;
    }
    locked.current = true;
    setPending(true);
    setError('');
    try {
      await createUnifiedDeployment({ generation_no: generation, semantic_version: version.trim() });
      onCreated();
    } catch (err: unknown) {
      setError(errorMessage(err));
    } finally {
      locked.current = false;
      setPending(false);
    }
  };

  return <Modal open={open} onClose={() => { if (!pending) onClose(); }} title="新建部署代次">
    <form onSubmit={submit} className="space-y-4">
      {error && <ErrorNotice message={error} />}
      <label className="block space-y-2 text-sm font-semibold text-[var(--text-primary)]"><span>代次编号</span><input type="number" min={1} step={1} required value={generation} onChange={event => setGeneration(Number(event.target.value))} disabled={pending} className={inputClass} /></label>
      <label className="block space-y-2 text-sm font-semibold text-[var(--text-primary)]"><span>语义版本</span><input required maxLength={64} value={version} onChange={event => setVersion(event.target.value)} disabled={pending} className={inputClass} /></label>
      <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="ghost" disabled={pending} onClick={onClose}>取消</Button><Button type="submit" loading={pending}><Plus size={15} />创建</Button></div>
    </form>
  </Modal>;
};
