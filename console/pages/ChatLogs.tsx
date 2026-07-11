import React, {useEffect, useState} from 'react';
import {
    Search,
    DollarSign,
    ChevronRight,
    X,
    RefreshCw,
    ChevronLeft,
    MessageSquare,
    User,
    Bot,
    Clock,
    Hash,
    FileText
} from 'lucide-react';
import {fetchConversations, fetchConversationMessages, ConversationListParams} from '../services/api';
import {fetchGwModels, GwModel} from '../services/gatewayApi';
import {Conversation, ChatMessage, UserRole} from '../types';
import ModelSelector from './playground/ModelSelector';

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
    image_url?: { url: string; detail?: string };
    file_url?: { url: string; content_type?: string };
}

const parseAttachments = (raw?: string): Attachment[] => {
    if (!raw) return [];
    try {
        return JSON.parse(raw);
    } catch {
        return [];
    }
};

const AttachmentRenderer: React.FC<{ attachments: Attachment[] }> = ({attachments}) => {
    const [preview, setPreview] = useState<string | null>(null);
    if (attachments.length === 0) return null;
    return (
        <>
            <div className="flex flex-wrap gap-2 mt-2">
                {attachments.map((att, i) => {
                    if (att.type === 'image_url' && att.image_url?.url) {
                        return (
                            <div key={i} className="relative group cursor-pointer" onClick={() => setPreview(att.image_url!.url)}>
                                <img src={att.image_url.url} alt="attachment" className="max-w-[200px] max-h-[150px] rounded-lg object-cover border border-white/20"/>
                            </div>
                        );
                    }
                    if (att.type === 'file_url' && att.file_url?.url) {
                        return (
                            <a key={i} href={att.file_url.url} target="_blank" rel="noopener noreferrer"
                               className="flex items-center gap-2 px-3 py-2 rounded-lg bg-black/20 border border-white/10 text-xs hover:bg-black/30 transition-colors">
                                <FileText size={14}/>
                                <span className="max-w-[160px] truncate opacity-80">{att.file_url.content_type || 'file'}</span>
                            </a>
                        );
                    }
                    return null;
                })}
            </div>
            {preview && (
                <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/70 backdrop-blur-sm" onClick={() => setPreview(null)}>
                    <img src={preview} alt="preview" className="max-w-[90vw] max-h-[90vh] rounded-xl shadow-2xl"/>
                </div>
            )}
        </>
    );
};

