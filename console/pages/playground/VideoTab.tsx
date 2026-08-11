import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import { Loader2, AlertCircle, Video, XCircle, CheckCircle2, Clock, Play, Download, Plus, Trash2, Upload, AtSign, Type, Image as ImageIcon, Images, Layers3 } from 'lucide-react';
import {
  playgroundCreateVideo, playgroundListVideos, playgroundCancelVideo,
  playgroundEstimateVideo, playgroundListVideoModels, playgroundUploadVideoAsset,
  VideoTask, VideoCreateParams, VideoContentItem, PlaygroundVideoModelOptions, PlaygroundVideoChannelOption,
} from '../../services/playgroundApi';
import { Select } from '../../components/ui';
import EnumSelect from './EnumSelect';

const RESOLUTIONS = ['480p', '720p', '1080p', '4k'];
const RATIOS = ['16:9', '9:16', '4:3', '3:4', '1:1', '21:9'];
const DURATION_OPTIONS = [
  { label: '4 秒', value: '4' },
  { label: '5 秒', value: '5' },
  { label: '8 秒', value: '8' },
  { label: '10 秒', value: '10' },
  { label: '15 秒', value: '15' },
  { label: '20 秒', value: '20' },
  { label: '30 秒', value: '30' },
];
const SEEDANCE25_DURATION_OPTIONS = DURATION_OPTIONS.filter(option => Number(option.value) >= 4);

type FilterType = 'all' | 'active' | 'completed' | 'failed';
type VideoTaskType = 'text' | 'first_frame' | 'first_last_frame' | 'multimodal';

type ReferenceKind = 'image' | 'video' | 'audio';
type ReferenceSource = 'url' | 'asset_id';

interface ReferenceInput {
  kind: ReferenceKind;
  role: VideoContentItem['role'];
  source: ReferenceSource;
  value: string;
  durationSeconds?: number;
}

const REFERENCE_LIMITS: Record<ReferenceKind, number> = { image: 9, video: 3, audio: 3 };
const SEEDANCE25_LIMITS: Record<ReferenceKind, number> = { image: 30, video: 10, audio: 10 };
const REFERENCE_LABELS: Record<ReferenceKind, string> = { image: '图片', video: '视频', audio: '音频' };

const REFERENCE_ROLES: Record<ReferenceKind, Array<{ label: string; value: VideoContentItem['role'] }>> = {
  image: [
    { label: '首帧', value: 'first_frame' },
    { label: '尾帧', value: 'last_frame' },
    { label: '参考图', value: 'reference_image' },
  ],
  video: [{ label: '参考视频', value: 'reference_video' }],
  audio: [{ label: '参考音频', value: 'reference_audio' }],
};

const REFERENCE_KIND_OPTIONS = [
  { label: '图片', value: 'image' },
  { label: '视频', value: 'video' },
  { label: '音频', value: 'audio' },
];

const REFERENCE_SOURCE_OPTIONS = [
  { label: '公网 URL', value: 'url' },
  { label: '素材 ID', value: 'asset_id' },
];

const VIDEO_TASK_TYPE_OPTIONS: Array<{ value: VideoTaskType; label: string; icon: typeof Type }> = [
  { value: 'text', label: '文生视频', icon: Type },
  { value: 'first_frame', label: '首帧生视频', icon: ImageIcon },
  { value: 'first_last_frame', label: '首尾帧视频', icon: Images },
  { value: 'multimodal', label: '多模态视频', icon: Layers3 },
];

const roleForTaskType = (taskType: VideoTaskType, kind: ReferenceKind, index = 0): VideoContentItem['role'] => {
  if (taskType === 'first_frame') return 'first_frame';
  if (taskType === 'first_last_frame') return index === 0 ? 'first_frame' : 'last_frame';
  return kind === 'image' ? 'reference_image' : REFERENCE_ROLES[kind][0].value;
};

const makeReference = (kind: ReferenceKind = 'image', taskType: VideoTaskType = 'multimodal', index = 0): ReferenceInput => ({
  kind,
  role: roleForTaskType(taskType, kind, index),
  source: 'url', value: '',
});

const getMediaDuration = (file: File): Promise<number | undefined> => {
  if (file.type.startsWith('image/')) return Promise.resolve(undefined);
  return new Promise(resolve => {
    const element = document.createElement(file.type.startsWith('audio/') ? 'audio' : 'video');
    const objectURL = URL.createObjectURL(file);
    element.preload = 'metadata';
    element.onloadedmetadata = () => {
      URL.revokeObjectURL(objectURL);
      resolve(Number.isFinite(element.duration) && element.duration > 0 ? element.duration : undefined);
    };
    element.onerror = () => {
      URL.revokeObjectURL(objectURL);
      resolve(undefined);
    };
    element.src = objectURL;
  });
};

