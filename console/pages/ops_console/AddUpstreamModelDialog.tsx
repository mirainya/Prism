import React, { useEffect, useMemo, useState } from 'react';
import { Braces, Save } from 'lucide-react';
import { Button, Modal, SegmentedControl, Select } from '../../components/ui';
import {
  createUnifiedActiveProduct,
  fetchUnifiedAdapterManifest,
  fetchUnifiedCatalogModelEntries,
  fetchUnifiedCatalogOptions,
  onboardUnifiedCatalogModel,
  type UnifiedAdapterManifest,
  type UnifiedCatalogModelEntry,
  type UnifiedCatalogOptions,
  type UnifiedCatalogRelease,
} from '../../services/unifiedGatewayApi';
import type { UnifiedChannel, UnifiedPool } from '../../services/unifiedChannelApi';
import { ErrorNotice, errorMessage } from '../unified_gateway/Feedback';
import {
  buildModelOnboardInput,
  modelTypeForAdapter,
  onboardingManifestChoices,
  validateNewModelOnboarding,
} from './onboardingForm';

const JsonEditor = React.lazy(() => import('../../components/ui/JsonEditor'));

const inputClass = 'mt-1 w-full rounded-md border border-[var(--border-soft)] bg-[var(--surface-card-solid)] px-3 py-2 text-sm font-normal';

const adapterLabels: Record<string, string> = {
  anthropic_messages: 'Anthropic Messages',
  generic: '通用 JSON 映射',
  google_generate_content: 'Google Generate Content',
  openai_chat: 'OpenAI Chat Completions',
  openai_images: 'OpenAI Images',
  openai_responses: 'OpenAI Responses',
  seedance: 'Seedance',
  volcengine_responses_v3: '火山 Responses V3',
};

const requestPaths: Record<string, string> = {
  anthropic_messages: '/v1/messages',
  generic: '/tasks',
  google_generate_content: '/v1beta/models/{model}:generateContent',
  openai_chat: '/v1/chat/completions',
  openai_images: '/v1/images/generations',
  openai_responses: '/v1/responses',
  seedance: '/api/v3/contents/generations/tasks',
  volcengine_responses_v3: '/api/v3/responses',
};

const genericMapping = (path: string) => JSON.stringify({
  adapter: {
    profile: 'json_task_v1',
    auth_location: 'header',
    auth_key: 'Authorization',
    auth_prefix: 'Bearer ',
    submit: { enabled: true, method: 'POST', path },
    poll: { enabled: true, method: 'GET', path: `${path.replace(/\/$/, '')}/{task_id}` },
    request: {
      fields: {
        model: 'model', prompt: 'prompt', resolution: 'resolution', ratio: 'ratio',
        duration: 'duration', audio: 'generate_audio', task_mode: 'task_mode',
      },
      params_mode: 'merge_missing',
    },
    response: {
      task_id_paths: ['data.id', 'id'],
      status_paths: ['data.status', 'status'],
      video_url_paths: ['data.result.video_url', 'data.video_url', 'result.video_url', 'video_url'],
      error_paths: ['data.error.message', 'data.message', 'error.message', 'message'],
      status_map: {
        queued: 'submitted', pending: 'submitted', running: 'tracking', processing: 'tracking',
        succeeded: 'completed', completed: 'completed', failed: 'failed', canceled: 'cancelled',
      },
      submit_default_status: 'submitted',
      poll_default_status: 'tracking',
      unknown_status: 'tracking',
    },
  },
}, null, 2);

const identityPart = (value: string) => value.toLowerCase()
  .replace(/[^a-z0-9]+/g, '-')
  .replace(/^-+|-+$/g, '') || 'model';

const loadModelEntries = async (releaseID: number, signal: AbortSignal) => {
  const items: UnifiedCatalogModelEntry[] = [];
  let page = 1;
  while (true) {
    const result = await fetchUnifiedCatalogModelEntries(releaseID, page, 100, signal);
    items.push(...result.items);
    if (items.length >= result.total || result.items.length === 0) return items;
    page += 1;
  }
};

