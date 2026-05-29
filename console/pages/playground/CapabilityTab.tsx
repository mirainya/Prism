import React, { useState, useEffect, useRef, useMemo, useCallback } from 'react';
import {
  Send, Loader2, Zap, AlertCircle, User as UserIcon,
  ChevronDown, ChevronRight, Bug,
  SlidersHorizontal, Plus, Search,
  X, CheckCircle2, XCircle, Upload
} from 'lucide-react';
import {
  playgroundListCapabilities, playgroundInvokeCapability,
  playgroundGetTask, playgroundListTasks, playgroundCancelTask, playgroundUploadFile,
} from '../../services/api';
import { PlaygroundCapability, CapabilityStandardParamSchema } from '../../types';
import { TaskResult, Attachment } from './types';
import StatusBadge from './StatusBadge';
import ModelSelector from './ModelSelector';
import EnumSelect from './EnumSelect';
import { CapabilityDebugPanel, CapabilityResultCard } from './CapabilityResultCard';
import {
  ACCEPTED_FILE_TYPES, FALLBACK_STANDARD_PARAMS, LONG_TEXT_FIELDS, CONTROL_FIELDS,
  CAPABILITY_TYPE_ORDER, formatTime, formatFileSize, getFileIcon,
  extractCapabilityModel, extractCapabilityPrompt, getCapabilityPromptPreview,
  getCapabilityTaskStatus, getCapabilityTypeBadgeClass, normalizeCapabilityValue,
  extractCapabilitySchema, isUploadableField, buildResultSummary,
} from './utils';

