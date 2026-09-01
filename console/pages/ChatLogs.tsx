import React, {useEffect, useMemo, useRef, useState} from 'react';
import {useNavigate} from 'react-router-dom';
import {
    Activity,
    AlertTriangle,
    CircleUserRound,
    Search,
    ChevronRight,
    RefreshCw,
    MessageSquare,
    User,
    Bot,
    Hash,
    FileText,
    Braces,
    BrainCircuit,
    Wrench,
    LayoutList,
    ListTree,
    RotateCcw,
    ReceiptText
} from 'lucide-react';
import {fetchConversations, fetchConversationMessages, fetchConversationTurns, fetchUsers, ConversationListParams} from '../services/api';
import {Conversation, ChatMessage, ConversationCanonicalItem, ConversationTurnRecord, User as PrismUser, UserRole} from '../types';
import {Dialog, Drawer, Pagination, Select} from '../components/ui';

const DEFAULT_PAGE_SIZE = 20;
const DETAIL_PAGE_SIZE = 200;
const INPUT_CLASS = 'w-full min-w-0 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 py-2 text-sm text-[var(--text-primary)] outline-none transition focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--primary)]/20';

type ConversationFilterDraft = {
    keyword: string;
    model: string;
    user_id: string;
    token_id: string;
    start_date: string;
    end_date: string;
};

const EMPTY_FILTERS: ConversationFilterDraft = {
    keyword: '',
    model: '',
    user_id: '',
    token_id: '',
    start_date: '',
    end_date: '',
};

const isAdmin = () => {
    try {
        const userStr = localStorage.getItem('prism_user');
        if (!userStr) return false;
        const user = JSON.parse(userStr);
        return user.role === UserRole.ADMIN;
    } catch {
        return false;
    }
};

const formatTime = (t: string) => {
    const d = new Date(t);
    const pad = (n: number) => n.toString().padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
};

interface Attachment {
    type: string;
    image_url?: string | { url: string; detail?: string };
    file_url?: string | { url: string; content_type?: string };
    url?: string;
    data?: string;
    media_type?: string;
    detail?: string;
    filename?: string;
    file_id?: string;
    [key: string]: unknown;
}

interface ConversationTurn {
    key: string;
    callId?: string;
    status?: string;
    messages: ChatMessage[];
    record?: ConversationTurnRecord;
}

const CALL_STATUS_LABELS: Record<string, {label: string; className: string}> = {
    received: {label: '已接收', className: 'bg-gray-100 text-gray-700'},
    in_progress: {label: '处理中', className: 'bg-blue-100 text-blue-700'},
    completed: {label: '成功', className: 'bg-green-100 text-green-700'},
    failed: {label: '失败', className: 'bg-red-100 text-red-700'},
    aborted: {label: '已中止', className: 'bg-amber-100 text-amber-700'},
    cancelled: {label: '已取消', className: 'bg-yellow-100 text-yellow-700'},
};

const formatMoney = (value: string | number | undefined) => {
    const raw = String(value ?? '0').trim();
    const match = /^([+-]?)(\d+)(?:\.(\d*))?$/.exec(raw);
    if (match) {
        const [, sign, integer, fraction = ''] = match;
        return `¥${sign}${integer}.${fraction.padEnd(8, '0').slice(0, 8)}`;
    }
    const parsed = Number(raw);
    return Number.isFinite(parsed) ? `¥${parsed.toFixed(8)}` : `¥${raw}`;
};

const hasPositiveAmount = (value: string | number | undefined) => {
    const parsed = Number(value ?? 0);
    return Number.isFinite(parsed) && parsed > 0;
};

const buildLegacyTurns = (messages: ChatMessage[]): ConversationTurn[] => {
    const turns: ConversationTurn[] = [];
    for (const message of messages) {
        const current = turns[turns.length - 1];
        if (message.callId) {
            if (current?.callId === message.callId) {
                current.messages.push(message);
                current.status ||= message.callStatus;
            } else {
                turns.push({key: message.callId, callId: message.callId, status: message.callStatus, messages: [message]});
            }
            continue;
        }
        if (!current || current.callId || message.role === 'user') {
            turns.push({key: `legacy-${message.id}`, messages: [message]});
        } else {
            current.messages.push(message);
        }
    }
    return turns;
};

const buildTurns = (messages: ChatMessage[], records: ConversationTurnRecord[]): ConversationTurn[] => {
    // 新 canonical turn 与旧 messages 可能同时存在；按 callId 去重后再合并，兼容迁移前后的历史数据。
    const recordedCallIds = new Set(records.map(turn => turn.callId));
    const legacyTurns = buildLegacyTurns(messages.filter(message => !message.callId || !recordedCallIds.has(message.callId)));
    const recordedTurns = records.map(turn => ({
            key: `turn-${turn.id}`,
            callId: turn.callId,
            status: turn.status,
            messages: [],
            record: turn,
        }));
    return [...legacyTurns, ...recordedTurns].sort((left, right) => {
        const leftTime = left.record?.createdAt || left.messages[0]?.createdAt || '';
        const rightTime = right.record?.createdAt || right.messages[0]?.createdAt || '';
        return leftTime.localeCompare(rightTime);
    });
};

const parseAttachments = (raw?: string): Attachment[] => {
    if (!raw) return [];
    try {
        return JSON.parse(raw);
    } catch {
        return [];
    }
};

