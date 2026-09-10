import React from 'react';

export const configurationInputClass =
  'w-full min-w-0 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 py-2.5 text-sm text-[var(--text-primary)] outline-none focus:border-[var(--primary)]';

export const ConcurrencyField: React.FC<{
  label: string;
  value: string;
  pending: boolean;
  onChange: (value: string) => void;
}> = ({ label, value, pending, onChange }) => (
  <div className="space-y-2 text-sm font-semibold">
    <span>{label}</span>
    <label className="flex items-center gap-2">
      <input
        type="checkbox"
        aria-label={`${label}不限`}
        checked={value === ''}
        disabled={pending}
        onChange={(event) => onChange(event.target.checked ? '' : '1')}
        className="h-4 w-4 accent-[var(--primary)]"
      />
      <span className="text-xs font-normal">不限</span>
    </label>
    <input
      type="number"
      min={1}
      max={1000000}
      step={1}
      aria-label={label}
      className={configurationInputClass}
      value={value}
      disabled={pending || value === ''}
      onChange={(event) => onChange(event.target.value)}
    />
  </div>
);
