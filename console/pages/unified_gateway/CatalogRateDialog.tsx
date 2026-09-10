import React, { useMemo, useRef, useState } from 'react';
import { Save } from 'lucide-react';
import { Button, Modal, Select } from '../../components/ui';
import {
  createUnifiedCatalogRate,
  type UnifiedCatalogProduct,
  type UnifiedCatalogRelease,
  type UnifiedCatalogSKU,
  type UnifiedRateEvidence,
} from '../../services/unifiedGatewayApi';
import { configurationInputClass as inputClass } from './ConfigurationFields';
import { ErrorNotice, errorMessage } from './Feedback';

const quantityOptions = [
  { value: 'one', label: '每次请求', unit: 'request', step: '0', max: '1' },
  { value: 'request.seconds', label: '请求秒数', unit: 'second', step: '1', max: '60' },
  { value: 'result.seconds', label: '结果秒数', unit: 'second', step: '1', max: '60' },
  { value: 'usage.input_tokens', label: '输入 Token', unit: 'token', step: '1', max: '200000' },
  { value: 'usage.output_tokens', label: '输出 Token', unit: 'token', step: '1', max: '200000' },
  { value: 'usage.uncached_input_tokens', label: '未缓存输入 Token', unit: 'token', step: '1', max: '200000' },
  { value: 'usage.cached_input_tokens', label: '缓存输入 Token', unit: 'token', step: '1', max: '200000' },
  { value: 'request.images', label: '请求图片数', unit: 'image', step: '1', max: '16' },
  { value: 'result.images', label: '结果图片数', unit: 'image', step: '1', max: '16' },
  { value: 'result.videos', label: '结果视频数', unit: 'video', step: '1', max: '4' },
  { value: 'result.megapixels', label: '结果百万像素', unit: 'megapixel', step: '0.01', max: '100' },
];

const eventOptions = [
  { value: 'call.succeeded', label: '生成成功' },
  { value: 'provider.accepted', label: '上游接受' },
  { value: 'delivery.ready', label: '交付可用' },
  { value: 'call.failed', label: '生成失败' },
  { value: 'call.cancelled', label: '任务取消' },
  { value: 'delivery.failed', label: '交付失败' },
];

export const CatalogRateDialog: React.FC<{
  release: UnifiedCatalogRelease;
  kind: 'sell' | 'cost';
  skus: UnifiedCatalogSKU[];
  products: UnifiedCatalogProduct[];
  evidence: UnifiedRateEvidence[];
  onClose: () => void;
  onSaved: () => void;
}> = ({ release, kind, skus, products, evidence, onClose, onSaved }) => {
  const parents = kind === 'sell'
    ? skus.map(item => ({ value: String(item.id), label: `${item.display_name} / ${item.sku_code}` }))
    : products.map(item => ({ value: String(item.cost_plan_id), label: `${item.product_code} / ${item.cost_plan_code}` }));
  const [parentID, setParentID] = useState(parents[0]?.value || '');
  const [evidenceID, setEvidenceID] = useState(evidence[0] ? String(evidence[0].id) : '');
  const [component, setComponent] = useState('base');
  const [source, setSource] = useState('one');
  const [event, setEvent] = useState('call.succeeded');
  const [scale, setScale] = useState('0');
  const [step, setStep] = useState('0');
  const [maximum, setMaximum] = useState('1');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const locked = useRef(false);
  const selectedEvidence = useMemo(() => evidence.find(item => String(item.id) === evidenceID), [evidence, evidenceID]);
  const selectedQuantity = useMemo(() => quantityOptions.find(item => item.value === source)!, [source]);
  const changeSource = (value: string) => {
    const item = quantityOptions.find(option => option.value === value)!;
    setSource(value);
    setStep(item.step);
    setMaximum(item.max);
  };
  const submit = async (submitEvent: React.FormEvent) => {
    submitEvent.preventDefault();
    const unitScale = Number(scale);
    if (locked.current || !parentID || !selectedEvidence || selectedEvidence.unit_code !== selectedQuantity.unit || !/^[a-z][a-z0-9_]{0,31}$/.test(component) || !Number.isInteger(unitScale) || unitScale < 0 || unitScale > 12 || !/^(0|[1-9]\d*)(\.\d+)?$/.test(step) || !/^[1-9]\d*(\.\d+)?$/.test(maximum)) {
      setError(selectedEvidence && selectedEvidence.unit_code !== selectedQuantity.unit ? `该证据单位为 ${selectedEvidence.unit_code}` : '请检查计费组件参数');
      return;
    }
    locked.current = true;
    setPending(true);
    setError('');
    try {
      await createUnifiedCatalogRate(release.id, kind, {
        parent_id: Number(parentID), expected_version: release.config_version, evidence_id: selectedEvidence.id,
        component_code: component, quantity_source: source, charge_event: event,
        unit_scale: unitScale, quantity_step: step, max_quantity: maximum,
      });
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      locked.current = false;
      setPending(false);
    }
  };
  return <Modal open onClose={() => !pending && onClose()} title={`新建${kind === 'sell' ? '售价' : '成本'}组件`}>
    <form onSubmit={submit} className="space-y-4">
      {error && <ErrorNotice message={error} />}
      <Field label={kind === 'sell' ? '公开规格' : '成本方案'}><Select value={parentID} onChange={setParentID} options={parents} /></Field>
      <Field label="已审核价格证据"><Select value={evidenceID} onChange={setEvidenceID} options={evidence.map(item => ({ value: String(item.id), label: `#${item.id} · ${item.unit_price} ${item.currency_code}/${item.unit_code}` }))} /></Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="组件标识"><input required maxLength={32} value={component} onChange={input => setComponent(input.target.value.toLowerCase())} className={`${inputClass} font-mono`} /></Field>
        <Field label="计量来源"><Select value={source} onChange={changeSource} options={quantityOptions} /></Field>
        <Field label="计费事件"><Select value={event} onChange={setEvent} options={eventOptions} /></Field>
        <Field label="价格数量级"><input type="number" min={0} max={12} step={1} value={scale} onChange={input => setScale(input.target.value)} className={inputClass} /></Field>
        <Field label="数量步长"><input inputMode="decimal" value={step} onChange={input => setStep(input.target.value)} className={inputClass} /></Field>
        <Field label="预授权上限"><input inputMode="decimal" value={maximum} onChange={input => setMaximum(input.target.value)} className={inputClass} /></Field>
      </div>
      <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="ghost" disabled={pending} onClick={onClose}>取消</Button><Button type="submit" loading={pending}><Save size={15} />保存</Button></div>
    </form>
  </Modal>;
};

const Field: React.FC<{ label: string; children: React.ReactNode }> = ({ label, children }) => <label className="block min-w-0 space-y-2 text-sm font-semibold"><span>{label}</span>{children}</label>;
