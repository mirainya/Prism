import type { ContentPart } from './types';

export const ACCEPTED_FILE_TYPES = 'image/png,image/jpeg,image/gif,image/webp,application/pdf,text/plain,text/csv,application/vnd.openxmlformats-officedocument.wordprocessingml.document';
export const MAX_FILE_SIZE = 20 * 1024 * 1024;
export const getClipboardFiles = (clipboardData: DataTransfer | null): File[] => clipboardData ? Array.from(clipboardData.files) : [];
export const parseJsonField = <T,>(value: string, fieldName: string): T | undefined => { if (!value.trim()) return undefined; try { return JSON.parse(value) as T; } catch { throw new Error(`${fieldName} 必须是有效 JSON`); } };
export const getContentText = (content: string | ContentPart[]): string => typeof content === 'string' ? content : content.filter(part => part.type === 'text').map(part => part.text || '').join('');
export const formatFileSize = (bytes: number): string => bytes < 1024 ? `${bytes} B` : bytes < 1024 * 1024 ? `${(bytes / 1024).toFixed(1)} KB` : `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
export const getFileIcon = (contentType: string): string => contentType.startsWith('image/') ? 'image' : contentType.startsWith('video/') ? 'video' : 'file';
export const parseStopSequences = (value: string): string[] | undefined => { const items = value.split(/\r?\n/).map(item => item.trim()).filter(Boolean); return items.length ? items : undefined; };
export const extractAssistantText = (content: any): string => { if (typeof content === 'string') return content; if (Array.isArray(content)) return content.filter(item => item?.type === 'text').map(item => item.text || '').join(''); if (content && typeof content === 'object') return extractAssistantText(content.content || content.text || ''); return ''; };
export const formatTime = (value?: string): string => { if (!value) return ''; const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false }); };
export const formatJson = (value: any): string => { if (value == null) return ''; if (typeof value === 'string') { try { return JSON.stringify(JSON.parse(value), null, 2); } catch { return value; } } try { return JSON.stringify(value, null, 2); } catch { return String(value); } };
