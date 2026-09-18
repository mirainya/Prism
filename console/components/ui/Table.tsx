import React from 'react';

// 控制台里的表格此前全是手写的 thead/tbody，二十来处各自维护一份表头底色、
// 行分隔线、hover 态和空状态文案，改一处样式改不动其余十九处。
//
// 这里把 UnifiedTable.tsx 里那套已经成型的样式上提为组件。故意保持最小：
// 列定义 + 行 + 空状态，不做排序、选择、粘性表头——那些目前没有一处在用，
// 加了就是为假想需求付费。
export interface TableColumn<T> {
  /** 列标题。为空字符串时表头留白（操作列常用）。 */
  header: React.ReactNode;
  render: (row: T, index: number) => React.ReactNode;
  /** 附加到该列 td 上的类名，用于对齐或限宽。 */
  className?: string;
  /** 单元格内容允许换行。默认不换行，与原有表格一致。 */
  wrap?: boolean;
}

interface TableProps<T> {
  columns: TableColumn<T>[];
  rows: T[];
  rowKey: (row: T, index: number) => React.Key;
  /** 无数据时整行展示的内容，默认「暂无记录」。 */
  empty?: React.ReactNode;
  /** 横向滚动阈值，例如 "720px"。不给则不设最小宽度。 */
  minWidth?: string;
  rowClassName?: (row: T) => string;
  /** 传 true 时给容器加 aria-busy，供读屏器与测试识别加载态。 */
  busy?: boolean;
  'aria-label'?: string;
}

export function Table<T>({ columns, rows, rowKey, empty = '暂无记录', minWidth, rowClassName, busy, ...rest }: TableProps<T>) {
  return <div className="overflow-x-auto" aria-busy={busy}>
    <table className="w-full text-left text-sm" style={minWidth ? { minWidth } : undefined} aria-label={rest['aria-label']}>
      <thead>
        <tr className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)]/50 text-xs text-[var(--text-secondary)]">
          {columns.map((column, index) => <th key={index} className="whitespace-nowrap px-4 py-3 font-semibold">{column.header}</th>)}
        </tr>
      </thead>
      <tbody className="divide-y divide-[var(--border-soft)]">
        {rows.length === 0
          ? <tr><td colSpan={columns.length} className="h-64 text-center text-sm text-[var(--text-secondary)]">{empty}</td></tr>
          : rows.map((row, rowIndex) => <tr key={rowKey(row, rowIndex)} className={`transition-colors hover:bg-[var(--surface-muted)]/50 ${rowClassName?.(row) || ''}`}>
            {columns.map((column, index) => <td key={index} className={`px-4 py-3 text-[var(--text-primary)] ${column.className || ''}`}>
              <div className={`flex min-h-8 ${column.wrap ? 'flex-col justify-center' : 'items-center whitespace-nowrap'}`}>{column.render(row, rowIndex)}</div>
            </td>)}
          </tr>)}
      </tbody>
    </table>
  </div>;
}