const ChatLogs: React.FC = () => {
    const [conversations, setConversations] = useState<Conversation[]>([]);
    const [total, setTotal] = useState(0);
    const [page, setPage] = useState(1);
    const [pageSize] = useState(20);
    const [isLoading, setIsLoading] = useState(true);
    const [models, setModels] = useState<GwModel[]>([]);

    // 抽屉状态
    const [isDrawerOpen, setIsDrawerOpen] = useState(false);
    const [selectedConversation, setSelectedConversation] = useState<Conversation | null>(null);
    const [messages, setMessages] = useState<ChatMessage[]>([]);
    const [loadingMessages, setLoadingMessages] = useState(false);

    // 筛选
    const [filters, setFilters] = useState<ConversationListParams>({});
    const [keyword, setKeyword] = useState('');

    useEffect(() => {
        if (isAdmin()) {
            fetchGwModels().then(setModels).catch(() => {
            });
        }
    }, []);

    const loadConversations = async (params?: ConversationListParams) => {
        setIsLoading(true);
        try {
            const resp = await fetchConversations({page, page_size: pageSize, ...filters, ...params});
            setConversations(resp.items);
            setTotal(resp.total);
        } catch (e) {
            console.error('Failed to load conversations', e);
        } finally {
            setIsLoading(false);
        }
    };

    useEffect(() => {
        loadConversations();
    }, [page, filters]);

    const openDetails = async (conv: Conversation) => {
        setIsDrawerOpen(true);
        setSelectedConversation(conv);
        setLoadingMessages(true);
        try {
            const resp = await fetchConversationMessages(conv.id, 1, 200);
            setMessages(resp.items);
        } catch (e) {
            console.error('Failed to load messages', e);
        } finally {
            setLoadingMessages(false);
        }
    };

    const handleSearch = () => {
        setPage(1);
        loadConversations({keyword});
    };

    const totalPages = Math.ceil(total / pageSize);

    return (
        <div className="space-y-4 md:space-y-6">
            <div className="flex items-center justify-between flex-wrap gap-3">
                <div>
                    <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">对话记录</h1>
                    <p className="text-[var(--text-secondary)] mt-1 text-sm md:text-base">查看所有 Chat 对话历史和费用明细</p>
                </div>
                <button
                    onClick={() => loadConversations()}
                    className="px-3 md:px-4 py-2 bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg text-sm font-medium hover:bg-[var(--surface)] transition-colors flex items-center gap-2"
                >
                    <RefreshCw size={16} className={isLoading ? 'animate-spin' : ''}/>
                    <span className="hidden md:inline">刷新</span>
                </button>
            </div>

            <div className="bg-[var(--surface-card)] rounded-2xl shadow-sm border border-[var(--border-soft)] overflow-hidden">
                <div className="p-3 md:p-4 border-b border-[var(--border-soft)] bg-[var(--surface)]/50 flex items-center gap-3 md:gap-4 flex-wrap">
                    <div className="relative flex-1 min-w-[160px] md:max-w-md">
                        <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" size={16}/>
                        <input
                            type="text"
                            value={keyword}
                            onChange={e => setKeyword(e.target.value)}
                            onKeyDown={e => e.key === 'Enter' && handleSearch()}
                            placeholder="搜索对话标题..."
                            className="w-full pl-9 pr-4 py-2 bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                        />
                    </div>
                    {isAdmin() && models.length > 0 && (
                        <ModelSelector
                            options={models.map(m => ({
                                id: m.model_name,
                                label: m.display_name || m.model_name,
                                provider: m.source_channel || m.group_name || 'other',
                            }))}
                            value={filters.model ?? ''}
                            onChange={v => { setFilters({...filters, model: v}); setPage(1); }}
                            allOption="所有模型"
                        />
                    )}
                </div>

                <div className="overflow-x-auto">
                    <table className="w-full text-left min-w-[600px]">
                        <thead>
                        <tr className="border-b border-[var(--border-soft)]">
                            <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">对话标题</th>
                            <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">模型</th>
                            <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-center hidden sm:table-cell">消息</th>
                            <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-center hidden sm:table-cell">Tokens</th>
                            <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-right">费用</th>
                            <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider hidden md:table-cell">时间</th>
                            <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-right">操作</th>
                        </tr>
                        </thead>
                        <tbody className="divide-y divide-gray-100">
                        {isLoading ? (
                            Array.from({length: 8}).map((_, i) => (
                                <tr key={i} className="animate-pulse">
                                    <td colSpan={7} className="px-3 md:px-6 py-4">
                                        <div className="h-4 bg-[var(--primary-lighter)] rounded w-full"></div>
                                    </td>
                                </tr>
                            ))
                        ) : conversations.length === 0 ? (
                            <tr>
                                <td colSpan={7} className="px-3 md:px-6 py-12 text-center text-[var(--text-secondary)]">暂无对话记录</td>
                            </tr>
                        ) : conversations.map(conv => (
                            <tr key={conv.id} className="hover:bg-[var(--primary-lighter)]/30 transition-colors cursor-pointer group"
                                onClick={() => openDetails(conv)}>
                                <td className="px-3 md:px-6 py-3 md:py-4">
                                    <div className="text-sm font-medium text-[var(--text-primary)] truncate max-w-[120px] md:max-w-[200px]"
                                         title={conv.title}>
                                        {conv.title}
                                    </div>
                                </td>
                                <td className="px-3 md:px-6 py-3 md:py-4">
                    <span
                        className="inline-flex px-2 py-0.5 rounded-full text-[10px] font-bold bg-indigo-100 text-[var(--primary)]">
                      {conv.model}
                    </span>
                                </td>
                                <td className="px-3 md:px-6 py-3 md:py-4 text-center hidden sm:table-cell">
                                    <span className="text-sm text-[var(--text-secondary)]">{conv.messageCount}</span>
                                </td>
                                <td className="px-3 md:px-6 py-3 md:py-4 text-center hidden sm:table-cell">
                                    <span className="text-sm text-[var(--text-secondary)]">{conv.totalTokens.toLocaleString()}</span>
                                </td>
                                <td className="px-3 md:px-6 py-3 md:py-4 text-right">
                                    <div className="text-xs text-[var(--text-secondary)] flex items-center justify-end gap-1">
                                        <DollarSign size={10}/>
                                        {Number(conv.totalCost || 0).toFixed(4)}
                                    </div>
                                </td>
                                <td className="px-3 md:px-6 py-3 md:py-4 hidden md:table-cell">
                                    <div className="text-[10px] text-[var(--text-secondary)]">{formatTime(conv.createdAt)}</div>
                                </td>
                                <td className="px-3 md:px-6 py-3 md:py-4 text-right">
                                    <ChevronRight size={16}
                                                  className="text-gray-300 group-hover:text-indigo-500 group-hover:translate-x-1 transition-all"/>
                                </td>
                            </tr>
                        ))}
                        </tbody>
                    </table>
                </div>

                {totalPages > 1 && (
                    <div className="p-3 md:p-4 border-t border-[var(--border-soft)] flex items-center justify-between">
                        <div className="text-xs md:text-sm text-[var(--text-secondary)]">
                            共 {total} 条，第 {page}/{totalPages} 页
                        </div>
                        <div className="flex items-center gap-2">
                            <button
                                onClick={() => setPage(p => Math.max(1, p - 1))}
                                disabled={page <= 1}
                                className="p-2 rounded-lg hover:bg-[var(--primary-lighter)] disabled:opacity-50 disabled:cursor-not-allowed"
                            >
                                <ChevronLeft size={16}/>
                            </button>
                            <button
                                onClick={() => setPage(p => Math.min(totalPages, p + 1))}
                                disabled={page >= totalPages}
                                className="p-2 rounded-lg hover:bg-[var(--primary-lighter)] disabled:opacity-50 disabled:cursor-not-allowed"
                            >
                                <ChevronRight size={16}/>
                            </button>
                        </div>
                    </div>
                )}
            </div>

            {isDrawerOpen && (
                <div className="fixed inset-0 z-50 flex justify-end bg-black/50 backdrop-blur-sm"
                     onClick={() => setIsDrawerOpen(false)}>
                    <div className="w-full max-w-2xl bg-[var(--surface-card)] shadow-2xl h-full flex flex-col"
                         onClick={e => e.stopPropagation()}>
                        <div className="p-4 md:p-6 border-b border-[var(--border-soft)] flex items-center justify-between">
                            <div>
                                <h2 className="text-lg md:text-xl font-bold text-[var(--text-primary)]">对话详情</h2>
                                {selectedConversation && (
                                    <p className="text-xs text-[var(--text-secondary)] mt-1 truncate max-w-[200px] md:max-w-[400px]">{selectedConversation.title}</p>
                                )}
                            </div>
                            <button onClick={() => setIsDrawerOpen(false)}
                                    className="p-2 hover:bg-[var(--primary-lighter)] rounded-full text-[var(--text-secondary)]">
                                <X size={24}/>
                            </button>
                        </div>

                        <div className="flex-1 overflow-y-auto">
                            {loadingMessages ? (
                                <div className="p-6 space-y-4 animate-pulse">
                                    <div className="h-32 bg-[var(--primary-lighter)] rounded-2xl"></div>
                                    <div className="h-48 bg-[var(--primary-lighter)] rounded-2xl"></div>
                                </div>
                            ) : selectedConversation ? (
                                <>
                                    <div className="p-6 bg-[var(--surface)] border-b border-[var(--border-soft)]">
                                        <div className="grid grid-cols-2 gap-4">
                                            <div className="space-y-1">
                                                <label
                                                    className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">模型</label>
                                                <p className="text-sm font-bold text-[var(--text-primary)]">{selectedConversation.model}</p>
                                            </div>
                                            <div className="space-y-1">
                                                <label
                                                    className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">消息数</label>
                                                <p className="text-sm font-bold text-[var(--text-primary)]">{selectedConversation.messageCount}</p>
                                            </div>
                                            <div className="space-y-1">
                                                <label className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">Token
                                                    用量</label>
                                                <p className="text-sm font-bold text-[var(--text-primary)]">{selectedConversation.totalTokens.toLocaleString()}</p>
                                            </div>
                                            <div className="space-y-1">
                                                <label
                                                    className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">总费用</label>
                                                <p className="text-sm font-bold text-green-600">{selectedConversation.totalCost.toFixed(4)}</p>
                                            </div>
                                            <div className="space-y-1">
                                                <label
                                                    className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">创建时间</label>
                                                <p className="text-sm text-[var(--text-primary)]">{formatTime(selectedConversation.createdAt)}</p>
                                            </div>
                                            <div className="space-y-1">
                                                <label
                                                    className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">更新时间</label>
                                                <p className="text-sm text-[var(--text-primary)]">{formatTime(selectedConversation.updatedAt)}</p>
                                            </div>
                                            {selectedConversation.systemPrompt && (
                                                <div className="col-span-2 space-y-1">
                                                    <label className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">System
                                                        Prompt</label>
                                                    <p className="text-sm text-[var(--text-primary)] whitespace-pre-wrap bg-[var(--surface-card)] p-3 rounded-lg border border-[var(--border-soft)] max-h-32 overflow-y-auto">
                                                        {selectedConversation.systemPrompt}
                                                    </p>
                                                </div>
                                            )}
                                        </div>
                                    </div>

                                    <div className="p-6 space-y-4">
                                        <div
                                            className="flex items-center gap-2 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-widest">
                                            <MessageSquare size={14}/>
                                            消息列表
                                        </div>
                                        <div className="space-y-3">
                                            {messages.map(msg => (
                                                <div
                                                    key={msg.id}
                                                    className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}
                                                >
                                                    <div
                                                        className={`max-w-[85%] ${msg.role === 'user' ? 'order-2' : 'order-1'}`}>
                                                        <div
                                                            className={`flex items-center gap-2 mb-1 ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                                                            {msg.role === 'user' ? (
                                                                <User size={12} className="text-blue-400"/>
                                                            ) : msg.role === 'assistant' ? (
                                                                <Bot size={12} className="text-emerald-400"/>
                                                            ) : (
                                                                <Hash size={12} className="text-[var(--text-secondary)]"/>
                                                            )}
                                                            <span
                                                                className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">
                                {msg.role === 'user' ? '用户' : msg.role === 'assistant' ? '助手' : '系统'}
                              </span>
                                                            <span
                                                                className="text-[10px] text-[var(--text-secondary)] opacity-60">{formatTime(msg.createdAt)}</span>
                                                        </div>
                                                        <div
                                                            className={`rounded-2xl px-4 py-3 text-sm leading-relaxed ${
                                                                msg.role === 'user'
                                                                    ? 'bg-blue-600 text-white rounded-br-sm'
                                                                    : msg.role === 'assistant'
                                                                        ? 'bg-[var(--surface)] border border-[var(--border-soft)] text-[var(--text-primary)] rounded-bl-sm'
                                                                        : 'bg-amber-500/10 text-amber-300 border border-amber-500/20'
                                                            }`}
                                                        >
                                                            <div
                                                                className="whitespace-pre-wrap break-words max-h-96 overflow-y-auto">{msg.content}</div>
                                                            <AttachmentRenderer attachments={parseAttachments(msg.attachments)}/>
                                                        </div>
                                                        {msg.role === 'assistant' && (msg.inputTokens > 0 || msg.outputTokens > 0 || msg.cost > 0) && (
                                                            <div
                                                                className="flex items-center gap-3 mt-1 text-[10px] text-[var(--text-secondary)]">
                                                                {(msg.inputTokens > 0 || msg.outputTokens > 0) && (
                                                                    <span className="flex items-center gap-1">
                                    <Hash size={10}/>
                                                                        {msg.inputTokens} / {msg.outputTokens} tokens
                                  </span>
                                                                )}
                                                                {msg.cost > 0 && (
                                                                    <span className="flex items-center gap-1">
                                    <DollarSign size={10}/>
                                                                        {Number(msg.cost || 0).toFixed(6)}
                                  </span>
                                                                )}
                                                                {msg.latencyMs > 0 && (
                                                                    <span className="flex items-center gap-1">
                                    <Clock size={10}/>
                                                                        {msg.latencyMs}ms
                                  </span>
                                                                )}
                                                            </div>
                                                        )}
                                                    </div>
                                                </div>
                                            ))}
                                            {messages.length === 0 && (
                                                <div className="text-center text-[var(--text-secondary)] py-8">暂无消息</div>
                                            )}
                                        </div>
                                    </div>
                                </>
                            ) : (
                                <div className="text-center text-[var(--text-secondary)] py-12">加载失败</div>
                            )}
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
};

export default ChatLogs;
