import React, { useRef, useState } from 'react';
import { Save } from 'lucide-react';
import { Button, Modal, Select } from '../../components/ui';
import { createUnifiedRateEvidence, type UnifiedCurrency } from '../../services/unifiedGatewayApi';
import { configurationInputClass as inputClass } from './ConfigurationFields';
import { ErrorNotice, errorMessage } from './Feedback';

const localDateTime = () => {
  const date = new Date(Date.now() - new Date().getTimezoneOffset() * 60000);
  return date.toISOString().slice(0, 16);
};

export const RateEvidenceDialog: React.FC<{
  currencies: UnifiedCurrency[];
  onClose: () => void;
  onSaved: () => void;
}> = ({ currencies, onClose, onSaved }) => {
  const active = currencies.filter(item => item.status === 'active');
  const [sourceType, setSourceType] = useState('vendor_document');
  const [authority, setAuthority] = useState('authoritative');
  const [reference, setReference] = useState('');
  const [observedAt, setObservedAt] = useState(localDateTime());
  const [unit, setUnit] = useState('request');
  const [price, setPrice] = useState('');
  const [currency, setCurrency] = useState(active[0] ? `${active[0].currency_code}:${active[0].definition_version}` : '');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const locked = useRef(false);
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const selected = active.find(item => `${item.currency_code}:${item.definition_version}` === currency);
    const timestamp = new Date(observedAt);
    if (locked.current || !selected || !reference.trim() || Number.isNaN(timestamp.getTime()) || !/^(0|[1-9]\d*)(\.\d+)?$/.test(price)) {
      setError('请检查来源、时间、单位和价格');
      return;
    }
    locked.current = true;
    setPending(true);
    setError('');
    try {
      await createUnifiedRateEvidence({ source_type: sourceType, authority_level: authority, source_reference: reference.trim(), observed_at: timestamp.toISOString(), unit_code: unit, unit_price: price, currency_code: selected.currency_code, currency_version: selected.definition_version });
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      locked.current = false;
      setPending(false);
    }
  };
  return <Modal open onClose={() => !pending && onClose()} title="提交价格证据"><form onSubmit={submit} className="space-y-4">{error && <ErrorNotice message={error} />}<div className="grid gap-4 sm:grid-cols-2"><Field label="来源类型"><Select value={sourceType} onChange={setSourceType} options={[{ value: 'vendor_document', label: '供应商文档' }, { value: 'vendor_api', label: '供应商接口' }, { value: 'contract', label: '商务合同' }, { value: 'manual', label: '人工依据' }]} /></Field><Field label="权威等级"><Select value={authority} onChange={setAuthority} options={[{ value: 'authoritative', label: '权威来源' }, { value: 'corroborating', label: '辅助来源' }, { value: 'manual', label: '人工确认' }]} /></Field><Field label="计量单位"><Select value={unit} onChange={setUnit} options={[{ value: 'request', label: '每次' }, { value: 'second', label: '每秒' }, { value: 'token', label: 'Token' }, { value: 'image', label: '图片' }, { value: 'video', label: '视频' }, { value: 'megapixel', label: '百万像素' }]} /></Field><Field label="币种"><Select value={currency} onChange={setCurrency} options={active.map(item => ({ value: `${item.currency_code}:${item.definition_version}`, label: `${item.currency_code} v${item.definition_version}` }))} /></Field><Field label="单位价格"><input required inputMode="decimal" value={price} onChange={event => setPrice(event.target.value)} className={inputClass} /></Field><Field label="观测时间"><input required type="datetime-local" value={observedAt} onChange={event => setObservedAt(event.target.value)} className={inputClass} /></Field></div><Field label="来源引用"><input required maxLength={512} value={reference} onChange={event => setReference(event.target.value)} placeholder="文档地址或合同编号" className={inputClass} /></Field><div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="ghost" disabled={pending} onClick={onClose}>取消</Button><Button type="submit" loading={pending}><Save size={15} />提交</Button></div></form></Modal>;
};
const Field: React.FC<{ label: string; children: React.ReactNode }> = ({ label, children }) => <label className="block min-w-0 space-y-2 text-sm font-semibold"><span>{label}</span>{children}</label>;
