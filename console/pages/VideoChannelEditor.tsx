import React, { useEffect, useMemo, useState } from 'react';
import { ArrowLeft, Braces, Check, ChevronRight, CircleDollarSign, FileCog, Image, Layers3, Plus, RefreshCw, Save, Settings2, Trash2 } from 'lucide-react';
import { useNavigate, useParams } from 'react-router-dom';
import JsonEditor from '../components/ui/JsonEditor';
import { ConfirmDialog, Select } from '../components/ui';
import {
  VideoChannel,
  DiscoveredVideoModel,
  VideoModelMapping,
  createVideoChannel,
  discoverVideoChannelModels,
  getVideoChannel,
  updateVideoChannel,
} from '../services/videoApi';

type CapabilityKey = 'first_frame' | 'last_frame' | 'audio' | 'web_search';

interface ChannelForm {
  name: string;
  adapterType: string;
  adapterProfile: string;
  baseURL: string;
  status: string;
  priority: number;
  requestTimeoutSeconds: number;
  models: VideoModelMapping[];
  capabilities: Record<string, boolean>;
  cancelMode: string;
  resultStorageEnabled: boolean;
  pricingMode: string;
  fixedPrice: string;
  markupRatio: string;
  assetResolver: string;
  extraConfig: string;
}

const BASE_SECTION_IDS = [
  { id: 'basic', label: '基础配置', icon: Settings2 },
  { id: 'models', label: '模型与能力', icon: Layers3 },
  { id: 'pricing', label: '计费', icon: CircleDollarSign },
  { id: 'assets', label: '素材交付', icon: Image },
];

const ADAPTER_SECTION = { id: 'adapter', label: '协议映射', icon: FileCog };

const CAPABILITY_OPTIONS: Array<{ key: CapabilityKey; label: string }> = [
  { key: 'first_frame', label: '首帧' },
  { key: 'last_frame', label: '尾帧' },
  { key: 'audio', label: '生成音频' },
  { key: 'web_search', label: '网络搜索' },
];

const inputClass = 'w-full h-10 px-3 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] text-sm text-[var(--text-primary)] focus:outline-none focus:ring-2 focus:ring-[var(--primary)]/25 focus:border-[var(--primary)]';
const labelClass = 'block text-xs font-semibold text-[var(--text-secondary)] mb-1.5';

const emptyForm = (): ChannelForm => ({
  name: '',
  adapterType: '',
  adapterProfile: '',
  baseURL: '',
  status: 'active',
  priority: 0,
  requestTimeoutSeconds: 30,
  models: [],
  capabilities: {},
  cancelMode: 'disabled',
  resultStorageEnabled: false,
  pricingMode: 'fixed',
  fixedPrice: '0',
  markupRatio: '1',
  assetResolver: 'direct_url',
  extraConfig: '{}',
});

const asObject = (value: unknown): Record<string, any> => {
  if (!value) return {};
  if (typeof value === 'string') {
    try { return JSON.parse(value); } catch { return {}; }
  }
  return typeof value === 'object' && !Array.isArray(value) ? value as Record<string, any> : {};
};

const asModels = (value: unknown): VideoModelMapping[] => {
  if (typeof value === 'string') {
    try { return asModels(JSON.parse(value)); } catch { return []; }
  }
  if (!Array.isArray(value)) return [];
  return value.flatMap(item => {
    if (typeof item === 'string' && item.trim()) {
      const model = item.trim();
      return [{ model_name: model, vendor_model: model }];
    }
    if (!item || typeof item !== 'object') return [];
    const raw = item as Record<string, unknown>;
    const modelName = String(raw.model_name || '').trim();
    if (!modelName) return [];
    return [{ model_name: modelName, vendor_model: String(raw.vendor_model || modelName).trim() || modelName }];
  });
};

const prettyObject = (value: unknown) => JSON.stringify(asObject(value), null, 2);

