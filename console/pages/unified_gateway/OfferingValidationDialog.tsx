import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Save } from 'lucide-react';
import { Button, Modal, Select } from '../../components/ui';
import { fetchManagedCredentials } from '../../services/unifiedCredentialApi';
import { recordUnifiedOfferingValidation, type UnifiedCatalogProduct, type UnifiedCredential } from '../../services/unifiedGatewayApi';
import { configurationInputClass as inputClass } from './ConfigurationFields';
import { ErrorNotice, errorMessage } from './Feedback';

export const OfferingValidationDialog: React.FC<{
  product: UnifiedCatalogProduct;
  onClose: () => void;
  onSaved: () => void;
}> = ({ product, onClose, onSaved }) => {
  const [credentials, setCredentials] = useState<UnifiedCredential[]>([]);
  const [credentialID, setCredentialID] = useState('');
  const [days, setDays] = useState('7');
  const [evidence, setEvidence] = useState('');
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const locked = useRef(false);

  useEffect(() => {
    const controller = new AbortController();
    fetchManagedCredentials(1, 100, product.credential_pool_id, controller.signal)
      .then(result => {
        if (controller.signal.aborted) return;
        const usable = result.items.filter(item => item.status === 'active' && item.current_version_id && item.purposes?.includes('catalog_discovery'));
        setCredentials(usable);
        setCredentialID(usable[0] ? String(usable[0].id) : '');
      })
      .catch(reason => { if (!controller.signal.aborted) setError(errorMessage(reason)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [product.credential_pool_id]);

  const selected = useMemo(() => credentials.find(item => String(item.id) === credentialID), [credentialID, credentials]);
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const validityDays = Number(days);
    if (locked.current || !selected || !evidence.trim() || !Number.isInteger(validityDays) || validityDays < 1 || validityDays > 30) {
      setError('请选择具备目录发现用途的凭据，并填写验证依据');
      return;
    }
    locked.current = true;
    setPending(true);
    setError('');
    try {
      await recordUnifiedOfferingValidation(product.offering_id, selected.id, {
        state: 'valid',
        valid_until: new Date(Date.now() + validityDays * 86400000).toISOString(),
        evidence: { note: evidence.trim(), product_code: product.product_code, vendor_model: product.vendor_model },
      });
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      locked.current = false;
      setPending(false);
    }
  };

  return <Modal open title="验证上游产品" onClose={() => !pending && onClose()}>
    <form onSubmit={submit} className="space-y-4">
      {error && <ErrorNotice message={error} />}
      <div className="border-y border-[var(--border-soft)] py-3 text-sm">
        <strong>{product.product_code}</strong>
        <div className="mt-1 text-xs text-[var(--text-secondary)]">{product.channel_name} / {product.pool_name} / {product.vendor_model}</div>
      </div>
      <label className="block space-y-2 text-sm font-semibold">
        <span>验证凭据</span>
        <Select value={credentialID} onChange={setCredentialID} disabled={loading || pending} options={credentials.map(item => ({ value: String(item.id), label: item.credential_code }))} placeholder={loading ? '正在读取' : '无可用验证凭据'} />
      </label>
      <label className="block space-y-2 text-sm font-semibold">
        <span>有效期</span>
        <Select value={days} onChange={setDays} disabled={pending} options={[{ value: '1', label: '1 天' }, { value: '7', label: '7 天' }, { value: '30', label: '30 天' }]} />
      </label>
      <label className="block space-y-2 text-sm font-semibold">
        <span>验证依据</span>
        <textarea required rows={4} maxLength={2000} value={evidence} onChange={event => setEvidence(event.target.value)} disabled={pending} className={`${inputClass} min-h-24 resize-y`} />
      </label>
      <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4">
        <Button type="button" variant="ghost" disabled={pending} onClick={onClose}>取消</Button>
        <Button type="submit" loading={pending} disabled={loading || !selected}><Save size={15} />确认有效</Button>
      </div>
    </form>
  </Modal>;
};