const normalizeMediaURL = (value?: string) => {
    if (!value) return '';
    if (/^data:(image|audio|video)\//i.test(value) || value.startsWith('blob:')) return value;
    try {
        const parsed = new URL(value, window.location.origin);
        return ['http:', 'https:'].includes(parsed.protocol) ? parsed.href : '';
    } catch {
        return '';
    }
};

const IMAGE_ATTACHMENT_TYPES = new Set(['image_url', 'input_image', 'output_image', 'image']);

const readMediaURL = (value: unknown) => {
    if (typeof value === 'string') return value;
    if (value && typeof value === 'object' && typeof (value as {url?: unknown}).url === 'string') {
        return (value as {url: string}).url;
    }
    return '';
};

const dataURL = (data: unknown, mediaType: unknown) => {
    if (typeof data !== 'string' || !data) return '';
    if (data.startsWith('data:')) return data;
    return `data:${typeof mediaType === 'string' && mediaType ? mediaType : 'application/octet-stream'};base64,${data}`;
};

const attachmentImageURL = (attachment: Attachment) =>
    readMediaURL(attachment.image_url)
    || readMediaURL(attachment.url)
    || dataURL(attachment.data, attachment.media_type);

const normalizeLinkURL = (value?: string) => {
    if (!value) return '';
    if (value.startsWith('blob:')) return value;
    try {
        const parsed = new URL(value, window.location.origin);
        return ['http:', 'https:'].includes(parsed.protocol) ? parsed.href : '';
    } catch {
        return '';
    }
};

const canAutoLoadMedia = (value: string) => {
    // 跨域媒体和 data URL 必须由用户主动加载，避免打开日志详情时自动请求不可信资源。
    if (value.startsWith('blob:')) return true;
    if (value.startsWith('data:')) return false;
    try {
        return new URL(value, window.location.origin).origin === window.location.origin;
    } catch {
        return false;
    }
};

const AttachmentRenderer: React.FC<{ attachments: Attachment[] }> = ({attachments}) => {
    const [preview, setPreview] = useState<string | null>(null);
    const [loadedImages, setLoadedImages] = useState<Set<number>>(() => new Set());
    if (attachments.length === 0) return null;
    return (
        <>
            <div className="flex flex-wrap gap-2 mt-2">
                {attachments.map((att, i) => {
                    const imageURL = IMAGE_ATTACHMENT_TYPES.has(att.type)
                        ? normalizeMediaURL(attachmentImageURL(att))
                        : '';
                    if (imageURL) {
                        const loaded = canAutoLoadMedia(imageURL) || loadedImages.has(i);
                        if (!loaded) {
                            return (
                                <button key={i} type="button" onClick={() => setLoadedImages(current => new Set(current).add(i))} className="inline-flex items-center gap-2 rounded-lg border border-current/15 px-3 py-2 text-xs font-semibold opacity-90 hover:opacity-100">
                                    <FileText size={14}/>加载图片预览
                                </button>
                            );
                        }
                        return (
                            <button key={i} type="button" className="relative cursor-pointer" onClick={() => setPreview(imageURL)}>
                                <img src={imageURL} alt="attachment" className="max-w-[200px] max-h-[150px] rounded-lg object-cover border border-white/20"/>
                            </button>
                        );
                    }
                    const rawFileURL = typeof att.file_url === 'string' ? att.file_url : att.file_url?.url;
                    const fileURL = normalizeLinkURL(rawFileURL);
                    const contentType = typeof att.file_url === 'object' ? att.file_url?.content_type : undefined;
                    if (fileURL) {
                        return (
                            <a key={i} href={fileURL} target="_blank" rel="noopener noreferrer"
                               className="flex items-center gap-2 px-3 py-2 rounded-lg bg-black/20 border border-white/10 text-xs hover:bg-black/30 transition-colors">
                                <FileText size={14}/>
                                <span className="max-w-[160px] truncate opacity-80">{att.filename || contentType || '附件'}</span>
                            </a>
                        );
                    }
                    return (
                        <div key={i} className="max-w-full rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-xs">
                            <div className="mb-1 flex items-center gap-2"><FileText size={14}/><span>{att.filename || att.type || '附件'}</span></div>
                            <pre className="max-w-[320px] overflow-x-auto whitespace-pre-wrap break-all opacity-70">{JSON.stringify(att, null, 2)}</pre>
                        </div>
                    );
                })}
            </div>
            <Dialog open={Boolean(preview)} onClose={() => setPreview(null)} motion="fade" ariaLabel="图片预览" containerClassName="items-center justify-center p-4" panelClassName="max-w-[95vw] max-h-[95vh]">
                {preview && <img src={preview} alt="preview" className="max-h-[90vh] max-w-[90vw] rounded-xl shadow-2xl"/>}
            </Dialog>
        </>
    );
};

const DeferredAudio: React.FC<{source: string}> = ({source}) => {
    const safeSource = normalizeMediaURL(source);
    const [loaded, setLoaded] = useState(() => Boolean(safeSource && canAutoLoadMedia(safeSource)));
    if (!safeSource) return null;
    return loaded ? (
        <audio controls preload="metadata" className="mt-2 max-w-full" src={safeSource}/>
    ) : (
        <button type="button" onClick={() => setLoaded(true)} className="mt-2 inline-flex items-center gap-2 rounded-lg border border-current/15 px-3 py-2 text-xs font-semibold opacity-90 hover:opacity-100">
            <FileText size={14}/>加载音频
        </button>
    );
};

const stringifyCanonicalValue = (value: unknown) => {
    if (typeof value === 'string') return value;
    try {
        return JSON.stringify(value, null, 2);
    } catch {
        return String(value);
    }
};

const CanonicalItemRenderer: React.FC<{item: ConversationCanonicalItem}> = ({item}) => {
    const canonical = item.canonical || {};
    const type = String(canonical.type || 'unknown');
    const role = String(canonical.role || (item.direction === 'input' ? 'user' : 'assistant'));
    const content = Array.isArray(canonical.content) ? canonical.content : [];

    if (type === 'message') {
        const text = content
            .filter((part: any) => ['input_text', 'output_text', 'text'].includes(part?.type))
            .map((part: any) => String(part.text || ''))
            .join('');
        const attachments: Attachment[] = content.flatMap((part: any): Attachment[] => {
            const embeddedURL = dataURL(part?.data, part?.media_type);
            if (IMAGE_ATTACHMENT_TYPES.has(part?.type)) {
                const imageURL = readMediaURL(part.url) || readMediaURL(part.image_url) || dataURL(part.data, part.media_type);
                if (imageURL) {
                    return [{type: part.type, image_url: {url: imageURL, detail: part.detail}}];
                }
            }
            if (['input_file', 'file'].includes(part?.type) && (part.url || embeddedURL)) {
                return [{type: 'file_url', file_url: {url: part.url || embeddedURL, content_type: part.media_type}, filename: part.filename}];
            }
            if (['input_file', 'file'].includes(part?.type) && part.file_id) {
                return [{type: 'file', file_id: part.file_id, filename: part.filename}];
            }
            return [];
        });
        const audioParts = content.filter((part: any) => ['input_audio', 'audio'].includes(part?.type));
        const reasoning = typeof canonical.extra?.['openai_chat.reasoning_content'] === 'string'
            ? canonical.extra['openai_chat.reasoning_content']
            : '';
        const unknownParts = content.filter((part: any) => ![
            'input_text', 'output_text', 'text', 'input_image', 'output_image', 'image', 'image_url', 'input_file', 'file', 'input_audio', 'audio',
        ].includes(part?.type));
        const isUser = role === 'user';
        return (
            <div className={`flex ${isUser ? 'justify-end' : 'justify-start'}`}>
                <div className="max-w-[85%]">
                    <div className={`mb-1 flex items-center gap-2 ${isUser ? 'justify-end' : 'justify-start'}`}>
                        {isUser ? <User size={12} className="text-blue-400"/> : <Bot size={12} className="text-emerald-400"/>}
                        <span className="text-[10px] font-bold uppercase text-[var(--text-secondary)]">
                            {role === 'user' ? '用户' : role === 'assistant' ? '助手' : role}
                        </span>
                    </div>
                    <div className={`rounded-lg px-4 py-3 text-sm leading-relaxed ${
                        isUser
                            ? 'bg-blue-600 text-white'
                            : 'border border-[var(--border-soft)] bg-[var(--surface)] text-[var(--text-primary)]'
                    }`}>
                        {text && <div className="max-h-96 overflow-y-auto whitespace-pre-wrap break-words">{text}</div>}
                        <AttachmentRenderer attachments={attachments}/>
                        {audioParts.map((part: any, index: number) => {
                            const source = part.url || (part.data ? `data:${part.media_type || `audio/${part.format || 'mpeg'}`};base64,${part.data}` : '');
                            return source ? <DeferredAudio key={index} source={source}/> : null;
                        })}
                        {reasoning && (
                            <div className="mt-2 border-t border-current/15 pt-2 text-xs opacity-80">
                                <div className="mb-1 flex items-center gap-1 font-semibold"><BrainCircuit size={12}/>推理</div>
                                <div className="max-h-48 overflow-auto whitespace-pre-wrap break-words">{reasoning}</div>
                            </div>
                        )}
                        {unknownParts.map((part: any, index: number) => (
                            <pre key={index} className="mt-2 max-w-full overflow-x-auto whitespace-pre-wrap break-all text-xs opacity-80">
                                {stringifyCanonicalValue(part)}
                            </pre>
                        ))}
                        {!text && !reasoning && attachments.length === 0 && audioParts.length === 0 && unknownParts.length === 0 && (
                            <span className="text-[var(--text-secondary)]">空消息</span>
                        )}
                    </div>
                </div>
            </div>
        );
    }

    if (type === 'function_call') {
        return (
            <div className="rounded-lg border border-blue-200 bg-blue-50 p-3 text-sm text-blue-950">
                <div className="mb-2 flex items-center gap-2 font-semibold"><Wrench size={14}/>工具调用：{String(canonical.name || '未命名')}</div>
                <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all text-xs">{stringifyCanonicalValue(canonical.arguments ?? {})}</pre>
            </div>
        );
    }

    if (type === 'function_call_output') {
        return (
            <div className="rounded-lg border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-950">
                <div className="mb-2 flex items-center gap-2 font-semibold"><Braces size={14}/>工具结果</div>
                <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all text-xs">{stringifyCanonicalValue(canonical.output ?? '')}</pre>
            </div>
        );
    }

    if (type === 'reasoning') {
        const reasoning = content.map((part: any) => String(part?.text || '')).join('');
        return (
            <div className="rounded-lg border border-violet-200 bg-violet-50 p-3 text-sm text-violet-950">
                <div className="mb-2 flex items-center gap-2 font-semibold"><BrainCircuit size={14}/>推理</div>
                <div className="max-h-64 overflow-auto whitespace-pre-wrap break-words text-xs">{reasoning || '未保存可见推理文本'}</div>
            </div>
        );
    }

    return (
        <div className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] p-3 text-sm">
            <div className="mb-2 flex items-center gap-2 font-semibold"><Braces size={14}/>{type}</div>
            <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all text-xs">{stringifyCanonicalValue(canonical)}</pre>
        </div>
    );
};