const channelToForm = (channel: VideoChannel): ChannelForm => {
  const pricing = asObject(channel.pricing);
  const legacyCapabilities = asObject(channel.capabilities) as Record<string, boolean>;
  const capabilities: Record<string, boolean> = {
    first_frame: channel.supports_first_frame ?? Boolean(legacyCapabilities.first_frame),
    last_frame: channel.supports_last_frame ?? Boolean(legacyCapabilities.last_frame),
    audio: channel.supports_audio ?? Boolean(legacyCapabilities.audio),
    web_search: channel.supports_web_search ?? Boolean(legacyCapabilities.web_search),
  };
  const extra = asObject(channel.extra_config);
  const adapter = asObject(extra.adapter);
  const localCancel = asObject(adapter.local_cancel);
  const cancelMode = String(channel.cancel_mode || (asObject(adapter.cancel).enabled ? 'provider' : localCancel.enabled ? 'local_only' : 'disabled'));
  return {
    name: channel.name || '',
    adapterType: channel.adapter_type || '',
    adapterProfile: channel.adapter_profile || String(adapter.profile || ''),
    baseURL: channel.base_url || '',
    status: channel.status || 'active',
    priority: channel.priority || 0,
    requestTimeoutSeconds: channel.request_timeout_seconds || Number(adapter.timeout_seconds || 30),
    models: asModels(channel.models),
    capabilities,
    cancelMode,
    resultStorageEnabled: channel.result_storage_enabled ?? Boolean(asObject(extra.result_storage).enabled),
    pricingMode: String(channel.pricing_mode || pricing.mode || 'fixed'),
    fixedPrice: String(channel.fixed_price ?? pricing.fixed_price ?? 0),
    markupRatio: String(channel.markup_ratio ?? pricing.markup_ratio ?? 1),
    assetResolver: channel.asset_resolver || 'direct_url',
    extraConfig: prettyObject(channel.extra_config),
  };
};

const formFingerprint = (form: ChannelForm) => JSON.stringify(form);

const parseJSONObject = (label: string, value: string) => {
  const parsed = value.trim() ? JSON.parse(value) : {};
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error(`${label}必须是 JSON 对象`);
  }
  return parsed;
};

const stripFormalSettings = (config: Record<string, any>) => {
  const adapter = asObject(config.adapter);
  delete adapter.profile;
  delete adapter.timeout_seconds;
  const cancel = asObject(adapter.cancel);
  delete cancel.enabled;
  if (Object.keys(cancel).length) adapter.cancel = cancel;
  else delete adapter.cancel;
  const localCancel = asObject(adapter.local_cancel);
  delete localCancel.enabled;
  if (Object.keys(localCancel).length) adapter.local_cancel = localCancel;
  else delete adapter.local_cancel;
  if (Object.keys(adapter).length) config.adapter = adapter;
  const resultStorage = asObject(config.result_storage);
  delete resultStorage.enabled;
  if (Object.keys(resultStorage).length) config.result_storage = resultStorage;
  else delete config.result_storage;
  return config;
};

const Section: React.FC<{ id: string; title: string; children: React.ReactNode }> = ({ id, title, children }) => (
  <section id={id} className="scroll-mt-24 py-6 first:pt-0 border-b border-[var(--border-soft)] last:border-b-0">
    <h2 className="text-base font-bold text-[var(--text-primary)] mb-5">{title}</h2>
    {children}
  </section>
);

const JsonField: React.FC<{
  label: string;
  value: string;
  onChange: (value: string) => void;
  height: string;
  error?: string;
}> = ({ label, value, onChange, height, error }) => {
  const format = () => {
    try { onChange(JSON.stringify(parseJSONObject(label, value), null, 2)); } catch { /* submit shows the error */ }
  };
  return (
    <div>
      <div className="flex items-center justify-between gap-3 mb-2">
        <label className="text-xs font-semibold text-[var(--text-secondary)]">{label}</label>
        <button type="button" onClick={format} className="inline-flex items-center gap-1.5 text-xs font-semibold text-[var(--primary)] hover:opacity-75">
          <Braces size={14} />格式化
        </button>
      </div>
      <JsonEditor value={value} onChange={onChange} height={height} />
      {error && <p className="mt-2 text-xs text-red-500">{error}</p>}
    </div>
  );
};

