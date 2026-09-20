import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  ChevronDown,
  Clock3,
  ExternalLink,
  Image as ImageIcon,
  Images,
  Loader2,
  Pencil,
  Play,
  RefreshCw,
  RotateCcw,
  Search,
  Sparkles,
  Trash2,
  Upload,
  X,
  XCircle,
} from 'lucide-react';
import { Button, Drawer, SegmentedControl, Select } from '../../components/ui';
import {
  PlaygroundImageGenerationParams,
  PlaygroundImageEditParams,
  PlaygroundImageGenerationResponse,
  PlaygroundImageModel,
  PlaygroundImageOutput,
  PlaygroundImageRequestError,
  playgroundEditImage,
  playgroundGenerateImage,
  playgroundListImageModels,
  playgroundUploadFile,
} from '../../services/playgroundApi';

interface ImageTabProps {
  tokenId: string;
}

type ImageOperation = 'generate' | 'edit';
type HistoryStatus = 'generating' | 'completed' | 'failed';

interface PreviewImage extends PlaygroundImageOutput {
  src: string;
}

interface ReferenceImage {
  id: string;
  name: string;
  preview: string;
  url?: string;
  uploading: boolean;
  error?: string;
}

interface ImageHistoryItem {
  id: string;
  status: HistoryStatus;
  operation: ImageOperation;
  modelId: string;
  modelName: string;
  prompt: string;
  request: PlaygroundImageGenerationParams | PlaygroundImageEditParams;
  outputs: PreviewImage[];
  rawResponse?: unknown;
  error?: string;
  createdAt: number;
}

interface PendingParameters {
  modelId: string;
  size?: string;
  aspectRatio?: string;
  quality?: string;
}

const previewSource = (image: PlaygroundImageOutput) => {
  if (image.url) return image.url;
  if (image.b64_json) return `data:image/png;base64,${image.b64_json}`;
  return '';
};

const createId = () => globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`;

const normalizeUploadURL = (value: string) => {
  const url = value.trim();
  if (!url || /^(https?:|data:)/i.test(url)) return url;
  return url.startsWith('//') ? `https:${url}` : `https://${url}`;
};

const formatTime = (timestamp: number) => new Intl.DateTimeFormat('zh-CN', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
}).format(timestamp);

const formatDateTime = (timestamp: number) => new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
}).format(timestamp);

const JsonBlock: React.FC<{ value: unknown; emptyText?: string }> = ({ value, emptyText = '暂无数据' }) => (
  value === undefined
    ? <div className="px-3 py-4 text-center text-xs text-[var(--text-tertiary)]">{emptyText}</div>
    : <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all p-3 text-xs leading-5 text-[var(--text-secondary)]">{JSON.stringify(value, null, 2)}</pre>
);

