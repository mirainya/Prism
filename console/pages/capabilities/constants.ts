import { CapabilityStandardParamSchema } from '../../types';

export const RESULT_MODES = [
    {value: 'sync', label: '同步'},
    {value: 'poll', label: '轮询'},
    {value: 'callback', label: '回调'},
];

export const STANDARD_PARAMS: Record<string, { name: string; type: string; group?: string; options?: string[] }> = {
    prompt: {name: '提示词', type: 'string', group: '通用'},
    negative_prompt: {name: '负向提示词', type: 'string', group: '通用'},
    callback_url: {name: '回调地址', type: 'string', group: '通用'},
    aspect_ratio: {name: '宽高比', type: 'enum', group: '尺寸', options: ['1:1', '16:9', '9:16', '4:3', '3:4', '3:2', '2:3']},
    width: {name: '宽度', type: 'number', group: '尺寸'},
    height: {name: '高度', type: 'number', group: '尺寸'},
    size: {name: '尺寸', type: 'enum', group: '尺寸', options: ['1024x1024', '1536x1024', '1024x1536', '1792x1024', '1024x1792', 'auto']},
    image_size: {name: '分辨率', type: 'enum', group: '尺寸', options: ['1K', '2K', '4K']},
    seed: {name: '随机种子', type: 'number', group: '生成控制'},
    steps: {name: '生成步数', type: 'number', group: '生成控制'},
    cfg_scale: {name: 'CFG强度', type: 'number', group: '生成控制'},
    strength: {name: '变化强度', type: 'number', group: '生成控制'},
    n: {name: '生成数量', type: 'number', group: '生成控制'},
    quality: {name: '图片质量', type: 'enum', group: '生成控制', options: ['auto', 'high', 'medium', 'low']},
    style: {name: '风格', type: 'enum', group: '生成控制', options: ['realistic', 'anime', 'cartoon']},
    response_format: {name: '响应格式', type: 'enum', group: '输出', options: ['url', 'b64_json']},
    output_format: {name: '输出格式', type: 'enum', group: '输出', options: ['png', 'jpeg', 'webp']},
    background: {name: '背景', type: 'enum', group: '输出', options: ['auto', 'transparent', 'opaque']},
    image_urls: {name: '图片URL列表', type: 'array', group: '输入'},
    seconds: {name: '时长(秒)', type: 'number', group: '视频'},
    fps: {name: '帧率', type: 'enum', group: '视频', options: ['24', '30', '60']},
};

export const STANDARD_RESPONSE: Record<string, { name: string; type: string; enumValues?: string[] }> = {
    task_id: {name: '任务ID', type: 'string'},
    status: {name: '状态', type: 'enum', enumValues: ['pending', 'processing', 'success', 'failed', 'cancelled']},
    progress: {name: '进度', type: 'number'},
    url: {name: '结果URL', type: 'string'},
    urls: {name: '结果URL列表', type: 'array'},
    data: {name: '结果数据', type: 'string'},
    error: {name: '错误信息', type: 'string'},
};

export const POLL_PARAMS: Record<string, { name: string; type: string }> = {
    task_id: {name: '任务ID', type: 'string'},
};

export const STANDARD_STATUS_VALUES = ['pending', 'processing', 'success', 'failed', 'cancelled'];

export const CAPABILITY_TYPES = [
    {value: 'chat', label: '对话'},
    {value: 'image', label: '图片'},
    {value: 'video', label: '视频'},
    {value: 'other', label: '其他'},
];

export const CAPABILITY_TYPE_ORDER = ['chat', 'image', 'video', 'other'] as const;

export const PARAM_TYPES = ['string', 'number', 'enum', 'array'];

export interface CustomParam {
    key: string;
    name: string;
    type: string;
    options: string;
    default: string;
    required: boolean;
}

export const normalizeText = (value: string | undefined | null) => (value || '').toLowerCase();

export const getCapabilityTypeLabel = (type?: string) => {
    switch (type) {
        case 'image': return '图片';
        case 'video': return '视频';
        case 'chat': return '对话';
        default: return '其他';
    }
};

export const getCapabilityTypeBadgeClass = (type?: string) => {
    switch (type) {
        case 'image': return 'bg-pink-100 text-pink-700';
        case 'video': return 'bg-violet-100 text-violet-700';
        case 'chat': return 'bg-green-100 text-green-700';
        default: return 'bg-gray-100 text-gray-700';
    }
};

export const formatPrice = (price?: number) => {
    const numeric = Number(price);
    return Number.isFinite(numeric) ? numeric.toString() : '0';
};
