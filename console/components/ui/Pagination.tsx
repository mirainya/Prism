import React from 'react';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { Select } from './Select';

export type PaginationItem = number | 'start-ellipsis' | 'end-ellipsis';

export const buildPaginationItems = (page: number, totalPages: number): PaginationItem[] => {
  const count = Math.max(1, Math.floor(totalPages));
  const current = Math.min(count, Math.max(1, Math.floor(page)));
  if (count <= 7) return Array.from({ length: count }, (_, index) => index + 1);
  if (current <= 4) return [1, 2, 3, 4, 5, 'end-ellipsis', count];
  if (current >= count - 3) return [1, 'start-ellipsis', count - 4, count - 3, count - 2, count - 1, count];
  return [1, 'start-ellipsis', current - 1, current, current + 1, 'end-ellipsis', count];
};

const DEFAULT_PAGE_SIZE_OPTIONS = [10, 20, 50, 100];

export const Pagination: React.FC<{
  page: number;
  pageSize: number;
  total: number;
  loading?: boolean;
  pageSizeOptions?: number[];
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: number) => void;
  className?: string;
}> = ({
  page,
  pageSize,
  total,
  loading = false,
  pageSizeOptions = DEFAULT_PAGE_SIZE_OPTIONS,
  onPageChange,
  onPageSizeChange,
  className = '',
}) => {
  const safeTotal = Math.max(0, total || 0);
  const totalPages = Math.max(1, Math.ceil(safeTotal / pageSize));
  const currentPage = Math.min(totalPages, Math.max(1, page));
  const firstItem = safeTotal === 0 ? 0 : (currentPage - 1) * pageSize + 1;
  const lastItem = safeTotal === 0 ? 0 : Math.min(safeTotal, currentPage * pageSize);
  const items = buildPaginationItems(currentPage, totalPages);

  const changePage = (nextPage: number) => {
    if (loading || nextPage === currentPage || nextPage < 1 || nextPage > totalPages) return;
    onPageChange(nextPage);
  };

  return (
    <nav aria-label="分页" className={`flex min-w-0 flex-wrap items-center justify-between gap-3 border-t border-[var(--border-soft)] px-4 py-3 ${className}`}>
      <div className="min-w-0 text-sm text-[var(--text-secondary)]">
        第 {firstItem}-{lastItem} 条，共 {safeTotal} 条
      </div>

      <div className="flex min-w-0 max-w-full flex-wrap items-center justify-end gap-3 overflow-x-auto pb-0.5">
        <div className="flex items-center gap-2 text-sm text-[var(--text-secondary)]">
          <span className="whitespace-nowrap">每页</span>
          <Select
            value={String(pageSize)}
            onChange={value => onPageSizeChange(Number(value))}
            disabled={loading}
            className="w-[92px]"
            options={pageSizeOptions.map(value => ({ label: `${value} 条`, value: String(value) }))}
          />
        </div>

        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => changePage(currentPage - 1)}
            disabled={loading || currentPage <= 1}
            className="inline-flex h-9 items-center gap-1 rounded-lg border border-[var(--border-soft)] px-2.5 text-sm font-medium text-[var(--text-primary)] hover:bg-[var(--surface)] disabled:cursor-not-allowed disabled:opacity-40"
            aria-label="上一页"
          >
            <ChevronLeft size={16} /><span className="hidden sm:inline">上一页</span>
          </button>

          {items.map(item => typeof item === 'number' ? (
            <button
              key={item}
              type="button"
              onClick={() => changePage(item)}
              disabled={loading}
              aria-label={`第 ${item} 页`}
              aria-current={item === currentPage ? 'page' : undefined}
              className={`h-9 min-w-9 rounded-lg border px-2 text-sm font-semibold transition ${item === currentPage ? 'border-[var(--primary)] bg-[var(--primary)] text-white' : 'border-[var(--border-soft)] text-[var(--text-primary)] hover:bg-[var(--surface)]'} disabled:cursor-not-allowed disabled:opacity-50`}
            >
              {item}
            </button>
          ) : (
            <span key={item} className="flex h-9 min-w-7 items-center justify-center text-sm text-[var(--text-secondary)]" aria-hidden="true">...</span>
          ))}

          <button
            type="button"
            onClick={() => changePage(currentPage + 1)}
            disabled={loading || currentPage >= totalPages}
            className="inline-flex h-9 items-center gap-1 rounded-lg border border-[var(--border-soft)] px-2.5 text-sm font-medium text-[var(--text-primary)] hover:bg-[var(--surface)] disabled:cursor-not-allowed disabled:opacity-40"
            aria-label="下一页"
          >
            <span className="hidden sm:inline">下一页</span><ChevronRight size={16} />
          </button>
        </div>
      </div>
    </nav>
  );
};
