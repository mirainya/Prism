import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import { Loader2, AlertCircle, Video, XCircle, CheckCircle2, Clock, Play, Download, Plus, Trash2, Upload, AtSign, Type, Image as ImageIcon, Images, Layers3, Film, Scissors } from 'lucide-react';
import {
  playgroundCreateVideo, playgroundListVideos, playgroundCancelVideo, playgroundPriorityQueueVideo,
  playgroundEstimateVideo, playgroundListVideoModels, playgroundUploadVideoAsset,
  VideoTask, VideoEstimate, VideoCreateParams, VideoContentItem, PlaygroundVideoModelOptions, PlaygroundVideoChannelOption,
  PlaygroundVideoServiceTierOption,
} from '../../services/playgroundApi';
import { Input, SegmentedControl, Select } from '../../components/ui';
import EnumSelect from './EnumSelect';


type FilterType = 'all' | 'active' | 'completed' | 'failed';
type VideoTaskType = 'text' | 'first_frame' | 'first_last_frame' | 'multimodal' | 'video_edit' | 'video_extension';

type ReferenceKind = 'image' | 'video' | 'audio';
type ReferenceSource = 'url' | 'asset_id';

interface ReferenceInput {
  kind: ReferenceKind;
  role: VideoContentItem['role'];
  source: ReferenceSource;
  value: string;
  durationSeconds?: number;
}

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
  { label: '公网 URL', value: 'url' as ReferenceSource },
  { label: '素材 ID', value: 'asset_id' as ReferenceSource },
];

const VIDEO_TASK_TYPE_OPTIONS: Array<{ value: VideoTaskType; label: string; icon: typeof Type }> = [
  { value: 'text', label: '文生视频', icon: Type },
  { value: 'first_frame', label: '首帧生视频', icon: ImageIcon },
  { value: 'first_last_frame', label: '首尾帧视频', icon: Images },
  { value: 'multimodal', label: '多模态视频', icon: Layers3 },
  { value: 'video_edit', label: '视频编辑', icon: Scissors },
  { value: 'video_extension', label: '视频拓展', icon: Film },
];

const roleForTaskType = (taskType: VideoTaskType, kind: ReferenceKind, index = 0): VideoContentItem['role'] => {
  if (taskType === 'first_frame') return 'first_frame';
  if (taskType === 'first_last_frame') return index === 0 ? 'first_frame' : 'last_frame';
  if (taskType === 'video_edit' && kind === 'video') return 'edit_source';
  if (taskType === 'video_extension' && kind === 'video') return 'source_video';
  return kind === 'image' ? 'reference_image' : REFERENCE_ROLES[kind][0].value;
};

const taskModeForTaskType = (taskType: VideoTaskType) => (
	taskType
);

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

const parameterValueKey = (value: string | number | boolean) => JSON.stringify(value) as string;

const randomInteger = (minimum = 1, maximum = 999999999999999) => {
  const min = Math.ceil(minimum);
  const max = Math.floor(maximum);
  return String(Math.floor(Math.random() * (max - min + 1)) + min);
};

const durationOptionsForModel = (options?: PlaygroundVideoModelOptions, hasVideoReference = false) => {
	const referenceMaximum = hasVideoReference ? options?.duration_max_with_video_reference || 0 : 0;
	const effectiveMaximum = referenceMaximum > 0
		? Math.min(options?.duration_max || referenceMaximum, referenceMaximum)
		: options?.duration_max || 0;
  if (options?.duration_options?.length) {
		return options.duration_options.filter(value => !effectiveMaximum || value <= effectiveMaximum)
			.map(value => ({ label: `${value} 秒`, value: String(value) }));
  }
  const minimum = options?.duration_min || 0;
  const maximum = effectiveMaximum;
  if (!minimum && !maximum) return [];
  const values: Array<{ label: string; value: string }> = [];
  if (minimum > 0 && maximum >= minimum && maximum - minimum <= 120) {
    // 展开数字范围，避免默认值（例如 5 秒）被重置为最小值。
    for (let value = minimum; value <= maximum; value += 1) {
      values.push({ label: `${value} 秒`, value: String(value) });
    }
  } else if (minimum > 0) {
    values.push({ label: `${minimum} 秒`, value: String(minimum) });
    if (maximum > minimum) values.push({ label: `${maximum} 秒`, value: String(maximum) });
  } else if (maximum > 0) {
    values.push({ label: `${maximum} 秒`, value: String(maximum) });
  }
  return values;
};

