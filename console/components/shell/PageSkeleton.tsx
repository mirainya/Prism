import React from 'react';

const SkeletonLine: React.FC<{ className: string }> = ({ className }) => (
  <div aria-hidden="true" className={`candy-skeleton rounded-md ${className}`} />
);

export const PageSkeleton: React.FC = () => (
  <div role="status" aria-label="页面加载中" className="relative space-y-4 overflow-hidden">
    <div className="absolute inset-x-0 top-0 h-1 overflow-hidden rounded-full bg-[var(--border-soft)]">
      <div className="candy-skeleton h-full w-2/5" />
    </div>

    <section className="border border-[var(--border-soft)] bg-[var(--surface-card)] px-4 py-4 pt-5 shadow-[var(--shadow-soft)]">
      <div className="flex items-center gap-3">
        <SkeletonLine className="h-10 w-10" />
        <div className="space-y-2">
          <SkeletonLine className="h-4 w-36" />
          <SkeletonLine className="h-3 w-52 max-w-[65vw]" />
        </div>
      </div>
    </section>

    <div className="grid gap-4 xl:grid-cols-2">
      {[0, 1].map(card => (
        <section key={card} className="border border-[var(--border-soft)] bg-[var(--surface-card)] p-4 shadow-[var(--shadow-soft)]">
          <div className="flex items-start justify-between gap-3">
            <div className="space-y-2">
              <SkeletonLine className="h-3 w-28" />
              <SkeletonLine className="h-5 w-44" />
            </div>
            <SkeletonLine className="h-8 w-16" />
          </div>
          <div className="mt-10 flex h-24 items-end gap-1.5 border-b border-[var(--border-soft)] px-1">
            {[45, 62, 82, 58, 78, 75, 61, 84, 57, 38].map((height, index) => (
              <div key={index} className="candy-skeleton flex-1 rounded-t-sm" style={{ height: `${height}%` }} />
            ))}
          </div>
        </section>
      ))}
    </div>

    <section className="border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-[var(--shadow-soft)]">
      <div className="border-b border-[var(--border-soft)] px-4 py-4">
        <SkeletonLine className="h-4 w-44" />
      </div>
      <div className="divide-y divide-[var(--border-soft)] px-4">
        {[0, 1, 2, 3].map(row => (
          <div key={row} className="flex items-center gap-4 py-4">
            <SkeletonLine className="h-2 w-2 shrink-0 rounded-full" />
            <SkeletonLine className="h-3 flex-1" />
            <SkeletonLine className="h-3 w-24" />
          </div>
        ))}
      </div>
    </section>
  </div>
);
