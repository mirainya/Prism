import React, { useMemo, useRef, useState } from 'react';
import { Braces, Save } from 'lucide-react';
import { Button, Modal, Select } from '../../components/ui';
import {
  createUnifiedCatalogProduct,
  type UnifiedCatalogOptions,
  type UnifiedCatalogRelease,
} from '../../services/unifiedGatewayApi';
import { configurationInputClass as inputClass } from './ConfigurationFields';
import {
  buildCatalogAllowedHosts,
  catalogDefaultRequestPath,
  catalogRequestMethods,
  catalogTaskPolicy,
  genericCatalogTemplate,
  isVideoCatalogAdapter,
  parseCatalogConstraints,
} from './catalogProductForm';
import { ErrorNotice, errorMessage } from './Feedback';

export const CatalogProductDialog: React.FC<{
  release: UnifiedCatalogRelease;
  options: UnifiedCatalogOptions;
  onClose: () => void;
  onSaved: () => void;
}> = ({ release, options, onClose, onSaved }) => {
  const initialAdapterCode = options.adapters[0]?.code || '';
  const initialPolicy = catalogTaskPolicy(initialAdapterCode);
  const initialRequestPath = catalogDefaultRequestPath(initialAdapterCode);
  const [channelPool, setChannelPool] = useState(options.channels[0] ? `${options.channels[0].id}:${options.channels[0].pool_id}` : '');
  const [adapter, setAdapter] = useState(options.adapters[0] ? `${options.adapters[0].code}@${options.adapters[0].version}` : '');
  const [productCode, setProductCode] = useState('');
  const [vendorModel, setVendorModel] = useState('');
  const [transportCode, setTransportCode] = useState('');
  const [baseURL, setBaseURL] = useState('');
  const [requestMethod, setRequestMethod] = useState('POST');
  const [requestPath, setRequestPath] = useState(initialRequestPath);
  const [taskScope, setTaskScope] = useState(initialPolicy.taskScope);
  const [cancelMode, setCancelMode] = useState(initialPolicy.cancelMode);
  const [sourcePolicy, setSourcePolicy] = useState('fixed');
  const [scopeKind, setScopeKind] = useState('credential_pool');
  const [scopeKey, setScopeKey] = useState(options.channels[0]?.pool_code || 'default');
  const [costPlan, setCostPlan] = useState('standard');
  const [timeout, setTimeoutValue] = useState('30000');
  const [resultHosts, setResultHosts] = useState('');
  const [constraints, setConstraints] = useState(initialAdapterCode === 'generic' ? genericCatalogTemplate('POST', initialRequestPath) : '{}');
  const [selectedSKUs, setSelectedSKUs] = useState<number[]>(options.skus[0] ? [options.skus[0].id] : []);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const locked = useRef(false);
  const selectedAdapter = useMemo(() => options.adapters.find(item => `${item.code}@${item.version}` === adapter), [adapter, options.adapters]);
  const selectedPool = useMemo(() => options.channels.find(item => `${item.id}:${item.pool_id}` === channelPool), [channelPool, options.channels]);
  const videoAdapter = Boolean(selectedAdapter && isVideoCatalogAdapter(selectedAdapter.code));
  const methodOptions = useMemo(() => catalogRequestMethods(selectedAdapter?.code || ''), [selectedAdapter?.code]);
  const selectPool = (value: string) => {
    setChannelPool(value);
    const pool = options.channels.find(item => `${item.id}:${item.pool_id}` === value);
    if (scopeKind === 'credential_pool' && pool?.pool_code) setScopeKey(pool.pool_code);
  };
  const selectAdapter = (value: string) => {
    const next = options.adapters.find(item => `${item.code}@${item.version}` === value);
    setAdapter(value);
    const methods = catalogRequestMethods(next?.code || '');
    const method = methods.includes(requestMethod) ? requestMethod : 'POST';
    if (method !== requestMethod) setRequestMethod(method);
    const policy = catalogTaskPolicy(next?.code || '');
    const nextPath = catalogDefaultRequestPath(next?.code || '');
    setTaskScope(policy.taskScope);
    setCancelMode(policy.cancelMode);
    setRequestPath(nextPath);
    if (next?.code === 'generic' && constraints.trim() === '{}') {
      setConstraints(genericCatalogTemplate(method, nextPath));
    }
  };
  const toggleSKU = (id: number) => setSelectedSKUs(current => current.includes(id) ? current.filter(value => value !== id) : [...current, id]);
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const timeoutMS = Number(timeout);
    const identity = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/;
    if (locked.current || !selectedAdapter || !selectedPool?.pool_id || !identity.test(productCode.trim()) || !identity.test(vendorModel.trim()) || !identity.test(transportCode.trim()) || !identity.test(costPlan.trim()) || selectedSKUs.length === 0 || !Number.isSafeInteger(timeoutMS) || timeoutMS < 100 || timeoutMS > 300000 || !scopeKey.trim()) {
      setError('请检查产品、线路、凭据池和规格配置');
      return;
    }
    let hosts;
    let parsedConstraints;
    try {
      hosts = buildCatalogAllowedHosts(baseURL, resultHosts);
      parsedConstraints = parseCatalogConstraints(constraints, selectedAdapter.code, requestMethod, requestPath.trim());
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '上游地址格式无效');
      return;
    }
    const actions = taskScope === 'task' ? [
      { action_code: 'submit', allowed_source_state: 'allocated', idempotency_mode: 'none', request_schema_version: 1, response_schema_version: 1 },
      { action_code: 'query', allowed_source_state: 'nonterminal', idempotency_mode: 'none', request_schema_version: 1, response_schema_version: 1 },
      ...(cancelMode === 'upstream' ? [{ action_code: 'cancel', allowed_source_state: 'nonterminal', idempotency_mode: 'none', request_schema_version: 1, response_schema_version: 1 }] : []),
    ] : [];
    locked.current = true;
    setPending(true);
    setError('');
    try {
      await createUnifiedCatalogProduct(release.id, {
        expected_version: release.config_version,
        channel_id: selectedPool.id,
        credential_pool_id: selectedPool.pool_id,
        product_code: productCode.trim().toLowerCase(), vendor_model: vendorModel.trim(),
        capability_constraints: parsedConstraints, constraints_schema_version: 1,
        adapter_code: selectedAdapter.code, adapter_version: selectedAdapter.version,
        transport_code: transportCode.trim().toLowerCase(), base_url: baseURL.trim(), protocol: selectedAdapter.protocol,
        request_method: requestMethod, request_path: requestPath.trim(), auth_scheme: 'bearer',
        transport_timeout_ms: timeoutMS, task_timeout_ms: timeoutMS,
        task_scope: taskScope, cancel_mode: cancelMode, source_url_policy: sourcePolicy,
        upstream_scope_kind: scopeKind, upstream_scope_key: scopeKey.trim(),
        allowed_hosts: hosts, actions, cost_plan_code: costPlan.trim().toLowerCase(),
        routes: selectedSKUs.map(sku_id => ({ sku_id, priority: 100, weight: 1 })),
      });
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      locked.current = false;
      setPending(false);
    }
  };
  return <Modal open width="max-w-4xl" onClose={() => !pending && onClose()} title="新建上游产品">
    <form onSubmit={submit} className="modal-form">
      <div className="modal-scroll-body space-y-5">
        {error && <ErrorNotice message={error} />}
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Field label="渠道与凭据池"><Select value={channelPool} onChange={selectPool} options={options.channels.map(item => ({ value: `${item.id}:${item.pool_id}`, label: `${item.name} / ${item.pool_name}` }))} /></Field>
        <Field label="适配器"><Select value={adapter} onChange={selectAdapter} options={options.adapters.map(item => ({ value: `${item.code}@${item.version}`, label: `${item.code} v${item.version}` }))} /></Field>
        <Field label="上游模型"><input required maxLength={128} value={vendorModel} onChange={event => setVendorModel(event.target.value)} className={`${inputClass} font-mono`} /></Field>
        <Field label="产品标识"><input required maxLength={128} value={productCode} onChange={event => setProductCode(event.target.value)} className={`${inputClass} font-mono`} /></Field>
        <Field label="传输标识"><input required maxLength={128} value={transportCode} onChange={event => setTransportCode(event.target.value)} className={`${inputClass} font-mono`} /></Field>
        <Field label="成本方案"><input required maxLength={128} value={costPlan} onChange={event => setCostPlan(event.target.value)} className={`${inputClass} font-mono`} /></Field>
        <Field label="上游地址"><input required type="url" maxLength={500} value={baseURL} onChange={event => setBaseURL(event.target.value)} className={inputClass} /></Field>
        <Field label="请求方法"><Select value={requestMethod} onChange={setRequestMethod} options={methodOptions.map(value => ({ value, label: value }))} /></Field>
        <Field label="请求路径"><input required maxLength={255} value={requestPath} onChange={event => setRequestPath(event.target.value)} className={`${inputClass} font-mono`} /></Field>
        <Field label="超时（毫秒）"><input required type="number" min={100} max={300000} step={100} value={timeout} onChange={event => setTimeoutValue(event.target.value)} className={inputClass} /></Field>
        <Field label="任务范围"><Select value={taskScope} onChange={setTaskScope} disabled={videoAdapter} options={videoAdapter ? [{ value: 'task', label: '异步任务' }] : [{ value: 'request', label: '请求期间' }, { value: 'none', label: '无任务占用' }]} /></Field>
        <Field label="取消能力"><Select value={cancelMode} onChange={setCancelMode} disabled options={[{ value: 'none', label: '不支持取消' }]} /></Field>
        <Field label="来源链接"><Select value={sourcePolicy} onChange={setSourcePolicy} options={[{ value: 'fixed', label: '固定有效期' }, { value: 'refreshable', label: '可重新查询' }]} /></Field>
        <Field label="状态作用域"><Select value={scopeKind} onChange={setScopeKind} options={[{ value: 'credential_pool', label: '凭据池' }, { value: 'credential', label: '单凭据' }, { value: 'product_transport', label: '产品传输' }, { value: 'channel', label: '渠道' }, { value: 'global', label: '全局' }]} /></Field>
        <Field label="作用域标识"><input required maxLength={255} value={scopeKey} onChange={event => setScopeKey(event.target.value)} className={`${inputClass} font-mono`} /></Field>
        <Field label="额外结果域名"><input maxLength={1000} placeholder="cdn.example.com, https://media.example.com" value={resultHosts} onChange={event => setResultHosts(event.target.value)} className={inputClass} /></Field>
        </div>
        <fieldset className="border-y border-[var(--border-soft)] py-4">
          <legend className="px-1 text-sm font-bold">能力与适配声明</legend>
          <div className="mt-2 space-y-2">
            <div className="flex justify-end">
              {selectedAdapter?.code === 'generic' && <Button type="button" size="sm" variant="ghost" disabled={pending} onClick={() => setConstraints(genericCatalogTemplate(requestMethod, requestPath.trim()))}><Braces size={15} />载入通用模板</Button>}
            </div>
            <textarea required spellCheck={false} value={constraints} onChange={event => setConstraints(event.target.value)} className={`${inputClass} min-h-64 resize-y font-mono leading-6`} />
          </div>
        </fieldset>
        <fieldset className="border-y border-[var(--border-soft)] py-4"><legend className="px-1 text-sm font-bold">可执行规格</legend><div className="mt-2 grid gap-2 sm:grid-cols-2">{options.skus.map(item => <label key={item.id} className="flex min-w-0 items-center gap-3 rounded-md px-2 py-2 text-sm hover:bg-[var(--surface-muted)]"><input type="checkbox" checked={selectedSKUs.includes(item.id)} onChange={() => toggleSKU(item.id)} className="h-4 w-4 accent-[var(--primary)]" /><span className="min-w-0 truncate">{item.name}</span><code className="ml-auto truncate text-xs text-[var(--text-secondary)]">{item.code}</code></label>)}</div></fieldset>
      </div>
      <div className="modal-footer"><Button type="button" variant="ghost" disabled={pending} onClick={onClose}>取消</Button><Button type="submit" loading={pending}><Save size={15} />保存</Button></div>
    </form>
  </Modal>;
};

const Field: React.FC<{ label: string; children: React.ReactNode }> = ({ label, children }) => <label className="block min-w-0 space-y-2 text-sm font-semibold"><span>{label}</span>{children}</label>;