const materialReferenceName = (items: ReferenceInput[], index: number) => {
  const kind = items[index].kind;
  const ordinal = items.slice(0, index + 1).filter(item => item.kind === kind).length;
  return `${REFERENCE_LABELS[kind]}${ordinal}`;
};

const referencePlaceholder = (kind: ReferenceKind) => {
  if (kind === 'image') return 'https://example.com/image.png';
  if (kind === 'audio') return 'https://example.com/audio.mp3';
  return 'https://example.com/video.mp4';
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
  const [serviceTier, setServiceTier] = useState<'standard' | 'priority' | 'vip'>('standard');
  const [parameterValues, setParameterValues] = useState<Record<string, string>>({});
  const [references, setReferences] = useState<ReferenceInput[]>([]);
  const [uploadingReference, setUploadingReference] = useState<number | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const [estimatedCost, setEstimatedCost] = useState<string | null>(null);
  const [estimateDetails, setEstimateDetails] = useState<VideoEstimate | null>(null);
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
  const currentModelOptions = activeModelOptions[model];
  const configuredTaskTypes = currentModelOptions?.task_types;
  const taskTypeOptions: VideoTaskType[] = configuredTaskTypes?.length
    ? configuredTaskTypes
    : [];
  const allowedRoles = currentModelOptions?.allowed_roles;
  const taskMode = taskModeForTaskType(taskType);
  const normalizedParameters = useMemo(() => (currentModelOptions?.parameters || [])
    .filter(Boolean)
    .map(parameter => ({
      ...parameter,
      options: Array.isArray(parameter.options) ? parameter.options.filter(Boolean) : [],
    })), [currentModelOptions?.parameters]);
  const visibleParameters = useMemo(() => normalizedParameters.filter(parameter => (
    !parameter.task_modes?.length || parameter.task_modes.includes(taskMode)
  )), [normalizedParameters, taskMode]);
  const multimodalLimits: Record<ReferenceKind, number> = {
    image: allowedRoles?.length && !allowedRoles.includes('reference_image') ? 0 : currentModelOptions?.max_images || Number.POSITIVE_INFINITY,
    video: allowedRoles?.length && !allowedRoles.includes('reference_video') ? 0 : currentModelOptions?.max_videos || Number.POSITIVE_INFINITY,
    audio: allowedRoles?.length && !allowedRoles.includes('reference_audio') ? 0 : currentModelOptions?.max_audios || Number.POSITIVE_INFINITY,
  };
  const referenceLimits: Record<ReferenceKind, number> = taskType === 'first_frame'
    ? { image: 1, video: 0, audio: 0 }
    : taskType === 'first_last_frame'
      ? { image: 2, video: 0, audio: 0 }
      : taskType === 'video_edit' || taskType === 'video_extension'
        ? { image: multimodalLimits.image, video: 1, audio: multimodalLimits.audio }
        : multimodalLimits;
  const configuredResolutions = currentModelOptions?.resolutions;
	const serviceTierDefinitions: PlaygroundVideoServiceTierOption[] = currentModelOptions?.service_tier_options?.length
		? currentModelOptions.service_tier_options
		: (currentModelOptions?.service_tiers || ['standard']).map(value => ({
			value,
			label: value === 'priority' ? '优先队列' : value === 'vip' ? '积分 VIP' : '标准队列',
		}));
	const selectedServiceTier = serviceTierDefinitions.find(option => option.value === serviceTier);
  const parameterResolutions = visibleParameters.flatMap(parameter => {
		const selected = parameterValues[parameter.name];
		return parameter.options?.find(option => parameterValueKey(option.value) === selected)?.adds_resolutions || [];
	});
  const resolutionOptions = [...(configuredResolutions || [])];
	for (const addedResolution of [...parameterResolutions, ...(selectedServiceTier?.adds_resolutions || [])]) {
		if (!resolutionOptions.includes(addedResolution)) resolutionOptions.push(addedResolution);
	}
  const ratioOptions = currentModelOptions?.ratios || [];
  const serviceTierOptions = serviceTierDefinitions.map(option => option.value);
  const durationOptions = durationOptionsForModel(currentModelOptions, references.some(item => item.kind === 'video'));
  const referenceKindOptions = REFERENCE_KIND_OPTIONS.filter(option => referenceLimits[option.value as ReferenceKind] > 0);
  const roleOptions = (kind: ReferenceKind, index: number) => {
    if (taskType === 'first_frame') return [{ label: '首帧', value: 'first_frame' as VideoContentItem['role'] }];
    if (taskType === 'first_last_frame') return [{
      label: index === 0 ? '首帧' : '尾帧',
      value: (index === 0 ? 'first_frame' : 'last_frame') as VideoContentItem['role'],
    }];
    if (taskType === 'video_edit' && kind === 'video') return [{ label: '编辑源视频', value: 'edit_source' as VideoContentItem['role'] }];
    if (taskType === 'video_extension' && kind === 'video') return [{ label: '拓展源视频', value: 'source_video' as VideoContentItem['role'] }];
    return REFERENCE_ROLES[kind].filter(option => option.value.startsWith('reference_') && (
      !allowedRoles?.length || allowedRoles.includes(option.value)
    ));
  };
  const roleLabel = (kind: ReferenceKind, index: number) => (
    roleOptions(kind, index).find(option => option.value === references[index]?.role)?.label || '参考素材'
  );
  const taskTypeLabel = VIDEO_TASK_TYPE_OPTIONS.find(option => option.value === taskType)?.label || '视频任务';
  const updateParameterValue = (name: string, value: string) => {
    const parameter = visibleParameters.find(item => item.name === name);
    const selected = parameter?.options.find(item => parameterValueKey(item.value) === value);
    setParameterValues(current => {
      const next = { ...current, [name]: value };
      if (selected?.value === true) {
        for (const conflict of parameter?.conflicts_with || []) {
          next[conflict] = parameterValueKey(false);
        }
      }
      return next;
    });
  };
  const videoParams = useMemo<VideoCreateParams>(() => {
    const content: VideoContentItem[] = references.map((item, index) => {
      const mapped: VideoContentItem = {
        type: `${item.kind}_url` as VideoContentItem['type'],
        role: item.role,
        client_ref_id: `ref_${index + 1}`,
      };
      if (item.source === 'asset_id') mapped.asset_id = item.value.trim();
      else mapped.url = item.value.trim();
      if (item.kind !== 'image' && item.durationSeconds) mapped.duration_seconds = item.durationSeconds;
      return mapped;
    });
    const params = Object.fromEntries(visibleParameters.flatMap(parameter => {
      const selected = parameterValues[parameter.name];
		if ((parameter.type === 'number' || parameter.type === 'integer') && selected !== undefined && selected !== '') {
			const numeric = Number(selected);
			return Number.isFinite(numeric) ? [[parameter.name, numeric]] : [];
		}
      const option = parameter.options.find(item => parameterValueKey(item.value) === selected);
      return option ? [[parameter.name, option.value]] : [];
    }));
    return {
      model, prompt: prompt.trim(), ...(channelId !== '0' ? { channel_id: Number(channelId) } : {}), resolution,
      ...(ratio ? { ratio } : {}),
      duration: Number(duration), generate_audio: generateAudio,
      task_mode: taskMode,
      service_tier: serviceTier,
      ...(content.length > 0 ? { content } : {}),
      ...(Object.keys(params).length > 0 ? { params } : {}),
    };
  }, [model, prompt, channelId, resolution, ratio, duration, generateAudio, taskMode, serviceTier, references, visibleParameters, parameterValues]);
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
      if (taskType === 'video_edit' || taskType === 'video_extension') {
        const source = items[0]?.kind === 'video' ? items[0] : makeReference('video', taskType, 0);
        const supporting = items.slice(1).filter(item => item.kind !== 'video');
        return [{ ...source, kind: 'video', role: roleForTaskType(taskType, 'video', 0) }, ...supporting.map(item => ({
          ...item, role: roleForTaskType(taskType, item.kind),
        }))];
      }
      const next = items.length > 0 ? items : [makeReference('image', taskType, 0)];
      return next.map(item => ({ ...item, role: roleForTaskType(taskType, item.kind) }));
    });
  }, [taskType]);

  useEffect(() => {
    // 渠道切换会经历一次“模型已变、参数尚未回填”的中间渲染。
    // 中间状态不应清空已有值，否则完整配置回来后会被错误重置为第一个选项。
    if (resolutionOptions.length > 0) {
      setResolution(value => resolutionOptions.includes(value) ? value : resolutionOptions[0]);
    }
    if (ratioOptions.length > 0) {
      setRatio(value => ratioOptions.includes(value) ? value : ratioOptions[0]);
    }
    if (durationOptions.length > 0) {
      setDuration(value => durationOptions.some(option => option.value === value) ? value : durationOptions[0].value);
    }
    if (serviceTierOptions.length > 0) {
      setServiceTier(value => serviceTierOptions.includes(value) ? value : serviceTierOptions[0]);
    }
    if (currentModelOptions?.allow_generated_audio === false) {
      setGenerateAudio(false);
    }
    if (taskType === 'multimodal') {
      setReferences(items => {
        const counts: Record<ReferenceKind, number> = { image: 0, video: 0, audio: 0 };
        const maximum = currentModelOptions?.max_media || Number.POSITIVE_INFINITY;
        const supported = items.filter(item => {
          if (referenceLimits[item.kind] <= counts[item.kind] || counts.image + counts.video + counts.audio >= maximum) return false;
          counts[item.kind]++;
          return true;
        }).map(item => ({ ...item, role: roleForTaskType(taskType, item.kind) }));
        if (supported.length > 0) return supported;
        const firstKind = (['image', 'video', 'audio'] as ReferenceKind[]).find(kind => referenceLimits[kind] > 0);
        return firstKind ? [makeReference(firstKind, taskType, 0)] : [];
      });
    }
  }, [model, activeModelOptions, taskType, serviceTierOptions]);

  useEffect(() => {
	const parameters = visibleParameters;
	setParameterValues(current => Object.fromEntries(parameters.map(parameter => {
		if (parameter.type === 'number' || parameter.type === 'integer') {
			const existing = current[parameter.name];
			const fallback = parameter.default === undefined
				? parameter.name === 'seed' ? randomInteger(parameter.min || 1, parameter.max || 999999999999999) : ''
				: String(parameter.default);
			const numeric = existing === undefined || existing === '' ? NaN : Number(existing);
			const withinRange = Number.isFinite(numeric) &&
				(parameter.min === undefined || numeric >= parameter.min) &&
				(parameter.max === undefined || numeric <= parameter.max);
			return [parameter.name, withinRange ? existing : fallback];
		}
      const validCurrent = parameter.options?.some(option => parameterValueKey(option.value) === current[parameter.name]);
      const fallback = parameter.default ?? parameter.options?.[0]?.value ?? '';
      return [parameter.name, validCurrent ? current[parameter.name] : parameterValueKey(fallback)];
    })));
  }, [model, activeModelOptions, taskMode]);

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
      setEstimateDetails(null);
      setEstimateState('idle');
      return;
    }
    const controller = new AbortController();
    setEstimatedCost(null);
    setEstimateDetails(null);
    setEstimateState('loading');
    const timer = setTimeout(() => {
      playgroundEstimateVideo(tokenId, videoParams, controller.signal)
        .then(result => {
          setEstimatedCost(result.estimated_cost);
          setEstimateDetails(result);
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
    if (taskType !== 'multimodal' && taskType !== 'video_edit' && taskType !== 'video_extension') return;
    if (currentModelOptions?.max_media && references.length >= currentModelOptions.max_media) {
      setError(`参考素材最多 ${currentModelOptions.max_media} 个`);
      return;
    }
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
    if ((taskType !== 'multimodal' && taskType !== 'video_edit' && taskType !== 'video_extension') ||
      (index === 0 && taskType !== 'multimodal')) return;
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
    if (taskType === 'video_edit' && (references[0]?.role !== 'edit_source' || references[0]?.kind !== 'video')) {
      setError('视频编辑需要 1 个编辑源视频');
      return;
    }
    if (taskType === 'video_extension' && (references[0]?.role !== 'source_video' || references[0]?.kind !== 'video')) {
      setError('视频拓展需要 1 个拓展源视频');
      return;
    }
    const exceededKind = (Object.keys(referenceLimits) as ReferenceKind[])
      .find(kind => counts[kind] > referenceLimits[kind]);
    if (exceededKind) {
      setError(`${REFERENCE_LABELS[exceededKind]}素材最多 ${referenceLimits[exceededKind]} 个`);
      return;
    }
    if (currentModelOptions?.max_media && references.length > currentModelOptions.max_media) {
      setError(`参考素材最多 ${currentModelOptions.max_media} 个`);
      return;
    }
    if (currentModelOptions?.require_visual_media_with_audio && counts.audio > 0 && counts.image === 0 && counts.video === 0) {
      setError('音频素材需同时添加图片或视频素材');
      return;
    }
    const minimumMediaDuration = currentModelOptions?.media_duration_min || 2;
    const maximumMediaDuration = currentModelOptions?.media_duration_max || 0;
    if (references.some(item => (item.kind === 'video' || item.kind === 'audio') &&
      (!item.durationSeconds || item.durationSeconds < minimumMediaDuration || (maximumMediaDuration > 0 && item.durationSeconds > maximumMediaDuration)))) {
      const range = maximumMediaDuration > 0 ? `${minimumMediaDuration}-${maximumMediaDuration}` : `${minimumMediaDuration} 以上`;
      setError(`视频或音频素材时长必须为 ${range} 秒`);
      return;
    }
    const videoDurationTotal = references.reduce((sum, item) => sum + (item.kind === 'video' ? item.durationSeconds || 0 : 0), 0);
    if (currentModelOptions?.max_video_duration_total && videoDurationTotal > currentModelOptions.max_video_duration_total) {
      setError(`视频素材总时长不能超过 ${currentModelOptions.max_video_duration_total} 秒`);
      return;
    }
    const audioDurationTotal = references.reduce((sum, item) => sum + (item.kind === 'audio' ? item.durationSeconds || 0 : 0), 0);
    if (currentModelOptions?.max_audio_duration_total && audioDurationTotal > currentModelOptions.max_audio_duration_total) {
      setError(`音频素材总时长不能超过 ${currentModelOptions.max_audio_duration_total} 秒`);
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

  const handlePriorityQueue = async (e: React.MouseEvent, taskId: string) => {
    e.stopPropagation();
    try {
      await playgroundPriorityQueueVideo(tokenId, taskId);
      await loadTasks();
    } catch (error: any) {
      setError(error?.message || '升级优先队列失败');
    }
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
          {ratioOptions.length > 0 && <div>
            <div className="text-xs text-[var(--text-secondary)] mb-1 font-medium">画面比例</div>
            <EnumSelect options={ratioOptions} value={ratio} onChange={setRatio} />
          </div>}
          <div>
            <div className="text-xs text-[var(--text-secondary)] mb-1 font-medium">时长</div>
            <Select options={durationOptions} value={duration} onChange={setDuration} />
          </div>
          {currentModelOptions?.allow_generated_audio !== false && <div className="flex items-end pb-1">
            <label className="flex items-center gap-2 cursor-pointer select-none">
              <input type="checkbox" checked={generateAudio} onChange={e => setGenerateAudio(e.target.checked)}
                className="w-4 h-4 rounded border-[var(--border-soft)] text-[var(--primary)] focus:ring-[var(--primary)]" />
              <span className="text-sm text-[var(--text-secondary)]">生成音频</span>
            </label>
          </div>}
        </div>

        {serviceTierOptions.length > 1 && (
          <div className="mb-3">
            <div className="text-xs text-[var(--text-secondary)] mb-1 font-medium">执行档位</div>
            <Select options={serviceTierDefinitions.map(option => ({
              label: option.surcharge_percent ? `${option.label} (+${option.surcharge_percent}%)` : option.label,
              value: option.value,
            }))} value={serviceTier} onChange={value => setServiceTier(value as typeof serviceTier)} />
          </div>
        )}

        {visibleParameters.map(parameter => (
          <div key={parameter.name} className="mb-3">
            <div className="text-xs text-[var(--text-secondary)] mb-1 font-medium">{parameter.label}</div>
			{parameter.type === 'number' || parameter.type === 'integer' ? (
				<Input type="number" value={parameterValues[parameter.name] || ''}
					min={parameter.min} max={parameter.max} step={parameter.type === 'integer' ? 1 : 'any'}
					onChange={event => updateParameterValue(parameter.name, event.target.value)} />
			) : (
				<Select options={(parameter.options || []).map(option => ({
					label: option.label,
					value: parameterValueKey(option.value),
				}))} value={parameterValues[parameter.name] || ''}
					onChange={value => updateParameterValue(parameter.name, value)} />
			)}
          </div>
        ))}

        <div className="mb-3 border-y border-[var(--border-soft)] py-3">
          <div className="flex items-center justify-between mb-2">
            <span className="text-xs text-[var(--text-secondary)] font-medium">{taskType === 'text' ? '无需参考素材' : `${taskTypeLabel}素材`}</span>
            {(taskType === 'multimodal' || taskType === 'video_edit' || taskType === 'video_extension') && (
              <button type="button" onClick={addReference}
                className="inline-flex items-center gap-1 text-xs font-medium text-[var(--primary)] hover:opacity-80">
                <Plus size={14} /> 添加素材
              </button>
            )}
          </div>
          {references.length > 0 && (
            <div className="divide-y divide-[var(--border-soft)]">
              {references.map((item, index) => (
                <div key={index} className="py-3 first:pt-0 last:pb-0 space-y-2.5">
                  <div className="flex min-w-0 items-center gap-2">
                    {taskType === 'multimodal' || index > 0 ? (
                      <Select options={referenceKindOptions} value={item.kind}
                        className="min-w-0 flex-1" onChange={value => changeReferenceKind(index, value as ReferenceKind)} />
                    ) : (
                      <span className="min-w-0 flex-1 text-sm font-medium text-[var(--text-primary)]">
                        {REFERENCE_LABELS[item.kind]}
                      </span>
                    )}
                    <span className="truncate text-xs text-[var(--text-tertiary)]">{roleLabel(item.kind, index)}</span>
                    <button type="button" onClick={() => setReferences(items => items.filter((_, itemIndex) => itemIndex !== index))}
                      disabled={taskType !== 'multimodal' && index === 0}
                      title="移除素材" aria-label="移除素材"
                      className="inline-flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg text-[var(--text-tertiary)] hover:bg-red-50 hover:text-red-500 disabled:cursor-not-allowed disabled:opacity-30">
                      <Trash2 size={15} />
                    </button>
                  </div>

                  <SegmentedControl options={REFERENCE_SOURCE_OPTIONS} value={item.source}
                    ariaLabel={`${materialReferenceName(references, index)}来源`}
                    onChange={value => updateReference(index, { source: value, value: '' })} />

                  <div className="flex min-w-0 gap-2">
                    <input value={item.value} onChange={event => updateReference(index, { value: event.target.value })}
                      aria-label={item.source === 'url' ? '素材公网 URL' : '素材 ID'}
                      placeholder={item.source === 'url' ? referencePlaceholder(item.kind) : 'asset_...'}
                      className="h-10 min-w-0 flex-1 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
                    <label title="上传素材" aria-label="上传素材"
                      className="inline-flex h-10 w-10 flex-shrink-0 cursor-pointer items-center justify-center rounded-lg border border-[var(--border-soft)] text-[var(--primary)] transition-colors hover:bg-[var(--surface)]">
                      <input type="file" className="sr-only" accept={`${item.kind}/*`} disabled={uploadingReference !== null}
                        onChange={event => handleReferenceUpload(index, event)} />
                      {uploadingReference === index ? <Loader2 size={15} className="animate-spin" /> : <Upload size={15} />}
                    </label>
                  </div>

                  {(item.kind === 'video' || item.kind === 'audio') && (
                    <div className="flex items-center justify-between gap-3">
                      <label htmlFor={`reference-duration-${index}`} className="text-xs font-medium text-[var(--text-secondary)]">素材时长</label>
                      <div className="relative w-28 flex-shrink-0">
                        <input id={`reference-duration-${index}`} type="number"
                          min={currentModelOptions?.media_duration_min || 2}
                          max={currentModelOptions?.media_duration_max || 30}
                          step={0.1} value={item.durationSeconds || ''}
                          onChange={event => updateReference(index, { durationSeconds: Number(event.target.value) || undefined })}
                          className="h-9 w-full rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] pl-3 pr-8 text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
                        <span className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs text-[var(--text-tertiary)]">秒</span>
                      </div>
                    </div>
                  )}
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
            {estimateState === 'ready' && estimatedCost !== null ? (
              <span className="inline-flex items-center gap-2">
                <span>¥{estimatedCost}</span>
                {estimateDetails?.units ? <span className="text-xs font-normal text-[var(--text-tertiary)]">{estimateDetails.units} 单位</span> : null}
              </span>
            ) : null}
            {estimateState === 'error' ? '估价失败' : null}
            {estimateState === 'idle' ? '--' : null}
          </span>
        </div>
        {estimateState === 'ready' && estimateDetails && (estimateDetails.billing_tier || estimateDetails.pricing_source || estimateDetails.currency) && (
          <div className="mb-3 text-right text-[11px] text-[var(--text-tertiary)]">
            {[estimateDetails.billing_tier, estimateDetails.pricing_source, estimateDetails.currency].filter(Boolean).join(' · ')}
          </div>
        )}

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
              {filteredTasks.map(task => {
                const taskOptions = channels.find(channel => channel.id === task.channel_id)?.model_options[task.model]
                  || modelOptions[task.model];
                const allowCancel = task.status === 'queued'
                  ? taskOptions?.allow_local_cancel !== false
                  : taskOptions ? Boolean(taskOptions.cancel_statuses?.includes(task.status)) : true;
                const allowPriority = (task.status === 'submitted' || task.status === 'tracking') &&
                  task.service_tier === 'standard' && Boolean(taskOptions?.service_tiers?.includes('priority'));
                return <TaskCard key={task.id} task={task} onCancel={handleCancel} onPriority={handlePriorityQueue} allowCancel={allowCancel} allowPriority={allowPriority} />;
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

const TaskCard: React.FC<{ task: VideoTask; onCancel: (e: React.MouseEvent, id: string) => void; onPriority: (e: React.MouseEvent, id: string) => void; allowCancel: boolean; allowPriority: boolean }> = ({ task, onCancel, onPriority, allowCancel, allowPriority }) => {
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
    <div className={`min-w-0 bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] overflow-hidden transition-all hover:shadow-md hover:-translate-y-0.5 ${task.status === 'cancelled' ? 'opacity-60' : ''}`}>
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
            <p className="max-w-full break-words overflow-hidden text-xs text-red-500 line-clamp-3">{task.error_message || '生成失败'}</p>
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
        <div className="flex min-w-0 items-start justify-between gap-2 mb-1.5">
          <span className={`inline-flex shrink-0 items-center gap-1 px-1.5 py-0.5 rounded text-[11px] font-medium ${sc.cls}`}>
            {sc.icon} {sc.label}
          </span>
          <span title={task.model} className="min-w-0 max-w-[68%] truncate rounded bg-[var(--surface)] px-1.5 py-0.5 text-right text-[11px] text-[var(--text-tertiary)]">{task.model}</span>
        </div>
        <p className="break-words text-sm text-[var(--text-primary)] line-clamp-2 mb-2">{task.prompt}</p>
        <div className="flex items-center gap-1.5 flex-wrap mb-2">
          {task.service_tier && <span className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--primary-soft)] text-[var(--primary)]">{task.service_tier === 'vip' ? 'VIP' : task.service_tier === 'priority' ? '优先' : '标准'}</span>}
          {task.queue_status && <span className="text-[10px] px-1.5 py-0.5 rounded bg-amber-50 text-amber-700">队列 {task.queue_status}</span>}
          {task.queue_position ? <span className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--surface)] text-[var(--text-tertiary)]">第 {task.queue_position} 位{task.queue_limit ? ` / ${task.queue_limit}` : ''}</span> : null}
          {task.h_channel_points_vip ? <span className="text-[10px] px-1.5 py-0.5 rounded bg-violet-50 text-violet-700">积分 VIP</span> : null}
          {task.priority_surcharge_percent ? <span className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--surface)] text-[var(--text-tertiary)]">+{task.priority_surcharge_percent}%</span> : null}
          {task.resolution && <span className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--surface)] text-[var(--text-tertiary)]">{task.resolution}</span>}
          {task.ratio && <span className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--surface)] text-[var(--text-tertiary)]">{task.ratio}</span>}
          {task.duration && <span className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--surface)] text-[var(--text-tertiary)]">{task.duration}s</span>}
        </div>
        <div className="flex min-w-0 items-center justify-between gap-2">
          <span className="min-w-0 truncate text-xs text-[var(--text-tertiary)]">{new Date(task.created_at).toLocaleString()}</span>
          <div className="flex shrink-0 items-center gap-2">
          {allowPriority && (
            <button onClick={e => onPriority(e, task.id)} className="text-xs text-[var(--primary)] hover:opacity-80 font-medium">升级优先</button>
          )}
          {!isTerminal(task.status) && allowCancel && (
            <button onClick={e => onCancel(e, task.id)} className="text-xs text-red-500 hover:text-red-600 font-medium">取消</button>
          )}
          {task.status === 'completed' && task.result?.video_url ? (
            <a href={task.result.video_url} download className="text-xs text-[var(--primary)] hover:underline flex items-center gap-0.5">
              <Download size={11} /> 下载
            </a>
          ) : null}
          </div>
        </div>
      </div>
    </div>
  );
};

export default VideoTab;