const countReferences = (items: ReferenceInput[]) => items.reduce<Record<ReferenceKind, number>>(
  (counts, item) => ({ ...counts, [item.kind]: counts[item.kind] + 1 }),
  { image: 0, video: 0, audio: 0 },
);

const materialReferenceName = (items: ReferenceInput[], index: number) => {
  const kind = items[index].kind;
  const ordinal = items.slice(0, index + 1).filter(item => item.kind === kind).length;
  return `${REFERENCE_LABELS[kind]}${ordinal}`;
};

const isTerminal = (s: string) => ['completed', 'failed', 'cancelled'].includes(s);

const ProgressRing: React.FC<{ percent: number }> = ({ percent }) => {
  const r = 22, c = 2 * Math.PI * r;
  return (
    <div className="relative inline-flex items-center justify-center">
      <svg width="56" height="56" className="-rotate-90">
        <circle cx="28" cy="28" r={r} fill="none" stroke="var(--border-soft)" strokeWidth="4" />
        <circle cx="28" cy="28" r={r} fill="none" stroke="var(--primary)" strokeWidth="4"
          strokeDasharray={c} strokeDashoffset={c * (1 - percent / 100)} strokeLinecap="round"
          className="transition-[stroke-dashoffset] duration-500" />
      </svg>
      <span className="absolute text-xs font-semibold text-[var(--primary)]">{percent}%</span>
    </div>
  );
};

