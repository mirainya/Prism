import React from 'react';
import { AlertTriangle } from 'lucide-react';

export const errorMessage = (error: unknown) => error instanceof Error ? error.message : '请求失败';
export const ErrorNotice: React.FC<{ message: string; onRetry?: () => void }> = ({ message, onRetry }) => <div role="alert" className="flex items-center gap-3 rounded-lg border border-rose-300/40 bg-rose-500/5 px-4 py-3 text-sm text-rose-600"><AlertTriangle size={17} className="shrink-0" /><span className="min-w-0 flex-1 break-words">{message}</span>{onRetry && <button type="button" onClick={onRetry} className="shrink-0 font-bold underline">重试</button>}</div>;