const isEmptyAssistantToolShell = (items: ConversationCanonicalItem[], index: number) => {
    const canonical = items[index]?.canonical || {};
    if (canonical.type !== 'message' || canonical.role !== 'assistant') return false;
    const content = Array.isArray(canonical.content) ? canonical.content : [];
    const hasVisibleContent = content.some((part: any) =>
        part?.text || part?.url || part?.data || part?.file_id || part?.filename || part?.transcript,
    );
    const reasoning = canonical.extra?.['openai_chat.reasoning_content'];
    if (hasVisibleContent || (typeof reasoning === 'string' && reasoning.trim())) return false;
    for (let next = index + 1; next < items.length; next++) {
        if (items[next]?.canonical?.type === 'reasoning') continue;
        return items[next]?.canonical?.type === 'function_call';
    }
    return false;
};

const visibleCanonicalItems = (items: ConversationCanonicalItem[]) =>
    items.filter((_, index) => !isEmptyAssistantToolShell(items, index));

const inputLooksLikeHistorySnapshot = (items: ConversationCanonicalItem[]) => {
    let userMessages = 0;
    for (const item of items) {
        const canonical = item.canonical || {};
        if (canonical.type === 'message') {
            if (canonical.role === 'assistant') return true;
            if (canonical.role === 'user') userMessages += 1;
        }
        if (canonical.type === 'function_call' || canonical.type === 'reasoning') return true;
    }
    return userMessages > 1;
};

const CONTEXT_MODE_LABELS: Record<ConversationTurnRecord['contextMode'], string> = {
    legacy: '旧记录',
    new: '新会话',
    explicit: '确定关联',
    inferred: '推断关联',
    snapshot: '完整上下文',
};

const contextModeLabel = (mode: ConversationTurnRecord['contextMode']) =>
    CONTEXT_MODE_LABELS[mode] || '未知';

const CanonicalTurnContent: React.FC<{record: ConversationTurnRecord}> = ({record}) => {
    const inputItems = visibleCanonicalItems(record.items.filter(item => item.direction === 'input'));
    const outputItems = visibleCanonicalItems(record.items.filter(item => item.direction === 'output'));
    const snapshot = record.contextMode === 'snapshot'
        || (record.contextMode === 'legacy' && inputLooksLikeHistorySnapshot(inputItems));

    return (
        <div className="space-y-5">
            {inputItems.length > 0 && (snapshot ? (
                <details className="border-y border-[var(--border-soft)] py-3">
                    <summary className="cursor-pointer list-none text-xs font-semibold text-[var(--text-secondary)]">
                        调用方携带的历史上下文 · {inputItems.length} 项
                    </summary>
                    <div className="mt-3 space-y-3">
                        {inputItems.map(item => <CanonicalItemRenderer key={item.id} item={item}/>)}
                    </div>
                </details>
            ) : (
                <section>
                    <div className="mb-3 text-xs font-semibold text-[var(--text-secondary)]">本次输入</div>
                    <div className="space-y-3">
                        {inputItems.map(item => <CanonicalItemRenderer key={item.id} item={item}/>)}
                    </div>
                </section>
            ))}
            {outputItems.length > 0 && (
                <section>
                    <div className="mb-3 text-xs font-semibold text-[var(--text-secondary)]">本次模型输出</div>
                    <div className="space-y-3">
                        {outputItems.map(item => <CanonicalItemRenderer key={item.id} item={item}/>)}
                    </div>
                </section>
            )}
        </div>
    );
};

