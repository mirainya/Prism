import React, { useMemo } from 'react';
import { Play, Link2, Image as ImageIcon, Bug, PanelLeft, FileJson } from 'lucide-react';
import { TaskResult } from './types';
import StatusBadge from './StatusBadge';
import DetailSection from './DetailSection';
import {
  extractMediaItems, extractLinkItems, buildResultSummary,
  extractCapabilityModel, extractCapabilityPrompt, getCapabilityTaskStatus, formatTime, formatJson,
} from './utils';

export const CapabilityResultCard: React.FC<{ task: TaskResult }> = ({ task }) => {
  const mediaItems = useMemo(
    () => extractMediaItems(task.result, 'result', [], { capabilityType: task.capabilityType }),
    [task.result, task.capabilityType],
  );
  const imageItems = useMemo(() => mediaItems.filter(item => item.type === 'image'), [mediaItems]);
  const videoItems = useMemo(() => mediaItems.filter(item => item.type === 'video'), [mediaItems]);
  const linkItems = useMemo(() => extractLinkItems(task.result).filter(item => !mediaItems.some(media => media.url === item.url)), [task.result, mediaItems]);
  const summary = useMemo(() => buildResultSummary(task.result), [task.result]);

  return (
    <div className="space-y-3">
      <div className="rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
        {summary}
      </div>
      {imageItems.length > 0 && (
        <div>
          <div className="mb-2 flex items-center gap-2 text-xs font-semibold text-[var(--text-secondary)]">
            <ImageIcon size={14} /> 图片结果
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            {imageItems.map((item, index) => (
              <a key={`${item.url}-${index}`} href={item.url} target="_blank" rel="noreferrer"
                className="group overflow-hidden rounded-xl border border-[var(--border-soft)] bg-[var(--surface)] hover:border-indigo-300">
                <img src={item.url} alt={`result-${index}`} className="h-56 w-full object-cover bg-[var(--primary-lighter)]" />
                <div className="space-y-1 border-t border-[var(--border-soft)] px-3 py-2 text-xs text-[var(--text-secondary)]">
                  <div className="truncate font-medium">{item.label}</div>
                  <div className="truncate">{item.url}</div>
                </div>
              </a>
            ))}
          </div>
        </div>
      )}
      {videoItems.length > 0 && (
        <div>
          <div className="mb-2 flex items-center gap-2 text-xs font-semibold text-[var(--text-secondary)]">
            <Play size={14} /> 视频结果
          </div>
          <div className="grid grid-cols-1 gap-3">
            {videoItems.map((item, index) => (
              <div key={`${item.url}-${index}`} className="overflow-hidden rounded-xl border border-[var(--border-soft)] bg-[var(--surface)]">
                <video src={item.url} controls className="h-72 w-full bg-black" preload="metadata" />
                <div className="space-y-1 border-t border-[var(--border-soft)] px-3 py-2 text-xs text-[var(--text-secondary)]">
                  <div className="truncate font-medium">{item.label}</div>
                  <a href={item.url} target="_blank" rel="noreferrer" className="block truncate text-[var(--primary)] hover:underline">{item.url}</a>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
      {linkItems.length > 0 && (
        <div>
          <div className="mb-2 flex items-center gap-2 text-xs font-semibold text-[var(--text-secondary)]">
            <Link2 size={14} /> 结果链接
          </div>
          <div className="space-y-2">
            {linkItems.map(item => (
              <a key={item.url} href={item.url} target="_blank" rel="noreferrer"
                className="flex items-center justify-between gap-3 rounded-lg border border-[var(--border-soft)] px-3 py-2 text-sm text-[var(--primary)] hover:bg-[var(--primary-lighter)]">
                <span className="truncate">{item.label}</span>
                <span className="truncate text-xs text-[var(--text-secondary)]">{item.url}</span>
              </a>
            ))}
          </div>
        </div>
      )}
      <details className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface)]">
        <summary className="cursor-pointer select-none px-3 py-2 text-xs font-semibold text-[var(--text-secondary)]">原始结果 JSON</summary>
        <pre className="max-h-72 overflow-auto border-t border-[var(--border-soft)] p-3 text-xs">{JSON.stringify(task.result, null, 2)}</pre>
      </details>
    </div>
  );
};
export const CapabilityDebugPanel: React.FC<{ task: TaskResult | null; embedded?: boolean }> = ({ task, embedded = false }) => {
  if (!task) {
    if (embedded) return <div className="p-5 text-[var(--text-secondary)] text-sm text-center">选择一个任务后，可在这里查看调试详情</div>;
    return (
      <div className="bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] overflow-hidden flex flex-col min-w-0 w-full h-full">
        <div className="px-4 py-3 border-b border-[var(--border-soft)] flex items-center gap-2 text-sm font-semibold text-[var(--text-primary)]">
          <Bug size={16} /> 能力调试详情
        </div>
        <div className="flex-1 flex items-center justify-center p-5 text-[var(--text-secondary)] text-sm text-center">
          选择一个任务后，可在这里查看调试详情
        </div>
      </div>
    );
  }

  const resolvedModel = extractCapabilityModel(task);
  const fullPrompt = extractCapabilityPrompt(task);
  const hasResult = ['completed', 'success'].includes(task.status) && task.result;

  // embedded: 供 Modal 内使用,去掉外框/自带标题/h-full(Modal 已提供标题与滚动)
  const body = (
    <>
        <div className="rounded-lg border border-[var(--border-soft)] p-3 space-y-3 text-sm">
          <div className="flex items-center justify-between">
            <span className="text-[var(--text-secondary)]">状态</span>
            <StatusBadge status={getCapabilityTaskStatus(task.status)} />
          </div>
          <div className="flex items-start justify-between gap-3"><span className="text-[var(--text-secondary)]">任务号</span><span className="text-right font-mono break-all">{task.taskNo}</span></div>
          <div className="flex items-start justify-between gap-3"><span className="text-[var(--text-secondary)]">能力</span><span className="text-right break-all">{task.capabilityName || task.capability || '-'}</span></div>
          <div className="flex items-start justify-between gap-3"><span className="text-[var(--text-secondary)]">渠道</span><span className="text-right break-all">{task.channel || '自动选择'}</span></div>
          <div className="flex items-start justify-between gap-3"><span className="text-[var(--text-secondary)]">实际模型</span><span className="text-right font-mono break-all">{resolvedModel}</span></div>
          <div className="flex items-start justify-between gap-3"><span className="text-[var(--text-secondary)]">供应商任务 ID</span><span className="text-right font-mono break-all">{task.vendorTaskId || '-'}</span></div>
          <div className="flex items-start justify-between gap-3"><span className="text-[var(--text-secondary)]">进度</span><span>{task.progress || 0}%</span></div>
          <div className="flex items-start justify-between gap-3"><span className="text-[var(--text-secondary)]">费用</span><span>¥{Number(task.cost || 0).toFixed(4)}</span></div>
          <div className="flex items-start justify-between gap-3"><span className="text-[var(--text-secondary)]">创建时间</span><span>{formatTime(task.createdAt)}</span></div>
          <div className="flex items-start justify-between gap-3"><span className="text-[var(--text-secondary)]">开始时间</span><span>{formatTime(task.startedAt)}</span></div>
          <div className="flex items-start justify-between gap-3"><span className="text-[var(--text-secondary)]">完成时间</span><span>{formatTime(task.completedAt)}</span></div>
        </div>
        {task.error && (
          <div className="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 whitespace-pre-wrap">{task.error}</div>
        )}
        {hasResult ? <CapabilityResultCard task={task} /> : null}
        <DetailSection title="完整提示词" icon={<PanelLeft size={14} />} content={fullPrompt || '-'} />
        <DetailSection title="前端提交参数" icon={<PanelLeft size={14} />} content={formatJson(task.params)} />
        <DetailSection title="后端原始参数" icon={<FileJson size={14} />} content={formatJson(task.rawParams)} />
        <DetailSection title="后端映射参数" icon={<FileJson size={14} />} content={formatJson(task.mappedParams)} />
        <DetailSection title="标准结果" icon={<FileJson size={14} />} content={formatJson(task.result)} />
        <DetailSection title="供应商原始响应" icon={<FileJson size={14} />} content={formatJson(task.vendorResponse)} />
    </>
  );

  if (embedded) {
    return <div className="space-y-3">{body}</div>;
  }

  return (
    <div className="bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] overflow-hidden flex flex-col min-w-0 w-full h-full">
      <div className="px-4 py-3 border-b border-[var(--border-soft)] flex items-center gap-2 text-sm font-semibold text-[var(--text-primary)]">
        <Bug size={16} /> 能力调试详情
      </div>
      <div className="flex-1 overflow-y-auto p-4 space-y-3">{body}</div>
    </div>
  );
};
