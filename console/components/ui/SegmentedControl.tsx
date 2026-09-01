export interface SegmentedOption<T extends string = string> {
  label: string
  value: T
}

interface SegmentedControlProps<T extends string> {
  options: SegmentedOption<T>[]
  value: T
  onChange: (value: T) => void
  ariaLabel: string
  className?: string
}

export const SegmentedControl = <T extends string,>({
  options,
  value,
  onChange,
  ariaLabel,
  className = '',
}: SegmentedControlProps<T>) => (
  <div
    role="radiogroup"
    aria-label={ariaLabel}
    className={`grid gap-1 rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] p-1 ${className}`}
    style={{ gridTemplateColumns: `repeat(${options.length}, minmax(0, 1fr))` }}
  >
    {options.map(option => {
      const selected = option.value === value
      return (
        <button
          key={option.value}
          type="button"
          role="radio"
          aria-checked={selected}
          onClick={() => onChange(option.value)}
          className={`min-h-8 rounded-md px-2 text-xs font-medium transition-colors ${selected
            ? 'bg-[var(--surface-card)] text-[var(--primary)] shadow-sm'
            : 'text-[var(--text-secondary)] hover:bg-[var(--surface-card)]/70 hover:text-[var(--text-primary)]'}`}
        >
          {option.label}
        </button>
      )
    })}
  </div>
)