const ImageTab: React.FC<ImageTabProps> = ({ tokenId }) => {
  const [models, setModels] = useState<PlaygroundImageModel[]>([]);
  const [model, setModel] = useState('');
  const [modelSearch, setModelSearch] = useState('');
  const [modelReloadKey, setModelReloadKey] = useState(0);
  const [operation, setOperation] = useState<ImageOperation>('generate');
  const [prompt, setPrompt] = useState('');
  const [size, setSize] = useState('');
  const [aspectRatio, setAspectRatio] = useState('');
  const [quality, setQuality] = useState('');
  const [referenceImages, setReferenceImages] = useState<ReferenceImage[]>([]);
  const [history, setHistory] = useState<ImageHistoryItem[]>([]);
  const [selectedHistoryId, setSelectedHistoryId] = useState('');
  const [selectedOutputIndex, setSelectedOutputIndex] = useState(0);
  const [loadingModels, setLoadingModels] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [catalogError, setCatalogError] = useState('');
  const [submitError, setSubmitError] = useState('');
  const requestRef = useRef<AbortController | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const promptRef = useRef<HTMLTextAreaElement>(null);
  const composerRef = useRef<HTMLElement>(null);
  const referenceImagesRef = useRef<ReferenceImage[]>([]);
  const pendingParametersRef = useRef<PendingParameters | null>(null);
  const activeTokenRef = useRef(tokenId);
  activeTokenRef.current = tokenId;

  useEffect(() => {
    referenceImagesRef.current = referenceImages;
  }, [referenceImages]);

  useEffect(() => () => {
    requestRef.current?.abort();
    referenceImagesRef.current.forEach(image => URL.revokeObjectURL(image.preview));
  }, []);

  useEffect(() => {
    requestRef.current?.abort();
    requestRef.current = null;
    pendingParametersRef.current = null;
    setSubmitting(false);
    setHistory([]);
    setSelectedHistoryId('');
    setSelectedOutputIndex(0);
    setSubmitError('');
    setModelSearch('');
    setReferenceImages(current => {
      current.forEach(image => URL.revokeObjectURL(image.preview));
      return [];
    });
  }, [tokenId]);

  useEffect(() => {
    let active = true;
    setLoadingModels(true);
    setCatalogError('');
    playgroundListImageModels(tokenId)
      .then(items => {
        if (!active) return;
        setModels(items);
        setModel(current => {
          if (items.some(item => item.id === current)) return current;
          return items.find(item => item.supports_generation)?.id || items.find(item => item.supports_edit)?.id || '';
        });
      })
      .catch(reason => {
        if (!active) return;
        setModels([]);
        setModel('');
        setCatalogError(reason instanceof Error ? reason.message : '图片模型读取失败');
      })
      .finally(() => {
        if (active) setLoadingModels(false);
      });
    return () => {
      active = false;
    };
  }, [modelReloadKey, tokenId]);

  const supportsSelectedOperation = useCallback((item: PlaygroundImageModel) => (
    operation === 'generate' ? item.supports_generation : item.supports_edit
  ), [operation]);
  const selectedModel = useMemo(
    () => models.find(item => item.id === model && supportsSelectedOperation(item)),
    [model, models, supportsSelectedOperation],
  );
  const selectedOptions = operation === 'generate' ? selectedModel?.generation_options : selectedModel?.edit_options;
  const sizes = selectedOptions?.sizes || [];
  const aspectRatios = selectedOptions?.aspect_ratios || [];
  const qualities = selectedOptions?.qualities || [];

  useEffect(() => {
    const pending = pendingParametersRef.current;
    if (pending && pending.modelId === selectedModel?.id) {
      const nextSize = pending.size && sizes.includes(pending.size) ? pending.size : '';
      const nextAspectRatio = pending.aspectRatio && aspectRatios.includes(pending.aspectRatio) ? pending.aspectRatio : '';
      setSize(nextSize || (!nextAspectRatio ? sizes[0] || '' : ''));
      setAspectRatio(nextAspectRatio);
      setQuality(pending.quality && qualities.includes(pending.quality) ? pending.quality : qualities[0] || '');
      pendingParametersRef.current = null;
    } else if (sizes.length > 0) {
      setSize(sizes[0]);
      setAspectRatio('');
      setQuality(current => qualities.includes(current) ? current : qualities[0] || '');
    } else {
      setSize('');
      setAspectRatio(aspectRatios[0] || '');
      setQuality(current => qualities.includes(current) ? current : qualities[0] || '');
    }
    setSubmitError('');
  }, [operation, selectedModel]);

  const operationOptions = useMemo(() => {
    const options: Array<{ value: ImageOperation; label: string }> = [];
    if (models.some(item => item.supports_generation)) options.push({ value: 'generate', label: '生成' });
    if (models.some(item => item.supports_edit)) options.push({ value: 'edit', label: '编辑' });
    return options;
  }, [models]);

  useEffect(() => {
    if (loadingModels || models.length === 0) return;
    if (models.some(supportsSelectedOperation)) return;
    setOperation(models.some(item => item.supports_generation) ? 'generate' : 'edit');
  }, [loadingModels, models, supportsSelectedOperation]);

  useEffect(() => {
    const available = models.filter(supportsSelectedOperation);
    setModel(current => available.some(item => item.id === current) ? current : available[0]?.id || '');
  }, [models, supportsSelectedOperation]);

  const filteredModels = useMemo(() => {
    const keyword = modelSearch.trim().toLocaleLowerCase();
    const available = models.filter(supportsSelectedOperation);
    const matches = keyword
      ? available.filter(item => `${item.name} ${item.id} ${item.description || ''}`.toLocaleLowerCase().includes(keyword))
      : available;
    if (selectedModel && !matches.some(item => item.id === selectedModel.id)) return [selectedModel, ...matches];
    return matches;
  }, [modelSearch, models, selectedModel, supportsSelectedOperation]);

  const modelOptions = filteredModels.map(item => ({
    value: item.id,
    label: item.name && item.name !== item.id ? `${item.name} (${item.id})` : item.id,
  }));
  const uploadedReferences = referenceImages.filter(image => image.url && !image.error);
  const hasUploadingReference = referenceImages.some(image => image.uploading);
  const supportsOperation = operation === 'generate'
    ? Boolean(selectedModel?.supports_generation)
    : Boolean(selectedModel?.supports_edit);
  const canSubmit = supportsOperation
    && Boolean(prompt.trim())
    && !submitting
    && (operation !== 'edit' || (!hasUploadingReference && uploadedReferences.length > 0));
  const selectedHistory = history.find(item => item.id === selectedHistoryId);
  const selectedOutput = selectedHistory?.outputs[selectedOutputIndex] || selectedHistory?.outputs[0];
  const hasEditModel = models.some(item => item.supports_edit);

  const replaceReferences = useCallback((next: ReferenceImage[]) => {
    setReferenceImages(current => {
      current.forEach(image => URL.revokeObjectURL(image.preview));
      return next;
    });
  }, []);

  const focusComposer = useCallback(() => {
    window.requestAnimationFrame(() => {
      composerRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
      promptRef.current?.focus();
    });
  }, []);

  const stageParameters = (request: PlaygroundImageGenerationParams, modelId = request.model) => {
    pendingParametersRef.current = {
      modelId,
      size: request.size,
      aspectRatio: request.aspect_ratio,
      quality: request.quality,
    };
    setSize(request.size || '');
    setAspectRatio(request.aspect_ratio || '');
    setQuality(request.quality || '');
  };

  const reuseConfiguration = (item: ImageHistoryItem) => {
    stageParameters(item.request);
    setOperation(item.operation);
    setModel(item.modelId);
    setModelSearch('');
    setPrompt(item.prompt);
    if (item.operation === 'edit' && 'image_urls' in item.request) {
      replaceReferences(item.request.image_urls.map((url, index) => ({
        id: createId(),
        name: `参考图 ${index + 1}`,
        preview: url,
        url,
        uploading: false,
      })));
    } else {
      replaceReferences([]);
    }
    setSelectedHistoryId('');
    setSubmitError('');
    focusComposer();
  };

  const editFromOutput = (item: ImageHistoryItem, output: PreviewImage) => {
    const editModel = models.find(candidate => candidate.id === item.modelId && candidate.supports_edit)
      || models.find(candidate => candidate.supports_edit);
    if (!editModel) {
      setSubmitError('当前令牌没有支持图片编辑的模型');
      setSelectedHistoryId('');
      focusComposer();
      return;
    }
    stageParameters(item.request, editModel.id);
    setOperation('edit');
    setModel(editModel.id);
    setModelSearch('');
    setPrompt('');
    replaceReferences([{
      id: createId(),
      name: '生成结果',
      preview: output.src,
      url: output.src,
      uploading: false,
    }]);
    setSelectedHistoryId('');
    setSubmitError('');
    focusComposer();
  };

  const uploadReference = useCallback(async (file: File) => {
    const id = createId();
    const preview = URL.createObjectURL(file);
    setReferenceImages(current => [...current, { id, name: file.name, preview, uploading: true }]);
    try {
      const result = await playgroundUploadFile(tokenId, file);
      if (activeTokenRef.current !== tokenId) return;
      const url = normalizeUploadURL(result.url);
      if (!url) throw new Error('上传响应中没有图片地址');
      setReferenceImages(current => current.map(image => image.id === id
        ? { ...image, uploading: false, url }
        : image));
    } catch (reason) {
      if (activeTokenRef.current !== tokenId) return;
      const message = reason instanceof Error ? reason.message : '上传失败';
      setReferenceImages(current => current.map(image => image.id === id
        ? { ...image, uploading: false, error: message }
        : image));
      setSubmitError(message);
    }
  }, [tokenId]);

  const handleReferenceFiles = (event: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files || []);
    event.target.value = '';
    const available = Math.max(0, 16 - referenceImages.length);
    if (available === 0) {
      setSubmitError('最多可添加 16 张参考图');
      return;
    }
    const accepted = files.slice(0, available).filter(file => {
      const valid = ['image/png', 'image/jpeg', 'image/webp'].includes(file.type) && file.size <= 20 * 1024 * 1024;
      if (!valid) setSubmitError('参考图需为 PNG、JPEG 或 WebP，单张不超过 20 MiB');
      return valid;
    });
    accepted.forEach(file => void uploadReference(file));
  };

  const removeReference = (id: string) => {
    setReferenceImages(current => {
      const target = current.find(image => image.id === id);
      if (target) URL.revokeObjectURL(target.preview);
      return current.filter(image => image.id !== id);
    });
  };

  const handleSubmit = async () => {
    if (!canSubmit || !selectedModel) return;
    const controller = new AbortController();
    requestRef.current = controller;
    const requestBase: PlaygroundImageGenerationParams = {
      model: selectedModel.id,
      prompt: prompt.trim(),
      size: sizes.includes(size) ? size : undefined,
      aspect_ratio: aspectRatios.includes(aspectRatio) ? aspectRatio : undefined,
      quality: qualities.includes(quality) ? quality : undefined,
    };
    const request: PlaygroundImageGenerationParams | PlaygroundImageEditParams = operation === 'edit'
      ? { ...requestBase, image_urls: uploadedReferences.map(image => image.url!) }
      : requestBase;
    const historyId = createId();
    const pending: ImageHistoryItem = {
      id: historyId,
      status: 'generating',
      operation,
      modelId: selectedModel.id,
      modelName: selectedModel.name || selectedModel.id,
      prompt: request.prompt,
      request,
      outputs: [],
      createdAt: Date.now(),
    };
    setHistory(current => [pending, ...current]);
    setSubmitting(true);
    setSubmitError('');
    let response: PlaygroundImageGenerationResponse | undefined;
    try {
      response = operation === 'edit'
        ? await playgroundEditImage(tokenId, request as PlaygroundImageEditParams, controller.signal)
        : await playgroundGenerateImage(tokenId, request, controller.signal);
      const outputs = response.data
        .map(image => ({ ...image, src: previewSource(image) }))
        .filter(image => image.src);
      if (outputs.length === 0) {
        throw new PlaygroundImageRequestError('响应中没有可显示的图片', response);
      }
      setHistory(current => current.map(item => item.id === historyId
        ? { ...item, status: 'completed', outputs, rawResponse: response }
        : item));
    } catch (reason) {
      if (reason instanceof DOMException && reason.name === 'AbortError') return;
      const message = reason instanceof Error ? reason.message : '图片生成失败';
      const rawResponse = reason instanceof PlaygroundImageRequestError ? reason.response : response;
      setHistory(current => current.map(item => item.id === historyId
        ? { ...item, status: 'failed', error: message, rawResponse }
        : item));
      setSubmitError(message);
    } finally {
      if (requestRef.current === controller) {
        requestRef.current = null;
        setSubmitting(false);
      }
    }
  };

  const clearHistory = () => {
    setHistory([]);
    setSelectedHistoryId('');
    setSelectedOutputIndex(0);
  };

  const openHistory = (id: string) => {
    setSelectedOutputIndex(0);
    setSelectedHistoryId(id);
  };

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 overflow-y-auto overscroll-contain md:overflow-hidden">
      <section className="flex min-h-[18rem] flex-none flex-col md:min-h-0 md:flex-1">
        <header className="mb-2 flex shrink-0 items-center justify-between gap-3 px-1">
          <div className="flex min-w-0 items-center gap-2">
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-[var(--primary-lighter)] text-[var(--primary)]"><Images size={16} /></span>
            <div className="min-w-0">
              <h2 className="text-sm font-semibold text-[var(--text-primary)]">作品</h2>
              <p className="text-[11px] text-[var(--text-tertiary)]">本次试用 · {history.length} 个任务</p>
            </div>
          </div>
          {history.length > 0 && (
            <button type="button" onClick={clearHistory} title="清空本次记录" aria-label="清空本次记录" className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-[var(--text-secondary)] transition-colors hover:bg-[var(--surface)] hover:text-red-500">
              <Trash2 size={15} />
            </button>
          )}
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto pr-1">
          {history.length === 0 ? (
            <div className="flex h-full min-h-64 items-center justify-center rounded-xl border border-dashed border-[var(--border-soft)] bg-[var(--surface-card)]/40 text-[var(--text-tertiary)]">
              <div className="text-center">
                <span className="mx-auto mb-3 flex h-14 w-14 items-center justify-center rounded-2xl bg-[var(--primary-lighter)] text-[var(--primary)]"><Sparkles size={26} /></span>
                <p className="text-sm font-medium text-[var(--text-secondary)]">从下方开始创作</p>
                <p className="mt-1 text-xs">结果会显示在这里</p>
              </div>
            </div>
          ) : (
            <div className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,13rem),1fr))] gap-3 pb-1">
              {history.map(item => {
                const cover = item.outputs[0];
                return (
                  <button key={item.id} type="button" onClick={() => openHistory(item.id)} className="group min-w-0 overflow-hidden rounded-xl border border-[var(--border-soft)] bg-[var(--surface-card)] text-left shadow-sm transition-all hover:-translate-y-0.5 hover:border-[var(--primary)] hover:shadow-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)]/30">
                    <div className="relative aspect-square overflow-hidden bg-[var(--surface)]">
                      {item.status === 'generating' ? (
                        <div className="flex h-full flex-col items-center justify-center gap-2 text-[var(--primary)]"><Loader2 size={28} className="animate-spin" /><span className="text-xs">生成中</span></div>
                      ) : item.status === 'failed' ? (
                        <div className="flex h-full flex-col items-center justify-center gap-2 px-4 text-center text-red-500"><XCircle size={30} /><span className="line-clamp-3 text-xs">{item.error}</span></div>
                      ) : cover ? (
                        <img src={cover.src} alt={item.prompt} className="h-full w-full object-cover transition-transform duration-300 group-hover:scale-[1.025]" />
                      ) : (
                        <div className="flex h-full items-center justify-center text-[var(--text-tertiary)]"><ImageIcon size={30} /></div>
                      )}
                      <span className={`absolute left-2 top-2 inline-flex items-center gap-1 rounded-md px-2 py-1 text-[10px] font-semibold shadow-sm backdrop-blur-md ${item.status === 'completed' ? 'bg-emerald-500/90 text-white' : item.status === 'failed' ? 'bg-red-500/90 text-white' : 'bg-[var(--surface-card)]/90 text-[var(--primary)]'}`}>
                        {item.status === 'completed' ? <CheckCircle2 size={11} /> : item.status === 'failed' ? <AlertCircle size={11} /> : <Clock3 size={11} />}
                        {item.status === 'completed' ? '已完成' : item.status === 'failed' ? '失败' : '处理中'}
                      </span>
                      {item.outputs.length > 1 && (
                        <span className="absolute right-2 top-2 inline-flex items-center gap-1 rounded-md bg-black/55 px-2 py-1 text-[10px] font-semibold text-white backdrop-blur-md"><Images size={11} />{item.outputs.length}</span>
                      )}
                    </div>
                    <div className="space-y-1.5 p-3">
                      <p className="line-clamp-2 min-h-10 text-sm font-medium leading-5 text-[var(--text-primary)]">{item.prompt}</p>
                      <div className="flex min-w-0 items-center justify-between gap-2 text-[11px] text-[var(--text-tertiary)]">
                        <span className="min-w-0 truncate">{item.modelName}</span>
                        <span className="shrink-0">{item.operation === 'edit' ? '编辑' : '生成'} · {formatTime(item.createdAt)}</span>
                      </div>
                    </div>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      </section>

      <section ref={composerRef} aria-label="图片创作工作台" className="flex-none overflow-hidden rounded-xl border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-[0_-8px_30px_rgba(15,23,42,0.05)] backdrop-blur-xl">
        <div className="grid gap-2 border-b border-[var(--border-soft)] p-2.5 sm:grid-cols-[auto_minmax(8rem,0.55fr)_minmax(13rem,1fr)] sm:items-center">
          {operationOptions.length > 0 ? (
            <SegmentedControl<ImageOperation>
              options={operationOptions}
              value={operation}
              onChange={nextOperation => {
                pendingParametersRef.current = null;
                setOperation(nextOperation);
              }}
              ariaLabel="图片操作"
            />
          ) : (
            <div className="flex h-9 items-center justify-center rounded-lg border border-[var(--border-soft)] text-xs text-[var(--text-tertiary)]">暂无可用操作</div>
          )}
          <label className="relative block min-w-0">
            <span className="sr-only">筛选模型</span>
            <Search size={14} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-tertiary)]" />
            <input value={modelSearch} onChange={event => setModelSearch(event.target.value)} placeholder="筛选模型" className="h-9 w-full rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] pl-9 pr-3 text-sm text-[var(--text-primary)] outline-none transition-colors focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--primary)]/20" />
          </label>
          <div className="min-w-0">
            <Select options={modelOptions} value={model} onChange={value => {
              pendingParametersRef.current = null;
              setModel(value);
            }} placeholder={loadingModels ? '加载模型中...' : modelOptions.length === 0 ? `暂无支持${operation === 'edit' ? '编辑' : '生成'}的模型` : '选择模型'} disabled={loadingModels || modelOptions.length === 0} />
          </div>
        </div>

        {(selectedModel?.description || catalogError) && (
          <div className="flex min-h-8 items-center justify-between gap-3 border-b border-[var(--border-soft)] px-3 py-1.5 text-xs">
            {catalogError ? (
              <div role="alert" className="flex min-w-0 items-center gap-2 text-red-600"><AlertCircle size={14} className="shrink-0" /><span className="truncate">{catalogError}</span></div>
            ) : (
              <p className="min-w-0 truncate text-[var(--text-tertiary)]">{selectedModel?.description}</p>
            )}
            {catalogError && (
              <button type="button" onClick={() => setModelReloadKey(key => key + 1)} title="重新读取模型" aria-label="重新读取模型" className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-red-600 hover:bg-red-50"><RefreshCw size={14} className={loadingModels ? 'animate-spin' : ''} /></button>
            )}
          </div>
        )}

        {operation === 'edit' && (
          <div className="border-b border-[var(--border-soft)] px-3 py-2">
            <input ref={fileInputRef} type="file" accept="image/png,image/jpeg,image/webp" multiple className="hidden" onChange={handleReferenceFiles} />
            <div className="flex min-w-0 items-center gap-2 overflow-x-auto pb-0.5">
              <button type="button" onClick={() => fileInputRef.current?.click()} disabled={submitting || referenceImages.length >= 16} className="flex h-16 w-16 shrink-0 flex-col items-center justify-center gap-1 rounded-lg border border-dashed border-[var(--border-soft)] text-[11px] font-medium text-[var(--primary)] transition-colors hover:border-[var(--primary)] hover:bg-[var(--primary-lighter)] disabled:cursor-not-allowed disabled:opacity-50">
                <Upload size={17} />添加
              </button>
              {referenceImages.map((image, index) => (
                <div key={image.id} className={`group relative h-16 w-16 shrink-0 overflow-hidden rounded-lg border bg-[var(--surface)] ${image.error ? 'border-red-300' : 'border-[var(--border-soft)]'}`} title={image.error || image.name}>
                  <img src={image.preview} alt={`参考图 ${index + 1}`} className="h-full w-full object-cover" />
                  {image.uploading && <div className="absolute inset-0 flex items-center justify-center bg-black/45"><Loader2 size={17} className="animate-spin text-white" /></div>}
                  {!image.uploading && !image.error && <CheckCircle2 size={14} className="absolute bottom-1 right-1 rounded-full bg-white text-emerald-500" />}
                  {image.error && <AlertCircle size={14} className="absolute bottom-1 right-1 rounded-full bg-white text-red-500" />}
                  <button type="button" onClick={() => removeReference(image.id)} title="移除参考图" aria-label={`移除 ${image.name}`} className="absolute right-1 top-1 flex h-6 w-6 items-center justify-center rounded-md bg-black/60 text-white opacity-100 transition-opacity hover:bg-red-500 md:opacity-0 md:group-hover:opacity-100"><X size={13} /></button>
                </div>
              ))}
              {referenceImages.length > 0 && <span className="shrink-0 px-1 text-[11px] text-[var(--text-tertiary)]">{referenceImages.length}/16</span>}
            </div>
          </div>
        )}

        <div className="p-3">
          <label className="block min-w-0">
            <span className="sr-only">提示词</span>
            <textarea ref={promptRef} value={prompt} onChange={event => setPrompt(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) void handleSubmit(); }} rows={2} placeholder={operation === 'edit' ? '描述需要如何修改参考图...' : '描述需要生成的图片...'} className="w-full resize-none rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 py-2 text-sm leading-6 text-[var(--text-primary)] outline-none transition-colors focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--primary)]/20" />
          </label>

          <div className="mt-2 flex flex-col gap-2 lg:flex-row lg:items-end">
            {(sizes.length > 0 || aspectRatios.length > 0 || qualities.length > 0) ? (
              <div className="grid min-w-0 flex-1 grid-cols-2 gap-2 sm:grid-cols-3 lg:flex lg:flex-wrap">
                {sizes.length > 0 && (
                  <div className="min-w-0 lg:w-36">
                    <div className="mb-1 text-[11px] font-medium text-[var(--text-secondary)]">尺寸{aspectRatios.length > 0 ? '（二选一）' : ''}</div>
                    <Select options={sizes.map(value => ({ label: value.replace(/x/i, ' × '), value }))} value={size} onChange={value => { setSize(value); setAspectRatio(''); }} />
                  </div>
                )}
                {aspectRatios.length > 0 && (
                  <div className="min-w-0 lg:w-32">
                    <div className="mb-1 text-[11px] font-medium text-[var(--text-secondary)]">比例{sizes.length > 0 ? '（二选一）' : ''}</div>
                    <Select options={aspectRatios.map(value => ({ label: value, value }))} value={aspectRatio} onChange={value => { setAspectRatio(value); setSize(''); }} />
                  </div>
                )}
                {qualities.length > 0 && (
                  <div className="min-w-0 lg:w-32">
                    <div className="mb-1 text-[11px] font-medium text-[var(--text-secondary)]">质量</div>
                    <Select options={qualities.map(value => ({ label: value, value }))} value={quality} onChange={setQuality} />
                  </div>
                )}
              </div>
            ) : (
              <div className="min-w-0 flex-1 text-xs text-[var(--text-tertiary)]">当前模型没有额外参数</div>
            )}
            <Button type="button" className="h-10 w-full shrink-0 lg:w-auto lg:min-w-32" loading={submitting} disabled={!canSubmit} onClick={() => void handleSubmit()}>
              {!submitting && <Play size={15} />}{submitting ? '处理中' : operation === 'edit' ? '开始编辑' : '开始生成'}
            </Button>
          </div>

          {submitError && <div role="alert" className="mt-2 flex items-start gap-2 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600"><AlertCircle size={15} className="mt-0.5 shrink-0" /><span className="min-w-0 break-words">{submitError}</span></div>}
        </div>
      </section>

      <Drawer
        open={Boolean(selectedHistory)}
        onClose={() => setSelectedHistoryId('')}
        title="图片任务详情"
        subtitle={selectedHistory ? `${selectedHistory.modelName} · ${selectedHistory.operation === 'edit' ? '编辑' : '生成'}` : undefined}
        width="max-w-5xl"
        panelClassName="bg-[var(--surface-card)]"
        headerActions={selectedOutput?.src ? (
          <a href={selectedOutput.src} target="_blank" rel="noreferrer" title="打开原图" aria-label="打开原图" className="flex h-9 w-9 items-center justify-center rounded-lg text-[var(--primary)] hover:bg-[var(--surface)]"><ExternalLink size={17} /></a>
        ) : undefined}
      >
        {selectedHistory && (
          <div className="min-h-0 flex-1 overflow-y-auto p-4 sm:p-5">
            {selectedOutput && (
              <a href={selectedOutput.src} target="_blank" rel="noreferrer" className="flex max-h-[58vh] min-h-72 items-center justify-center overflow-hidden rounded-xl border border-[var(--border-soft)] bg-[var(--surface)]">
                <img src={selectedOutput.src} alt="当前输出图片" className="max-h-[58vh] w-full object-contain" />
              </a>
            )}

            {selectedHistory.outputs.length > 1 && (
              <div className="mt-3 flex gap-2 overflow-x-auto pb-1">
                {selectedHistory.outputs.map((image, index) => (
                  <button key={`${image.src.slice(0, 80)}-${index}`} type="button" onClick={() => setSelectedOutputIndex(index)} aria-label={`查看输出图片 ${index + 1}`} className={`h-16 w-16 shrink-0 overflow-hidden rounded-lg border-2 bg-[var(--surface)] transition-colors ${index === selectedOutputIndex ? 'border-[var(--primary)]' : 'border-transparent hover:border-[var(--border-soft)]'}`}>
                    <img src={image.src} alt="" className="h-full w-full object-cover" />
                  </button>
                ))}
              </div>
            )}

            <div className="mt-4 flex flex-wrap gap-2">
              <Button type="button" variant="secondary" onClick={() => reuseConfiguration(selectedHistory)}><RotateCcw size={15} />复用配置</Button>
              {selectedOutput && hasEditModel && (
                <Button type="button" onClick={() => editFromOutput(selectedHistory, selectedOutput)}><Pencil size={15} />作为参考图编辑</Button>
              )}
            </div>

            {selectedHistory.error && <div className="mt-4 flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700"><AlertCircle size={16} className="mt-0.5 shrink-0" /><span className="whitespace-pre-wrap break-words">{selectedHistory.error}</span></div>}

            <section className="mt-4 border-y border-[var(--border-soft)] py-4">
              <div className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
                <div><div className="text-xs text-[var(--text-tertiary)]">状态</div><div className="mt-0.5 font-medium text-[var(--text-primary)]">{selectedHistory.status === 'completed' ? '已完成' : selectedHistory.status === 'failed' ? '失败' : '处理中'}</div></div>
                <div><div className="text-xs text-[var(--text-tertiary)]">操作</div><div className="mt-0.5 font-medium text-[var(--text-primary)]">{selectedHistory.operation === 'edit' ? '图片编辑' : '图片生成'}</div></div>
                <div><div className="text-xs text-[var(--text-tertiary)]">提交时间</div><div className="mt-0.5 font-medium text-[var(--text-primary)]">{formatDateTime(selectedHistory.createdAt)}</div></div>
                <div className="sm:col-span-2 lg:col-span-3"><div className="text-xs text-[var(--text-tertiary)]">模型</div><div className="mt-0.5 break-all font-mono text-[var(--text-primary)]">{selectedHistory.modelId}</div></div>
                {(selectedHistory.request.size || selectedHistory.request.aspect_ratio || selectedHistory.request.quality || ('image_urls' in selectedHistory.request && selectedHistory.request.image_urls.length > 0)) && (
                  <div className="flex flex-wrap gap-2 sm:col-span-2 lg:col-span-3">
                    {selectedHistory.request.size && <span className="rounded-md bg-[var(--surface)] px-2 py-1 text-xs text-[var(--text-secondary)]">尺寸 {selectedHistory.request.size.replace(/x/i, ' × ')}</span>}
                    {selectedHistory.request.aspect_ratio && <span className="rounded-md bg-[var(--surface)] px-2 py-1 text-xs text-[var(--text-secondary)]">比例 {selectedHistory.request.aspect_ratio}</span>}
                    {selectedHistory.request.quality && <span className="rounded-md bg-[var(--surface)] px-2 py-1 text-xs text-[var(--text-secondary)]">质量 {selectedHistory.request.quality}</span>}
                    {'image_urls' in selectedHistory.request && <span className="rounded-md bg-[var(--surface)] px-2 py-1 text-xs text-[var(--text-secondary)]">参考图 {selectedHistory.request.image_urls.length} 张</span>}
                  </div>
                )}
                <div className="sm:col-span-2 lg:col-span-3"><div className="text-xs text-[var(--text-tertiary)]">提示词</div><div className="mt-1 whitespace-pre-wrap leading-6 text-[var(--text-primary)]">{selectedHistory.prompt}</div></div>
              </div>
            </section>

            <div className="mt-4 space-y-2">
              <details className="group overflow-hidden rounded-lg border border-[var(--border-soft)]">
                <summary className="flex cursor-pointer list-none items-center justify-between bg-[var(--surface)] px-3 py-2.5 text-xs font-semibold text-[var(--text-secondary)]">
                  请求 JSON<ChevronDown size={15} className="transition-transform group-open:rotate-180" />
                </summary>
                <JsonBlock value={selectedHistory.request} />
              </details>
              <details className="group overflow-hidden rounded-lg border border-[var(--border-soft)]">
                <summary className="flex cursor-pointer list-none items-center justify-between bg-[var(--surface)] px-3 py-2.5 text-xs font-semibold text-[var(--text-secondary)]">
                  原始响应 JSON<ChevronDown size={15} className="transition-transform group-open:rotate-180" />
                </summary>
                <JsonBlock value={selectedHistory.rawResponse} />
              </details>
            </div>
          </div>
        )}
      </Drawer>
    </div>
  );
};

export default ImageTab;
