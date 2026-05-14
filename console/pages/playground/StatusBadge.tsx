import React from 'react';
import { Radio, CheckCircle2, XCircle, Square, Clock3, Loader2 } from 'lucide-react';

const StatusBadge: React.FC<{ status?: string }> = ({ status }) => {
  const config = {
    streaming: { label: '流式接收中', className: 'bg-blue-100 text-blue-700', icon: <Radio size={12} /> },
    completed: { label: '已完成', className: 'bg-green-100 text-green-700', icon: <CheckCircle2 size={12} /> },
    success: { label: '已完成', className: 'bg-green-100 text-green-700', icon: <CheckCircle2 size={12} /> },
    failed: { label: '失败', className: 'bg-red-100 text-red-700', icon: <XCircle size={12} /> },
    aborted: { label: '已中断', className: 'bg-amber-100 text-amber-700', icon: <Square size={12} /> },
    pending: { label: '等待中', className: 'bg-[var(--primary-lighter)] text-[var(--text-primary)]', icon: <Clock3 size={12} /> },
    processing: { label: '处理中', className: 'bg-indigo-100 text-[var(--primary)]', icon: <Loader2 size={12} className="animate-spin" /> },
    running: { label: '处理中', className: 'bg-indigo-100 text-[var(--primary)]', icon: <Loader2 size={12} className="animate-spin" /> },
  } as const;
  const item = config[(status || 'pending') as keyof typeof config] || config.pending;
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-1 rounded-full text-xs ${item.className}`}>
      {item.icon}
      {item.label}
    </span>
  );
};

export default StatusBadge;