const VideoChannelEditor: React.FC = () => {
  const navigate = useNavigate();
  const params = useParams<{ id: string }>();
  const isCreate = !params.id;
  const [form, setForm] = useState<ChannelForm>(emptyForm);
  const [modelDraft, setModelDraft] = useState<VideoModelMapping>({ model_name: '', vendor_model: '' });
  const [activeSection, setActiveSection] = useState('basic');
  const [initialFingerprint, setInitialFingerprint] = useState(formFingerprint(emptyForm()));
  const [loading, setLoading] = useState(!isCreate);
  const [saving, setSaving] = useState(false);
  const [discoveringModels, setDiscoveringModels] = useState(false);
  const [discoveredModels, setDiscoveredModels] = useState<DiscoveredVideoModel[]>([]);
  const [modelDiscoveryError, setModelDiscoveryError] = useState('');
  const [discardDialogOpen, setDiscardDialogOpen] = useState(false);
  const [error, setError] = useState('');
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const contentScrollRef = React.useRef<HTMLDivElement | null>(null);
  const sectionIds = useMemo(
    () => form.adapterType === 'generic' ? [...BASE_SECTION_IDS, ADAPTER_SECTION] : BASE_SECTION_IDS,
    [form.adapterType],
  );

  const dirty = useMemo(() => formFingerprint(form) !== initialFingerprint, [form, initialFingerprint]);
  const estimatedPrice = useMemo(() => {
    const price = Number(form.fixedPrice);
    const markup = Number(form.markupRatio);
    return Number.isFinite(price) && Number.isFinite(markup) ? price * markup : 0;
  }, [form.fixedPrice, form.markupRatio]);

  useEffect(() => {
    if (isCreate) {
      const next = emptyForm();
      setForm(next);
      setInitialFingerprint(formFingerprint(next));
      setLoading(false);
      return;
    }
    const id = Number(params.id);
    if (!Number.isInteger(id) || id <= 0) {
      setError('无效的渠道 ID');
      setLoading(false);
      return;
    }
    setLoading(true);
    getVideoChannel(id)
      .then(channel => {
        const next = channelToForm(channel);
        setForm(next);
        setInitialFingerprint(formFingerprint(next));
      })
      .catch(err => setError(err?.message || '加载渠道失败'))
      .finally(() => setLoading(false));
  }, [isCreate, params.id]);

  useEffect(() => {
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (!dirty) return;
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', beforeUnload);
    return () => window.removeEventListener('beforeunload', beforeUnload);
  }, [dirty]);

  useEffect(() => {
    const container = contentScrollRef.current;
    const sections = sectionIds
      .map(section => document.getElementById(section.id))
      .filter((section): section is HTMLElement => Boolean(section));
    const observer = new IntersectionObserver(entries => {
      const visible = entries
        .filter(entry => entry.isIntersecting)
        .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)[0];
      if (visible?.target.id) setActiveSection(visible.target.id);
    }, { root: container, rootMargin: '-12% 0px -70% 0px', threshold: 0 });
    sections.forEach(section => observer.observe(section));
    return () => observer.disconnect();
  }, [loading, sectionIds]);

  const updateForm = <K extends keyof ChannelForm>(key: K, value: ChannelForm[K]) => {
    setForm(current => ({ ...current, [key]: value }));
    setError('');
  };

  const addModel = () => {
    const modelName = modelDraft.model_name.trim();
    const vendorModel = modelDraft.vendor_model.trim() || modelName;
    if (!modelName) return;
    if (form.models.some(model => model.model_name === modelName)) {
      setFieldErrors(current => ({ ...current, models: `公开模型 ${modelName} 已存在` }));
      return;
    }
    updateForm('models', [...form.models, { model_name: modelName, vendor_model: vendorModel }]);
    setModelDraft({ model_name: '', vendor_model: '' });
    setFieldErrors(current => ({ ...current, models: '' }));
  };

  const addDiscoveredModel = (vendorModel: string) => {
    if (form.models.some(model => model.vendor_model === vendorModel)) return;
    updateForm('models', [...form.models, { model_name: vendorModel, vendor_model: vendorModel }]);
    setFieldErrors(current => ({ ...current, models: '' }));
  };

  const discoverModels = async () => {
    const id = Number(params.id);
    if (!Number.isInteger(id) || id <= 0) return;
    setDiscoveringModels(true);
    setModelDiscoveryError('');
    try {
      const result = await discoverVideoChannelModels(id);
      setDiscoveredModels(result.models || []);
    } catch (err: any) {
      setModelDiscoveryError(err?.message || '读取上游模型失败');
    } finally {
      setDiscoveringModels(false);
    }
  };

  const goBack = () => {
    if (dirty) {
      setDiscardDialogOpen(true);
      return;
    }
    navigate('/video-channels');
  };

  const discardChanges = () => {
    setDiscardDialogOpen(false);
    navigate('/video-channels');
  };

  const scrollTo = (id: string) => {
    setActiveSection(id);
    const container = contentScrollRef.current;
    const section = document.getElementById(id);
    if (!container || !section) return;
    container.scrollTo({ top: Math.max(0, section.offsetTop - 16), behavior: 'smooth' });
  };

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    setError('');
    const nextErrors: Record<string, string> = {};
    let extraConfig: Record<string, any> = {};
    if (form.adapterType === 'generic') {
      try { extraConfig = stripFormalSettings(parseJSONObject('协议配置', form.extraConfig)); } catch (err: any) { nextErrors.extraConfig = err.message; }
    }
    if (!form.models.length) nextErrors.models = '至少添加一个模型';
    if (!form.name.trim()) nextErrors.name = '请输入渠道名称';
    if (!form.baseURL.trim()) nextErrors.baseURL = '请输入 Base URL';
    if (!form.adapterType) nextErrors.adapterType = '请选择上游协议';
    setFieldErrors(nextErrors);
    if (Object.keys(nextErrors).length) {
      const first = nextErrors.name || nextErrors.baseURL || nextErrors.adapterType ? 'basic' : nextErrors.models ? 'models' : 'adapter';
      scrollTo(first);
      return;
    }

    const payload: Partial<VideoChannel> = {
      name: form.name.trim(),
      adapter_type: form.adapterType,
      adapter_profile: form.adapterProfile.trim(),
      base_url: form.baseURL.trim(),
      status: form.status,
      priority: form.priority,
      request_timeout_seconds: form.requestTimeoutSeconds,
      models: form.models,
      supports_first_frame: form.capabilities.first_frame,
      supports_last_frame: form.capabilities.last_frame,
      supports_audio: form.capabilities.audio,
      supports_web_search: form.capabilities.web_search,
      cancel_mode: form.cancelMode,
      pricing_mode: form.pricingMode,
      fixed_price: Number(form.fixedPrice),
      markup_ratio: Number(form.markupRatio),
      asset_resolver: form.assetResolver,
      result_storage_enabled: form.resultStorageEnabled,
      extra_config: extraConfig,
    };

    setSaving(true);
    try {
      if (isCreate) await createVideoChannel(payload);
      else await updateVideoChannel(Number(params.id), payload);
      navigate('/video-channels');
    } catch (err: any) {
      setError(err?.message || '保存渠道失败');
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return <div className="py-24 text-center text-sm text-[var(--text-secondary)]">加载中...</div>;
  }

  return (
    <>
      <form onSubmit={handleSubmit} className="flex h-[calc(100dvh-5.5rem)] min-h-0 flex-col gap-3 md:h-[calc(100dvh-8rem)]">
      <div className="editor-toolbar editor-toolbar-top z-30 flex-shrink-0">
        <div className="mx-auto flex min-h-14 w-full max-w-[1800px] items-center justify-between gap-3 px-4 py-2.5 md:min-h-16 md:px-5">
          <div className="flex items-center gap-3 min-w-0">
            <button type="button" onClick={goBack} title="返回视频渠道" className="editor-toolbar-back">
              <ArrowLeft size={18} />
            </button>
            <span className="editor-toolbar-mark" aria-hidden="true">
              <Settings2 size={17} strokeWidth={2.2} />
            </span>
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <h1 className="editor-toolbar-title">{isCreate ? '新建视频渠道' : form.name || '编辑视频渠道'}</h1>
                {dirty && <span className="editor-toolbar-dirty">未保存</span>}
              </div>
              <p className="editor-toolbar-meta">{form.baseURL || '未设置 Base URL'}</p>
            </div>
          </div>
          <div className="flex items-center gap-2 shrink-0">
            <button type="button" onClick={goBack} className="editor-toolbar-cancel hidden sm:inline-flex">取消</button>
            <button type="submit" disabled={saving} className="editor-toolbar-primary">
              <Save size={16} />{saving ? '保存中...' : '保存'}
            </button>
          </div>
        </div>
      </div>

      {error && <div className="flex-shrink-0 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-600">{error}</div>}

      <div className="flex min-h-0 flex-1 flex-col gap-3 lg:grid lg:grid-cols-[220px_minmax(0,1fr)] lg:grid-rows-[minmax(0,1fr)] lg:gap-5">
        <nav aria-label="渠道配置导航" className="flex flex-shrink-0 gap-1.5 overflow-x-auto rounded-2xl border border-[var(--border-soft)] bg-[var(--surface-card)] p-2.5 shadow-sm lg:h-full lg:flex-col lg:overflow-y-auto">
          <div className="hidden lg:flex items-center justify-between px-3 pt-1 pb-2">
            <span className="text-[11px] font-bold tracking-[0.08em] text-[var(--text-muted)] uppercase">配置导航</span>
            <span className="text-[11px] font-semibold text-[var(--text-muted)]">{sectionIds.length} 项</span>
          </div>
          {sectionIds.map((section, index) => {
            const Icon = section.icon;
            const active = activeSection === section.id;
            return (
              <button key={section.id} type="button" onClick={() => scrollTo(section.id)} aria-current={active ? 'location' : undefined}
                className={`group relative flex h-12 min-w-max items-center gap-3 rounded-xl px-3 text-left whitespace-nowrap transition-all ${active
                  ? 'bg-[var(--primary-lighter)] text-[var(--primary)] shadow-[inset_3px_0_0_var(--primary)]'
                  : 'text-[var(--text-secondary)] hover:bg-[var(--surface)] hover:text-[var(--text-primary)]'}`}>
                <span className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg transition-colors ${active ? 'bg-[var(--surface-card)] text-[var(--primary)]' : 'bg-[var(--surface)] text-[var(--text-muted)] group-hover:text-[var(--text-primary)]'}`}>
                  <Icon size={17} strokeWidth={active ? 2.3 : 2} />
                </span>
                <span className="flex-1 text-sm font-bold">{section.label}</span>
                <span className={`text-[10px] font-bold tabular-nums ${active ? 'text-[var(--primary)]/70' : 'text-[var(--text-muted)]'}`}>{String(index + 1).padStart(2, '0')}</span>
                <ChevronRight size={15} className={`shrink-0 transition-transform ${active ? 'translate-x-0.5 opacity-100' : 'opacity-0 group-hover:opacity-60'}`} />
              </button>
            );
          })}
        </nav>

        <div ref={contentScrollRef} className="editor-content min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain rounded-2xl border border-[var(--border-soft)] bg-[var(--surface-card)] px-4 md:px-7">
          <Section id="basic" title="基础配置">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
              <div>
                <label className={labelClass}>渠道名称</label>
                <input value={form.name} onChange={event => updateForm('name', event.target.value)} className={inputClass} />
                {fieldErrors.name && <p className="mt-1.5 text-xs text-red-500">{fieldErrors.name}</p>}
              </div>
              <div>
                <label className={labelClass}>上游协议</label>
                <Select value={form.adapterType} onChange={value => {
                  setForm(current => ({
                    ...current, adapterType: value,
                    adapterProfile: value === 'generic' ? (current.adapterProfile || 'json_task_v1') : '',
                    pricingMode: value === 'generic' ? current.pricingMode : 'fixed',
                  }));
                  setFieldErrors(current => ({ ...current, adapterType: '' }));
                  setError('');
                }} options={[
                  { value: 'generic', label: '通用 JSON 任务协议' },
                  { value: 'seedance', label: 'Seedance 官方协议' },
                ]} placeholder="选择协议" />
                {fieldErrors.adapterType && <p className="mt-1.5 text-xs text-red-500">{fieldErrors.adapterType}</p>}
              </div>
              <div className="md:col-span-2">
                <label className={labelClass}>Base URL</label>
                <input value={form.baseURL} onChange={event => updateForm('baseURL', event.target.value)} placeholder="https://" className={inputClass} />
                {fieldErrors.baseURL && <p className="mt-1.5 text-xs text-red-500">{fieldErrors.baseURL}</p>}
              </div>
              <div>
                <label className={labelClass}>状态</label>
                <Select value={form.status} onChange={value => updateForm('status', value)} options={[
                  { value: 'active', label: '启用' },
                  { value: 'inactive', label: '停用' },
                ]} />
              </div>
              <div>
                <label className={labelClass}>优先级</label>
                <input type="number" value={form.priority} onChange={event => updateForm('priority', Number(event.target.value))} className={inputClass} />
              </div>
              <div>
                <label className={labelClass}>请求超时（秒）</label>
                <input type="number" min="1" max="300" value={form.requestTimeoutSeconds} onChange={event => updateForm('requestTimeoutSeconds', Number(event.target.value))} className={inputClass} />
              </div>
              {form.adapterType === 'generic' && (
                <div>
                  <label className={labelClass}>适配器版本</label>
                  <Select value={form.adapterProfile} onChange={value => updateForm('adapterProfile', value)} options={[
                    { value: 'json_task_v1', label: 'JSON Task V1' },
                  ]} />
                </div>
              )}
            </div>
          </Section>

          <Section id="models" title="模型与能力">
            <div>
              <div className="mb-1.5 flex items-center justify-between gap-3">
                <label className="text-xs font-semibold text-[var(--text-secondary)]">模型映射</label>
                {!isCreate && form.adapterType === 'generic' && (
                  <button type="button" onClick={discoverModels} disabled={discoveringModels}
                    className="inline-flex h-8 items-center gap-1.5 px-2.5 rounded-md text-xs font-semibold text-[var(--primary)] hover:bg-[var(--primary-lighter)] disabled:opacity-50">
                    <RefreshCw size={13} className={discoveringModels ? 'animate-spin' : ''} />
                    {discoveringModels ? '读取中...' : '读取上游模型'}
                  </button>
                )}
              </div>
              <div className="grid grid-cols-1 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_40px] gap-2">
                <input value={modelDraft.model_name} onChange={event => setModelDraft(current => ({ ...current, model_name: event.target.value }))}
                  onKeyDown={event => {
                    if (event.key === 'Enter') {
                      event.preventDefault();
                      addModel();
                    }
                  }}
                  placeholder="公开模型名，如 video-fast" className={inputClass} />
                <input value={modelDraft.vendor_model} onChange={event => setModelDraft(current => ({ ...current, vendor_model: event.target.value }))}
                  onKeyDown={event => {
                    if (event.key === 'Enter') {
                      event.preventDefault();
                      addModel();
                    }
                  }}
                  placeholder="上游模型名，留空则同名" className={inputClass} />
                <button type="button" onClick={addModel} title="添加模型映射" className="w-10 h-10 shrink-0 inline-flex items-center justify-center rounded-lg border border-[var(--border-soft)] text-[var(--primary)] hover:bg-[var(--primary-lighter)]">
                  <Plus size={17} />
                </button>
              </div>
              {fieldErrors.models && <p className="mt-1.5 text-xs text-red-500">{fieldErrors.models}</p>}
              {modelDiscoveryError && <p className="mt-2 text-xs text-red-500">{modelDiscoveryError}</p>}
              {discoveredModels.length > 0 && (
                <div className="mt-3">
                  <div className="mb-2 text-xs font-semibold text-[var(--text-secondary)]">上游可用模型</div>
                  <div className="flex flex-wrap gap-2">
                    {discoveredModels.map(model => {
                      const added = form.models.some(item => item.vendor_model === model.vendor_model);
                      return (
                        <button key={model.vendor_model} type="button" disabled={added} onClick={() => addDiscoveredModel(model.vendor_model)}
                          title={added ? `${model.vendor_model} 已加入映射` : `加入 ${model.vendor_model}`}
                          className="inline-flex h-8 items-center gap-1.5 px-2.5 rounded-md border border-[var(--border-soft)] bg-[var(--surface)] text-xs font-mono text-[var(--text-primary)] hover:border-[var(--primary)] hover:text-[var(--primary)] disabled:opacity-60 disabled:hover:border-[var(--border-soft)] disabled:hover:text-[var(--text-primary)]">
                          {added ? <Check size={13} /> : <Plus size={13} />}{model.vendor_model}
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}
              <div className="mt-3 min-h-11 space-y-2 p-2 rounded-lg border border-[var(--border-soft)] bg-[var(--surface)]">
                {form.models.length === 0 ? <span className="px-2 py-1 text-xs text-[var(--text-secondary)]">暂无模型映射</span> : form.models.map(model => (
                  <div key={model.model_name} className="grid grid-cols-[minmax(0,1fr)_24px_minmax(0,1fr)_28px] items-center gap-2 min-h-9 px-2 rounded-md bg-[var(--surface-card)] border border-[var(--border-soft)] text-xs font-mono text-[var(--text-primary)]">
                    <span className="truncate" title={model.model_name}>{model.model_name}</span>
                    <span className="text-center text-[var(--text-secondary)]">→</span>
                    <span className="truncate" title={model.vendor_model}>{model.vendor_model}</span>
                    <button type="button" onClick={() => updateForm('models', form.models.filter(item => item.model_name !== model.model_name))} title={`删除 ${model.model_name}`} className="w-6 h-6 inline-flex items-center justify-center rounded hover:bg-red-50 text-[var(--text-secondary)] hover:text-red-500">
                      <Trash2 size={12} />
                    </button>
                  </div>
                ))}
              </div>
            </div>

            <div className="mt-6">
              <label className={labelClass}>能力声明</label>
              <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-2">
                {CAPABILITY_OPTIONS.map(option => {
                  const checked = Boolean(form.capabilities[option.key]);
                  return (
                    <label key={option.key} className="h-11 px-3 flex items-center justify-between gap-3 rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] cursor-pointer">
                      <span className="text-sm font-medium text-[var(--text-primary)]">{option.label}</span>
                      <input type="checkbox" checked={checked} onChange={event => updateForm('capabilities', { ...form.capabilities, [option.key]: event.target.checked })}
                        className="w-4 h-4 accent-[var(--primary)]" />
                    </label>
                  );
                })}
              </div>
            </div>
            <div className="mt-6 max-w-md">
              <label className={labelClass}>取消策略</label>
              <Select value={form.cancelMode} onChange={value => updateForm('cancelMode', value)} options={[
                { value: 'disabled', label: '不支持取消' },
                { value: 'local_only', label: '仅取消本地排队任务' },
                { value: 'provider', label: '调用上游取消' },
              ]} />
            </div>
          </Section>

          <Section id="pricing" title="计费">
            <div className="grid grid-cols-1 md:grid-cols-3 gap-5">
              <div>
                <label className={labelClass}>计费模式</label>
                <Select value={form.pricingMode} onChange={value => updateForm('pricingMode', value)} options={[
                  { value: 'fixed', label: '固定价格' },
                  ...(form.adapterType === 'generic' ? [{ value: 'upstream_estimate', label: '上游估价' }] : []),
                ]} />
              </div>
              <div>
                <label className={labelClass}>{form.pricingMode === 'fixed' ? '固定价格' : '上游基础费用'}</label>
                {form.pricingMode === 'fixed' ? (
                  <input type="number" min="0" step="0.0001" value={form.fixedPrice} onChange={event => updateForm('fixedPrice', event.target.value)} className={inputClass} />
                ) : (
                  <div className={`${inputClass} flex items-center text-[var(--text-secondary)]`}>按请求估价</div>
                )}
              </div>
              <div>
                <label className={labelClass}>加价倍率</label>
                <input type="number" min="0" step="0.01" value={form.markupRatio} onChange={event => updateForm('markupRatio', event.target.value)} className={inputClass} />
              </div>
            </div>
            <div className="mt-4 flex items-center justify-between h-11 px-3 rounded-lg border border-[var(--border-soft)] bg-[var(--surface)]">
              <span className="text-sm text-[var(--text-secondary)]">预留费用</span>
              <span className="text-sm font-bold text-[var(--text-primary)]">
                {form.pricingMode === 'fixed' ? `¥${estimatedPrice.toFixed(4)}` : '动态'}
              </span>
            </div>
          </Section>

          <Section id="assets" title="素材交付">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
              <div>
                <label className={labelClass}>素材处理</label>
                <Select value={form.assetResolver} onChange={value => updateForm('assetResolver', value)} options={[
                  { value: 'direct_url', label: '直接使用素材 URL' },
                  { value: 'presigned_upload', label: '上传至上游存储' },
                ]} />
              </div>
              <label className="h-10 mt-[22px] px-3 flex items-center justify-between gap-3 rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] cursor-pointer">
                <span className="text-sm font-medium text-[var(--text-primary)]">转存生成结果</span>
                <input type="checkbox" checked={form.resultStorageEnabled} onChange={event => updateForm('resultStorageEnabled', event.target.checked)} className="w-4 h-4 accent-[var(--primary)]" />
              </label>
            </div>
          </Section>

          {form.adapterType === 'generic' && (
            <Section id="adapter" title="协议映射">
              <JsonField label="协议配置" value={form.extraConfig} onChange={value => updateForm('extraConfig', value)} height="420px" error={fieldErrors.extraConfig} />
            </Section>
          )}
        </div>
      </div>

      <div className="editor-actionbar z-20 flex-shrink-0">
        <div className="mx-auto flex min-h-14 w-full max-w-[1800px] items-center justify-between gap-3 px-4 py-2.5 md:min-h-16 md:px-5">
          <span className={`editor-save-status ${dirty ? 'editor-save-status-dirty' : ''}`}>
            {!dirty && <Check size={14} />}{dirty ? '有未保存的修改' : '已保存'}
          </span>
          <div className="flex items-center gap-2">
            <button type="button" onClick={goBack} className="editor-toolbar-cancel">取消</button>
            <button type="submit" disabled={saving} className="editor-toolbar-primary editor-toolbar-primary-wide">
              <Save size={16} />{saving ? '保存中...' : '保存渠道'}
            </button>
          </div>
        </div>
      </div>
      </form>
      <ConfirmDialog
        open={discardDialogOpen}
        title="放弃未保存的修改？"
        description="当前编辑内容尚未保存，离开后这些修改将丢失。"
        confirmLabel="放弃修改"
        cancelLabel="继续编辑"
        tone="warning"
        onConfirm={discardChanges}
        onCancel={() => setDiscardDialogOpen(false)}
      />
    </>
  );
};

export default VideoChannelEditor;