export const AddUpstreamModelDialog: React.FC<{
  channel: UnifiedChannel;
  release: UnifiedCatalogRelease;
  pools: UnifiedPool[];
  onClose: () => void;
  onSaved: () => void;
}> = ({ channel, release, pools, onClose, onSaved }) => {
  const [mode, setMode] = useState<'existing' | 'new'>('existing');
  const [options, setOptions] = useState<UnifiedCatalogOptions | null>(null);
  const [models, setModels] = useState<UnifiedCatalogModelEntry[]>([]);
  const [manifest, setManifest] = useState<UnifiedAdapterManifest | null>(null);
  const [manifestLoading, setManifestLoading] = useState(false);
  const [manifestError, setManifestError] = useState('');
  const [poolID, setPoolID] = useState(String(pools.find((pool) => pool.status === 'active')?.id || ''));
  const [adapter, setAdapter] = useState('');
  const [vendorModel, setVendorModel] = useState('');
  const [baseURL, setBaseURL] = useState('');
  const [requestPath, setRequestPath] = useState('/v1/chat/completions');
  const [mapping, setMapping] = useState('{}');
  const [skuIDs, setSkuIDs] = useState<number[]>([]);
  const [apiName, setAPIName] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [downstreamPath, setDownstreamPath] = useState('');
  const [variantCode, setVariantCode] = useState('default');
  const [billingUnit, setBillingUnit] = useState<'' | 'request' | 'second'>('');
  const [billingMaxQuantity, setBillingMaxQuantity] = useState('');
  const [sellPrice, setSellPrice] = useState('');
  const [costPrice, setCostPrice] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    const controller = new AbortController();
    Promise.all([
      fetchUnifiedCatalogOptions(release.id, controller.signal),
      loadModelEntries(release.id, controller.signal),
    ]).then(([nextOptions, nextModels]) => {
      if (controller.signal.aborted) return;
      setOptions(nextOptions);
      setModels(nextModels);
      const preferred = channel.model_types.includes('video')
        ? 'generic'
        : channel.model_types.includes('image') ? 'openai_images' : 'openai_chat';
      const initial = nextOptions.adapters.find((item) => item.code === preferred) || nextOptions.adapters[0];
      if (initial) {
        setAdapter(`${initial.code}@${initial.version}`);
        const path = requestPaths[initial.code] || '/';
        setRequestPath(path);
        setMapping(initial.code === 'generic' ? genericMapping(path) : '{}');
      }
    }).catch((reason: unknown) => {
      if (!controller.signal.aborted) setError(errorMessage(reason));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [channel.model_types, release.id]);

  const selectedAdapter = options?.adapters.find((item) => `${item.code}@${item.version}` === adapter);
  const selectedPool = pools.find((pool) => String(pool.id) === poolID);
  const targetType = modelTypeForAdapter(selectedAdapter?.code || 'openai_chat', manifest?.capability);
  const compatibleModels = useMemo(() => models.filter((model) => model.model_type === targetType), [models, targetType]);
  const availableSKUs = compatibleModels.flatMap((model) => model.skus.map((sku) => ({ ...sku, modelName: model.display_name || model.model_code })));
  const manifestChoices = useMemo(
    () => onboardingManifestChoices(selectedAdapter?.code || '', manifest),
    [manifest, selectedAdapter?.code],
  );

  useEffect(() => {
    if (mode !== 'new' || !selectedAdapter) {
      setManifest(null);
      setManifestError('');
      setManifestLoading(false);
      return;
    }
    const controller = new AbortController();
    setManifest(null);
    setManifestError('');
    setManifestLoading(true);
    fetchUnifiedAdapterManifest(selectedAdapter.code, selectedAdapter.version, controller.signal)
      .then((nextManifest) => {
        if (controller.signal.aborted) return;
        const choices = onboardingManifestChoices(selectedAdapter.code, nextManifest);
        setManifest(nextManifest);
        setDownstreamPath((current) => choices.downstreamPaths.includes(current) ? current : (choices.downstreamPaths[0] || ''));
        setVariantCode((current) => choices.variants.some((item) => item.code === current)
          ? current
          : choices.variants.length === 1 ? choices.variants[0].code : '');
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setManifestError(errorMessage(reason));
      })
      .finally(() => {
        if (!controller.signal.aborted) setManifestLoading(false);
      });
    return () => controller.abort();
  }, [mode, selectedAdapter?.code, selectedAdapter?.version]);

  const changeAdapter = (value: string) => {
    const next = options?.adapters.find((item) => `${item.code}@${item.version}` === value);
    const path = requestPaths[next?.code || ''] || '/';
    const choices = onboardingManifestChoices(next?.code || '', null);
    setAdapter(value);
    setRequestPath(path);
    setMapping(next?.code === 'generic' ? genericMapping(path) : '{}');
    setSkuIDs([]);
    setManifest(null);
    setDownstreamPath(choices.downstreamPaths[0] || '');
    setVariantCode(next?.code === 'generic' ? 'default' : '');
    setBillingUnit('');
    setBillingMaxQuantity('');
  };

  const changeRequestPath = (value: string) => {
    setMapping((current) => selectedAdapter?.code === 'generic' && current === genericMapping(requestPath)
      ? genericMapping(value)
      : current);
    setRequestPath(value);
  };

  const toggleSKU = (id: number) => setSkuIDs((current) => current.includes(id)
    ? current.filter((value) => value !== id)
    : [...current, id]);

  const selectedVariant = manifest?.variants?.find((item) => item.code === variantCode);
  const suggestedMaxSeconds = selectedVariant?.param_hints?.duration?.max || '';

  const changeVariant = (value: string) => {
    setVariantCode(value);
    if (billingUnit !== 'second') return;
    const maxSeconds = manifest?.variants?.find((item) => item.code === value)?.param_hints?.duration?.max || '';
    setBillingMaxQuantity(maxSeconds);
  };

  const changeBillingUnit = (value: string) => {
    const next = value === 'second' ? 'second' : value === 'request' ? 'request' : '';
    setBillingUnit(next);
    setBillingMaxQuantity(next === 'second' ? suggestedMaxSeconds : '');
  };

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!selectedAdapter || !selectedPool || selectedPool.status !== 'active') {
      setError('请选择有效的接入格式和 Key 池');
      return;
    }
    if (mode === 'existing' && skuIDs.length === 0) {
      setError('请至少选择一个已有对外模型');
      return;
    }
    const modelName = vendorModel.trim();
    const endpoint = baseURL.trim().replace(/\/$/, '');
    const path = requestPath.trim();
    if (!modelName || !endpoint || !path.startsWith('/')) {
      setError('请填写上游模型、上游地址和请求路径');
      return;
    }
    try {
      const parsedURL = new URL(endpoint);
      if (!['http:', 'https:'].includes(parsedURL.protocol) || !parsedURL.hostname || parsedURL.username || parsedURL.password) throw new Error();
    } catch {
      setError('上游地址必须是有效的 HTTP 或 HTTPS 地址');
      return;
    }
    let parsedMapping: Record<string, unknown>;
    try {
      parsedMapping = JSON.parse(mapping) as Record<string, unknown>;
      if (!parsedMapping || typeof parsedMapping !== 'object' || Array.isArray(parsedMapping)) throw new Error();
    } catch {
      setError('JSON 映射必须是有效对象');
      return;
    }
    const stamp = Date.now().toString(36);
    const onboardingValues = {
      releaseId: release.id,
      configVersion: release.config_version,
      channelId: channel.id,
      channelCode: channel.channel_code,
      poolId: selectedPool.id,
      poolCode: selectedPool.pool_code,
      adapterCode: selectedAdapter.code,
      adapterVersion: selectedAdapter.version,
      adapterCapability: manifest?.capability || '',
      apiName,
      displayName,
      vendorModel: modelName,
      baseURL: endpoint,
      requestPath: path,
      downstreamPath,
      variantCode,
      capabilityConstraints: parsedMapping,
      billingUnit,
      billingMaxQuantity,
      sellPrice,
      costPrice,
      uniqueSuffix: stamp,
    };
    if (mode === 'new') {
      const validationError = validateNewModelOnboarding(onboardingValues);
      if (validationError) {
        setError(validationError);
        return;
      }
    }
    setSaving(true);
    setError('');
    try {
      if (mode === 'new') {
        await onboardUnifiedCatalogModel(buildModelOnboardInput(onboardingValues));
      } else {
        const stem = `${identityPart(channel.channel_code)}-${identityPart(modelName)}`;
        const productCode = `${stem.slice(0, 118 - stamp.length)}-${stamp}`;
        const transportCode = `${identityPart(selectedAdapter.code).slice(0, 108)}-${stamp}`;
        // Declaration-driven async image transports use the same task
        // lifecycle as video products. Without this branch, associating an
        // existing public image SKU with Sub2API's /async endpoint would be
        // persisted as a request-scoped product and never enqueue polling.
        const asyncImage = parsedMapping.async_image;
        const taskBased = targetType === 'video' || (
          targetType === 'image'
          && selectedAdapter.code === 'openai_images'
          && Boolean(asyncImage)
          && typeof asyncImage === 'object'
          && !Array.isArray(asyncImage)
        );
        await createUnifiedActiveProduct({
          expected_active_release_id: release.id,
          expected_config_version: release.config_version,
          channel_id: channel.id,
          credential_pool_id: selectedPool.id,
          product_code: productCode,
          vendor_model: modelName,
          capability_constraints: parsedMapping,
          constraints_schema_version: 1,
          adapter_code: selectedAdapter.code,
          adapter_version: selectedAdapter.version,
          transport_code: transportCode,
          base_url: endpoint,
          request_method: 'POST',
          request_path: path,
          auth_scheme: 'bearer',
          transport_timeout_ms: 30000,
          task_timeout_ms: 30000,
          task_scope: taskBased ? 'task' : 'request',
          cancel_mode: 'none',
          source_url_policy: 'fixed',
          upstream_scope_kind: 'credential_pool',
          upstream_scope_key: selectedPool.pool_code,
          allowed_hosts: [],
          actions: taskBased ? [
            { action_code: 'submit', allowed_source_state: 'allocated', idempotency_mode: 'none', request_schema_version: 1, response_schema_version: 1 },
            { action_code: 'query', allowed_source_state: 'nonterminal', idempotency_mode: 'none', request_schema_version: 1, response_schema_version: 1 },
          ] : [],
          cost_plan_code: 'standard',
          routes: skuIDs.map((sku_id) => ({ sku_id, priority: 100, weight: 100 })),
        });
      }
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setSaving(false);
    }
  };

  return <Modal open title="接入上游模型" width="max-w-3xl" onClose={() => { if (!saving) onClose(); }}>
    <form className="space-y-4" onSubmit={submit}>
      <SegmentedControl
        ariaLabel="模型接入方式"
        value={mode}
        onChange={(value) => { setMode(value); setError(''); }}
        options={[
          { value: 'existing', label: '关联已有模型' },
          { value: 'new', label: '新建公开模型' },
        ]}
      />
      {(error || manifestError) && <ErrorNotice message={error || manifestError} />}
      <div className="grid gap-3 sm:grid-cols-2">
        {mode === 'new' && <>
          <label className="block text-sm font-semibold">公开调用名<input autoFocus value={apiName} onChange={(event) => setAPIName(event.target.value)} maxLength={128} spellCheck={false} className={`${inputClass} font-mono`} placeholder="seedance2.5-md-480p" /></label>
          <label className="block text-sm font-semibold">显示名<input value={displayName} onChange={(event) => setDisplayName(event.target.value)} maxLength={128} className={inputClass} placeholder="Seedance 2.5 MD 480p" /></label>
        </>}
        <label className="block text-sm font-semibold">Key 池<Select value={poolID} onChange={setPoolID} disabled={loading || saving} className="mt-1" options={pools.filter((pool) => pool.status === 'active').map((pool) => ({ value: String(pool.id), label: pool.display_name }))} /></label>
        <label className="block text-sm font-semibold">接入格式<Select value={adapter} onChange={changeAdapter} disabled={loading || saving} className="mt-1" options={(options?.adapters || []).map((item) => ({ value: `${item.code}@${item.version}`, label: adapterLabels[item.code] || item.code }))} /></label>
        <label className="block text-sm font-semibold">上游模型<input autoFocus={mode === 'existing'} value={vendorModel} onChange={(event) => setVendorModel(event.target.value)} maxLength={255} spellCheck={false} className={`${inputClass} font-mono`} placeholder="发给上游的模型名" /></label>
        <label className="block text-sm font-semibold">上游地址<input value={baseURL} onChange={(event) => setBaseURL(event.target.value)} maxLength={500} spellCheck={false} className={`${inputClass} font-mono`} placeholder="https://api.example.com" /></label>
        <label className="block text-sm font-semibold sm:col-span-2">上游请求路径<input value={requestPath} onChange={(event) => changeRequestPath(event.target.value)} maxLength={255} spellCheck={false} className={`${inputClass} font-mono`} /></label>
        {mode === 'new' && <>
          <label className="block text-sm font-semibold">下游入口<Select value={downstreamPath} onChange={setDownstreamPath} disabled={manifestLoading || manifestChoices.downstreamPaths.length === 0} className="mt-1" options={manifestChoices.downstreamPaths.map((path) => ({ value: path, label: path }))} />{!manifestLoading && manifestChoices.downstreamPaths.length === 0 && <span className="mt-1 block text-xs font-normal text-[var(--text-secondary)]">该接入格式未声明下游入口</span>}</label>
          <label className="block text-sm font-semibold">模型规格<Select value={variantCode} onChange={changeVariant} placeholder="请选择规格" disabled={manifestLoading} className="mt-1" options={manifestChoices.variants.map((item) => ({ value: item.code, label: item.label }))} /></label>
          <label className="block text-sm font-semibold">计费单位<Select value={billingUnit} onChange={changeBillingUnit} placeholder="请选择计费单位" className="mt-1" options={[
            { value: 'request', label: '按次' },
            ...(targetType === 'video' ? [{ value: 'second', label: '按请求秒数' }] : []),
          ]} /></label>
          {billingUnit === 'second' && <label className="block text-sm font-semibold">单次最多秒数<input value={billingMaxQuantity} onChange={(event) => setBillingMaxQuantity(event.target.value)} inputMode="numeric" maxLength={5} spellCheck={false} className={`${inputClass} font-mono`} placeholder="30" /></label>}
          <label className="block text-sm font-semibold">售价 / {billingUnit === 'second' ? '秒' : '次'}<input value={sellPrice} onChange={(event) => setSellPrice(event.target.value)} inputMode="decimal" maxLength={37} spellCheck={false} className={`${inputClass} font-mono`} placeholder="1.25" /></label>
          <label className="block text-sm font-semibold">成本 / {billingUnit === 'second' ? '秒' : '次'}<input value={costPrice} onChange={(event) => setCostPrice(event.target.value)} inputMode="decimal" maxLength={37} spellCheck={false} className={`${inputClass} font-mono`} placeholder="0.80" /></label>
        </>}
      </div>

      {mode === 'existing' && <fieldset className="rounded-md border border-[var(--border-soft)] p-3">
        <legend className="px-1 text-sm font-semibold">对外模型</legend>
        {loading ? <p className="py-3 text-xs text-[var(--text-secondary)]">正在读取</p> : availableSKUs.length === 0 ? <p className="py-3 text-xs text-[var(--text-secondary)]">当前目录没有可匹配的模型</p> : <div className="grid max-h-48 gap-1 overflow-y-auto sm:grid-cols-2">{availableSKUs.map((sku) => <label key={sku.id} className="flex min-w-0 items-center gap-2 rounded-md px-2 py-2 text-xs hover:bg-[var(--surface-muted)]"><input type="checkbox" checked={skuIDs.includes(sku.id)} onChange={() => toggleSKU(sku.id)} className="h-4 w-4 accent-[var(--primary)]" /><span className="min-w-0"><strong className="block truncate">{sku.modelName}</strong><code className="block truncate text-[10px] font-normal text-[var(--text-secondary)]">{sku.api_name} · {sku.route_template}</code></span></label>)}</div>}
      </fieldset>}

      <label className="block text-sm font-semibold"><span className="inline-flex items-center gap-1.5"><Braces size={14} />{targetType === 'video' ? '上下游 JSON 映射' : '参数与能力配置'}</span><div className="mt-1"><React.Suspense fallback={<div className="grid h-56 place-items-center rounded-md border border-[var(--border-soft)] text-xs text-[var(--text-secondary)]">正在加载编辑器</div>}><JsonEditor value={mapping} onChange={setMapping} height="14rem" /></React.Suspense></div></label>

      <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="ghost" onClick={onClose} disabled={saving}>取消</Button><Button type="submit" loading={saving} disabled={loading || (mode === 'new' && manifestLoading)}><Save size={14} />{mode === 'new' ? '新建并生效' : '保存并生效'}</Button></div>
    </form>
  </Modal>;
};