const formatDuration = (milliseconds: number) => milliseconds >= 1000
    ? `${(milliseconds / 1000).toFixed(2)}s`
    : `${milliseconds || 0}ms`;

const parsePositive = (value: string) => {
    const parsed = Number(value);
    return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
};

const StatusBadge: React.FC<{status?: string}> = ({status}) => {
    if (!status) return <span className="text-sm text-[var(--text-secondary)]">-</span>;
    const meta = CALL_STATUS_LABELS[status] || {label: status, className: 'bg-gray-100 text-gray-700'};
    return <span className={`inline-flex rounded-md px-2 py-1 text-xs font-semibold ${meta.className}`}>{meta.label}</span>;
};

const Info: React.FC<{label: string; children: React.ReactNode; mono?: boolean}> = ({label, children, mono}) => (
    <div className="min-w-0 py-2">
        <div className="mb-1 text-[11px] font-semibold text-[var(--text-secondary)]">{label}</div>
        <div className={`break-words text-sm text-[var(--text-primary)] ${mono ? 'font-mono' : ''}`}>{children ?? '-'}</div>
    </div>
);

const MessageRecord: React.FC<{message: ChatMessage; admin: boolean}> = ({message, admin}) => {
    const roleMeta = message.role === 'user'
        ? {label: '用户', icon: User, className: 'text-blue-600'}
        : message.role === 'assistant'
            ? {label: '助手', icon: Bot, className: 'text-emerald-600'}
            : {label: '系统', icon: Hash, className: 'text-amber-600'};
    const RoleIcon = roleMeta.icon;
    const hasMetadata = message.inputTokens > 0 || message.outputTokens > 0 || hasPositiveAmount(message.cost)
        || message.latencyMs > 0 || Boolean(message.finishReason) || Boolean(message.model)
        || (admin && Boolean(message.requestLogId || message.channelId || message.accountId));

    return (
        <article className="grid gap-3 border-t border-[var(--border-soft)] py-4 first:border-t-0 md:grid-cols-[108px_minmax(0,1fr)]">
            <div className="flex items-center gap-2 self-start text-xs font-semibold md:pt-0.5">
                <RoleIcon size={14} className={roleMeta.className}/>
                <span className="text-[var(--text-primary)]">{roleMeta.label}</span>
                <span className="text-[10px] font-normal text-[var(--text-secondary)] md:hidden">{formatTime(message.createdAt)}</span>
            </div>
            <div className="min-w-0">
                <div className="hidden text-[10px] text-[var(--text-secondary)] md:block">{formatTime(message.createdAt)}</div>
                <div className="mt-1 max-h-96 overflow-y-auto whitespace-pre-wrap break-words text-sm leading-6 text-[var(--text-primary)]">
                    {message.content || <span className="text-[var(--text-secondary)]">无文本内容</span>}
                </div>
                <AttachmentRenderer attachments={parseAttachments(message.attachments)}/>
                {message.reasoningContent && (
                    <details className="mt-3 rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] px-3 py-2">
                        <summary className="cursor-pointer text-xs font-semibold text-[var(--text-secondary)]">推理内容</summary>
                        <div className="mt-2 max-h-64 overflow-y-auto whitespace-pre-wrap break-words text-xs leading-5 text-[var(--text-primary)]">{message.reasoningContent}</div>
                    </details>
                )}
                {hasMetadata && (
                    <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-[var(--border-soft)] pt-2 text-[10px] text-[var(--text-secondary)]">
                        {(message.inputTokens > 0 || message.outputTokens > 0) && <span>{message.inputTokens} / {message.outputTokens} tokens</span>}
                        {hasPositiveAmount(message.cost) && <span>{formatMoney(message.cost)}</span>}
                        {message.latencyMs > 0 && <span>{formatDuration(message.latencyMs)}</span>}
                        {message.model && <span>{message.model}</span>}
                        {message.finishReason && <span>结束原因：{message.finishReason}</span>}
                        {admin && Number(message.requestLogId) > 0 && <span>请求日志 #{message.requestLogId}</span>}
                        {admin && Number(message.channelId) > 0 && <span>渠道 #{message.channelId}</span>}
                        {admin && Number(message.accountId) > 0 && <span>账号 #{message.accountId}</span>}
                    </div>
                )}
            </div>
        </article>
    );
};

type ConversationTimelineProps = {
    turns: ConversationTurn[];
    conversation: Conversation;
    admin: boolean;
    onOpenCall: (callId: string) => void;
};

const ConversationTimeline: React.FC<ConversationTimelineProps> = ({turns, conversation, admin, onOpenCall}) => {
    if (turns.length === 0) {
        return <div className="py-16 text-center text-sm text-[var(--text-secondary)]">暂无对话内容</div>;
    }

    return (
        <div className="divide-y divide-[var(--border-soft)] border-y border-[var(--border-soft)]">
            {turns.map((turn, turnIndex) => {
                const status = turn.status || (turn.callId === conversation.lastCallId ? conversation.lastStatus : undefined);
                return (
                    <section key={turn.key} className="py-5">
                        <div className="flex flex-wrap items-start justify-between gap-3">
                            <div className="min-w-0">
                                <div className="flex flex-wrap items-center gap-2">
                                    <h3 className="text-sm font-bold text-[var(--text-primary)]">
                                        {turn.record ? `第 ${turn.record.sequence} 次调用` : `历史调用 ${turnIndex + 1}`}
                                    </h3>
                                    <StatusBadge status={status}/>
                                    {turn.record && (
                                        <span className="rounded-md bg-gray-100 px-2 py-1 text-xs font-semibold text-gray-600">
                                            {contextModeLabel(turn.record.contextMode)}
                                        </span>
                                    )}
                                </div>
                                <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-[var(--text-secondary)]">
                                    <span>{formatTime(turn.record?.createdAt || turn.messages[0]?.createdAt || conversation.createdAt)}</span>
                                    {turn.record?.model && <span>{turn.record.model}</span>}
                                    {turn.record?.finishReason && <span>结束原因：{turn.record.finishReason}</span>}
                                </div>
                            </div>
                            {turn.callId && (
                                <button type="button" onClick={() => onOpenCall(turn.callId!)} className="inline-flex max-w-full items-center gap-1.5 rounded-lg border border-[var(--border-soft)] px-2.5 py-1.5 text-xs font-semibold text-[var(--primary)] hover:bg-[var(--primary-lighter)]">
                                    <Activity size={13}/><span className="max-w-56 truncate font-mono">{turn.callId}</span>
                                </button>
                            )}
                        </div>

                        <div className="mt-4">
                            {turn.record && <CanonicalTurnContent record={turn.record}/>}
                            {turn.messages.map(message => <MessageRecord key={message.id} message={message} admin={admin}/>)}
                        </div>

                        {turn.record && (turn.record.status === 'failed' || turn.record.status === 'aborted') && (
                            <div className="mt-3 flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-800">
                                <AlertTriangle size={16} className="mt-0.5 shrink-0"/>
                                <div className="min-w-0">
                                    <div className="font-semibold">{turn.record.status === 'aborted' ? '请求在完成前中止' : '请求失败'}</div>
                                    {turn.record.errorMessage && <div className="mt-1 break-words text-xs">{turn.record.errorMessage}</div>}
                                    {(turn.record.errorType || turn.record.errorCode) && (
                                        <div className="mt-1 break-all font-mono text-[10px] opacity-75">{[turn.record.errorType, turn.record.errorCode].filter(Boolean).join(' / ')}</div>
                                    )}
                                </div>
                            </div>
                        )}

                        {turn.record && (
                            <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-[var(--border-soft)] pt-3 text-xs text-[var(--text-secondary)]">
                                <span>输入 / 输出：{turn.record.inputTokens} / {turn.record.outputTokens}</span>
                                <span>总计：{turn.record.totalTokens}</span>
                                <span>{formatMoney(turn.record.cost)}</span>
                                <span>{formatDuration(turn.record.latencyMs)}</span>
                                {admin && Number(turn.record.requestLogId) > 0 && <span>请求日志 #{turn.record.requestLogId}</span>}
                                {admin && turn.record.providerResponseId && <span className="max-w-64 truncate font-mono" title={turn.record.providerResponseId}>上游响应 {turn.record.providerResponseId}</span>}
                            </div>
                        )}
                    </section>
                );
            })}
        </div>
    );
};

const StructuredTurns: React.FC<{records: ConversationTurnRecord[]; admin: boolean; onOpenCall: (callId: string) => void}> = ({records, admin, onOpenCall}) => {
    if (records.length === 0) {
        return <div className="py-16 text-center text-sm text-[var(--text-secondary)]">旧对话没有规范调用记录</div>;
    }
    return (
        <div className="divide-y divide-[var(--border-soft)] border-y border-[var(--border-soft)]">
            {records.map(record => (
                <section key={record.id} className="py-4">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                        <div className="flex items-center gap-2"><h3 className="text-sm font-bold text-[var(--text-primary)]">第 {record.sequence} 次调用</h3><StatusBadge status={record.status}/></div>
                        <button type="button" onClick={() => onOpenCall(record.callId)} className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-soft)] px-2.5 py-1.5 text-xs font-semibold text-[var(--primary)] hover:bg-[var(--primary-lighter)]"><Activity size={13}/>查看调用</button>
                    </div>
                    <div className="mt-2 grid gap-x-6 md:grid-cols-2 xl:grid-cols-4">
                        <Info label="模型">{record.model || '-'}</Info>
                        <Info label="创建时间">{formatTime(record.createdAt)}</Info>
                        <Info label="输入 / 输出">{record.inputTokens} / {record.outputTokens}</Info>
                        <Info label="总 Token">{record.totalTokens}</Info>
                        <Info label="费用">{formatMoney(record.cost)}</Info>
                        <Info label="耗时">{formatDuration(record.latencyMs)}</Info>
                        <Info label="结束原因">{record.finishReason || '-'}</Info>
                        <Info label="上下文模式">{contextModeLabel(record.contextMode)}</Info>
                        <Info label="内容项">{record.items.length}</Info>
                        {admin && <Info label="请求日志 ID">{record.requestLogId || '-'}</Info>}
                        {admin && <Info label="上游响应 ID" mono>{record.providerResponseId || '-'}</Info>}
                    </div>
                    {(record.errorMessage || record.errorCode || record.errorType) && (
                        <div className="mt-2 rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700">
                            <div className="font-semibold">{record.errorCode || record.errorType || '请求失败'}</div>
                            {record.errorMessage && <div className="mt-1 whitespace-pre-wrap break-words text-xs">{record.errorMessage}</div>}
                        </div>
                    )}
                </section>
            ))}
        </div>
    );
};

const ChatLogs: React.FC = () => {
    const navigate = useNavigate();
    const admin = useMemo(() => isAdmin(), []);
    const [conversations, setConversations] = useState<Conversation[]>([]);
    const [total, setTotal] = useState(0);
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
    const [isLoading, setIsLoading] = useState(true);
    const [loadError, setLoadError] = useState('');
    const [refreshKey, setRefreshKey] = useState(0);
    const [users, setUsers] = useState<PrismUser[]>([]);
    const [draft, setDraft] = useState<ConversationFilterDraft>({...EMPTY_FILTERS});
    const [filters, setFilters] = useState<ConversationFilterDraft>({...EMPTY_FILTERS});
    const [isDrawerOpen, setIsDrawerOpen] = useState(false);
    const [selectedConversation, setSelectedConversation] = useState<Conversation | null>(null);
    const [messages, setMessages] = useState<ChatMessage[]>([]);
    const [turnRecords, setTurnRecords] = useState<ConversationTurnRecord[]>([]);
    const [messageTotal, setMessageTotal] = useState(0);
    const [turnTotal, setTurnTotal] = useState(0);
    const [messagePage, setMessagePage] = useState(1);
    const [turnPage, setTurnPage] = useState(1);
    const [loadingMessages, setLoadingMessages] = useState(false);
    const [loadingMore, setLoadingMore] = useState(false);
    const [messagesError, setMessagesError] = useState('');
    const [detailWarning, setDetailWarning] = useState('');
    const [activeTab, setActiveTab] = useState<'overview' | 'conversation' | 'turns'>('overview');
    const listRequest = useRef(0);
    const detailRequest = useRef(0);
    const turns = useMemo(() => buildTurns(messages, turnRecords), [messages, turnRecords]);

    useEffect(() => {
        if (!admin) return;
        fetchUsers().then(setUsers).catch(() => setUsers([]));
    }, [admin]);

    useEffect(() => {
        const requestNo = ++listRequest.current;
        // 过滤和页码快速变化时旧请求可能后返回，序号确保只有最新请求能更新列表。
        setIsLoading(true);
        setLoadError('');
        const params: ConversationListParams = {
            page,
            page_size: pageSize,
            keyword: filters.keyword.trim() || undefined,
            model: filters.model.trim() || undefined,
            start_date: filters.start_date || undefined,
            end_date: filters.end_date || undefined,
            ...(admin ? {
                user_id: parsePositive(filters.user_id),
                token_id: parsePositive(filters.token_id),
            } : {}),
        };
        fetchConversations(params).then(resp => {
            if (requestNo !== listRequest.current) return;
            setConversations(resp.items);
            setTotal(resp.total);
            const lastPage = Math.max(1, Math.ceil(resp.total / pageSize));
            if (page > lastPage) setPage(lastPage);
        }).catch(error => {
            if (requestNo !== listRequest.current) return;
            setConversations([]);
            setTotal(0);
            setLoadError(error instanceof Error ? error.message : '对话记录加载失败');
        }).finally(() => {
            if (requestNo === listRequest.current) setIsLoading(false);
        });
        return () => {
            if (requestNo === listRequest.current) listRequest.current += 1;
        };
    }, [admin, filters, page, pageSize, refreshKey]);

    const openDetails = async (conv: Conversation) => {
        const requestNo = ++detailRequest.current;
        setIsDrawerOpen(true);
        setSelectedConversation(conv);
        setActiveTab('overview');
        setMessages([]);
        setTurnRecords([]);
        setMessageTotal(0);
        setTurnTotal(0);
        setMessagePage(1);
        setTurnPage(1);
        setMessagesError('');
        setDetailWarning('');
        setLoadingMessages(true);
        setLoadingMore(false);
        try {
            // 两套历史独立加载，任一接口失败时仍展示另一套可用数据。
            const [messageResult, turnResult] = await Promise.allSettled([
                fetchConversationMessages(conv.id, 1, DETAIL_PAGE_SIZE),
                fetchConversationTurns(conv.id, 1, DETAIL_PAGE_SIZE),
            ]);
            if (requestNo !== detailRequest.current) return;
            const warnings: string[] = [];
            if (messageResult.status === 'fulfilled') {
                setMessages(messageResult.value.items);
                setMessageTotal(messageResult.value.total);
            } else {
                warnings.push(messageResult.reason instanceof Error ? `旧消息：${messageResult.reason.message}` : '旧消息加载失败');
            }
            if (turnResult.status === 'fulfilled') {
                setTurnRecords(turnResult.value.items);
                setTurnTotal(turnResult.value.total);
            } else {
                warnings.push(turnResult.reason instanceof Error ? `轮次记录：${turnResult.reason.message}` : '轮次记录加载失败');
            }
            if (messageResult.status === 'rejected' && turnResult.status === 'rejected') {
                setMessagesError(warnings.join('；'));
            } else {
                setDetailWarning(warnings.join('；'));
            }
        } catch (e) {
            if (requestNo !== detailRequest.current) return;
            console.error('Failed to load messages', e);
            setMessages([]);
            setTurnRecords([]);
            setMessagesError(e instanceof Error ? e.message : '对话消息加载失败');
        } finally {
            if (requestNo === detailRequest.current) setLoadingMessages(false);
        }
    };

    const closeDetails = () => {
        detailRequest.current += 1;
        setIsDrawerOpen(false);
        setSelectedConversation(null);
        setMessages([]);
        setTurnRecords([]);
        setMessageTotal(0);
        setTurnTotal(0);
        setMessagePage(1);
        setTurnPage(1);
        setMessagesError('');
        setDetailWarning('');
        setLoadingMessages(false);
        setLoadingMore(false);
    };

    const loadMoreDetails = async (scope: 'all' | 'turns' = 'all') => {
        if (!selectedConversation || loadingMore) return;
        const loadMessages = scope === 'all' && messages.length < messageTotal;
        const loadTurns = turnRecords.length < turnTotal;
        if (!loadMessages && !loadTurns) return;
        const requestNo = detailRequest.current;
        setLoadingMore(true);
        const [messageResult, turnResult] = await Promise.allSettled([
            loadMessages
                ? fetchConversationMessages(selectedConversation.id, messagePage + 1, DETAIL_PAGE_SIZE)
                : Promise.resolve(null),
            loadTurns
                ? fetchConversationTurns(selectedConversation.id, turnPage + 1, DETAIL_PAGE_SIZE)
                : Promise.resolve(null),
        ]);
        if (requestNo !== detailRequest.current) return;
        const warnings: string[] = [];
        if (loadMessages) {
            if (messageResult.status === 'fulfilled' && messageResult.value) {
                setMessages(current => [...current, ...messageResult.value!.items]);
                setMessageTotal(messageResult.value.total);
                setMessagePage(current => current + 1);
            } else if (messageResult.status === 'rejected') {
                warnings.push(messageResult.reason instanceof Error ? `旧消息：${messageResult.reason.message}` : '旧消息加载失败');
            }
        }
        if (loadTurns) {
            if (turnResult.status === 'fulfilled' && turnResult.value) {
                setTurnRecords(current => [...current, ...turnResult.value!.items]);
                setTurnTotal(turnResult.value.total);
                setTurnPage(current => current + 1);
            } else if (turnResult.status === 'rejected') {
                warnings.push(turnResult.reason instanceof Error ? `轮次记录：${turnResult.reason.message}` : '轮次记录加载失败');
            }
        }
        if (warnings.length > 0) {
            setDetailWarning(current => Array.from(new Set([...current.split('；'), ...warnings].filter(Boolean))).join('；'));
        }
        setLoadingMore(false);
    };

    const updateDraft = (key: keyof ConversationFilterDraft, value: string) => {
        setDraft(current => ({...current, [key]: value}));
    };

    const search = () => {
        setPage(1);
        setFilters({...draft});
    };

    const reset = () => {
        setPage(1);
        setDraft({...EMPTY_FILTERS});
        setFilters({...EMPTY_FILTERS});
    };

    const refresh = () => {
        setPage(1);
        setRefreshKey(value => value + 1);
    };

    const changePageSize = (value: number) => {
        setPageSize(value);
        setPage(1);
    };

    const openCall = (callId: string) => {
        closeDetails();
        navigate(`/calls?call_id=${encodeURIComponent(callId)}`);
    };

    const tableColumnCount = admin ? 8 : 7;
    const canLoadMoreDetails = messages.length < messageTotal || turnRecords.length < turnTotal;
    const detailLoadSummary = `消息 ${messages.length}/${messageTotal} · 轮次 ${turnRecords.length}/${turnTotal}`;

    return (
        <div className="space-y-5">
            <header className="flex items-center justify-between gap-3">
                <h1 className="flex items-center gap-2 text-xl font-bold text-[var(--text-primary)] md:text-2xl"><MessageSquare size={23}/>对话记录</h1>
                <button type="button" title="刷新" aria-label="刷新" onClick={refresh} className="flex h-9 w-9 items-center justify-center rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] text-[var(--text-primary)] hover:bg-[var(--surface)]">
                    <RefreshCw size={17} className={isLoading ? 'animate-spin' : ''}/>
                </button>
            </header>

            <section className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-4">
                <form onSubmit={event => { event.preventDefault(); search(); }}>
                <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
                    <label className="text-xs font-semibold text-[var(--text-secondary)]">对话标题
                        <input value={draft.keyword} onChange={event => updateDraft('keyword', event.target.value)} placeholder="标题关键词" className={`${INPUT_CLASS} mt-1`}/>
                    </label>
                    <label className="text-xs font-semibold text-[var(--text-secondary)]">模型
                        <input value={draft.model} onChange={event => updateDraft('model', event.target.value)} placeholder="模型名称" className={`${INPUT_CLASS} mt-1`}/>
                    </label>
                    {admin && (
                        <div className="text-xs font-semibold text-[var(--text-secondary)]">用户
                            <Select value={draft.user_id} onChange={v => updateDraft('user_id', v)} className="mt-1" options={[{ label: '全部用户', value: '' }, ...users.map(user => ({ label: `${user.username} (#${user.id})`, value: String(user.id) }))]} />
                        </div>
                    )}
                    {admin && (
                        <label className="text-xs font-semibold text-[var(--text-secondary)]">Token ID
                            <input type="number" min="1" value={draft.token_id} onChange={event => updateDraft('token_id', event.target.value)} placeholder="Token ID" className={`${INPUT_CLASS} mt-1`}/>
                        </label>
                    )}
                    <label className="text-xs font-semibold text-[var(--text-secondary)]">开始日期
                        <input type="date" value={draft.start_date} onChange={event => updateDraft('start_date', event.target.value)} className={`${INPUT_CLASS} mt-1`}/>
                    </label>
                    <label className="text-xs font-semibold text-[var(--text-secondary)]">结束日期
                        <input type="date" value={draft.end_date} onChange={event => updateDraft('end_date', event.target.value)} className={`${INPUT_CLASS} mt-1`}/>
                    </label>
                </div>
                <div className="mt-4 flex justify-end gap-2">
                    <button type="button" onClick={reset} className="flex items-center gap-2 rounded-lg border border-[var(--border-soft)] px-3 py-2 text-sm font-medium text-[var(--text-secondary)] hover:bg-[var(--surface)]"><RotateCcw size={15}/>重置</button>
                    <button type="submit" className="flex items-center gap-2 rounded-lg bg-[var(--primary)] px-4 py-2 text-sm font-semibold text-white hover:opacity-90"><Search size={15}/>查询</button>
                </div>
                </form>
            </section>

            <section className="overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)]">
                {loadError && <div className="flex items-center gap-2 border-b border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700"><AlertTriangle size={16}/>{loadError}</div>}
                <div className="overflow-x-auto">
                    <table className="w-full min-w-[1240px] table-fixed text-left">
                        <thead className="border-b border-[var(--border-soft)] bg-[var(--surface)]/60 text-xs text-[var(--text-secondary)]">
                        <tr>
                            <th className="w-[280px] px-4 py-3">更新时间 / 对话</th>
                            <th className="w-[210px] px-4 py-3">模型 / 状态</th>
                            {admin && <th className="w-[130px] px-4 py-3">归属</th>}
                            <th className="w-[80px] px-4 py-3 text-right">消息</th>
                            <th className="w-[100px] px-4 py-3 text-right">Token</th>
                            <th className="w-[140px] px-4 py-3 text-right">费用</th>
                            <th className="w-[250px] px-4 py-3">最近调用</th>
                            <th className="w-10 px-3 py-3"/>
                        </tr>
                        </thead>
                        <tbody className="divide-y divide-[var(--border-soft)]">
                        {isLoading ? Array.from({length: 7}).map((_, index) => (
                            <tr key={index} className="animate-pulse"><td colSpan={tableColumnCount} className="px-4 py-4"><div className="h-4 rounded bg-[var(--primary-lighter)]"/></td></tr>
                        )) : conversations.length === 0 ? (
                            <tr><td colSpan={tableColumnCount} className="px-4 py-16 text-center text-sm text-[var(--text-secondary)]">暂无对话记录</td></tr>
                        ) : conversations.map(conversation => (
                            <tr key={conversation.id} tabIndex={0} onClick={() => openDetails(conversation)} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); openDetails(conversation); } }} className="cursor-pointer transition hover:bg-[var(--primary-lighter)]/50 focus:bg-[var(--primary-lighter)]/50 focus:outline-none">
                                <td className="px-4 py-3">
                                    <div className="text-sm text-[var(--text-primary)]">{formatTime(conversation.updatedAt)}</div>
                                    <div className="mt-1 max-w-72 truncate text-xs font-semibold text-[var(--text-primary)]" title={conversation.title}>{conversation.title || '未命名对话'}</div>
                                    <div className="mt-0.5 font-mono text-[10px] text-[var(--text-secondary)]">会话 #{conversation.id}</div>
                                </td>
                                <td className="px-4 py-3">
                                    <div className="max-w-56 truncate text-sm font-semibold text-[var(--text-primary)]" title={conversation.model}>{conversation.model || '-'}</div>
                                    <div className="mt-1"><StatusBadge status={conversation.lastStatus}/></div>
                                </td>
                                {admin && <td className="px-4 py-3"><div className="text-sm text-[var(--text-primary)]">用户 #{conversation.userId || '-'}</div><div className="mt-1 text-xs text-[var(--text-secondary)]">Token #{conversation.tokenId || '-'}</div></td>}
                                <td className="whitespace-nowrap px-4 py-3 text-right text-sm font-semibold text-[var(--text-primary)]">{conversation.messageCount} 条</td>
                                <td className="whitespace-nowrap px-4 py-3 text-right text-sm text-[var(--text-primary)]">{conversation.totalTokens.toLocaleString()}</td>
                                <td className="whitespace-nowrap px-4 py-3 text-right text-sm font-semibold text-[var(--text-primary)]">{formatMoney(conversation.totalCost)}</td>
                                <td className="px-4 py-3"><div className="max-w-48 truncate font-mono text-xs text-[var(--text-primary)]" title={conversation.lastCallId}>{conversation.lastCallId || '-'}</div><div className="mt-1 text-xs text-[var(--text-secondary)]">创建于 {formatTime(conversation.createdAt)}</div></td>
                                <td className="px-3 py-3 text-right"><ChevronRight size={17} className="text-[var(--text-secondary)]"/></td>
                            </tr>
                        ))}
                        </tbody>
                    </table>
                </div>
                <Pagination page={page} pageSize={pageSize} total={total} loading={isLoading} onPageChange={setPage} onPageSizeChange={changePageSize} />
            </section>

            <Drawer open={isDrawerOpen && Boolean(selectedConversation)} onClose={closeDetails} title="对话详情" subtitle={selectedConversation ? (selectedConversation.title || `会话 #${selectedConversation.id}`) : '加载中'} width="max-w-5xl" panelClassName="bg-[var(--surface)]">
                {selectedConversation && (
                    <>
                        <div role="tablist" className="flex overflow-x-auto border-b border-[var(--border-soft)] px-4">
                            {([
                                ['overview', '概览', LayoutList],
                                ['conversation', '对话内容', MessageSquare],
                                ['turns', '调用记录', ListTree],
                            ] as const).map(([key, label, Icon]) => (
                                <button key={key} type="button" role="tab" aria-selected={activeTab === key} onClick={() => setActiveTab(key)} className={`flex shrink-0 items-center gap-2 border-b-2 px-4 py-3 text-sm font-semibold ${activeTab === key ? 'border-[var(--primary)] text-[var(--primary)]' : 'border-transparent text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}>
                                    <Icon size={16}/>{label}
                                </button>
                            ))}
                        </div>

                        <div className="flex-1 overflow-y-auto p-5">
                            {activeTab === 'overview' ? (
                                <div className="space-y-5">
                                    <section>
                                        <h3 className="mb-2 flex items-center gap-2 text-sm font-bold text-[var(--text-primary)]"><CircleUserRound size={16}/>会话</h3>
                                        <div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-2 xl:grid-cols-3">
                                            <Info label="状态"><StatusBadge status={selectedConversation.lastStatus}/></Info>
                                            <Info label="模型">{selectedConversation.model || '-'}</Info>
                                            <Info label="会话 ID">{selectedConversation.id}</Info>
                                            <Info label="标题">{selectedConversation.title || '未命名对话'}</Info>
                                            <Info label="消息数">{selectedConversation.messageCount}</Info>
                                            <Info label="规范轮次">{loadingMessages ? '加载中' : `${turnRecords.length} / ${turnTotal}`}</Info>
                                            {admin && <Info label="用户 ID">{selectedConversation.userId || '-'}</Info>}
                                            {admin && <Info label="Token ID">{selectedConversation.tokenId || '-'}</Info>}
                                            {admin && <Info label="最后请求日志 ID">{selectedConversation.lastRequestLogId || '-'}</Info>}
                                            <Info label="最近调用">
                                                {selectedConversation.lastCallId ? (
                                                    <button type="button" onClick={() => openCall(selectedConversation.lastCallId!)} className="inline-flex max-w-full items-center gap-1.5 text-[var(--primary)] hover:underline">
                                                        <Activity size={13}/><span className="truncate font-mono text-xs">{selectedConversation.lastCallId}</span>
                                                    </button>
                                                ) : '-'}
                                            </Info>
                                        </div>
                                    </section>

                                    <section>
                                        <h3 className="mb-2 flex items-center gap-2 text-sm font-bold text-[var(--text-primary)]"><ReceiptText size={16}/>Usage 与费用</h3>
                                        <div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-3">
                                            <Info label="总 Token">{selectedConversation.totalTokens.toLocaleString()}</Info>
                                            <Info label="累计费用">{formatMoney(selectedConversation.totalCost)}</Info>
                                            <Info label="平均每条消息">{selectedConversation.messageCount > 0 ? Math.round(selectedConversation.totalTokens / selectedConversation.messageCount).toLocaleString() : '0'} tokens</Info>
                                        </div>
                                    </section>

                                    <section>
                                        <h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">时间</h3>
                                        <div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-2">
                                            <Info label="创建时间">{formatTime(selectedConversation.createdAt)}</Info>
                                            <Info label="更新时间">{formatTime(selectedConversation.updatedAt)}</Info>
                                        </div>
                                    </section>

                                    {selectedConversation.systemPrompt && (
                                        <section>
                                            <h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">System Prompt</h3>
                                            <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] p-4 font-sans text-sm leading-6 text-[var(--text-primary)]">{selectedConversation.systemPrompt}</pre>
                                        </section>
                                    )}

                                    {detailWarning && <div className="flex items-center gap-2 rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800"><AlertTriangle size={17}/>{detailWarning}</div>}
                                    {messagesError && <div className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700"><AlertTriangle size={17}/>{messagesError}</div>}
                                </div>
                            ) : activeTab === 'conversation' ? (
                                loadingMessages ? (
                                    <div className="space-y-4 animate-pulse"><div className="h-24 rounded-lg bg-[var(--primary-lighter)]"/><div className="h-48 rounded-lg bg-[var(--primary-lighter)]"/></div>
                                ) : messagesError ? (
                                    <div className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700"><AlertTriangle size={17}/>{messagesError}</div>
                                ) : (
                                    <div className="space-y-4">
                                        {detailWarning && <div className="flex items-center gap-2 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800"><AlertTriangle size={16}/>{detailWarning}</div>}
                                        <ConversationTimeline turns={turns} conversation={selectedConversation} admin={admin} onOpenCall={openCall}/>
                                        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border-soft)] pt-3 text-xs text-[var(--text-secondary)]">
                                            <span>{detailLoadSummary}</span>
                                            {canLoadMoreDetails && <button type="button" disabled={loadingMore} onClick={() => loadMoreDetails()} className="inline-flex items-center gap-2 rounded-lg border border-[var(--border-soft)] px-3 py-2 text-sm font-semibold text-[var(--primary)] hover:bg-[var(--primary-lighter)] disabled:opacity-50"><RefreshCw size={14} className={loadingMore ? 'animate-spin' : ''}/>加载更多</button>}
                                        </div>
                                    </div>
                                )
                            ) : loadingMessages ? (
                                <div className="space-y-4 animate-pulse"><div className="h-24 rounded-lg bg-[var(--primary-lighter)]"/><div className="h-48 rounded-lg bg-[var(--primary-lighter)]"/></div>
                            ) : messagesError ? (
                                <div className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700"><AlertTriangle size={17}/>{messagesError}</div>
                            ) : (
                                <div className="space-y-4">
                                    {detailWarning && <div className="flex items-center gap-2 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800"><AlertTriangle size={16}/>{detailWarning}</div>}
                                    <StructuredTurns records={turnRecords} admin={admin} onOpenCall={openCall}/>
                                    <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border-soft)] pt-3 text-xs text-[var(--text-secondary)]">
                                        <span>轮次 {turnRecords.length}/{turnTotal}</span>
                                        {turnRecords.length < turnTotal && <button type="button" disabled={loadingMore} onClick={() => loadMoreDetails('turns')} className="inline-flex items-center gap-2 rounded-lg border border-[var(--border-soft)] px-3 py-2 text-sm font-semibold text-[var(--primary)] hover:bg-[var(--primary-lighter)] disabled:opacity-50"><RefreshCw size={14} className={loadingMore ? 'animate-spin' : ''}/>加载更多</button>}
                                    </div>
                                </div>
                            )}
                        </div>
                    </>
                )}
            </Drawer>
        </div>
    );
};

export default ChatLogs;
