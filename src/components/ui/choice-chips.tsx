'use client';

import { useId } from 'react';
import { cn } from '@/lib/utils';

/** One chip: the value it stands for and the text it shows. */
export interface ChoiceChipOption {
  value: string;
  label: string;
}

/** A short list of options as radio chips: one is always chosen, a click changes it. */
export function ChoiceChips({ label, options, current, disabled, onChange, id, describedBy }: {
  /** Accessible name of the group. */
  label: string;
  options: ChoiceChipOption[];
  current: string;
  disabled?: boolean;
  onChange: (value: string) => void;
  /** Id for the group element, so a Field's help text can describe it. */
  id?: string;
  describedBy?: string;
}) {
  const labelId = useId();
  return (
    <div id={id} role="radiogroup" aria-labelledby={labelId} aria-describedby={describedBy} className="flex flex-wrap gap-1">
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          role="radio"
          aria-checked={current === option.value}
          disabled={disabled}
          onClick={() => onChange(option.value)}
          className={cn(
            'h-7 rounded-md border px-2 text-xs tabular-nums transition-colors focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-50',
            current === option.value ? 'border-primary bg-primary/15 text-primary' : 'border-border bg-surface text-foreground hover:bg-panel-raised',
          )}
        >
          {option.label}
        </button>
      ))}
      <span id={labelId} hidden>{label}</span>
    </div>
  );
}
