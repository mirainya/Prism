import React, { useMemo, useRef, useState } from 'react';
import { Save } from 'lucide-react';
import { Button, Modal, Select } from '../../components/ui';
import { createUnifiedCatalogSKU, type UnifiedCatalogRelease } from '../../services/unifiedGatewayApi';
import { configurationInputClass as inputClass } from './ConfigurationFields';
import { ErrorNotice, errorMessage } from './Feedback';

const operations = [
  { value: 'chat.completions', label: '聊天补全', method: 'POST', path: '/v1/chat/completions' },
  { value: 'responses.create', label: 'Responses', method: 'POST', path: '/v1/responses' },
  { value: 'video.generate', label: '视频生成', method: 'POST', path: '/v1/videos/generations' },
];

export const CatalogSKUDialog: React.FC<{
  release: UnifiedCatalogRelease;
  onClose: () => void;
  onSaved: () => void;
}> = ({ release, onClose, onSaved }) => {
  const [modelCode, setModelCode] = useState('');
  const [apiName, setAPIName] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [skuCode, setSKUCode] = useState('');
  const [operation, setOperation] = useState('video.generate');
  const [delivery, setDelivery] = useState('reference');
  const [idempotency, setIdempotency] = useState('optional');
  const [tiers, setTiers] = useState('standard');
  const [tags, setTags] = useState('video');
  const [maxResults, setMaxResults] = useState('1');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const locked = useRef(false);
  const selectedOperation = useMemo(() => operations.find(item => item.value === operation)!, [operation]);
  const splitValues = (value: string) => [...new Set(value.split(',').map(item => item.trim().toLowerCase()).filter(Boolean))];
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const maximum = Number(maxResults);
    const identity = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/;
    if (locked.current || !identity.test(modelCode.trim()) || !identity.test(apiName.trim()) || !identity.test(skuCode.trim()) || !displayName.trim() || !Number.isSafeInteger(maximum) || maximum < 1 || maximum > 64 || splitValues(tiers).length === 0) {
      setError('请检查模型、规格标识和结果数量');
      return;
    }
    locked.current = true;
    setPending(true);
    setError('');
    try {
      await createUnifiedCatalogSKU(release.id, {
        expected_version: release.config_version,
        model_code: modelCode.trim(), api_name: apiName.trim(), display_name: displayName.trim(), description: '', visibility: 'visible',
        capability_tags: splitValues(tags), operation_code: operation, contract_version: 1,
        http_method: selectedOperation.method, route_template: selectedOperation.path, normalization_version: 1,
        sku_code: skuCode.trim().toLowerCase(), delivery_mode: delivery, max_results: maximum,
        idempotency_mode: idempotency, service_tiers: splitValues(tiers),
      });
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      locked.current = false;
      setPending(false);
    }
  };
  return <Modal open onClose={() => !pending && onClose()} title="新建公开规格">
    <form onSubmit={submit} className="space-y-4">
      {error && <ErrorNotice message={error} />}
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="模型标识"><input required maxLength={128} value={modelCode} onChange={event => setModelCode(event.target.value)} className={`${inputClass} font-mono`} /></Field>
        <Field label="公开模型名"><input required maxLength={128} value={apiName} onChange={event => setAPIName(event.target.value)} className={`${inputClass} font-mono`} /></Field>
        <Field label="展示名称"><input required maxLength={128} value={displayName} onChange={event => setDisplayName(event.target.value)} className={inputClass} /></Field>
        <Field label="规格标识"><input required maxLength={128} value={skuCode} onChange={event => setSKUCode(event.target.value)} className={`${inputClass} font-mono`} /></Field>
        <Field label="公开操作"><Select value={operation} onChange={setOperation} options={operations} /></Field>
        <Field label="交付方式"><Select value={delivery} onChange={setDelivery} options={[{ value: 'reference', label: '原始引用' }, { value: 'managed_copy', label: '托管副本' }]} /></Field>
        <Field label="幂等策略"><Select value={idempotency} onChange={setIdempotency} options={[{ value: 'optional', label: '可选' }, { value: 'required', label: '必须提供' }, { value: 'forbidden', label: '禁止' }]} /></Field>
        <Field label="最大结果数"><input type="number" min={1} max={64} step={1} value={maxResults} onChange={event => setMaxResults(event.target.value)} className={inputClass} /></Field>
        <Field label="服务等级"><input value={tiers} maxLength={256} onChange={event => setTiers(event.target.value)} className={inputClass} /></Field>
        <Field label="能力标签"><input value={tags} maxLength={512} onChange={event => setTags(event.target.value)} className={inputClass} /></Field>
      </div>
      <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="ghost" disabled={pending} onClick={onClose}>取消</Button><Button type="submit" loading={pending}><Save size={15} />保存</Button></div>
    </form>
  </Modal>;
};

const Field: React.FC<{ label: string; children: React.ReactNode }> = ({ label, children }) => <label className="block min-w-0 space-y-2 text-sm font-semibold"><span>{label}</span>{children}</label>;
