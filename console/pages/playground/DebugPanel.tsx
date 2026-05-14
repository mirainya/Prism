import React from 'react';
import { Bug, Eye, PanelLeft, FileJson } from 'lucide-react';
import { PlaygroundDebugDetail, PlaygroundConversation } from '../../types';
import StatusBadge from './StatusBadge';
import DetailSection from './DetailSection';
import { formatJson } from './utils';

const DebugPanel: React.FC<{
  debugDetail: PlaygroundDebugDetail | null;
  lastPayload: Record<string, any> | null;
  compact?: boolean;
  showAllDetails?: boolean;
  onExpandFull?: () => void;
  currentConversationMeta?: PlaygroundConversation | null;
}> = ({ debugDetail, lastPayload, compact = false, showAllDetails = false, onExpandFull, currentConversationMeta }) => {
  const summaryRows = [
    { label: '日志 ID', value: debugDetail?.requestLogId || currentConversationMeta?.lastRequestLogId || '-' },
    { label: '会话 ID', value: debugDetail?.conversationId || currentConversationMeta?.id || '-' },
    { label: '渠道', value: `${debugDetail?.channelName || '-'}${debugDetail?.channelType ? ` (${debugDetail.channelType})` : ''}` },
    { label: '模型', value: debugDetail?.modelCode || '-' },
    { label: '供应商模型', value: debugDetail?.vendorModel || '-' },
    { label: '请求路径', value: debugDetail?.requestPath || '-' },
    { label: '模式', value: debugDetail?.isStream ? '流式' : '非流式' },
    { label: '耗时', value: debugDetail?.latencyMs ? `${(debugDetail.latencyMs / 1000).toFixed(2)}s` : '-' },
    { label: 'Finish Reason', value: debugDetail?.finishReason || '-' },
  ];

  return (
    <div className={`bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] overflow-hidden flex flex-col min-w-0 ${compact ? 'w-full h-full' : 'w-[24rem] flex-shrink-0'}`}>
      <div className="px-4 py-3 border-b border-[var(--border-soft)] flex items-center gap-2 text-sm font-semibold text-[var(--text-primary)]">
        <Bug size={16} /> 调试面板
      </div>
      <div className="flex-1 overflow-y-auto p-4 space-y-4">
        <div className="rounded-lg border border-[var(--border-soft)] p-3 space-y-3 text-sm">
          <div className="flex items-center justify-between">
            <span className="text-[var(--text-secondary)]">状态</span>
            <StatusBadge status={debugDetail?.status || 'pending'} />
          </div>
          {summaryRows.map(row => (
            <div key={row.label} className="flex items-start justify-between gap-3">
              <span className="text-[var(--text-secondary)] text-sm">{row.label}</span>
              <span className="text-right text-sm break-all font-mono">{row.value}</span>
            </div>
          ))}
        </div>

        {debugDetail?.errorMessage && (
          <div className="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 whitespace-pre-wrap">
            {debugDetail.errorMessage}
          </div>
        )}

        <div>
          <div className="text-xs font-semibold text-[var(--text-secondary)] mb-2">响应摘要</div>
          <div className="rounded-lg border border-[var(--border-soft)] p-3 text-sm text-[var(--text-primary)] whitespace-pre-wrap min-h-20">
            {debugDetail?.responsePreview || '暂无'}
          </div>
        </div>

        {debugDetail?.usage && (
          <div className="rounded-lg border border-[var(--border-soft)] p-3 text-sm space-y-2">
            <div className="text-xs font-semibold text-[var(--text-secondary)]">Usage</div>
            <div className="flex items-center justify-between"><span className="text-[var(--text-secondary)]">Prompt</span><span>{debugDetail.usage.prompt_tokens}</span></div>
            <div className="flex items-center justify-between"><span className="text-[var(--text-secondary)]">Completion</span><span>{debugDetail.usage.completion_tokens}</span></div>
            <div className="flex items-center justify-between"><span className="text-[var(--text-secondary)]">Total</span><span>{debugDetail.usage.total_tokens}</span></div>
          </div>
        )}

        {showAllDetails ? (
          <>
            <div className="flex items-center justify-between gap-2">
              <div className="text-xs font-semibold text-[var(--text-secondary)]">完整调试详情</div>
            </div>
            <div>
              <div className="text-xs font-semibold text-[var(--text-secondary)] mb-2">前端参数</div>
              <pre className="bg-[var(--surface)] border border-[var(--border-soft)] rounded-lg p-3 text-xs overflow-auto max-h-72">{formatJson(lastPayload)}</pre>
            </div>
            <div>
              <div className="text-xs font-semibold text-[var(--text-secondary)] mb-2">上游请求体</div>
              <pre className="bg-[var(--surface)] border border-[var(--border-soft)] rounded-lg p-3 text-xs overflow-auto max-h-72">{formatJson(debugDetail?.requestBody)}</pre>
            </div>
            <div>
              <div className="text-xs font-semibold text-[var(--text-secondary)] mb-2">请求头摘要</div>
              <pre className="bg-[var(--surface)] border border-[var(--border-soft)] rounded-lg p-3 text-xs overflow-auto max-h-56">{formatJson(debugDetail?.requestHeaders)}</pre>
            </div>
            <div>
              <div className="text-xs font-semibold text-[var(--text-secondary)] mb-2">响应体 / 错误详情</div>
              <pre className="bg-[var(--surface)] border border-[var(--border-soft)] rounded-lg p-3 text-xs overflow-auto max-h-[32rem]">{formatJson(debugDetail?.responseBody)}</pre>
            </div>
          </>
        ) : (
          <>
            <button
              type="button"
              onClick={() => onExpandFull?.()}
              className="w-full inline-flex items-center justify-center gap-2 px-3 py-2 rounded-lg border border-[var(--border-soft)] text-sm text-[var(--text-secondary)] hover:bg-[var(--surface)]"
              disabled={!debugDetail}
            >
              <Eye size={14} /> 查看完整调试
            </button>
            <DetailSection title="前端参数" icon={<PanelLeft size={14} />} content={formatJson(lastPayload)} />
            <DetailSection title="上游请求体" icon={<FileJson size={14} />} content={formatJson(debugDetail?.requestBody)} />
            <DetailSection title="请求头摘要" icon={<FileJson size={14} />} content={formatJson(debugDetail?.requestHeaders)} />
            <DetailSection title="响应体 / 错误详情" icon={<FileJson size={14} />} content={formatJson(debugDetail?.responseBody)} />
          </>
        )}
      </div>
    </div>
  );
};

export default DebugPanel;