const CapabilityTab: React.FC<{ tokenId: string }> = ({ tokenId }) => {
  const [capabilities, setCapabilities] = useState<PlaygroundCapability[]>([]);
  const [selectedCap, setSelectedCap] = useState('');
  const [showCapabilityPicker, setShowCapabilityPicker] = useState(false);
  const [capabilitySearch, setCapabilitySearch] = useState('');
  const [capabilityTypeFilter, setCapabilityTypeFilter] = useState('');
  const [params, setParams] = useState<Record<string, string>>({ prompt: '' });
  const [showAdvancedParams, setShowAdvancedParams] = useState(false);
  const [tasks, setTasks] = useState<TaskResult[]>([]);
  const [selectedTaskNo, setSelectedTaskNo] = useState<string>('');
  const [taskFilter, setTaskFilter] = useState<'all' | 'current'>('current');
  const [taskSearch, setTaskSearch] = useState('');
  const [showDebugDrawer, setShowDebugDrawer] = useState(false);
  const [showParamPanel, setShowParamPanel] = useState(true);
  const [hasTouchedParamPanel, setHasTouchedParamPanel] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState('');
  const pollTimers = useRef<Record<string, ReturnType<typeof setInterval>>>({});
  const activeTokenRef = useRef(tokenId);
  const capabilityPickerRef = useRef<HTMLDivElement | null>(null);
  const capFileInputRef = useRef<HTMLInputElement>(null);
  const [capUploadingField, setCapUploadingField] = useState<string | null>(null);
  const [capAttachments, setCapAttachments] = useState<Record<string, Attachment[]>>({});
  const capAttachmentsRef = useRef<Record<string, Attachment[]>>({});

  const clearCapabilityAttachments = useCallback(() => {
    setCapAttachments(prev => {
      (Object.values(prev) as Attachment[][]).flat().forEach(att => { if (att.preview) URL.revokeObjectURL(att.preview); });
      return {};
    });
    setCapUploadingField(null);
  }, []);

  const handleFieldUpload = useCallback((key: string, file: File) => {
    if (!tokenId) return;
    const att: Attachment = {
      id: crypto.randomUUID(), file,
      preview: file.type.startsWith('image/') ? URL.createObjectURL(file) : undefined,
      uploading: true, uploaded: false, contentType: file.type,
    };
    setCapAttachments(prev => ({ ...prev, [key]: [...(prev[key] || []), att] }));
    playgroundUploadFile(tokenId, file)
      .then(result => {
        let url = result.url;
        if (url && !url.startsWith('http://') && !url.startsWith('https://')) url = 'https://' + url;
        setCapAttachments(prev => ({ ...prev, [key]: (prev[key] || []).map(a => a.id === att.id ? { ...a, uploading: false, uploaded: true, url } : a) }));
      })
      .catch(err => setCapAttachments(prev => ({ ...prev, [key]: (prev[key] || []).map(a => a.id === att.id ? { ...a, uploading: false, error: err.message || '上传失败' } : a) })));
  }, [tokenId]);

  const removeCapAttachment = useCallback((key: string, id: string) => {
    setCapAttachments(prev => {
      const list = prev[key] || [];
      const att = list.find(a => a.id === id);
      if (att?.preview) URL.revokeObjectURL(att.preview);
      return { ...prev, [key]: list.filter(a => a.id !== id) };
    });
  }, []);

  const triggerFieldUpload = useCallback((key: string) => {
    setCapUploadingField(key);
    setTimeout(() => capFileInputRef.current?.click(), 0);
  }, []);

  const mergeTask = (prev: TaskResult[], nextTask: TaskResult) => {
    const index = prev.findIndex(task => task.taskNo === nextTask.taskNo);
    if (index === -1) return [nextTask, ...prev];
    const merged = [...prev];
    merged[index] = {
      ...merged[index], ...nextTask,
      params: nextTask.params ?? merged[index].params,
      rawParams: nextTask.rawParams ?? merged[index].rawParams,
      mappedParams: nextTask.mappedParams ?? merged[index].mappedParams,
      vendorResponse: nextTask.vendorResponse ?? merged[index].vendorResponse,
      vendorTaskId: nextTask.vendorTaskId ?? merged[index].vendorTaskId,
      capabilityType: nextTask.capabilityType ?? merged[index].capabilityType,
      createdAt: nextTask.createdAt ?? merged[index].createdAt,
      startedAt: nextTask.startedAt ?? merged[index].startedAt,
      completedAt: nextTask.completedAt ?? merged[index].completedAt,
      result: nextTask.result ?? merged[index].result,
    };
    return merged;
  };

  const clearAllPolling = () => { Object.values(pollTimers.current).forEach(clearInterval); pollTimers.current = {}; };
  useEffect(() => { activeTokenRef.current = tokenId; }, [tokenId]);
  useEffect(() => { capAttachmentsRef.current = capAttachments; }, [capAttachments]);

  useEffect(() => {
    if (!tokenId) return;
    clearCapabilityAttachments();
    playgroundListCapabilities(tokenId).then(rawCaps => {
      const caps = rawCaps.filter(c => c.type !== 'chat');
      setCapabilities(caps);
      setSelectedCap(prev => prev && caps.some((cap: PlaygroundCapability) => cap.code === prev) ? prev : (caps[0]?.code || ''));
    }).catch(() => setCapabilities([]));
  }, [tokenId, clearCapabilityAttachments]);

  useEffect(() => () => {
    clearAllPolling();
    (Object.values(capAttachmentsRef.current) as Attachment[][]).flat().forEach(att => { if (att.preview) URL.revokeObjectURL(att.preview); });
  }, []);

  const currentCap = capabilities.find(c => c.code === selectedCap);
  const selectedChannel = useMemo(() => {
    if (!params.channel || !currentCap?.channels) return null;
    return currentCap.channels.find(ch => `${ch.channelType}::${ch.interactionMode || 'sync'}` === params.channel) || null;
  }, [params.channel, currentCap]);
  const currentSchemaEntries = useMemo(() => {
    if (selectedChannel?.paramSchema && Object.keys(selectedChannel.paramSchema).length > 0) {
      return extractCapabilitySchema({ ...currentCap!, standardParams: selectedChannel.paramSchema });
    }
    return extractCapabilitySchema(currentCap);
  }, [currentCap, selectedChannel]);
  const hasExplicitSchema = Boolean((selectedChannel?.paramSchema && Object.keys(selectedChannel.paramSchema).length > 0) || (currentCap?.standardParams && Object.keys(currentCap.standardParams).length > 0));

  const capabilityTypes = useMemo(() => {
    const types = new Set<string>(capabilities.map(cap => cap.type || 'other'));
    return CAPABILITY_TYPE_ORDER.filter(type => types.has(type)).concat(
      Array.from(types).filter(type => !CAPABILITY_TYPE_ORDER.includes(type as typeof CAPABILITY_TYPE_ORDER[number])).sort(),
    );
  }, [capabilities]);

  const filteredCapabilities = useMemo(() => {
    const keyword = capabilitySearch.trim().toLowerCase();
    return capabilities.filter(cap => {
      if (cap.type === 'chat') return false;
      const matchesType = !capabilityTypeFilter || (cap.type || 'other') === capabilityTypeFilter;
      const matchesKeyword = !keyword || [cap.name, cap.code, cap.description].some(field => String(field || '').toLowerCase().includes(keyword));
      return matchesType && matchesKeyword;
    });
  }, [capabilities, capabilitySearch, capabilityTypeFilter]);

  const capabilityFilteredTasks = useMemo(() => {
    if (taskFilter === 'current' && selectedCap) return tasks.filter(task => task.capability === selectedCap);
    return tasks;
  }, [tasks, taskFilter, selectedCap]);

  const filteredTasks = useMemo(() => {
    const keyword = taskSearch.trim().toLowerCase();
    if (!keyword) return capabilityFilteredTasks;
    return capabilityFilteredTasks.filter(task => {
      const promptPreview = getCapabilityPromptPreview(task).toLowerCase();
      const capabilityLabel = String(task.capabilityName || task.capability || '').toLowerCase();
      const taskNo = String(task.taskNo || '').toLowerCase();
      return taskNo.includes(keyword) || promptPreview.includes(keyword) || capabilityLabel.includes(keyword);
    });
  }, [capabilityFilteredTasks, taskSearch]);

  const selectedTask = useMemo(() => filteredTasks.find(task => task.taskNo === selectedTaskNo) || filteredTasks[0] || null, [filteredTasks, selectedTaskNo]);
  const requiredSchemaEntries = useMemo(() => currentSchemaEntries.filter(([, schema]) => schema.required), [currentSchemaEntries]);
  const completedRequiredCount = useMemo(() => requiredSchemaEntries.filter(([key]) => String(params[key] || '').trim()).length, [params, requiredSchemaEntries]);
  const missingRequiredFields = useMemo(() => requiredSchemaEntries.filter(([key]) => !String(params[key] || '').trim()).map(([, schema]) => schema.name), [requiredSchemaEntries, params]);
  const isSubmitDisabled = isSubmitting || !selectedCap || missingRequiredFields.length > 0;

  const hydrateTask = async (taskNo: string) => {
    const detail = await playgroundGetTask(tokenId, taskNo);
    setTasks(prev => mergeTask(prev, {
      taskNo: detail.taskNo, status: detail.status, progress: detail.progress || 0,
      result: detail.result, error: detail.error || '', cost: detail.cost || 0,
      rawParams: detail.rawParams, mappedParams: detail.mappedParams,
      vendorResponse: detail.vendorResponse, vendorTaskId: detail.vendorTaskId,
      createdAt: detail.createdAt, startedAt: detail.startedAt, completedAt: detail.completedAt,
    }));
    return detail;
  };

  const startPolling = (taskNo: string) => {
    if (pollTimers.current[taskNo]) return;
    pollTimers.current[taskNo] = setInterval(async () => {
      try {
        const detail = await hydrateTask(taskNo);
        if (['completed', 'success', 'failed', 'cancelled'].includes(detail.status)) {
          clearInterval(pollTimers.current[taskNo]);
          delete pollTimers.current[taskNo];
        }
      } catch {
        clearInterval(pollTimers.current[taskNo]);
        delete pollTimers.current[taskNo];
      }
    }, 3000);
  };

  const loadCapabilityHistory = async () => {
    if (!tokenId) return;
    const currentTokenId = tokenId;
    const data = await playgroundListTasks(currentTokenId, { page: 1, page_size: 20 });
    const historyTasks: TaskResult[] = data.items.map((item: any) => ({
      taskNo: item.taskNo, status: item.status, progress: item.progress || 0, result: null,
      error: item.error || '', cost: item.cost || 0, capability: item.capability,
      capabilityName: item.capabilityName,
      capabilityType: capabilities.find(cap => cap.code === item.capability)?.type,
      channel: item.channel, refunded: item.refunded, createdAt: item.createdAt, completedAt: item.completedAt,
    }));
    setTasks(historyTasks);
    setSelectedTaskNo(prev => {
      if (prev && historyTasks.some(task => task.taskNo === prev)) return prev;
      return historyTasks[0]?.taskNo || '';
    });
    const detailTargets = historyTasks.slice(0, 5);
    await Promise.allSettled(detailTargets.map(task => playgroundGetTask(currentTokenId, task.taskNo).then(detail => {
      if (currentTokenId !== activeTokenRef.current) return;
      setTasks(prev => mergeTask(prev, {
        taskNo: detail.taskNo, status: detail.status, progress: detail.progress || 0,
        result: detail.result, error: detail.error || '', cost: detail.cost || 0,
        rawParams: detail.rawParams, mappedParams: detail.mappedParams,
        vendorResponse: detail.vendorResponse, vendorTaskId: detail.vendorTaskId,
        createdAt: detail.createdAt, startedAt: detail.startedAt, completedAt: detail.completedAt,
      }));
    })));
    historyTasks.forEach(task => {
      if (['pending', 'processing', 'running'].includes(task.status)) startPolling(task.taskNo);
    });
  };
  useEffect(() => {
    clearAllPolling(); setTasks([]); setSelectedTaskNo(''); setShowDebugDrawer(false);
    setHasTouchedParamPanel(false); setShowParamPanel(true);
    if (!tokenId) return;
    loadCapabilityHistory().catch(() => setTasks([]));
  }, [tokenId]);

  useEffect(() => {
    if (!selectedTaskNo || !tokenId) return;
    hydrateTask(selectedTaskNo).then(detail => {
      if (['pending', 'processing', 'running'].includes(detail.status)) startPolling(detail.taskNo);
    }).catch(() => undefined);
  }, [selectedTaskNo, tokenId]);

  useEffect(() => {
    if (!showCapabilityPicker) return;
    const handlePointerDownOutside = (event: MouseEvent) => {
      if (!capabilityPickerRef.current?.contains(event.target as Node)) setShowCapabilityPicker(false);
    };
    document.addEventListener('mousedown', handlePointerDownOutside);
    return () => document.removeEventListener('mousedown', handlePointerDownOutside);
  }, [showCapabilityPicker]);

  useEffect(() => {
    setTaskFilter('current'); setShowAdvancedParams(false); setShowCapabilityPicker(false);
    setCapabilitySearch(''); setCapabilityTypeFilter('');
    setParams(prev => {
      const next: Record<string, string> = {};
      if (prev.channel) next.channel = prev.channel;
      currentSchemaEntries.forEach(([key]) => { if (typeof prev[key] === 'string') next[key] = prev[key]; });
      if (!currentSchemaEntries.some(([key]) => key === 'prompt') && !next.prompt) next.prompt = '';
      return next;
    });
  }, [selectedCap, currentSchemaEntries, currentCap?.type]);

  useEffect(() => {
    if (hasTouchedParamPanel) return;
    setShowParamPanel(!(tasks.length > 0 || !!selectedTaskNo));
  }, [tasks.length, selectedTaskNo, hasTouchedParamPanel, selectedCap]);

  const resetCapabilityFilters = () => {
    setCapabilitySearch(''); setCapabilityTypeFilter('');
  };

  const handleSelectCapability = (capabilityCode: string) => {
    clearCapabilityAttachments();
    setSelectedCap(capabilityCode);
    setShowCapabilityPicker(false);
    const cap = capabilities.find(c => c.code === capabilityCode);
    const defaults: Record<string, string> = { prompt: '' };
    if (cap?.standardParams) {
      for (const [key, schema] of Object.entries(cap.standardParams) as [string, CapabilityStandardParamSchema][]) {
        if (schema.default != null) defaults[key] = String(schema.default);
      }
    }
    setParams(prev => ({ ...defaults, prompt: prev.prompt || '' }));
  };

  const handleSubmit = async () => {
    if (!selectedCap || isSubmitting) return;
    const anyUploading = (Object.values(capAttachments) as Attachment[][]).some(list => list.some(a => a.uploading));
    if (anyUploading) { setError('请等待文件上传完成'); return; }
    setError(''); setIsSubmitting(true);
    try {
      const requestParams = Object.entries(params).reduce<Record<string, any>>((acc, [key, value]) => {
        if (key === 'channel') {
          if (value) {
            const [ch, mode] = String(value).split('::');
            acc.channel = ch;
            if (mode && mode !== 'sync') acc.interaction_mode = mode;
          }
          return acc;
        }
        const schema = (selectedChannel?.paramSchema && selectedChannel.paramSchema[key]) || currentCap?.standardParams?.[key] || FALLBACK_STANDARD_PARAMS[key];
        const stringValue = String(value || '');
        if (!schema) { const trimmed = stringValue.trim(); if (trimmed) acc[key] = trimmed; return acc; }
        const normalized = normalizeCapabilityValue(schema, stringValue);
        if (normalized !== undefined) acc[key] = normalized;
        return acc;
      }, {});

      for (const [key, list] of Object.entries(capAttachments) as [string, Attachment[]][]) {
        const urls = list.filter(a => a.uploaded && a.url).map(a => a.url!);
        if (urls.length === 0) continue;
        const cap = capabilities.find(c => c.code === selectedCap);
        const schema = cap?.standardParams?.[key] || FALLBACK_STANDARD_PARAMS[key];
        if (schema?.type === 'array' || key === 'image_urls') {
          const existing = Array.isArray(requestParams[key]) ? requestParams[key] as string[] : [];
          requestParams[key] = [...existing, ...urls];
        } else {
          requestParams[key] = urls[urls.length - 1];
        }
      }

      const { channel, interaction_mode, ...invokeParams } = requestParams;
      const res = await playgroundInvokeCapability(tokenId, selectedCap, { channel, interaction_mode, params: invokeParams });
      const taskNo = res.data?.task_id || res.data?.task_no || res.task_id || '';
      if (taskNo) {
        const newTask: TaskResult = {
          taskNo, status: 'processing', progress: 0, result: null, error: '', cost: 0,
          capability: selectedCap, capabilityName: currentCap?.name, capabilityType: currentCap?.type,
          params: requestParams, createdAt: new Date().toISOString(),
        };
        setTasks(prev => mergeTask(prev, newTask));
        setSelectedTaskNo(taskNo);
        startPolling(taskNo);
      } else {
        const syncTaskNo = `sync-${Date.now()}`;
        setTasks(prev => mergeTask(prev, {
          taskNo: syncTaskNo, status: 'completed', progress: 100, result: res.data || res,
          error: '', cost: 0, capability: selectedCap, capabilityName: currentCap?.name,
          capabilityType: currentCap?.type, params: requestParams, createdAt: new Date().toISOString(),
        }));
        setSelectedTaskNo(syncTaskNo);
      }
    } catch (err: any) {
      setError(err.message || '调用失败');
    } finally {
      setIsSubmitting(false);
    }
  };
  return (
    <div className="relative h-[calc(100dvh-180px)] md:h-[calc(100dvh-220px)] overflow-hidden">
      <input ref={capFileInputRef} type="file" accept={ACCEPTED_FILE_TYPES} className="hidden"
        onChange={e => { const file = e.target.files?.[0]; if (file && capUploadingField) handleFieldUpload(capUploadingField, file); e.target.value = ''; setCapUploadingField(null); }} />
      {showDebugDrawer && (
        <>
          <div className="absolute inset-0 z-20 bg-black/20 xl:hidden" onClick={() => setShowDebugDrawer(false)} />
          <div className="absolute inset-y-0 right-0 z-30 w-full max-w-[28rem] p-2 xl:hidden">
            <div className="relative h-full">
              <button type="button" onClick={() => setShowDebugDrawer(false)} className="absolute right-3 top-3 z-10 px-2 py-1 rounded-md bg-[var(--surface-card)]/90 border border-[var(--border-soft)] text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)]">关闭</button>
              <CapabilityDebugPanel task={selectedTask} />
            </div>
          </div>
        </>
      )}

      <div className="h-full flex gap-4">
        <div className="flex-1 flex flex-col bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] overflow-hidden min-w-0">
          <div className="px-3 md:px-4 py-2 border-b border-[var(--border-soft)] flex items-center justify-between gap-2">
            <div className="flex items-center gap-2 text-sm font-medium text-[var(--text-primary)] min-w-0">
              <Zap size={15} /> <span className="hidden sm:inline">能力任务</span>
              <StatusBadge status={selectedTask ? getCapabilityTaskStatus(selectedTask.status) : 'pending'} />
            </div>
            <div className="flex items-center gap-1.5 flex-wrap justify-end">
              <span className="text-[11px] text-[var(--text-secondary)] hidden sm:inline">{filteredTasks.length > 0 ? `${filteredTasks.length} 条` : ''}</span>
              <div className="flex items-center gap-0.5 bg-[var(--primary-lighter)] rounded-lg p-0.5">
                <button type="button" onClick={() => setTaskFilter('all')} className={`px-2 py-1 rounded-md text-[11px] font-medium transition-all ${taskFilter === 'all' ? 'bg-[var(--surface-card)] text-[var(--primary)] shadow-sm' : 'text-[var(--text-secondary)]'}`}>全部</button>
                <button type="button" onClick={() => setTaskFilter('current')} className={`px-2 py-1 rounded-md text-[11px] font-medium transition-all ${taskFilter === 'current' ? 'bg-[var(--surface-card)] text-[var(--primary)] shadow-sm' : 'text-[var(--text-secondary)]'}`}>当前</button>
              </div>
              <button type="button" onClick={() => setShowDebugDrawer(true)} className="inline-flex items-center gap-1 px-2 py-1 rounded-lg border border-[var(--border-soft)] text-[11px] text-[var(--text-secondary)] hover:bg-[var(--surface)] xl:hidden" disabled={!selectedTask}><Bug size={12} /></button>
            </div>
          </div>

          <div className="px-3 md:px-4 py-1.5 border-b border-[var(--border-soft)] space-y-3">
            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-[minmax(0,1.2fr)_220px_minmax(0,1fr)] gap-2 md:gap-3 items-start">
              <div>
                <label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">能力</label>
                <div ref={capabilityPickerRef} className="relative">
                  <button type="button" onClick={() => setShowCapabilityPicker(prev => !prev)}
                    className="w-full h-10 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 text-left hover:bg-[var(--surface)] transition-colors focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                    <div className="flex h-full items-center justify-between gap-2">
                      <div className="min-w-0 flex-1 flex items-center gap-2">
                        <span className="text-sm truncate">{currentCap?.name || '请选择能力'}</span>
                        {currentCap?.type && <span className={`shrink-0 text-[10px] px-1.5 py-0.5 rounded ${getCapabilityTypeBadgeClass(currentCap.type)}`}>{currentCap.type}</span>}
                      </div>
                      <ChevronDown size={14} className="text-[var(--text-tertiary)] flex-shrink-0" />
                    </div>
                  </button>
                  {showCapabilityPicker && (
                    <div className="absolute top-full left-0 mt-1 w-full min-w-[320px] bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-xl shadow-lg z-50 flex flex-col" style={{ maxHeight: 'min(380px, calc(100vh - 200px))' }}>
                      <div className="px-2 pt-2 pb-1 space-y-2 sticky top-0 bg-[var(--surface-card)] border-b border-[var(--border-soft)]">
                        <div className="relative">
                          <Search size={13} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--text-tertiary)]" />
                          <input type="text" value={capabilitySearch} onChange={e => setCapabilitySearch(e.target.value)} placeholder="搜索能力名 / code / 描述" className="w-full pl-8 pr-3 py-1.5 text-sm border border-[var(--border-soft)] rounded-lg bg-[var(--surface)] focus:outline-none focus:ring-1 focus:ring-[var(--primary)]" />
                        </div>
                        {capabilityTypes.length > 1 && (
                          <div className="flex items-center gap-1 flex-wrap">
                            <button type="button" onClick={() => setCapabilityTypeFilter('')} className={`px-2 py-1 rounded-md text-[11px] font-medium transition-all ${!capabilityTypeFilter ? 'bg-[var(--primary-lighter)] text-[var(--primary)]' : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}>全部</button>
                            {capabilityTypes.map(type => (
                              <button key={type} type="button" onClick={() => setCapabilityTypeFilter(type)} className={`px-2 py-1 rounded-md text-[11px] font-medium transition-all ${capabilityTypeFilter === type ? 'bg-[var(--primary-lighter)] text-[var(--primary)]' : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}>{type}</button>
                            ))}
                          </div>
                        )}
                      </div>
                      <div className="overflow-y-auto flex-1 py-1">
                        {filteredCapabilities.length === 0 ? (
                          <div className="py-4 text-center text-sm text-[var(--text-tertiary)]">无匹配结果</div>
                        ) : filteredCapabilities.map(cap => (
                          <button key={cap.code} type="button" onClick={() => handleSelectCapability(cap.code)}
                            className={`w-full text-left px-3 py-2 text-sm hover:bg-[var(--surface)] transition-colors ${selectedCap === cap.code ? 'bg-[var(--primary-lighter)] text-[var(--primary)]' : 'text-[var(--text-primary)]'}`}>
                            <div className="flex items-center gap-2 min-w-0">
                              <span className="font-medium truncate">{cap.name}</span>
                              <code className="shrink-0 text-[10px] px-1.5 py-0.5 rounded bg-[var(--surface)] text-[var(--text-secondary)]">{cap.code}</code>
                              <span className={`shrink-0 text-[10px] px-1.5 py-0.5 rounded ${getCapabilityTypeBadgeClass(cap.type)}`}>{cap.type}</span>
                            </div>
                            {cap.description && <div className="mt-0.5 text-[11px] text-[var(--text-tertiary)] truncate">{cap.description}</div>}
                          </button>
                        ))}
                      </div>
                    </div>
                  )}
                </div>
              </div>
              <div>
                <label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">渠道</label>
                <ModelSelector
                  options={currentCap?.channels?.map((ch: any) => ({ id: `${ch.channelType}::${ch.interactionMode || 'sync'}`, label: `${ch.model}${ch.interactionMode && ch.interactionMode !== 'sync' ? ` · ${ch.interactionMode}` : ''}`, provider: ch.channelName })) || []}
                  value={params.channel || ''}
                  onChange={v => setParams(prev => ({ ...prev, channel: v }))}
                  placeholder="选择渠道"
                  allOption="自动选择"
                />
              </div>
              <div>
                <label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">搜索任务</label>
                <div className="relative">
                  <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" />
                  <input type="text" value={taskSearch} onChange={e => setTaskSearch(e.target.value)} placeholder="搜索 taskNo / prompt / 能力名" className="w-full h-10 pl-9 pr-3 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface-card)] focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
                </div>
              </div>
            </div>
          </div>

          <div className="px-4 py-1 border-b border-[var(--border-soft)] text-[11px] text-[var(--text-secondary)] flex items-center gap-3 flex-wrap">
            <span>{selectedTask ? `当前任务 #${selectedTask.taskNo}` : '等待提交任务'}</span>
            {selectedTask?.capabilityName || selectedTask?.capability ? <span>能力 {selectedTask.capabilityName || selectedTask.capability}</span> : null}
            {selectedTask ? <span>模型 {extractCapabilityModel(selectedTask)}</span> : null}
            {selectedTask ? <span>渠道 {selectedTask.channel || '自动选择'}</span> : null}
            {selectedTask ? <span>费用 ¥{Number(selectedTask.cost || 0).toFixed(4)}</span> : null}
          </div>
          <div className="flex-1 overflow-y-auto p-4 space-y-4 min-h-0">
            {filteredTasks.length === 0 ? (
              <div className="flex flex-col items-center justify-center h-full text-gray-300">
                <Zap size={56} strokeWidth={1} />
                <p className="mt-3 text-sm">{taskSearch.trim() ? '没有匹配到任务，试试换个关键词' : (taskFilter === 'current' ? '当前能力下暂无任务，试试切到"全部"查看历史任务' : '选择能力，在底部输入 Prompt 开始调用')}</p>
              </div>
            ) : (
              filteredTasks.map(task => {
                const promptPreview = getCapabilityPromptPreview(task);
                const resolvedModel = extractCapabilityModel(task);
                const taskStatus = getCapabilityTaskStatus(task.status);
                const isProcessing = ['pending', 'processing', 'running'].includes(task.status);
                const isSuccess = ['completed', 'success'].includes(task.status) && !!task.result;
                const isSelected = selectedTaskNo === task.taskNo;
                return (
                  <button key={task.taskNo} type="button"
                    onClick={() => { setSelectedTaskNo(task.taskNo); if (window.innerWidth < 1280) setShowDebugDrawer(true); }}
                    className={`w-full text-left rounded-2xl border overflow-hidden transition-colors ${isSelected ? 'border-indigo-200 ring-2 ring-indigo-100' : 'border-[var(--border-soft)] hover:border-gray-300'}`}>
                    <div className="px-4 py-4 bg-gradient-to-b from-white to-gray-50 space-y-3">
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0 flex-1 space-y-3">
                          <div className="flex items-center gap-2 flex-wrap">
                            <StatusBadge status={taskStatus} />
                            <span className="text-sm font-medium text-gray-800">{task.capabilityName || task.capability || '能力任务'}</span>
                            <span className="text-xs text-[var(--text-secondary)]">{task.taskNo}</span>
                          </div>
                          <div className="grid gap-2 text-xs text-[var(--text-secondary)] sm:grid-cols-2 xl:grid-cols-4">
                            <span>渠道：{task.channel || '自动选择'}</span>
                            <span>实际模型：{resolvedModel}</span>
                            <span>进度：{task.progress || 0}%</span>
                            <span>时间：{formatTime(task.createdAt)}</span>
                          </div>
                          <div className="rounded-xl bg-[var(--primary)] text-white px-4 py-3 text-sm whitespace-pre-wrap break-words">{promptPreview}</div>
                        </div>
                        <div className="flex items-center gap-2 text-[var(--text-secondary)] flex-shrink-0">
                          <UserIcon size={16} />
                          {isSelected ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                        </div>
                      </div>
                      <div>
                        {isProcessing ? (
                          <div className="rounded-xl bg-[var(--surface)] border border-[var(--border-soft)] p-4 space-y-3">
                            <div className="flex items-center justify-between">
                              <div className="flex items-center gap-2 text-[var(--text-secondary)] text-sm"><Loader2 size={16} className="animate-spin" /><span>任务正在处理中，请稍候...</span></div>
                              <button type="button" onClick={e => { e.stopPropagation(); if (tokenId) playgroundCancelTask(tokenId, task.taskNo).then(() => setTasks(prev => prev.map(t => t.taskNo === task.taskNo ? {...t, status: 'cancelled'} : t))); }} className="px-2 py-1 text-xs text-red-600 hover:bg-red-50 rounded-lg border border-red-200">取消</button>
                            </div>
                            <div className="w-full bg-[var(--surface-card)] rounded-full h-1.5">
                              <div className="bg-[var(--primary-lighter)]0 h-1.5 rounded-full transition-all duration-500" style={{ width: `${Math.max(task.progress, 8)}%` }} />
                            </div>
                          </div>
                        ) : task.status === 'failed' ? (
                          <div className="rounded-xl bg-red-50 border border-red-200 p-4 text-sm text-red-600 whitespace-pre-wrap">{task.error || '任务失败'}</div>
                        ) : isSuccess ? (
                          <div className="rounded-xl bg-emerald-50 border border-emerald-200 px-4 py-3 text-sm text-emerald-800">{buildResultSummary(task.result)}</div>
                        ) : (
                          <div className="rounded-xl bg-[var(--surface)] border border-[var(--border-soft)] p-4 text-sm text-[var(--text-secondary)]">暂无结果</div>
                        )}
                      </div>
                      <div className="flex items-center gap-3 text-xs text-[var(--text-secondary)] flex-wrap">
                        <span>费用 ¥{Number(task.cost || 0).toFixed(4)}</span>
                        {task.vendorTaskId ? <span>vendor #{task.vendorTaskId}</span> : null}
                        {extractCapabilityPrompt(task) ? <span>提示词长度 {extractCapabilityPrompt(task).length}</span> : null}
                      </div>
                    </div>
                  </button>
                );
              })
            )}
          </div>
          {error && (
            <div className="mx-4 mb-2 px-3 py-2 bg-red-50 text-red-600 text-sm rounded-lg flex items-center gap-2">
              <AlertCircle size={14} /> {error}
            </div>
          )}

          <div className="border-t border-[var(--border-soft)] bg-[var(--surface-card)]">
            <div className="px-3 md:px-4 py-2 md:py-3 flex flex-col gap-2 md:gap-3 md:flex-row md:items-center md:justify-between">
              <button type="button" onClick={() => { setHasTouchedParamPanel(true); setShowParamPanel(prev => !prev); }}
                className="flex-1 text-left rounded-2xl border border-[var(--border-soft)] bg-[var(--surface)] px-4 py-3 hover:bg-[var(--primary-lighter)] transition-colors">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0 space-y-1.5">
                    <div className="flex items-center gap-2 text-sm font-medium text-[var(--text-primary)]">
                      <SlidersHorizontal size={15} /><span>参数面板</span>
                      <span className="text-xs text-[var(--text-secondary)]">{currentCap?.name || '未选择能力'}</span>
                    </div>
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-[var(--text-secondary)]">
                      <span>字段 {currentSchemaEntries.length}</span>
                      <span>必填 {completedRequiredCount}/{requiredSchemaEntries.length || 0}</span>
                      {!hasExplicitSchema ? <span>fallback 模式</span> : null}
                      {missingRequiredFields.length > 0 ? <span>待补充：{missingRequiredFields.join('、')}</span> : <span>可直接提交</span>}
                    </div>
                  </div>
                  <div className="flex items-center gap-1 text-xs text-[var(--primary)] flex-shrink-0">
                    <span>{showParamPanel ? '收起' : '展开'}</span>
                    {showParamPanel ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                  </div>
                </div>
              </button>
              <button onClick={handleSubmit} disabled={isSubmitDisabled}
                className="px-4 py-3 bg-[var(--primary)] text-white rounded-xl hover:opacity-90 disabled:opacity-40 disabled:cursor-not-allowed transition-colors h-[52px] flex items-center justify-center gap-2 min-w-[120px] md:self-stretch">
                {isSubmitting ? <><Loader2 size={16} className="animate-spin" /> 提交中</> : <><Send size={18} /> 提交</>}
              </button>
            </div>

            {showParamPanel && (
              <div className="px-3 md:px-4 pb-3 md:pb-4 space-y-3 overflow-y-auto" style={{ maxHeight: '240px' }}>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                  {currentSchemaEntries.map(([key, schema]) => {
                    const value = params[key] || '';
                    const commonClassName = 'w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]';
                    const label = `${schema.name}${schema.required ? ' *' : ''}`;
                    const uploadable = isUploadableField(key);
                    const fieldAttachments = capAttachments[key] || [];

                    const attachmentCards = uploadable && fieldAttachments.length > 0 ? (
                      <div className="flex flex-wrap gap-2 mt-2">
                        {fieldAttachments.map(att => (
                          <div key={att.id} className="relative group">
                            {att.preview ? (
                              <div className="relative w-16 h-16 rounded-xl overflow-hidden border border-[var(--border-soft)] bg-[var(--surface)]">
                                <img src={att.preview} alt="" className="w-full h-full object-cover" />
                                {att.uploading && <div className="absolute inset-0 bg-black/40 flex items-center justify-center"><Loader2 size={16} className="animate-spin text-white" /></div>}
                                {att.error && <div className="absolute inset-0 bg-red-500/60 flex items-center justify-center"><XCircle size={16} className="text-white" /></div>}
                                {att.uploaded && <div className="absolute bottom-0.5 right-0.5 w-4 h-4 bg-emerald-500 rounded-full flex items-center justify-center"><CheckCircle2 size={10} className="text-white" /></div>}
                                <button onClick={() => removeCapAttachment(key, att.id)} className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center md:opacity-0 md:group-hover:opacity-100 transition-all shadow-sm"><X size={10} /></button>
                              </div>
                            ) : (
                              <div className="relative flex items-center gap-2 pl-2 pr-6 py-1.5 rounded-xl border border-[var(--border-soft)] bg-[var(--surface)] max-w-[180px]">
                                <div className="w-7 h-7 rounded-lg bg-[var(--primary-lighter)] flex items-center justify-center flex-shrink-0 text-sm">{getFileIcon(att.contentType)}</div>
                                <div className="min-w-0 flex-1">
                                  <div className="text-[11px] font-medium text-[var(--text-primary)] truncate">{att.file.name}</div>
                                  <div className="text-[10px] text-[var(--text-secondary)] flex items-center gap-1">
                                    {att.uploading && <><Loader2 size={8} className="animate-spin text-indigo-500" /><span className="text-indigo-500">上传中</span></>}
                                    {att.uploaded && <><CheckCircle2 size={8} className="text-emerald-500" /><span>{formatFileSize(att.file.size)}</span></>}
                                    {att.error && <><XCircle size={8} className="text-red-500" /><span className="text-red-500 truncate">{att.error}</span></>}
                                  </div>
                                </div>
                                <button onClick={() => removeCapAttachment(key, att.id)} className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center md:opacity-0 md:group-hover:opacity-100 transition-all shadow-sm"><X size={10} /></button>
                              </div>
                            )}
                          </div>
                        ))}
                      </div>
                    ) : null;

                    const uploadArea = uploadable ? (
                      <button type="button" onClick={() => triggerFieldUpload(key)} disabled={isSubmitting}
                        className="mt-2 w-full flex items-center justify-center gap-2 px-3 py-2 border-2 border-dashed border-[var(--border-soft)] rounded-xl text-xs text-[var(--text-secondary)] hover:text-[var(--primary)] hover:border-indigo-300 hover:bg-[var(--primary-lighter)]/50 transition-all disabled:opacity-40">
                        <Upload size={14} /><span>点击上传文件</span>
                      </button>
                    ) : null;

                    const selectOptions = schema.options || schema.enumValues;
                    if (schema.type === 'enum' || (selectOptions && selectOptions.length > 0)) {
                      return (
                        <div key={key}><label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">{label}</label>
                          <EnumSelect options={selectOptions!} value={value} onChange={v => setParams(prev => ({ ...prev, [key]: v }))} placeholder="请选择" disabled={isSubmitting} description={schema.description} />
                        </div>
                      );
                    }
                    if (schema.type === 'array' && uploadable) {
                      return (<div key={key} className="md:col-span-2"><label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">{label}</label>{attachmentCards}{uploadArea}</div>);
                    }
                    if (schema.type === 'array') {
                      return (<div key={key} className="md:col-span-2"><label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">{label}</label><textarea value={value} onChange={e => setParams(prev => ({ ...prev, [key]: e.target.value }))} placeholder="每行一个值" rows={3} className={`${commonClassName} resize-none`} disabled={isSubmitting} /></div>);
                    }
                    if (schema.type === 'string' && LONG_TEXT_FIELDS.has(key)) {
                      return (<div key={key} className="md:col-span-2"><label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">{label}</label><textarea value={value} onChange={e => setParams(prev => ({ ...prev, [key]: e.target.value }))} placeholder={`请输入${schema.name}`} rows={key === 'prompt' ? 4 : 3} className={`${commonClassName} resize-none`} disabled={isSubmitting} /></div>);
                    }
                    if (uploadable) {
                      return (<div key={key} className="md:col-span-2"><label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">{label}</label>{attachmentCards}{fieldAttachments.length === 0 && uploadArea}</div>);
                    }
                    return (<div key={key}><label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">{label}</label><input type={schema.type === 'number' ? 'number' : 'text'} value={value} onChange={e => setParams(prev => ({ ...prev, [key]: e.target.value }))} placeholder={`请输入${schema.name}`} className={commonClassName} disabled={isSubmitting} /></div>);
                  })}
                </div>
                {!hasExplicitSchema && (
                  <div className="rounded-lg border border-dashed border-[var(--border-soft)] bg-[var(--surface)] px-3 py-2 text-xs text-[var(--text-secondary)]">
                    当前能力尚未配置 standard_params，已回退为基础 prompt 输入。
                    <button type="button" onClick={() => setShowAdvancedParams(prev => !prev)} className="ml-2 text-[var(--primary)] hover:text-[var(--primary)]">
                      {showAdvancedParams ? '收起高级参数' : '展开高级参数'}
                    </button>
                  </div>
                )}

                {!hasExplicitSchema && showAdvancedParams && (
                  <div>
                    <label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">高级 image_urls</label>
                    {(capAttachments['image_urls'] || []).length > 0 && (
                      <div className="flex flex-wrap gap-2 mb-2">
                        {(capAttachments['image_urls'] || []).map(att => (
                          <div key={att.id} className="relative group">
                            {att.preview ? (
                              <div className="relative w-16 h-16 rounded-xl overflow-hidden border border-[var(--border-soft)] bg-[var(--surface)]">
                                <img src={att.preview} alt="" className="w-full h-full object-cover" />
                                {att.uploading && <div className="absolute inset-0 bg-black/40 flex items-center justify-center"><Loader2 size={16} className="animate-spin text-white" /></div>}
                                {att.uploaded && <div className="absolute bottom-0.5 right-0.5 w-4 h-4 bg-emerald-500 rounded-full flex items-center justify-center"><CheckCircle2 size={10} className="text-white" /></div>}
                                <button onClick={() => removeCapAttachment('image_urls', att.id)} className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center md:opacity-0 md:group-hover:opacity-100 transition-all shadow-sm"><X size={10} /></button>
                              </div>
                            ) : (
                              <div className="relative flex items-center gap-2 pl-2 pr-6 py-1.5 rounded-xl border border-[var(--border-soft)] bg-[var(--surface)] max-w-[180px]">
                                <div className="w-7 h-7 rounded-lg bg-[var(--primary-lighter)] flex items-center justify-center flex-shrink-0 text-sm">{getFileIcon(att.contentType)}</div>
                                <div className="min-w-0 flex-1">
                                  <div className="text-[11px] font-medium text-[var(--text-primary)] truncate">{att.file.name}</div>
                                  <div className="text-[10px] text-[var(--text-secondary)] flex items-center gap-1">
                                    {att.uploading && <><Loader2 size={8} className="animate-spin text-indigo-500" /><span className="text-indigo-500">上传中</span></>}
                                    {att.uploaded && <><CheckCircle2 size={8} className="text-emerald-500" /><span>{formatFileSize(att.file.size)}</span></>}
                                  </div>
                                </div>
                                <button onClick={() => removeCapAttachment('image_urls', att.id)} className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center md:opacity-0 md:group-hover:opacity-100 transition-all shadow-sm"><X size={10} /></button>
                              </div>
                            )}
                          </div>
                        ))}
                      </div>
                    )}
                    <button type="button" onClick={() => triggerFieldUpload('image_urls')} disabled={isSubmitting}
                      className="w-full flex items-center justify-center gap-2 px-3 py-2 border-2 border-dashed border-[var(--border-soft)] rounded-xl text-xs text-[var(--text-secondary)] hover:text-[var(--primary)] hover:border-indigo-300 hover:bg-[var(--primary-lighter)]/50 transition-all disabled:opacity-40">
                      <Upload size={14} /><span>点击上传图片</span>
                    </button>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>

        <div className="hidden xl:flex xl:w-[24rem] xl:flex-shrink-0 xl:self-stretch min-w-0">
          <CapabilityDebugPanel task={selectedTask} />
        </div>
      </div>
    </div>
  );
};

export default CapabilityTab;
