import React, { useState, useEffect, useRef, useMemo, useCallback } from 'react';
import {
  Send, Bot, Loader2, Square, Trash2,
  AlertCircle, User as UserIcon,
  ChevronDown, Bug,
  SlidersHorizontal, PanelLeft, FileJson, Plus,
  Paperclip, X, CheckCircle2, XCircle, Upload
} from 'lucide-react';
import {
  playgroundListModels, playgroundChatCompletions,
  playgroundGetConversationMessages, playgroundGetDebug,
  playgroundListConversations, playgroundUploadFile,
} from '../../services/api';
import { PlaygroundModelInfo, PlaygroundConversation, PlaygroundDebugDetail, PlaygroundMessage } from '../../types';
import { ChatMessage, ChatState, ContentPart, Attachment } from './types';
import ThinkingBlock from './ThinkingBlock';
import StatusBadge from './StatusBadge';
import HistoryPanel from './HistoryPanel';
import DebugPanel from './DebugPanel';
import ModelSelector from './ModelSelector';
import {
  parseJsonField, getFileIcon, parseStopSequences, extractAssistantText,
  formatFileSize, ACCEPTED_FILE_TYPES, MAX_FILE_SIZE,
} from './utils';

const ChatTab: React.FC<{ tokenId: string }> = ({ tokenId }) => {
  const [models, setModels] = useState<PlaygroundModelInfo[]>([]);
  const [selectedModel, setSelectedModel] = useState('');
  const [systemPrompt, setSystemPrompt] = useState('');
  const [temperature, setTemperature] = useState(0.7);
  const [maxTokens, setMaxTokens] = useState(200000);
  const [topP, setTopP] = useState(1);
  const [presencePenalty, setPresencePenalty] = useState(0);
  const [frequencyPenalty, setFrequencyPenalty] = useState(0);
  const [stop, setStop] = useState('');
  const [seed, setSeed] = useState('');
  const [userValue, setUserValue] = useState('');
  const [conversationId, setConversationId] = useState('');
  const [responseFormatText, setResponseFormatText] = useState('');
  const [toolsText, setToolsText] = useState('');
  const [toolChoiceText, setToolChoiceText] = useState('');
  const [showAdvancedDrawer, setShowAdvancedDrawer] = useState(false);
  const [showHistoryDrawer, setShowHistoryDrawer] = useState(false);
  const [showDebugDrawer, setShowDebugDrawer] = useState(false);
  const [showSystemPrompt, setShowSystemPrompt] = useState(false);
  const [showFullDebug, setShowFullDebug] = useState(false);
  const [pendingModel, setPendingModel] = useState<string | null>(null);
  const [stream, setStream] = useState(false);
  const [input, setInput] = useState('');
  const [chat, setChat] = useState<ChatState>({ messages: [], isStreaming: false, usage: null, latencyMs: null, statusText: '等待发送' });
  const [error, setError] = useState('');
  const [historyItems, setHistoryItems] = useState<PlaygroundConversation[]>([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [selectedConversationId, setSelectedConversationId] = useState<number | undefined>();
  const [currentConversationMeta, setCurrentConversationMeta] = useState<PlaygroundConversation | null>(null);
  const [debugDetail, setDebugDetail] = useState<PlaygroundDebugDetail | null>(null);
  const [lastPayload, setLastPayload] = useState<Record<string, any> | null>(null);
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [isDragging, setIsDragging] = useState(false);
  const dragCounterRef = useRef(0);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const abortRef = useRef<AbortController | null>(null);
  const streamFlushTimerRef = useRef<number | null>(null);
  const lastAutoScrollAtRef = useRef(0);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const attachmentsRef = useRef<Attachment[]>([]);

  const clearChatAttachments = useCallback(() => {
    setAttachments(prev => {
      prev.forEach(att => { if (att.preview) URL.revokeObjectURL(att.preview); });
      return [];
    });
  }, []);

  const selectedModelInfo = useMemo(() => models.find(model => model.id === selectedModel), [models, selectedModel]);
  const streamDisabled = selectedModelInfo?.supports_stream === false;
  const effectiveConversationId = selectedConversationId ? String(selectedConversationId) : conversationId.trim();
  const conversationModel = currentConversationMeta?.model;
  const hasConversationMessages = chat.messages.some(msg => msg.role === 'user' || msg.role === 'assistant');
  const modelChangedOnConversation = Boolean(selectedConversationId && conversationModel && selectedModel && conversationModel !== selectedModel && hasConversationMessages);

  const loadHistory = async () => {
    if (!tokenId) return;
    setHistoryLoading(true);
    try {
      const result = await playgroundListConversations(tokenId, { page: 1, page_size: 30 });
      setHistoryItems(result.items || []);
      setCurrentConversationMeta(prev => prev ? (result.items.find((item: PlaygroundConversation) => item.id === prev.id) || prev) : prev);
    } catch {
      setHistoryItems([]);
    } finally {
      setHistoryLoading(false);
    }
  };
  useEffect(() => {
    if (!tokenId) return;
    clearChatAttachments();
    playgroundListModels(tokenId)
      .then(m => {
        setModels(m);
        if (m.length > 0) {
          setSelectedModel(prev => (prev && m.some((item: PlaygroundModelInfo) => item.id === prev) ? prev : m[0].id));
        }
      })
      .catch(() => setModels([]));
    loadHistory();
  }, [tokenId, clearChatAttachments]);

  useEffect(() => {
    if (!selectedModelInfo) return;
    if (selectedModelInfo.supports_stream === false) { setStream(false); return; }
    setStream(Boolean(selectedModelInfo.default_stream));
  }, [selectedModelInfo?.id, selectedModelInfo?.supports_stream, selectedModelInfo?.default_stream]);

  useEffect(() => {
    const now = Date.now();
    const behavior: ScrollBehavior = now - lastAutoScrollAtRef.current < 150 ? 'auto' : 'smooth';
    messagesEndRef.current?.scrollIntoView({ behavior });
    lastAutoScrollAtRef.current = now;
  }, [chat.messages]);

  useEffect(() => { attachmentsRef.current = attachments; }, [attachments]);

  useEffect(() => () => {
    if (streamFlushTimerRef.current !== null) window.clearTimeout(streamFlushTimerRef.current);
    attachmentsRef.current.forEach(att => { if (att.preview) URL.revokeObjectURL(att.preview); });
  }, []);

  const applyConversationMessages = async (conversation: PlaygroundConversation) => {
    clearChatAttachments();
    setSelectedConversationId(conversation.id);
    setConversationId(String(conversation.id));
    setSelectedModel(conversation.model);
    setCurrentConversationMeta(conversation);
    setPendingModel(null);
    setShowHistoryDrawer(false);
    try {
      const result = await playgroundGetConversationMessages(tokenId, conversation.id);
      const loadedMessages: ChatMessage[] = result.items.map((msg: PlaygroundMessage) => ({
        role: msg.role, content: msg.content, reasoningContent: msg.reasoningContent,
        requestLogId: msg.requestLogId, finishReason: msg.finishReason, status: 'completed' as const,
      }));
      const fallbackRequestLogId = result.conversation.lastRequestLogId
        || [...result.items].reverse().find((msg: PlaygroundMessage) => typeof msg.requestLogId === 'number' && msg.requestLogId > 0)?.requestLogId;
      const debug = fallbackRequestLogId ? await playgroundGetDebug(tokenId, fallbackRequestLogId).catch(() => null) : null;
      setChat({ messages: loadedMessages, isStreaming: false, usage: null, latencyMs: null, statusText: '已载入历史会话' });
      setCurrentConversationMeta({ ...result.conversation, lastRequestLogId: fallbackRequestLogId });
      setDebugDetail(debug);
      setError('');
    } catch (err: any) {
      setError(err.message || '加载历史会话失败');
    }
  };
  const handleSend = async () => {
    const hasText = input.trim().length > 0;
    const readyAttachments = attachments.filter(a => a.uploaded && a.url);
    const hasAttachments = readyAttachments.length > 0;
    if ((!hasText && !hasAttachments) || chat.isStreaming || !selectedModel) return;
    if (attachments.some(a => a.uploading)) { setError('请等待文件上传完成'); return; }
    if (attachments.some(a => a.error)) { setError('有文件上传失败，请移除后重试'); return; }
    setError('');
    if (modelChangedOnConversation) { setError('当前模型与已加载会话模型不一致，请先确认切换策略。'); return; }

    let responseFormat: any, tools: any, toolChoice: any;
    try {
      responseFormat = parseJsonField(responseFormatText, 'response_format');
      tools = parseJsonField(toolsText, 'tools');
      toolChoice = parseJsonField(toolChoiceText, 'tool_choice');
    } catch (err: any) { setError(err.message || '高级参数格式错误'); return; }

    let userContent: string | ContentPart[];
    if (hasAttachments) {
      const parts: ContentPart[] = [];
      if (hasText) parts.push({ type: 'text', text: input.trim() });
      for (const att of readyAttachments) {
        if (att.contentType.startsWith('image/')) parts.push({ type: 'image_url', image_url: { url: att.url! } });
        else parts.push({ type: 'file_url', file_url: { url: att.url!, content_type: att.contentType } });
      }
      userContent = parts;
    } else {
      userContent = input.trim();
    }

    const userMsg: ChatMessage = { role: 'user', content: userContent, status: 'completed' };
    const hasConversation = Boolean(effectiveConversationId);
    const allMessages: ChatMessage[] = hasConversation
      ? [...chat.messages, userMsg]
      : [...chat.messages.filter(msg => msg.role !== 'system'), userMsg];
    const incrementalMessages = hasConversation ? [userMsg] : allMessages.map(m => ({ role: m.role, content: m.content }));
    const requestMessages = systemPrompt.trim() && !hasConversation
      ? [{ role: 'system', content: systemPrompt.trim() }, ...incrementalMessages]
      : incrementalMessages;

    const payload: Record<string, any> = {
      model: selectedModel, messages: requestMessages, temperature, max_tokens: maxTokens,
      stop: parseStopSequences(stop), stream, seed: seed.trim() ? Number(seed) : undefined,
      user: userValue.trim() || undefined, conversation_id: effectiveConversationId || undefined,
      response_format: responseFormat, tools, tool_choice: toolChoice,
    };
    if (topP !== 1) payload.top_p = topP;
    if (presencePenalty !== 0) payload.presence_penalty = presencePenalty;
    if (frequencyPenalty !== 0) payload.frequency_penalty = frequencyPenalty;
    setLastPayload(payload);
    setChat(prev => ({
      ...prev,
      messages: [...prev.messages.filter(msg => msg.role !== 'system'), userMsg, { role: 'assistant', content: '', status: 'streaming' }],
      isStreaming: stream, usage: null, latencyMs: null, statusText: stream ? '正在流式接收...' : '正在请求...',
    }));
    setInput('');

    const startTime = Date.now();
    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const res = await playgroundChatCompletions(tokenId, payload, controller.signal);
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.message || `请求失败 (${res.status})`);
      }

      let assistantContent = '', reasoningContent = '';
      let usage: ChatState['usage'] = null;
      let finalStatus: ChatMessage['status'] = 'completed';
      let debug: PlaygroundDebugDetail | null = null;
      let finishReason = '';
      if (stream) {
        if (!res.body) throw new Error('无响应流');
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';

        const flushStreamMessage = () => {
          if (streamFlushTimerRef.current !== null) return;
          streamFlushTimerRef.current = window.setTimeout(() => {
            streamFlushTimerRef.current = null;
            setChat(prev => {
              const msgs = [...prev.messages];
              msgs[msgs.length - 1] = { role: 'assistant', content: assistantContent, reasoningContent: reasoningContent || undefined, status: 'streaming', finishReason };
              return { ...prev, messages: msgs, statusText: '正在流式接收...' };
            });
          }, 80);
        };

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const events = buffer.split('\n\n');
          buffer = events.pop() || '';
          for (const eventBlock of events) {
            const lines = eventBlock.split('\n');
            let eventName = 'message';
            const dataLines: string[] = [];
            for (const line of lines) {
              if (line.startsWith('event: ')) eventName = line.slice(7).trim();
              if (line.startsWith('data: ')) dataLines.push(line.slice(6));
            }
            const data = dataLines.join('\n');
            if (!data || data === '[DONE]') continue;
            if (eventName === 'prism-debug') { debug = JSON.parse(data) as PlaygroundDebugDetail; setDebugDetail(debug); continue; }
            try {
              const parsed = JSON.parse(data);
              const delta = parsed.choices?.[0]?.delta;
              if (delta?.content) assistantContent += delta.content;
              if (delta?.reasoning_content) reasoningContent += delta.reasoning_content;
              if (parsed.choices?.[0]?.finish_reason) finishReason = parsed.choices[0].finish_reason;
              if (parsed.usage) usage = { input: parsed.usage.prompt_tokens || 0, output: parsed.usage.completion_tokens || 0, total: parsed.usage.total_tokens || 0 };
              flushStreamMessage();
            } catch { /* ignore chunk parse errors */ }
          }
        }
        if (streamFlushTimerRef.current !== null) { window.clearTimeout(streamFlushTimerRef.current); streamFlushTimerRef.current = null; }
      } else {
        const parsed = await res.json();
        assistantContent = extractAssistantText(parsed.choices?.[0]?.message?.content);
        reasoningContent = parsed.choices?.[0]?.message?.reasoning_content || '';
        finishReason = parsed.choices?.[0]?.finish_reason || parsed.debug?.finishReason || '';
        if (parsed.usage) usage = { input: parsed.usage.prompt_tokens || 0, output: parsed.usage.completion_tokens || 0, total: parsed.usage.total_tokens || 0 };
        debug = parsed.debug || null;
        setDebugDetail(debug);
        if (parsed.conversation_id) { setConversationId(parsed.conversation_id); setSelectedConversationId(Number(parsed.conversation_id)); }
      }

      setChat(prev => {
        const msgs = [...prev.messages];
        msgs[msgs.length - 1] = {
          role: 'assistant', content: assistantContent, reasoningContent: reasoningContent || undefined,
          status: finalStatus, finishReason, requestLogId: debug?.requestLogId,
        };
        return { ...prev, messages: msgs, isStreaming: false, usage, latencyMs: Date.now() - startTime, statusText: finalStatus === 'completed' ? '已完成' : '请求结束' };
      });

      if (debug?.conversationId) {
        setConversationId(String(debug.conversationId));
        setSelectedConversationId(debug.conversationId);
        setHistoryItems(prev => {
          const existing = prev.find(item => item.id === debug!.conversationId);
          const updatedItem: PlaygroundConversation = existing ? {
            ...existing, model: selectedModel, systemPrompt, lastRequestLogId: debug!.requestLogId,
            lastStatus: debug!.status || 'completed', updatedAt: new Date().toISOString(),
            messageCount: Math.max(existing.messageCount, chat.messages.length + 2),
          } : {
            id: debug!.conversationId, userId: 0, tokenId: Number(tokenId),
            title: input.trim().slice(0, 20) || `会话 #${debug!.conversationId}`,
            model: selectedModel, systemPrompt, lastRequestLogId: debug!.requestLogId,
            lastStatus: debug!.status || 'completed', totalTokens: usage?.total || 0,
            messageCount: chat.messages.length + 2, totalCost: 0, status: 1,
            createdAt: new Date().toISOString(), updatedAt: new Date().toISOString(),
          };
          setCurrentConversationMeta(updatedItem);
          return existing ? prev.map(item => item.id === debug!.conversationId ? updatedItem : item) : [updatedItem, ...prev];
        });
      } else {
        void loadHistory();
      }
    } catch (err: any) {
      const aborted = err.name === 'AbortError';
      setError(aborted ? '请求已中断' : (err.message || '请求失败'));
      setChat(prev => {
        const msgs = [...prev.messages];
        if (msgs.length > 0) msgs[msgs.length - 1] = { ...msgs[msgs.length - 1], status: aborted ? 'aborted' : 'failed' };
        return { ...prev, messages: msgs, isStreaming: false, statusText: aborted ? '已手动中断' : '请求失败' };
      });
    } finally {
      abortRef.current = null;
    }
  };
  const handleStop = () => { abortRef.current?.abort(); };

  const resetConversationState = (statusText = '等待发送') => {
    setChat({ messages: [], isStreaming: false, usage: null, latencyMs: null, statusText });
    setError(''); setDebugDetail(null); setConversationId(''); setSelectedConversationId(undefined);
    setCurrentConversationMeta(null); setPendingModel(null); setInput('');
    clearChatAttachments(); setShowFullDebug(false);
  };

  const handleClear = () => resetConversationState('等待发送');
  const handleCreateNewConversation = () => { resetConversationState('已开启新会话'); setShowHistoryDrawer(false); };

  const handleModelSelect = (nextModel: string) => {
    setSelectedModel(nextModel);
    if (selectedConversationId && hasConversationMessages && conversationModel && nextModel !== conversationModel) {
      setPendingModel(nextModel);
      setError('你已切换到新模型。请新建会话，或清空当前会话后再发送。');
      return;
    }
    setPendingModel(null);
  };

  const startFreshConversationWithModel = () => {
    if (!pendingModel) return;
    setSelectedModel(pendingModel);
    resetConversationState('已切换模型，请开始新会话');
    setPendingModel(null);
  };

  const addFiles = useCallback((files: FileList | File[]) => {
    const newAttachments: Attachment[] = [];
    for (const file of Array.from(files)) {
      if (file.size > MAX_FILE_SIZE) { setError(`文件 ${file.name} 超过 20MB 限制`); continue; }
      if (!ACCEPTED_FILE_TYPES.split(',').includes(file.type)) { setError(`不支持的文件类型: ${file.type || file.name}`); continue; }
      const att: Attachment = {
        id: crypto.randomUUID(), file,
        preview: file.type.startsWith('image/') ? URL.createObjectURL(file) : undefined,
        uploading: true, uploaded: false, contentType: file.type,
      };
      newAttachments.push(att);
    }
    if (newAttachments.length === 0) return;
    setAttachments(prev => [...prev, ...newAttachments]);
    for (const att of newAttachments) {
      playgroundUploadFile(tokenId, att.file)
        .then(result => setAttachments(prev => prev.map(a => a.id === att.id ? { ...a, uploading: false, uploaded: true, url: result.url, thUrl: result.thUrl } : a)))
        .catch(err => setAttachments(prev => prev.map(a => a.id === att.id ? { ...a, uploading: false, error: err.message || '上传失败' } : a)));
    }
  }, [tokenId]);

  const removeAttachment = useCallback((id: string) => {
    setAttachments(prev => {
      const att = prev.find(a => a.id === id);
      if (att?.preview) URL.revokeObjectURL(att.preview);
      return prev.filter(a => a.id !== id);
    });
  }, []);

  const handlePaste = useCallback((e: React.ClipboardEvent) => {
    const files = e.clipboardData?.files;
    if (files && files.length > 0) { e.preventDefault(); addFiles(files); }
  }, [addFiles]);

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault(); e.stopPropagation();
    dragCounterRef.current = 0; setIsDragging(false);
    const files = e.dataTransfer?.files;
    if (files && files.length > 0) addFiles(files);
  }, [addFiles]);

  const handleDragOver = useCallback((e: React.DragEvent) => { e.preventDefault(); e.stopPropagation(); }, []);

  const handleDragEnter = useCallback((e: React.DragEvent) => {
    e.preventDefault(); e.stopPropagation();
    dragCounterRef.current++;
    if (e.dataTransfer?.types?.includes('Files')) setIsDragging(true);
  }, []);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault(); e.stopPropagation();
    dragCounterRef.current--;
    if (dragCounterRef.current === 0) setIsDragging(false);
  }, []);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleSend(); }
  };
  return (
    <div className="relative h-[calc(100vh-220px)] overflow-hidden">
      {(showHistoryDrawer || showAdvancedDrawer || showDebugDrawer) && (
        <div className="absolute inset-0 z-20 bg-black/20" onClick={() => { setShowHistoryDrawer(false); setShowAdvancedDrawer(false); setShowDebugDrawer(false); }} />
      )}
      {showHistoryDrawer && (
        <div className="absolute inset-y-0 left-0 z-30">
          <HistoryPanel items={historyItems} selectedConversationId={selectedConversationId} currentModel={selectedModel}
            onSelect={applyConversationMessages} onCreateNew={handleCreateNewConversation} loading={historyLoading} />
        </div>
      )}
      {showAdvancedDrawer && (
        <div className="absolute inset-y-0 right-0 z-30 w-[24rem] bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] overflow-y-auto p-4 space-y-4 shadow-2xl">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 text-sm font-semibold text-[var(--text-primary)]"><SlidersHorizontal size={16} /> 参数设置</div>
            <button type="button" onClick={() => setShowAdvancedDrawer(false)} className="text-xs text-[var(--text-secondary)] hover:text-[var(--text-secondary)]">关闭</button>
          </div>
          <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Temperature: {temperature}</label><input type="range" min="0" max="2" step="0.1" value={temperature} onChange={e => setTemperature(Number(e.target.value))} className="w-full accent-indigo-600" /></div>
          <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Max Tokens</label><input type="number" min={1} max={200000} value={maxTokens} onChange={e => setMaxTokens(Number(e.target.value))} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
          <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Top P</label><input type="number" min={0} max={1} step="0.1" value={topP} onChange={e => setTopP(Number(e.target.value))} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
          <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Presence Penalty</label><input type="number" min={-2} max={2} step="0.1" value={presencePenalty} onChange={e => setPresencePenalty(Number(e.target.value))} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
          <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Frequency Penalty</label><input type="number" min={-2} max={2} step="0.1" value={frequencyPenalty} onChange={e => setFrequencyPenalty(Number(e.target.value))} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
          <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Stop（每行一个）</label><textarea value={stop} onChange={e => setStop(e.target.value)} rows={3} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm resize-none focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
          <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Seed</label><input type="number" value={seed} onChange={e => setSeed(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
          <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">User</label><input type="text" value={userValue} onChange={e => setUserValue(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
          <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Conversation ID</label><input type="text" value={effectiveConversationId} onChange={e => setConversationId(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
          <div className="border border-[var(--border-soft)] rounded-xl overflow-hidden">
            <div className="px-3 py-2 bg-[var(--surface)] text-xs font-semibold text-[var(--text-secondary)]">高级参数（JSON）</div>
            <div className="p-3 space-y-3">
              <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">response_format</label><textarea value={responseFormatText} onChange={e => setResponseFormatText(e.target.value)} rows={3} placeholder='{"type":"json_object"}' className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-xs font-mono resize-none focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
              <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">tools</label><textarea value={toolsText} onChange={e => setToolsText(e.target.value)} rows={4} placeholder="[...]" className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-xs font-mono resize-none focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
              <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">tool_choice</label><textarea value={toolChoiceText} onChange={e => setToolChoiceText(e.target.value)} rows={2} placeholder='"auto"' className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-xs font-mono resize-none focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
            </div>
          </div>
        </div>
      )}
      {showDebugDrawer && (
        <>
          <div className="absolute inset-0 z-20 bg-black/20 xl:hidden" onClick={() => setShowDebugDrawer(false)} />
          <div className="absolute inset-y-0 right-0 z-30 w-full max-w-[28rem] p-2 xl:hidden">
            <div className="relative h-full">
              <button type="button" onClick={() => setShowDebugDrawer(false)} className="absolute right-3 top-3 z-10 px-2 py-1 rounded-md bg-[var(--surface-card)]/90 border border-[var(--border-soft)] text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)]">关闭</button>
              <DebugPanel debugDetail={debugDetail} lastPayload={lastPayload} compact showAllDetails={showFullDebug} onExpandFull={() => setShowFullDebug(true)} currentConversationMeta={currentConversationMeta} />
            </div>
          </div>
        </>
      )}
      <div className="h-full flex gap-4">
        <div className="flex-1 flex flex-col bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] overflow-hidden min-w-0">
          <div className="px-4 py-2 border-b border-[var(--border-soft)] flex items-center justify-between gap-3 flex-wrap">
            <div className="flex items-center gap-2 min-w-0">
              <ModelSelector options={models.map(m => ({ id: m.id, provider: m.owned_by }))} value={selectedModel} onChange={handleModelSelect} />
            </div>
            <div className="flex items-center gap-2 flex-wrap">
              <button type="button" onClick={() => setShowHistoryDrawer(prev => !prev)} className="inline-flex items-center gap-1.5 px-2 py-1 rounded-lg border border-[var(--border-soft)] text-[11px] text-[var(--text-secondary)] hover:bg-[var(--surface)]"><Plus size={12} /> 历史</button>
              <button type="button" onClick={() => setShowSystemPrompt(prev => !prev)} className="inline-flex items-center gap-1.5 px-2 py-1 rounded-lg border border-[var(--border-soft)] text-[11px] text-[var(--text-secondary)] hover:bg-[var(--surface)]">System</button>
              <button type="button" onClick={() => setShowAdvancedDrawer(prev => !prev)} className="inline-flex items-center gap-1.5 px-2 py-1 rounded-lg border border-[var(--border-soft)] text-[11px] text-[var(--text-secondary)] hover:bg-[var(--surface)]"><SlidersHorizontal size={12} /> 参数</button>
              <button type="button" onClick={() => { setShowDebugDrawer(prev => !prev); setShowFullDebug(false); }} className="inline-flex items-center gap-1.5 px-2 py-1 rounded-lg border border-[var(--border-soft)] text-[11px] text-[var(--text-secondary)] hover:bg-[var(--surface)] xl:hidden"><Bug size={12} /> 调试</button>
              <button type="button" onClick={handleClear} className="inline-flex items-center gap-1.5 px-2 py-1 rounded-lg border border-[var(--border-soft)] text-[11px] text-[var(--text-secondary)] hover:bg-[var(--surface)]"><Trash2 size={12} /> 清空</button>
            </div>
          </div>

          {pendingModel && (
            <div className="mx-4 mt-2 px-3 py-2 bg-amber-50 border border-amber-200 text-amber-700 text-sm rounded-lg flex items-center justify-between gap-2">
              <span>已切换到 {pendingModel}，当前会话使用 {conversationModel}</span>
              <div className="flex gap-2">
                <button type="button" onClick={startFreshConversationWithModel} className="px-2 py-1 text-xs bg-amber-100 hover:bg-amber-200 rounded-lg">新建会话</button>
                <button type="button" onClick={() => { setSelectedModel(conversationModel!); setPendingModel(null); setError(''); }} className="px-2 py-1 text-xs bg-amber-100 hover:bg-amber-200 rounded-lg">恢复原模型</button>
              </div>
            </div>
          )}

          <div className="px-4 py-1 border-b border-[var(--border-soft)] flex items-center gap-3 flex-wrap">
            <label className="flex items-center gap-2 cursor-pointer select-none">
              <span className="text-xs text-[var(--text-secondary)]">Stream</span>
              <button type="button" aria-label="切换 Stream" disabled={streamDisabled} onClick={() => !streamDisabled && setStream(prev => !prev)}
                className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${streamDisabled ? 'bg-gray-200 cursor-not-allowed' : stream ? 'bg-[var(--primary)]' : 'bg-gray-300'}`}>
                <span className={`inline-block h-5 w-5 transform rounded-full bg-[var(--surface-card)] shadow-sm transition-transform ${stream ? 'translate-x-5' : 'translate-x-1'}`} />
              </button>
            </label>
            <div className={`rounded-lg border px-3 py-1 text-[11px] ${streamDisabled ? 'border-amber-200 bg-amber-50 text-amber-700' : 'border-[var(--border-soft)] bg-[var(--surface)] text-[var(--text-secondary)]'}`}>
              {streamDisabled ? '当前模型渠道声明为不支持流式，已自动关闭 stream。' : `当前模型默认 stream: ${selectedModelInfo?.default_stream ? '开启' : '关闭'}`}
            </div>
          </div>

          {(showSystemPrompt || systemPrompt.trim()) && (
            <div className="px-4 py-1.5 border-b border-[var(--border-soft)]">
              <label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">System Prompt</label>
              <textarea value={systemPrompt} onChange={e => setSystemPrompt(e.target.value)} rows={2} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm resize-none focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
            </div>
          )}

          <div className="px-4 py-1 border-b border-[var(--border-soft)] text-[11px] text-[var(--text-secondary)] flex items-center gap-3 flex-wrap">
            <span>{chat.statusText}</span>
            {conversationModel ? <span>会话模型 {conversationModel}</span> : null}
            {chat.latencyMs ? <span>耗时 {(chat.latencyMs / 1000).toFixed(2)}s</span> : null}
            {chat.usage ? <span>Tokens {chat.usage.input}/{chat.usage.output}/{chat.usage.total || (chat.usage.input + chat.usage.output)}</span> : null}
            {effectiveConversationId ? <span>会话 #{effectiveConversationId}</span> : null}
          </div>
          <div className="flex-1 overflow-y-auto p-4 space-y-4 min-h-0">
            {chat.messages.length === 0 && (
              <div className="flex flex-col items-center justify-center h-full text-gray-300">
                <Bot size={56} strokeWidth={1} />
                <p className="mt-3 text-sm">选择模型，开始对话</p>
              </div>
            )}
            {chat.messages.map((msg, i) => (
              <div key={i} className={`flex gap-3 ${msg.role === 'user' ? 'justify-end' : ''}`}>
                {msg.role === 'assistant' && (
                  <div className="w-8 h-8 rounded-lg bg-indigo-100 flex items-center justify-center flex-shrink-0">
                    <Bot size={16} className="text-[var(--primary)]" />
                  </div>
                )}
                <div className={`max-w-[85%] rounded-2xl text-sm ${msg.role === 'user' ? 'px-4 py-3 bg-[var(--primary)] text-white rounded-br-md' : 'bg-[var(--primary-lighter)] text-gray-800 rounded-bl-md'}`}>
                  {msg.role === 'assistant' && msg.reasoningContent && <ThinkingBlock content={msg.reasoningContent} />}
                  <div className={msg.role === 'assistant' ? 'px-4 py-3 whitespace-pre-wrap' : 'whitespace-pre-wrap'}>
                    {typeof msg.content === 'string' ? (
                      msg.content || (msg.status === 'streaming' ? <Loader2 size={16} className="animate-spin text-[var(--text-secondary)]" /> : null)
                    ) : (
                      <div className="space-y-2">
                        {msg.content.map((part, pi) => {
                          if (part.type === 'text' && part.text) return <span key={pi} className="whitespace-pre-wrap">{part.text}</span>;
                          if (part.type === 'image_url' && part.image_url) return (
                            <a key={pi} href={part.image_url.url} target="_blank" rel="noreferrer" className="block">
                              <img src={part.image_url.url} alt="" className={`max-w-[280px] max-h-[200px] rounded-xl object-cover shadow-sm hover:shadow-md transition-shadow ${msg.role === 'user' ? 'border border-white/20' : 'border border-[var(--border-soft)]'}`} />
                            </a>
                          );
                          if (part.type === 'file_url' && part.file_url) {
                            const fileName = decodeURIComponent(part.file_url.url.split('/').pop() || '文件');
                            const ct = part.file_url.content_type || '';
                            return (
                              <a key={pi} href={part.file_url.url} target="_blank" rel="noreferrer"
                                className={`inline-flex items-center gap-2 px-3 py-2 rounded-xl text-xs transition-colors ${msg.role === 'user' ? 'bg-[var(--surface-card)]/10 hover:bg-[var(--surface-card)]/20 border border-white/10' : 'bg-[var(--surface-card)] hover:bg-[var(--surface)] border border-[var(--border-soft)] shadow-sm'}`}>
                                <span className="text-base">{getFileIcon(ct)}</span>
                                <span className="truncate max-w-[160px] font-medium">{fileName}</span>
                              </a>
                            );
                          }
                          return null;
                        })}
                      </div>
                    )}
                  </div>
                  {msg.role === 'assistant' && (
                    <div className="px-4 pb-3 flex items-center gap-3 text-xs text-[var(--text-secondary)] flex-wrap">
                      <StatusBadge status={msg.status || 'completed'} />
                      {msg.finishReason ? <span>finish: {msg.finishReason}</span> : null}
                      {msg.requestLogId ? <span>log #{msg.requestLogId}</span> : null}
                    </div>
                  )}
                </div>
                {msg.role === 'user' && (
                  <div className="w-8 h-8 rounded-lg bg-gray-200 flex items-center justify-center flex-shrink-0">
                    <UserIcon size={16} className="text-[var(--text-secondary)]" />
                  </div>
                )}
              </div>
            ))}
            <div ref={messagesEndRef} />
          </div>

          {error && (
            <div className="mx-4 mb-2 px-3 py-2 bg-red-50 text-red-600 text-sm rounded-lg flex items-center gap-2">
              <AlertCircle size={14} /> {error}
            </div>
          )}

          <div className="border-t border-[var(--border-soft)] p-4 relative" onDrop={handleDrop} onDragOver={handleDragOver} onDragEnter={handleDragEnter} onDragLeave={handleDragLeave}>
            <input ref={fileInputRef} type="file" accept={ACCEPTED_FILE_TYPES} multiple className="hidden" onChange={e => { if (e.target.files) addFiles(e.target.files); e.target.value = ''; }} />
            {isDragging && (
              <div className="absolute inset-0 z-10 bg-[var(--primary-lighter)]/80 backdrop-blur-sm border-2 border-dashed border-indigo-400 rounded-xl flex items-center justify-center pointer-events-none">
                <div className="flex flex-col items-center gap-2 text-[var(--primary)]"><Upload size={32} className="animate-bounce" /><span className="text-sm font-medium">松开以添加文件</span></div>
              </div>
            )}
            <div className={`border rounded-2xl transition-all ${isDragging ? 'border-indigo-400 ring-2 ring-indigo-200' : 'border-[var(--border-soft)] focus-within:border-indigo-400 focus-within:ring-2 focus-within:ring-indigo-100'}`}>
              {attachments.length > 0 && (
                <div className="px-3 pt-3 pb-2 flex flex-wrap gap-2 border-b border-[var(--border-soft)]">
                  {attachments.map(att => (
                    <div key={att.id} className="relative group animate-in fade-in zoom-in-95 duration-200">
                      {att.preview ? (
                        <div className="relative w-16 h-16 rounded-xl overflow-hidden border border-[var(--border-soft)] bg-[var(--surface)]">
                          <img src={att.preview} alt="" className="w-full h-full object-cover" />
                          {att.uploading && <div className="absolute inset-0 bg-black/40 flex items-center justify-center"><Loader2 size={16} className="animate-spin text-white" /></div>}
                          {att.error && <div className="absolute inset-0 bg-red-500/60 flex items-center justify-center"><XCircle size={16} className="text-white" /></div>}
                          {att.uploaded && <div className="absolute bottom-0.5 right-0.5 w-4 h-4 bg-emerald-500 rounded-full flex items-center justify-center"><CheckCircle2 size={10} className="text-white" /></div>}
                          <button onClick={() => removeAttachment(att.id)} className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-all shadow-sm"><X size={10} /></button>
                        </div>
                      ) : (
                        <div className="relative flex items-center gap-2.5 pl-2.5 pr-7 py-2 rounded-xl border border-[var(--border-soft)] bg-[var(--surface)] hover:bg-[var(--primary-lighter)] transition-colors max-w-[200px]">
                          <div className="w-8 h-8 rounded-lg bg-[var(--primary-lighter)] flex items-center justify-center flex-shrink-0 text-base">{getFileIcon(att.contentType)}</div>
                          <div className="min-w-0 flex-1">
                            <div className="text-xs font-medium text-[var(--text-primary)] truncate">{att.file.name}</div>
                            <div className="text-[10px] text-[var(--text-secondary)] mt-0.5 flex items-center gap-1">
                              {att.uploading && <><Loader2 size={8} className="animate-spin text-indigo-500" /><span className="text-indigo-500">上传中</span></>}
                              {att.uploaded && <><CheckCircle2 size={8} className="text-emerald-500" /><span>{formatFileSize(att.file.size)}</span></>}
                              {att.error && <><XCircle size={8} className="text-red-500" /><span className="text-red-500 truncate">{att.error}</span></>}
                            </div>
                          </div>
                          <button onClick={() => removeAttachment(att.id)} className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-all shadow-sm"><X size={10} /></button>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
              <div className="flex items-end gap-1.5 p-2">
                <button onClick={() => fileInputRef.current?.click()} disabled={chat.isStreaming} className="p-2 text-[var(--text-secondary)] hover:text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg disabled:opacity-40 transition-all flex-shrink-0 mb-0.5" title="添加附件"><Paperclip size={18} /></button>
                <textarea value={input} onChange={e => setInput(e.target.value)} onKeyDown={handleKeyDown} onPaste={handlePaste} placeholder="输入消息... (Enter 发送, Shift+Enter 换行, 可粘贴/拖拽文件)" rows={2} className="flex-1 px-2 py-2.5 text-sm resize-none focus:outline-none bg-transparent max-h-32" disabled={chat.isStreaming} />
                {chat.isStreaming ? (
                  <button onClick={handleStop} className="px-4 py-2.5 bg-red-500 text-white rounded-xl hover:bg-red-600 transition-colors flex-shrink-0"><Square size={18} /></button>
                ) : (
                  <button onClick={handleSend} disabled={(!input.trim() && attachments.filter(a => a.uploaded).length === 0) || !selectedModel} className="px-4 py-2.5 bg-[var(--primary)] text-white rounded-xl hover:opacity-90 disabled:opacity-30 disabled:cursor-not-allowed transition-colors flex-shrink-0 flex items-center gap-1.5">
                    <Send size={16} /><span className="text-sm font-medium">发送</span>
                  </button>
                )}
              </div>
            </div>
          </div>
        </div>

        <div className="hidden xl:flex xl:w-[24rem] xl:flex-shrink-0 xl:self-stretch min-w-0">
          <DebugPanel debugDetail={debugDetail} lastPayload={lastPayload} compact onExpandFull={() => { setShowFullDebug(true); setShowDebugDrawer(true); }} currentConversationMeta={currentConversationMeta} />
        </div>
      </div>
    </div>
  );
};

export default ChatTab;
