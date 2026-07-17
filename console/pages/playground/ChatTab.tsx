import React, { useState, useEffect, useRef, useMemo, useCallback } from 'react';
import {
  Send, Bot, Loader2, Square, Trash2,
  AlertCircle, User as UserIcon,
  Bug,
  SlidersHorizontal, PanelLeft, FileJson, Plus,
  Paperclip, X, CheckCircle2, XCircle, Upload, MoreHorizontal, Wrench
} from 'lucide-react';
import {
  playgroundListModels, playgroundChatCompletions, playgroundResponses, playgroundAnthropicMessages,
  playgroundGetConversationMessages, playgroundGetDebug,
  playgroundListConversations, playgroundUploadFile,
} from '../../services/api';
import { PlaygroundModelInfo, PlaygroundConversation, PlaygroundDebugDetail, PlaygroundMessage } from '../../types';
import { ChatMessage, ChatState, ContentPart, Attachment, PlaygroundProtocol } from './types';
import ThinkingBlock from './ThinkingBlock';
import StatusBadge from './StatusBadge';
import HistoryPanel from './HistoryPanel';
import DebugPanel from './DebugPanel';
import ModelSelector from './ModelSelector';
import ThinkingSelect from './ThinkingSelect';
import {
  parseJsonField, getFileIcon, parseStopSequences,
  formatFileSize, ACCEPTED_FILE_TYPES, MAX_FILE_SIZE, getClipboardFiles,
} from './utils';
import {
  PLAYGROUND_PROTOCOLS, buildProtocolPayload, consumeProtocolStreamEvent,
  createProtocolStreamState, parseProtocolResponse,
} from './protocol';