const VideoTab: React.FC<{ tokenId: string }> = ({ tokenId }) => {
  const [models, setModels] = useState<string[]>([]);
  const [modelOptions, setModelOptions] = useState<Record<string, PlaygroundVideoModelOptions>>({});
  const [channels, setChannels] = useState<PlaygroundVideoChannelOption[]>([]);
  const [channelId, setChannelId] = useState('0');
  const [model, setModel] = useState('');
  const [taskType, setTaskType] = useState<VideoTaskType>('text');
  const [prompt, setPrompt] = useState('');
  const [resolution, setResolution] = useState('720p');
  const [ratio, setRatio] = useState('16:9');
  const [duration, setDuration] = useState('5');
  const [generateAudio, setGenerateAudio] = useState(true);
  const [priority, setPriority] = useState('5');
  const [references, setReferences] = useState<ReferenceInput[]>([]);
  const [uploadingReference, setUploadingReference] = useState<number | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const [estimatedCost, setEstimatedCost] = useState<string | null>(null);
  const [estimateState, setEstimateState] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle');
  const [tasks, setTasks] = useState<VideoTask[]>([]);
  const [filter, setFilter] = useState<FilterType>('all');
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const promptRef = useRef<HTMLTextAreaElement | null>(null);
  const selectedChannel = channels.find(channel => String(channel.id) === channelId);
  const availableModels = selectedChannel?.models || models;
  const activeModelOptions = selectedChannel?.model_options || modelOptions;
  const channelSelectOptions = [
    { label: '自动选择', value: '0' },
    ...channels.map(channel => ({ label: channel.name, value: String(channel.id) })),
  ];
  const isSeedance25 = model === 'seedance-2.5';
  const configuredTaskTypes = activeModelOptions[model]?.task_types;
  const taskTypeOptions: VideoTaskType[] = configuredTaskTypes?.length
    ? configuredTaskTypes
    : isSeedance25 ? ['multimodal'] : ['text', 'multimodal'];
  const referenceLimits: Record<ReferenceKind, number> = taskType === 'first_frame'
    ? { image: 1, video: 0, audio: 0 }
    : taskType === 'first_last_frame'
      ? { image: 2, video: 0, audio: 0 }
      : isSeedance25 ? SEEDANCE25_LIMITS : REFERENCE_LIMITS;
  const configuredResolutions = activeModelOptions[model]?.resolutions;
  const resolutionOptions = configuredResolutions?.length
    ? configuredResolutions
    : isSeedance25 ? ['480p', '720p'] : RESOLUTIONS;
  const durationOptions = isSeedance25 ? SEEDANCE25_DURATION_OPTIONS : DURATION_OPTIONS;
  const roleOptions = (kind: ReferenceKind, index: number) => {
    if (taskType === 'first_frame') return [{ label: '首帧', value: 'first_frame' as VideoContentItem['role'] }];
    if (taskType === 'first_last_frame') return [{
      label: index === 0 ? '首帧' : '尾帧',
      value: (index === 0 ? 'first_frame' : 'last_frame') as VideoContentItem['role'],
    }];
    return REFERENCE_ROLES[kind].filter(option => option.value.startsWith('reference_'));
  };
  const taskTypeLabel = VIDEO_TASK_TYPE_OPTIONS.find(option => option.value === taskType)?.label || '视频任务';
  const videoParams = useMemo<VideoCreateParams>(() => {
    const content: VideoContentItem[] = references.map(item => {
      const mapped: VideoContentItem = {
        type: `${item.kind}_url` as VideoContentItem['type'],
        role: item.role,
      };
      if (item.source === 'asset_id') mapped.asset_id = item.value.trim();
      else mapped.url = item.value.trim();
      if (item.kind !== 'image' && item.durationSeconds) mapped.duration_seconds = item.durationSeconds;
      return mapped;
    });
    return {
      model, prompt: prompt.trim(), ...(channelId !== '0' ? { channel_id: Number(channelId) } : {}), resolution, ratio,
      duration: Number(duration), generate_audio: generateAudio,
      task_mode: taskType === 'text' ? 'text' : 'references',
      ...(content.length > 0 ? { content } : {}),
      ...(isSeedance25 ? { priority: Number(priority) } : {}),
    };
  }, [model, prompt, channelId, resolution, ratio, duration, generateAudio, taskType, references, isSeedance25, priority]);
  const estimateReady = Boolean(
    model && (taskType === 'text' ? prompt.trim() : prompt.trim() || references.length > 0) &&
    (taskType === 'text' || references.length > 0) &&
    references.every(item => item.value.trim()) &&
    references.every(item => item.kind === 'image' || Boolean(item.durationSeconds)),
  );

  useEffect(() => {
    setModel(current => availableModels.includes(current) ? current : (availableModels[0] || ''));
  }, [availableModels, channelId]);

  useEffect(() => {
    setTaskType(current => taskTypeOptions.includes(current) ? current : (taskTypeOptions[0] || 'text'));
  }, [model, activeModelOptions]);

  useEffect(() => {
    setReferences(items => {
      if (taskType === 'text') return [];
      if (taskType === 'first_frame') {
        const first = items[0]?.kind === 'image' ? items[0] : makeReference('image', taskType, 0);
        return [{ ...first, kind: 'image', role: 'first_frame' }];
      }
      if (taskType === 'first_last_frame') {
        const first = items[0]?.kind === 'image' ? items[0] : makeReference('image', taskType, 0);
        const last = items[1]?.kind === 'image' ? items[1] : makeReference('image', taskType, 1);
        return [
          { ...first, kind: 'image', role: 'first_frame', durationSeconds: undefined },
          { ...last, kind: 'image', role: 'last_frame', durationSeconds: undefined },
        ];
      }
      const next = items.length > 0 ? items : [makeReference('image', taskType, 0)];
      return next.map(item => ({ ...item, role: roleForTaskType(taskType, item.kind) }));
    });
  }, [taskType]);

  useEffect(() => {
    const configuredResolutions = activeModelOptions[model]?.resolutions;
    const allowedResolutions = configuredResolutions?.length
      ? configuredResolutions
      : isSeedance25 ? ['480p', '720p'] : RESOLUTIONS;
    setResolution(value => allowedResolutions.includes(value) ? value : allowedResolutions[0]);
    if (isSeedance25) {
      setDuration(value => Number(value) < 4 ? '4' : value);
      setResolution(value => value === '480p' || value === '720p' ? value : '480p');
      setGenerateAudio(false);
      setReferences(items => items.map(item => ({
        ...item,
        role: roleForTaskType(taskType, item.kind),
      })));
    }
  }, [model, activeModelOptions, isSeedance25, taskType]);

  useEffect(() => {
    playgroundListVideoModels(tokenId).then(result => {
      setModels(result.models);
      setModelOptions(result.model_options || {});
      setChannels(result.channels || []);
      if (result.models.length > 0 && !model) setModel(result.models[0]);
    }).catch(() => {});
  }, [tokenId]);

  useEffect(() => {
    if (!estimateReady) {
      setEstimatedCost(null);
      setEstimateState('idle');
      return;
    }
    const controller = new AbortController();
    setEstimatedCost(null);
    setEstimateState('loading');
    const timer = setTimeout(() => {
      playgroundEstimateVideo(tokenId, videoParams, controller.signal)
        .then(result => {
          setEstimatedCost(result.estimated_cost);
          setEstimateState('ready');
        })
        .catch(error => {
          if (error?.name !== 'AbortError') setEstimateState('error');
        });
    }, 600);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [tokenId, videoParams, estimateReady]);

  const loadTasks = useCallback(async () => {
    try {
      const res = await playgroundListVideos(tokenId);
      setTasks(res.items);
    } catch {}
  }, [tokenId]);

  useEffect(() => {
    loadTasks();
    return () => { if (pollRef.current) clearInterval(pollRef.current); };
  }, [tokenId]);

  useEffect(() => {
    const hasActive = tasks.some(t => !isTerminal(t.status));
    if (hasActive && !pollRef.current) {
      pollRef.current = setInterval(async () => {
        const res = await playgroundListVideos(tokenId);
        setTasks(res.items);
        if (!res.items.some(t => !isTerminal(t.status))) {
          if (pollRef.current) { clearInterval(pollRef.current); pollRef.current = null; }
        }
      }, 4000);
    } else if (!hasActive && pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
    return () => { if (pollRef.current) { clearInterval(pollRef.current); pollRef.current = null; } };
  }, [tasks, tokenId]);

  const updateReference = (index: number, patch: Partial<ReferenceInput>) => {
    setReferences(items => items.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item));
  };

  const addReference = () => {
    if (taskType !== 'multimodal') return;
    const counts = countReferences(references);
    const kind = (Object.keys(referenceLimits) as ReferenceKind[])
      .find(candidate => counts[candidate] < referenceLimits[candidate]);
    if (!kind) {
      setError('素材数量已达上限');
      return;
    }
    setError('');
    setReferences(items => [...items, makeReference(kind, taskType, items.length)]);
  };

  const changeReferenceKind = (index: number, kind: ReferenceKind) => {
    if (taskType !== 'multimodal') return;
    const counts = countReferences(references);
    if (references[index].kind !== kind && counts[kind] >= referenceLimits[kind]) {
      setError(`${REFERENCE_LABELS[kind]}素材最多 ${referenceLimits[kind]} 个`);
      return;
    }
    setError('');
    updateReference(index, {
      kind,
      role: roleForTaskType(taskType, kind, index),
      durationSeconds: kind === 'image' ? undefined : references[index].durationSeconds,
    });
  };

  const insertMaterialReference = (name: string) => {
    const input = promptRef.current;
    const start = input?.selectionStart ?? prompt.length;
    const end = input?.selectionEnd ?? prompt.length;
    const before = prompt.slice(0, start);
    const after = prompt.slice(end);
    const prefix = before && !/\s$/.test(before) ? ' ' : '';
    const suffix = after && !/^\s/.test(after) ? ' ' : '';
    const next = `${before}${prefix}${name}${suffix}${after}`;
    const cursor = before.length + prefix.length + name.length;
    setPrompt(next);
    requestAnimationFrame(() => {
      input?.focus();
      input?.setSelectionRange(cursor, cursor);
    });
  };

  const handleReferenceUpload = async (index: number, event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (!file) return;
    const reference = references[index];
    setUploadingReference(index);
    setError('');
    try {
      const durationSeconds = await getMediaDuration(file);
      if ((reference.kind === 'video' || reference.kind === 'audio') && !durationSeconds) {
        throw new Error('无法读取素材时长，请改用可播放的文件');
      }
      const asset = await playgroundUploadVideoAsset(tokenId, file, reference.kind, durationSeconds);
      updateReference(index, { source: 'asset_id', value: asset.id, durationSeconds });
    } catch (e: any) {
      setError(e?.message || '素材上传失败');
    } finally {
      setUploadingReference(null);
    }
  };

  const handleSubmit = async () => {
    if ((!prompt.trim() && references.length === 0) || submitting) return;
    const counts = countReferences(references);
    if (taskType === 'text' && references.length > 0) {
      setError('文生视频不需要参考素材');
      return;
    }
    if (taskType === 'first_frame' && (references.length !== 1 || references[0]?.role !== 'first_frame')) {
      setError('首帧任务需要 1 张首帧图片');
      return;
    }
    if (taskType === 'first_last_frame' && (
      references.length !== 2 || references[0]?.role !== 'first_frame' || references[1]?.role !== 'last_frame'
    )) {
      setError('首尾帧任务需要首帧和尾帧两张图片');
      return;
    }
    const exceededKind = (Object.keys(referenceLimits) as ReferenceKind[])
      .find(kind => counts[kind] > referenceLimits[kind]);
    if (exceededKind) {
      setError(`${REFERENCE_LABELS[exceededKind]}素材最多 ${referenceLimits[exceededKind]} 个`);
      return;
    }
    if (counts.audio > 0 && counts.image === 0 && counts.video === 0) {
      setError('音频素材需同时添加图片或视频素材');
      return;
    }
    if (references.some(item => (item.kind === 'video' || item.kind === 'audio') &&
      (!item.durationSeconds || item.durationSeconds < 2))) {
      setError('视频或音频素材必须填写 2 秒以上的时长');
      return;
    }
    if (isSeedance25 && references.length > 50) {
      setError('2.5 的参考素材总数不能超过 50 个');
      return;
    }
    if (isSeedance25 && references.some(item => (item.kind === 'video' || item.kind === 'audio') &&
      (!item.durationSeconds || item.durationSeconds < 2 || item.durationSeconds > 30))) {
      setError('2.5 的视频或音频素材时长必须为 2-30 秒');
      return;
    }
    if (isSeedance25 && references.reduce((sum, item) =>
      sum + ((item.kind === 'video' || item.kind === 'audio') ? (item.durationSeconds || 0) : 0), 0) > 60) {
      setError('2.5 的视频和音频素材时长总和不能超过 60 秒');
      return;
    }
    const content = videoParams.content || [];
    if (content.some(item => !item.url && !item.asset_id)) {
      setError('请填写每个素材的 URL 或素材 ID');
      return;
    }
    setSubmitting(true);
    setError('');
    try {
      await playgroundCreateVideo(tokenId, videoParams);
      setPrompt('');
      setReferences([]);
      await loadTasks();
    } catch (e: any) {
      setError(e?.message || '提交失败');
    } finally {
      setSubmitting(false);
    }
  };

  const handleCancel = async (e: React.MouseEvent, taskId: string) => {
    e.stopPropagation();
    try {
      await playgroundCancelVideo(tokenId, taskId);
      await loadTasks();
    } catch {}
  };

  const filteredTasks = tasks.filter(t => {
    if (filter === 'all') return true;
    if (filter === 'active') return !isTerminal(t.status);
    if (filter === 'completed') return t.status === 'completed';
    return t.status === 'failed' || t.status === 'cancelled';
  });

  const filters: { key: FilterType; label: string }[] = [
    { key: 'all', label: '全部' },
    { key: 'active', label: '生成中' },
    { key: 'completed', label: '已完成' },
    { key: 'failed', label: '失败' },
  ];

  return (
    <div className="h-full flex flex-col xl:flex-row gap-4 overflow-hidden">
      {/* 左侧: 创建面板 */}
      <div className="xl:w-[340px] flex-shrink-0 bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] p-5 flex flex-col overflow-y-auto">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-4 flex items-center gap-2">
          <Video size={18} className="text-[var(--primary)]" />
          创建视频
        </h3>

         <div className="mb-3">
           <div className="text-xs text-[var(--text-secondary)] mb-1 font-medium">渠道</div>
           <Select options={channelSelectOptions} value={channelId} onChange={setChannelId} />
         </div>

         <div className="mb-3">
           <div className="text-xs text-[var(--text-secondary)] mb-1 font-medium">模型</div>
           {availableModels.length > 0 ? (
             <EnumSelect options={availableModels} value={model} onChange={setModel} />
          ) : (
            <div className="text-xs text-[var(--text-tertiary)] py-2">加载中...</div>
          )}
        </div>

        <div className="mb-3">
          <div className="text-xs text-[var(--text-secondary)] mb-1 font-medium">任务类型</div>
          <div className="grid grid-cols-2 gap-1 p-1 rounded-lg bg-[var(--surface)] border border-[var(--border-soft)]" role="tablist">
            {VIDEO_TASK_TYPE_OPTIONS.filter(option => taskTypeOptions.includes(option.value)).map(option => {
              const Icon = option.icon;
              const selected = option.value === taskType;
              return (
                <button key={option.value} type="button" role="tab" aria-selected={selected}
                  onClick={() => setTaskType(option.value)}
                  className={`inline-flex items-center justify-center gap-1.5 min-h-9 px-2 py-1.5 rounded-md text-xs font-medium transition-colors ${selected
                    ? 'bg-[var(--surface-card)] text-[var(--primary)] shadow-sm'
                    : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--surface-card)]/70'}`}>
                  <Icon size={14} />
                  <span className="truncate">{option.label}</span>
                </button>
              );
            })}
          </div>
        </div>

        <div className="grid grid-cols-2 gap-3 mb-3">
          <div>
            <div className="text-xs text-[var(--text-secondary)] mb-1 font-medium">分辨率</div>
            <EnumSelect options={resolutionOptions} value={resolution} onChange={setResolution} />
          </div>
          <div>
            <div className="text-xs text-[var(--text-secondary)] mb-1 font-medium">画面比例</div>
            <EnumSelect options={RATIOS} value={ratio} onChange={setRatio} />
          </div>
          <div>
            <div className="text-xs text-[var(--text-secondary)] mb-1 font-medium">时长</div>
            <Select options={durationOptions} value={duration} onChange={setDuration} />
          </div>
          <div className="flex items-end pb-1">
            <label className="flex items-center gap-2 cursor-pointer select-none">
              <input type="checkbox" checked={generateAudio} disabled={isSeedance25} onChange={e => setGenerateAudio(e.target.checked)}
                className="w-4 h-4 rounded border-[var(--border-soft)] text-[var(--primary)] focus:ring-[var(--primary)]" />
              <span className="text-sm text-[var(--text-secondary)]">生成音频</span>
            </label>
          </div>
        </div>

        {isSeedance25 && (
          <div className="mb-3">
            <div className="text-xs text-[var(--text-secondary)] mb-1 font-medium">队列</div>
            <Select options={[
              { label: '普通', value: '5' },
              { label: '优先', value: '4' },
            ]} value={priority} onChange={setPriority} />
          </div>
        )}

        <div className="mb-3 border-y border-[var(--border-soft)] py-3">
          <div className="flex items-center justify-between mb-2">
            <span className="text-xs text-[var(--text-secondary)] font-medium">{taskType === 'text' ? '无需参考素材' : `${taskTypeLabel}素材`}</span>
            {taskType === 'multimodal' && (
              <button type="button" onClick={addReference}
                className="inline-flex items-center gap-1 text-xs font-medium text-[var(--primary)] hover:opacity-80">
                <Plus size={14} /> 添加素材
              </button>
            )}
          </div>
          {references.length > 0 && (
            <div className="divide-y divide-[var(--border-soft)]">
              {references.map((item, index) => (
                <div key={index} className="py-2 first:pt-0 last:pb-0 space-y-2">
                  <div className="grid grid-cols-2 gap-2">
                    <Select options={taskType === 'multimodal' ? REFERENCE_KIND_OPTIONS : [{ label: '图片', value: 'image' }]} value={item.kind}
                      disabled={taskType !== 'multimodal'} onChange={value => {
                      changeReferenceKind(index, value as ReferenceKind);
                    }} />
                    <Select options={roleOptions(item.kind, index)} value={item.role} disabled={taskType !== 'multimodal'}
                      onChange={value => updateReference(index, { role: value as VideoContentItem['role'] })} />
                  </div>
                  <div className="flex gap-2">
                    <Select options={REFERENCE_SOURCE_OPTIONS} value={item.source}
                      onChange={value => updateReference(index, { source: value as ReferenceSource, value: '' })}
                      className="w-[112px] flex-shrink-0" />
                    <input value={item.value} onChange={event => updateReference(index, { value: event.target.value })}
                      placeholder={item.source === 'url' ? 'https://...' : 'asset_...'}
                      className="min-w-0 flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface-card)] focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
                    {(item.kind === 'video' || item.kind === 'audio') && item.source === 'url' && (
                      <input type="number" min={2} max={30} step={0.1} value={item.durationSeconds || ''}
                        onChange={event => updateReference(index, { durationSeconds: Number(event.target.value) || undefined })}
                        placeholder="秒数" title="视频或音频时长（秒）"
                        className="w-20 flex-shrink-0 px-2 py-2 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface-card)] focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
                    )}
                    <label title="上传素材" className="w-9 h-9 flex-shrink-0 inline-flex items-center justify-center rounded-lg text-[var(--primary)] hover:bg-[var(--surface)] cursor-pointer">
                      <input type="file" className="sr-only" accept={`${item.kind}/*`} disabled={uploadingReference !== null}
                        onChange={event => handleReferenceUpload(index, event)} />
                      {uploadingReference === index ? <Loader2 size={15} className="animate-spin" /> : <Upload size={15} />}
                    </label>
                    <button type="button" onClick={() => setReferences(items => items.filter((_, itemIndex) => itemIndex !== index))}
                      disabled={taskType !== 'multimodal'}
                      title="移除素材" aria-label="移除素材"
                      className="w-9 h-9 flex-shrink-0 inline-flex items-center justify-center rounded-lg text-[var(--text-tertiary)] hover:text-red-500 hover:bg-red-50 disabled:opacity-40 disabled:cursor-not-allowed">
                      <Trash2 size={15} />
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="mb-3 flex-1">
          <div className="flex items-center justify-between gap-2 mb-1">
            <div className="text-xs text-[var(--text-secondary)] font-medium">提示词</div>
            {references.length > 0 && (
              <div className="flex items-center justify-end gap-1 flex-wrap">
                {references.map((_, index) => {
                  const name = materialReferenceName(references, index);
                  return (
                    <button key={`${name}-${index}`} type="button" title={`插入${name}`}
                      onClick={() => insertMaterialReference(name)}
                      className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded text-[11px] text-[var(--primary)] hover:bg-[var(--surface)]">
                      <AtSign size={11} />{name}
                    </button>
                  );
                })}
              </div>
            )}
          </div>
          <textarea ref={promptRef} value={prompt} onChange={e => setPrompt(e.target.value)}
            placeholder="描述你想生成的视频内容..."
            rows={5}
            onKeyDown={e => { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) handleSubmit(); }}
            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface-card)] resize-none focus:outline-none focus:ring-2 focus:ring-[var(--primary)] transition-colors" />
        </div>

        {error && (
          <div className="mb-3 px-3 py-2 bg-red-50 text-red-600 text-sm rounded-lg flex items-center gap-2">
            <AlertCircle size={14} /> {error}
          </div>
        )}

        <div className="mb-3 pt-3 border-t border-[var(--border-soft)] flex items-center justify-between text-sm">
          <span className="text-[var(--text-secondary)]">预计费用</span>
          <span className="font-semibold text-[var(--text-primary)] inline-flex items-center gap-1.5">
            {estimateState === 'loading' && <Loader2 size={14} className="animate-spin" />}
            {estimateState === 'ready' && estimatedCost !== null ? `¥${estimatedCost}` : null}
            {estimateState === 'error' ? '估价失败' : null}
            {estimateState === 'idle' ? '--' : null}
          </span>
        </div>

        <button onClick={handleSubmit} disabled={!model || (!prompt.trim() && references.length === 0) || submitting}
          className="w-full py-3 rounded-xl bg-[var(--primary)] text-white text-sm font-medium disabled:opacity-50 flex items-center justify-center gap-2 hover:opacity-90 transition-opacity">
          {submitting ? <Loader2 size={16} className="animate-spin" /> : <Play size={16} />}
          开始生成
        </button>
        <p className="text-xs text-[var(--text-tertiary)] mt-2 text-center">Ctrl+Enter 快捷提交</p>
      </div>

      {/* 右侧: 任务画廊 */}
      <div className="flex-1 flex flex-col min-w-0 min-h-0">
        <div className="flex items-center justify-between mb-3 flex-shrink-0">
          <div className="flex items-center gap-1">
            {filters.map(f => (
              <button key={f.key} onClick={() => setFilter(f.key)}
                className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${filter === f.key ? 'bg-[var(--primary)] text-white' : 'text-[var(--text-secondary)] hover:bg-[var(--surface)]'}`}>
                {f.label}
              </button>
            ))}
          </div>
          <span className="text-xs text-[var(--text-tertiary)]">{filteredTasks.length} 个任务</span>
        </div>

        <div className="flex-1 min-h-0 overflow-y-auto">
          {filteredTasks.length === 0 ? (
            <div className="h-full flex items-center justify-center text-[var(--text-secondary)]">
              <div className="text-center">
                <Video size={48} className="mx-auto mb-3 opacity-30" />
                <p className="text-sm">{tasks.length === 0 ? '提交一个视频生成请求开始试用' : '没有匹配的任务'}</p>
              </div>
            </div>
          ) : (
            <div className="grid grid-cols-1 lg:grid-cols-2 xl:grid-cols-2 2xl:grid-cols-3 gap-3">
              {filteredTasks.map(task => (
                <TaskCard key={task.id} task={task} onCancel={handleCancel} allowCancel={task.model !== 'seedance-2.5'} />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

const TaskCard: React.FC<{ task: VideoTask; onCancel: (e: React.MouseEvent, id: string) => void; allowCancel: boolean }> = ({ task, onCancel, allowCancel }) => {
  const statusMap: Record<string, { label: string; icon: React.ReactNode; cls: string }> = {
    queued: { label: '排队中', icon: <Clock size={12} />, cls: 'bg-gray-100 text-gray-600' },
    submitted: { label: '已提交', icon: <Loader2 size={12} className="animate-spin" />, cls: 'bg-blue-50 text-blue-600' },
    tracking: { label: '生成中', icon: <Loader2 size={12} className="animate-spin" />, cls: 'bg-blue-50 text-blue-600' },
    completed: { label: '已完成', icon: <CheckCircle2 size={12} />, cls: 'bg-green-50 text-green-600' },
    failed: { label: '失败', icon: <XCircle size={12} />, cls: 'bg-red-50 text-red-600' },
    cancelled: { label: '已取消', icon: <XCircle size={12} />, cls: 'bg-gray-100 text-gray-500' },
  };
  const sc = statusMap[task.status] || statusMap.queued;

  return (
    <div className={`bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] overflow-hidden transition-all hover:shadow-md hover:-translate-y-0.5 ${task.status === 'cancelled' ? 'opacity-60' : ''}`}>
      {/* 预览区 */}
      <div className="aspect-video bg-[var(--surface)] flex items-center justify-center relative">
        {task.status === 'completed' && task.result?.video_url ? (
          <>
            <video src={task.result.video_url} controls className="w-full h-full object-contain" />
            {task.duration && (
              <span className="absolute bottom-2 right-2 px-1.5 py-0.5 rounded bg-black/60 text-white text-[10px]">
                0:{String(task.duration).padStart(2, '0')}
              </span>
            )}
          </>
        ) : task.status === 'failed' ? (
          <div className="text-center px-4">
            <XCircle size={28} className="mx-auto mb-1.5 text-red-400" />
            <p className="text-xs text-red-500 line-clamp-2">{task.error_message || '生成失败'}</p>
          </div>
        ) : task.status === 'tracking' || task.status === 'submitted' ? (
          <div className="text-center">
            <ProgressRing percent={task.progress || 0} />
            <p className="text-xs text-[var(--text-secondary)] mt-2">生成中...</p>
          </div>
        ) : task.status === 'queued' ? (
          <div className="text-center">
            <div className="relative inline-flex items-center justify-center w-10 h-10">
              <Clock size={20} className="text-[var(--text-tertiary)]" />
            </div>
            <p className="text-xs text-[var(--text-tertiary)] mt-1">排队等待中</p>
          </div>
        ) : (
          <XCircle size={24} className="text-[var(--text-tertiary)]" />
        )}
      </div>

      {/* 信息区 */}
      <div className="p-3">
        <div className="flex items-center justify-between mb-1.5">
          <span className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px] font-medium ${sc.cls}`}>
            {sc.icon} {sc.label}
          </span>
          <span className="text-[11px] px-1.5 py-0.5 rounded bg-[var(--surface)] text-[var(--text-tertiary)]">{task.model}</span>
        </div>
        <p className="text-sm text-[var(--text-primary)] line-clamp-2 mb-2">{task.prompt}</p>
        <div className="flex items-center gap-1.5 flex-wrap mb-2">
          {task.resolution && <span className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--surface)] text-[var(--text-tertiary)]">{task.resolution}</span>}
          {task.ratio && <span className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--surface)] text-[var(--text-tertiary)]">{task.ratio}</span>}
          {task.duration && <span className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--surface)] text-[var(--text-tertiary)]">{task.duration}s</span>}
        </div>
        <div className="flex items-center justify-between">
          <span className="text-xs text-[var(--text-tertiary)]">{new Date(task.created_at).toLocaleString()}</span>
          {!isTerminal(task.status) && allowCancel ? (
            <button onClick={e => onCancel(e, task.id)} className="text-xs text-red-500 hover:text-red-600 font-medium">取消</button>
          ) : task.status === 'completed' && task.result?.video_url ? (
            <a href={task.result.video_url} download className="text-xs text-[var(--primary)] hover:underline flex items-center gap-0.5">
              <Download size={11} /> 下载
            </a>
          ) : null}
        </div>
      </div>
    </div>
  );
};

export default VideoTab;