const ChatTab: React.FC<{ tokenId: string }> = ({ tokenId }) => {
  const [models, setModels] = useState<PlaygroundModelInfo[]>([]);
  const [selectedModel, setSelectedModel] = useState('');
  const [protocol, setProtocol] = useState<PlaygroundProtocol>('chat');
  const [thinkingLevel, setThinkingLevel] = useState('');
  const [systemPrompt, setSystemPrompt] = useState('');
  const [temperature, setTemperature] = useState(0.7);
  const [maxTokens, setMaxTokens] = useState(4096);
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
  const [showMobileMenu, setShowMobileMenu] = useState(false);
  const [pendingModel, setPendingModel] = useState<string | null>(null);
  const [stream, setStream] = useState(false);
  const [input, setInput] = useState('');
  const [chat, setChat] = useState<ChatState>({ messages: [], isStreaming: false, usage: null, latencyMs: null, statusText: '等待发送' });
  const [error, setError] = useState('');
  const [historyItems, setHistoryItems] = useState<PlaygroundConversation[]>([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [loadingConversationId, setLoadingConversationId] = useState<number | undefined>();
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
  const conversationLoadRef = useRef(0);
  const historyLoadRef = useRef(0);
  const modelLoadRef = useRef(0);
  const activeTokenRef = useRef(tokenId);
  activeTokenRef.current = tokenId;

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
  const protocolInfo = PLAYGROUND_PROTOCOLS.find(item => item.value === protocol)!;

  const loadHistory = async () => {
    if (!tokenId) return;
    const requestNo = ++historyLoadRef.current;
    const requestedToken = tokenId;
    setHistoryLoading(true);
    try {
      const pageSize = 100;
      const result = await playgroundListConversations(requestedToken, { page: 1, page_size: pageSize });
      const allItems = [...(result.items || [])];
      const pageCount = Math.ceil(result.total / pageSize);
      for (let currentPage = 2; currentPage <= pageCount; currentPage += 1) {
        if (requestNo !== historyLoadRef.current || requestedToken !== activeTokenRef.current) return;
        const next = await playgroundListConversations(requestedToken, { page: currentPage, page_size: pageSize });
        allItems.push(...(next.items || []));
      }
      if (requestNo !== historyLoadRef.current || requestedToken !== activeTokenRef.current) return;
      setHistoryItems(allItems);
      setCurrentConversationMeta(prev => prev ? (allItems.find((item: PlaygroundConversation) => item.id === prev.id) || prev) : prev);
    } catch {
      if (requestNo === historyLoadRef.current && requestedToken === activeTokenRef.current) setHistoryItems([]);
    } finally {
      if (requestNo === historyLoadRef.current && requestedToken === activeTokenRef.current) setHistoryLoading(false);
    }
  };
  useEffect(() => {
    if (!tokenId) return;
    conversationLoadRef.current += 1;
    historyLoadRef.current += 1;
    modelLoadRef.current += 1;
    setLoadingConversationId(undefined);
    clearChatAttachments();
    const requestNo = modelLoadRef.current;
    const requestedToken = tokenId;
    playgroundListModels(tokenId)
      .then(m => {
        if (requestNo !== modelLoadRef.current || requestedToken !== activeTokenRef.current) return;
        setModels(m);
        if (m.length > 0) {
          setSelectedModel(prev => (prev && m.some((item: PlaygroundModelInfo) => item.id === prev) ? prev : m[0].id));
        }
      })
      .catch(() => {
        if (requestNo === modelLoadRef.current && requestedToken === activeTokenRef.current) setModels([]);
      });
    loadHistory();
  }, [tokenId, clearChatAttachments]);

  useEffect(() => {
    if (!selectedModelInfo) return;
    if (selectedModelInfo.supports_stream === false) { setStream(false); return; }
    setStream(Boolean(selectedModelInfo.default_stream));
  }, [selectedModelInfo?.id, selectedModelInfo?.supports_stream, selectedModelInfo?.default_stream]);

  // 切换模型时,思考档位重置为该模型默认
  useEffect(() => {
    setThinkingLevel(selectedModelInfo?.thinking?.default || '');
  }, [selectedModelInfo?.id]);

  useEffect(() => {
    const limit = selectedModelInfo?.max_tokens || 0;
    if (limit > 0) setMaxTokens(current => Math.min(current, limit));
  }, [selectedModelInfo?.id, selectedModelInfo?.max_tokens]);

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
    // token 或选择变化会使旧加载失效；双重检查避免历史响应覆盖另一个会话。
    const requestNo = ++conversationLoadRef.current;
    const requestedToken = tokenId;
    setLoadingConversationId(conversation.id);
    setError('');
    setShowHistoryDrawer(false);
    try {
      const pageSize = 200;
      const result = await playgroundGetConversationMessages(requestedToken, conversation.id, 1, pageSize);
      const allItems = [...result.items];
      const pageCount = Math.ceil(result.total / pageSize);
      for (let currentPage = 2; currentPage <= pageCount; currentPage += 1) {
        if (requestNo !== conversationLoadRef.current || requestedToken !== activeTokenRef.current) return;
        const next = await playgroundGetConversationMessages(requestedToken, conversation.id, currentPage, pageSize);
        allItems.push(...next.items);
      }
      if (requestNo !== conversationLoadRef.current || requestedToken !== activeTokenRef.current) return;
      const loadedMessages: ChatMessage[] = allItems.map((msg: PlaygroundMessage) => ({
        role: msg.role, content: msg.content, reasoningContent: msg.reasoningContent,
        requestLogId: msg.requestLogId, finishReason: msg.finishReason, status: 'completed' as const,
      }));
      const fallbackRequestLogId = result.conversation.lastRequestLogId
        || [...allItems].reverse().find((msg: PlaygroundMessage) => typeof msg.requestLogId === 'number' && msg.requestLogId > 0)?.requestLogId;
      const debug = fallbackRequestLogId ? await playgroundGetDebug(requestedToken, fallbackRequestLogId).catch(() => null) : null;
      if (requestNo !== conversationLoadRef.current || requestedToken !== activeTokenRef.current) return;
      clearChatAttachments();
      setSelectedConversationId(conversation.id);
      setConversationId(String(conversation.id));
      setSelectedModel(conversation.model);
      setPendingModel(null);
      setChat({ messages: loadedMessages, isStreaming: false, usage: null, latencyMs: null, statusText: '已载入历史会话' });
      setCurrentConversationMeta({ ...result.conversation, lastRequestLogId: fallbackRequestLogId });
      setDebugDetail(debug);
      setError('');
    } catch (err: any) {
      if (requestNo !== conversationLoadRef.current || requestedToken !== activeTokenRef.current) return;
      setError(err.message || '加载历史会话失败');
    } finally {
      if (requestNo === conversationLoadRef.current && requestedToken === activeTokenRef.current) {
        setLoadingConversationId(undefined);
      }
    }
  };
  const handleSend = async () => {
    const hasText = input.trim().length > 0;
    const readyAttachments = attachments.filter(a => a.uploaded && a.url);
    const hasAttachments = readyAttachments.length > 0;
    if ((!hasText && !hasAttachments) || chat.isStreaming || loadingConversationId !== undefined || !selectedModel) return;
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

    conversationLoadRef.current += 1;
    setLoadingConversationId(undefined);

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
    // 目前仅 Chat 暴露 Prism conversation_id 增量续话；Responses/Messages 仍发送完整可见历史。
    const hasConversation = protocol === 'chat' && Boolean(effectiveConversationId);
    const allMessages: ChatMessage[] = [
      ...chat.messages.filter(msg => msg.role !== 'system' && msg.status !== 'failed' && msg.status !== 'aborted'),
      userMsg,
    ];
    const reasoningEffort = thinkingLevel
      && !selectedModelInfo?.thinking?.locked
      && thinkingLevel !== selectedModelInfo?.thinking?.default
      ? thinkingLevel
      : undefined;
    const payload = buildProtocolPayload({
      protocol,
      model: selectedModel,
      messages: allMessages,
      currentMessage: userMsg,
      systemPrompt,
      hasConversation,
      conversationId: hasConversation ? effectiveConversationId : undefined,
      temperature,
      maxTokens,
      topP,
      presencePenalty,
      frequencyPenalty,
      stop: parseStopSequences(stop),
      stream,
      seed: seed.trim() ? Number(seed) : undefined,
      user: userValue.trim() || undefined,
      responseFormat,
      tools,
      toolChoice,
      reasoningEffort,
    });
    setLastPayload(payload);
    setChat(prev => ({
      ...prev,
      messages: [...prev.messages.filter(msg => msg.role !== 'system'), userMsg, { role: 'assistant', content: '', status: 'streaming' }],
      isStreaming: true, usage: null, latencyMs: null, statusText: stream ? '正在流式接收...' : '正在请求...',
    }));
    setInput('');
    clearChatAttachments();

    const startTime = Date.now();
    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const request = protocol === 'chat'
        ? playgroundChatCompletions
        : protocol === 'responses'
          ? playgroundResponses
          : playgroundAnthropicMessages;
      const res = await request(tokenId, payload, controller.signal);
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error?.message || data.message || `请求失败 (${res.status})`);
      }
      const headerDebugLogId = Number(res.headers.get('X-Prism-Request-Log-ID') || 0) || null;

      let assistantContent = '', reasoningContent = '';
      let toolCalls: ChatMessage['toolCalls'] = [];
      let usage: ChatState['usage'] = null;
      let finalStatus: ChatMessage['status'] = 'completed';
      let debug: PlaygroundDebugDetail | null = null;
      let pendingDebugLogId: number | null = null;
      let finishReason = '';
      if (stream) {
        if (!res.body) throw new Error('无响应流');
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        const streamState = createProtocolStreamState();

        // 高频 token 先累计在局部变量，最多每 80ms 更新一次 React 状态，避免逐帧重渲染。
        const flushStreamMessage = () => {
          if (streamFlushTimerRef.current !== null) return;
          streamFlushTimerRef.current = window.setTimeout(() => {
            streamFlushTimerRef.current = null;
            setChat(prev => {
              const msgs = [...prev.messages];
              msgs[msgs.length - 1] = {
                role: 'assistant', content: assistantContent, reasoningContent: reasoningContent || undefined,
                toolCalls: toolCalls.length > 0 ? toolCalls.map(call => ({ ...call })) : undefined, status: 'streaming', finishReason,
              };
              return { ...prev, messages: msgs, statusText: '正在流式接收...' };
            });
          }, 80);
        };

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          buffer = buffer.replace(/\r\n/g, '\n');
          // SSE 帧可能跨网络 chunk，末尾不完整片段保留到下一次读取。
          const events = buffer.split('\n\n');
          buffer = events.pop() || '';
          for (const eventBlock of events) {
            const lines = eventBlock.split('\n');
            let eventName = 'message';
            const dataLines: string[] = [];
            for (const line of lines) {
              if (line.startsWith('event:')) eventName = line.slice(6).trim();
              if (line.startsWith('data:')) dataLines.push(line.slice(5).trimStart());
            }
            const data = dataLines.join('\n');
            if (!data || data === '[DONE]') continue;
            if (eventName === 'prism-debug') {
              try { pendingDebugLogId = JSON.parse(data).request_log_id ?? null; } catch { /* ignore */ }
              continue;
            }
            let parsed: any;
            try { parsed = JSON.parse(data); } catch { continue; }
            consumeProtocolStreamEvent(protocol, eventName, parsed, streamState);
            if (streamState.error) throw new Error(streamState.error);
            assistantContent = streamState.content;
            reasoningContent = streamState.reasoningContent;
            toolCalls = streamState.toolCalls;
            finishReason = streamState.finishReason;
            usage = streamState.usage;
            flushStreamMessage();
          }
        }
        if (streamFlushTimerRef.current !== null) { window.clearTimeout(streamFlushTimerRef.current); streamFlushTimerRef.current = null; }
        // 流式结束后据 request_log_id 拉取完整调试详情(与历史载入同源)
        const streamDebugLogId = pendingDebugLogId ?? headerDebugLogId;
        if (streamDebugLogId !== null) {
          debug = await playgroundGetDebug(tokenId, streamDebugLogId).catch(() => null);
          if (debug) setDebugDetail(debug);
        }
      } else {
        const parsed = await res.json();
        if (parsed.error) throw new Error(parsed.error.message || parsed.error);
        const result = parseProtocolResponse(protocol, parsed);
        assistantContent = result.content;
        reasoningContent = result.reasoningContent;
        toolCalls = result.toolCalls;
        finishReason = result.finishReason;
        usage = result.usage;
        debug = result.debug || null;
        if (!debug && headerDebugLogId !== null) {
          debug = await playgroundGetDebug(tokenId, headerDebugLogId).catch(() => null);
        }
        setDebugDetail(debug);
        if (result.conversationId) { setConversationId(result.conversationId); setSelectedConversationId(Number(result.conversationId)); }
      }

      setChat(prev => {
        const msgs = [...prev.messages];
        msgs[msgs.length - 1] = {
          role: 'assistant', content: assistantContent, reasoningContent: reasoningContent || undefined,
          toolCalls: toolCalls.length > 0 ? toolCalls.map(call => ({ ...call })) : undefined,
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
      if (streamFlushTimerRef.current !== null) {
        window.clearTimeout(streamFlushTimerRef.current);
        streamFlushTimerRef.current = null;
      }
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
    // 使正在加载的历史失效，并同步清理附件 object URL 与调试状态。
    conversationLoadRef.current += 1;
    setLoadingConversationId(undefined);
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

  const handleProtocolChange = (nextProtocol: PlaygroundProtocol) => {
    if (chat.isStreaming || nextProtocol === protocol) return;
    conversationLoadRef.current += 1;
    setLoadingConversationId(undefined);
    setProtocol(nextProtocol);
    setConversationId('');
    setSelectedConversationId(undefined);
    setCurrentConversationMeta(null);
    setDebugDetail(null);
    setLastPayload(null);
    setError('');
    const next = PLAYGROUND_PROTOCOLS.find(item => item.value === nextProtocol)!;
    setChat(prev => ({ ...prev, statusText: `已切换至 ${next.label}` }));
  };

  const renderProtocolSwitch = (compact = false) => (
    <div className="inline-flex items-center rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] p-0.5 flex-shrink-0" role="group" aria-label="请求协议">
      {PLAYGROUND_PROTOCOLS.map(item => (
        <button
          key={item.value}
          type="button"
          title={item.endpoint}
          disabled={chat.isStreaming}
          onClick={() => handleProtocolChange(item.value)}
          className={`${compact ? 'px-2' : 'px-2.5'} h-7 rounded-md text-[11px] font-medium transition-colors disabled:opacity-40 ${protocol === item.value ? 'bg-[var(--surface-card)] text-[var(--primary)] shadow-sm' : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}
        >
          {item.label}
        </button>
      ))}
    </div>
  );

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

  useEffect(() => {
    const handleDocumentPaste = (event: ClipboardEvent) => {
      if (event.defaultPrevented) return;
      const files = getClipboardFiles(event.clipboardData);
      if (files.length === 0) return;

      event.preventDefault();
      addFiles(files);
    };

    document.addEventListener('paste', handleDocumentPaste);
    return () => document.removeEventListener('paste', handleDocumentPaste);
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
    <div className="relative h-[calc(100dvh-180px)] md:h-[calc(100dvh-220px)] overflow-hidden">
      {(showHistoryDrawer || showAdvancedDrawer || showDebugDrawer) && (
        <div className="absolute inset-0 z-20 bg-black/20" onClick={() => { setShowHistoryDrawer(false); setShowAdvancedDrawer(false); setShowDebugDrawer(false); }} />
      )}
      {showHistoryDrawer && (
        <div className="absolute inset-y-0 left-0 z-30">
          <HistoryPanel items={historyItems} selectedConversationId={selectedConversationId} currentModel={selectedModel}
            loadingConversationId={loadingConversationId}
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
          <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">{protocol === 'chat' ? 'Max Tokens' : 'Max Output Tokens'}</label><input type="number" min={1} max={selectedModelInfo?.max_tokens && selectedModelInfo.max_tokens > 0 ? selectedModelInfo.max_tokens : 131072} value={maxTokens} onChange={e => setMaxTokens(Number(e.target.value))} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
          <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Top P</label><input type="number" min={0} max={1} step="0.1" value={topP} onChange={e => setTopP(Number(e.target.value))} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
          {protocol === 'chat' && <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Presence Penalty</label><input type="number" min={-2} max={2} step="0.1" value={presencePenalty} onChange={e => setPresencePenalty(Number(e.target.value))} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>}
          {protocol === 'chat' && <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Frequency Penalty</label><input type="number" min={-2} max={2} step="0.1" value={frequencyPenalty} onChange={e => setFrequencyPenalty(Number(e.target.value))} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>}
          {protocol !== 'responses' && <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Stop（每行一个）</label><textarea value={stop} onChange={e => setStop(e.target.value)} rows={3} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm resize-none focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>}
          {protocol === 'chat' && <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Seed</label><input type="number" value={seed} onChange={e => setSeed(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>}
          <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">{protocol === 'anthropic' ? 'User ID' : 'User'}</label><input type="text" value={userValue} onChange={e => setUserValue(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>
          {protocol === 'chat' && <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">Conversation ID</label><input type="text" value={effectiveConversationId} onChange={e => setConversationId(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>}
          <div className="border border-[var(--border-soft)] rounded-xl overflow-hidden">
            <div className="px-3 py-2 bg-[var(--surface)] text-xs font-semibold text-[var(--text-secondary)]">高级参数（JSON）</div>
            <div className="p-3 space-y-3">
              {protocol !== 'anthropic' && <div><label className="block text-xs font-medium text-[var(--text-secondary)] mb-1">{protocol === 'responses' ? 'text.format' : 'response_format'}</label><textarea value={responseFormatText} onChange={e => setResponseFormatText(e.target.value)} rows={3} placeholder='{"type":"json_object"}' className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-xs font-mono resize-none focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" /></div>}
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
          {/* Mobile: 极简顶栏 */}
          <div className="px-3 py-2 border-b border-[var(--border-soft)] flex flex-col gap-2 md:hidden">
            <div className="flex items-center justify-between gap-2">
              <ModelSelector options={models.map(m => ({ id: m.id, provider: m.group || m.owned_by }))} value={selectedModel} onChange={handleModelSelect} className="min-w-0 flex-1" />
              <button type="button" onClick={() => setShowMobileMenu(true)} className="p-2 rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface)]"><MoreHorizontal size={18} /></button>
            </div>
            {renderProtocolSwitch(true)}
          </div>
          {/* Mobile: 底部菜单 */}
          {showMobileMenu && (
            <>
              <div className="fixed inset-0 z-50 bg-black/30 md:hidden" onClick={() => setShowMobileMenu(false)} />
              <div className="fixed bottom-0 left-0 right-0 z-50 bg-[var(--surface-card)] rounded-t-2xl border-t border-[var(--border-soft)] p-4 space-y-1 md:hidden animate-slide-in-right">
                <div className="w-10 h-1 bg-gray-300 rounded-full mx-auto mb-3" />
                <button type="button" onClick={() => { setShowMobileMenu(false); setShowHistoryDrawer(true); }} className="w-full flex items-center gap-3 px-4 py-3 rounded-xl hover:bg-[var(--surface)] text-sm text-[var(--text-primary)]"><Plus size={16} className="text-[var(--text-secondary)]" /> 历史会话</button>
                <button type="button" onClick={() => { setShowMobileMenu(false); setShowSystemPrompt(prev => !prev); }} className="w-full flex items-center gap-3 px-4 py-3 rounded-xl hover:bg-[var(--surface)] text-sm text-[var(--text-primary)]"><FileJson size={16} className="text-[var(--text-secondary)]" /> {protocol === 'responses' ? 'Instructions' : 'System Prompt'}</button>
                <button type="button" onClick={() => { setShowMobileMenu(false); setShowAdvancedDrawer(true); }} className="w-full flex items-center gap-3 px-4 py-3 rounded-xl hover:bg-[var(--surface)] text-sm text-[var(--text-primary)]"><SlidersHorizontal size={16} className="text-[var(--text-secondary)]" /> 参数设置</button>
                <button type="button" onClick={() => { setShowMobileMenu(false); setShowDebugDrawer(true); setShowFullDebug(false); }} className="w-full flex items-center gap-3 px-4 py-3 rounded-xl hover:bg-[var(--surface)] text-sm text-[var(--text-primary)]"><Bug size={16} className="text-[var(--text-secondary)]" /> 调试信息</button>
                <div className="flex items-center justify-between px-4 py-3 rounded-xl">
                  <span className="text-sm text-[var(--text-primary)]">Stream 模式</span>
                  <button type="button" aria-label="切换 Stream" disabled={streamDisabled} onClick={() => !streamDisabled && setStream(prev => !prev)}
                    className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${streamDisabled ? 'bg-gray-200 cursor-not-allowed' : stream ? 'bg-[var(--primary)]' : 'bg-gray-300'}`}>
                    <span className={`inline-block h-5 w-5 transform rounded-full bg-[var(--surface-card)] shadow-sm transition-transform ${stream ? 'translate-x-5' : 'translate-x-1'}`} />
                  </button>
                </div>
                <button type="button" onClick={() => { setShowMobileMenu(false); handleClear(); }} className="w-full flex items-center gap-3 px-4 py-3 rounded-xl hover:bg-red-50 text-sm text-red-500"><Trash2 size={16} /> 清空对话</button>
              </div>
            </>
          )}
          {/* Desktop: 完整工具栏 */}
          <div className="hidden md:flex px-4 py-2 border-b border-[var(--border-soft)] items-center gap-2 flex-wrap">
            <div className="flex items-center gap-2 min-w-0 flex-1 basis-[21rem]">
              <ModelSelector options={models.map(m => ({ id: m.id, provider: m.group || m.owned_by }))} value={selectedModel} onChange={handleModelSelect} className="min-w-[10rem] max-w-[13rem] flex-1" />
              {renderProtocolSwitch()}
            </div>
            <div className="flex items-center gap-1.5 ml-auto">
              <button type="button" aria-label="历史" title="历史" onClick={() => setShowHistoryDrawer(prev => !prev)} className="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-[var(--border-soft)] text-[var(--text-secondary)] hover:bg-[var(--surface)]"><PanelLeft size={13} /></button>
              <button type="button" aria-label={protocol === 'responses' ? 'Instructions' : 'System'} title={protocol === 'responses' ? 'Instructions' : 'System'} onClick={() => setShowSystemPrompt(prev => !prev)} className="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-[var(--border-soft)] text-[var(--text-secondary)] hover:bg-[var(--surface)]"><FileJson size={13} /></button>
              <button type="button" aria-label="参数" title="参数" onClick={() => setShowAdvancedDrawer(prev => !prev)} className="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-[var(--border-soft)] text-[var(--text-secondary)] hover:bg-[var(--surface)]"><SlidersHorizontal size={13} /></button>
              <button type="button" aria-label="调试" title="调试" onClick={() => { setShowDebugDrawer(prev => !prev); setShowFullDebug(false); }} className="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-[var(--border-soft)] text-[var(--text-secondary)] hover:bg-[var(--surface)] xl:hidden"><Bug size={13} /></button>
              <button type="button" aria-label="清空" title="清空" onClick={handleClear} className="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-[var(--border-soft)] text-[var(--text-secondary)] hover:bg-[var(--surface)]"><Trash2 size={13} /></button>
            </div>
          </div>

          {pendingModel && (
            <div className="mx-3 md:mx-4 mt-2 px-3 py-2 bg-amber-50 border border-amber-200 text-amber-700 text-sm rounded-lg flex items-center justify-between gap-2">
              <span className="text-xs">已切换到 {pendingModel}</span>
              <div className="flex gap-1">
                <button type="button" onClick={startFreshConversationWithModel} className="px-2 py-1 text-xs bg-amber-100 hover:bg-amber-200 rounded-lg">新建</button>
                <button type="button" onClick={() => { setSelectedModel(conversationModel!); setPendingModel(null); setError(''); }} className="px-2 py-1 text-xs bg-amber-100 hover:bg-amber-200 rounded-lg">恢复</button>
              </div>
            </div>
          )}

          {/* Desktop: Stream & status bars */}
          <div className="hidden md:flex px-4 py-1 border-b border-[var(--border-soft)] items-center gap-3 flex-wrap">
            <label className="flex items-center gap-2 cursor-pointer select-none">
              <span className="text-xs text-[var(--text-secondary)]">Stream</span>
              <button type="button" aria-label="切换 Stream" disabled={streamDisabled} onClick={() => !streamDisabled && setStream(prev => !prev)}
                className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${streamDisabled ? 'bg-gray-200 cursor-not-allowed' : stream ? 'bg-[var(--primary)]' : 'bg-gray-300'}`}>
                <span className={`inline-block h-5 w-5 transform rounded-full bg-[var(--surface-card)] shadow-sm transition-transform ${stream ? 'translate-x-5' : 'translate-x-1'}`} />
              </button>
            </label>
            <div className={`rounded-lg border px-3 py-1 text-[11px] ${streamDisabled ? 'border-amber-200 bg-amber-50 text-amber-700' : 'border-[var(--border-soft)] bg-[var(--surface)] text-[var(--text-secondary)]'}`}>
              {streamDisabled ? '不支持流式' : `stream: ${selectedModelInfo?.default_stream ? '开启' : '关闭'}`}
            </div>
            {selectedModelInfo?.thinking && selectedModelInfo.thinking.options.length > 0 && (
              <div className="flex items-center gap-1.5">
                <span className="text-xs text-[var(--text-secondary)]">思考</span>
                <ThinkingSelect options={selectedModelInfo.thinking.options} value={thinkingLevel}
                  onChange={setThinkingLevel} locked={selectedModelInfo.thinking.locked} />
                {selectedModelInfo.thinking.locked && <span className="text-[10px] text-amber-600">已锁定</span>}
              </div>
            )}
          </div>

          {(showSystemPrompt || systemPrompt.trim()) && (
            <div className="px-3 md:px-4 py-1.5 border-b border-[var(--border-soft)]">
              <label className="block text-[11px] font-medium text-[var(--text-secondary)] mb-1">{protocol === 'responses' ? 'Instructions' : 'System Prompt'}</label>
              <textarea value={systemPrompt} onChange={e => setSystemPrompt(e.target.value)} rows={2} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm resize-none focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
            </div>
          )}

          <div className="hidden md:flex px-4 py-1 border-b border-[var(--border-soft)] text-[11px] text-[var(--text-secondary)] items-center gap-3 flex-wrap">
            <span>{chat.statusText}</span>
            <span className="font-mono">{protocolInfo.endpoint}</span>
            {conversationModel ? <span>会话模型 {conversationModel}</span> : null}
            {chat.latencyMs ? <span>耗时 {(chat.latencyMs / 1000).toFixed(2)}s</span> : null}
            {chat.usage ? <span>Tokens {chat.usage.input}/{chat.usage.output}/{chat.usage.total || (chat.usage.input + chat.usage.output)}</span> : null}
            {protocol === 'chat' && effectiveConversationId ? <span>会话 #{effectiveConversationId}</span> : null}
          </div>
          <div className="flex-1 overflow-y-auto p-3 md:p-4 space-y-4 min-h-0">
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
                  {msg.role === 'assistant' && msg.toolCalls && msg.toolCalls.length > 0 && (
                    <div className="px-4 pb-3 space-y-2">
                      {msg.toolCalls.map((call, callIndex) => (
                        <div key={call.id || callIndex} className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] overflow-hidden">
                          <div className="px-3 py-1.5 border-b border-[var(--border-soft)] flex items-center gap-2 text-xs font-medium text-[var(--text-primary)]">
                            <Wrench size={12} className="text-[var(--primary)]" />
                            <span className="truncate">{call.name}</span>
                            {call.id ? <span className="ml-auto font-mono text-[10px] text-[var(--text-secondary)] truncate max-w-32">{call.id}</span> : null}
                          </div>
                          <pre className="px-3 py-2 text-[11px] leading-5 font-mono whitespace-pre-wrap break-all text-[var(--text-secondary)] max-h-48 overflow-auto">{call.arguments || '{}'}</pre>
                        </div>
                      ))}
                    </div>
                  )}
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
                          <button onClick={() => removeAttachment(att.id)} className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center md:opacity-0 md:group-hover:opacity-100 transition-all shadow-sm"><X size={10} /></button>
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
                          <button onClick={() => removeAttachment(att.id)} className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-black/60 hover:bg-red-500 text-white rounded-full flex items-center justify-center md:opacity-0 md:group-hover:opacity-100 transition-all shadow-sm"><X size={10} /></button>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
              <div className="flex items-end gap-1.5 p-2">
                <button onClick={() => fileInputRef.current?.click()} disabled={chat.isStreaming || loadingConversationId !== undefined} className="p-2 text-[var(--text-secondary)] hover:text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg disabled:opacity-40 transition-all flex-shrink-0 mb-0.5" title="添加附件"><Paperclip size={18} /></button>
				<textarea value={input} onChange={e => setInput(e.target.value)} onKeyDown={handleKeyDown} placeholder={loadingConversationId !== undefined ? '正在加载历史会话' : '输入消息'} rows={2} className="flex-1 px-2 py-2.5 text-sm resize-none focus:outline-none bg-transparent max-h-32" disabled={chat.isStreaming || loadingConversationId !== undefined} />
                {chat.isStreaming ? (
                  <button onClick={handleStop} className="px-4 py-2.5 bg-red-500 text-white rounded-xl hover:bg-red-600 transition-colors flex-shrink-0"><Square size={18} /></button>
                ) : (
                  <button onClick={handleSend} disabled={loadingConversationId !== undefined || (!input.trim() && attachments.filter(a => a.uploaded).length === 0) || !selectedModel} className="px-3 md:px-4 py-2.5 bg-[var(--primary)] text-white rounded-xl hover:opacity-90 disabled:opacity-30 disabled:cursor-not-allowed transition-colors flex-shrink-0 flex items-center gap-1.5">
                    <Send size={16} /><span className="text-sm font-medium hidden sm:inline">发送</span>
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
