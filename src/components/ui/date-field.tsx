'use client';

import { useMemo, useState } from 'react';
import { Popover } from '@base-ui/react/popover';
import { CalendarDays, ChevronLeft, ChevronRight } from 'lucide-react';

import { useAnchoredPositioner } from '@/components/ui/overlay';
import { cn } from '@/lib/utils';
import { addMonths, buildMonthGrid, formatLocalDate, parseDate } from './date-field-math';

const WEEK_START = 1;

const MONTH_BUTTON = 'inline-flex size-7 items-center justify-center rounded-md text-foreground hover:bg-accent focus-visible:outline-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-40';

export interface DateFieldLabels {
  placeholder: string;
  previousMonth: string;
  nextMonth: string;
  today: string;
  clear: string;
}

export interface DateFieldProps {
  /** Fixed UI text; the schema-aware wrapper in components/form supplies it. */
  labels: DateFieldLabels;
  /** BCP 47 locale for the month title and weekday names (the UI locale). */
  locale: string;
  /** 'YYYY-MM-DD', or '' for no date. */
  value: string;
  onValueChange: (value: string) => void;
  /** Inclusive bounds, 'YYYY-MM-DD'. */
  min?: string;
  max?: string;
  /** Shows a clear action; the field may then hold ''. */
  clearable?: boolean;
  disabled?: boolean;
  className?: string;
  id?: string;
  'aria-describedby'?: string;
  'aria-invalid'?: boolean;
  'aria-required'?: boolean;
}

/** In-page date picker (month grid in a popover) replacing the browser's native date input. */
export function DateField({
  labels, locale, value, onValueChange, min, max, clearable = true, disabled = false, className,
  id, 'aria-describedby': describedBy, 'aria-invalid': invalid,
}: DateFieldProps) {
  const layer = useAnchoredPositioner();
  const [open, setOpen] = useState(false);
  const selected = parseDate(value);
  const [view, setView] = useState(() => viewOf(value));

  const grid = useMemo(() => buildMonthGrid(view.year, view.month, WEEK_START), [view]);
  const monthTitle = useMemo(() => new Intl.DateTimeFormat(locale, { year: 'numeric', month: 'long' }), [locale]);
  const weekdays = useMemo(() => {
    const weekdayName = new Intl.DateTimeFormat(locale, { weekday: 'narrow' });
    // 2024-01-01 is a Monday; offset from it so the header matches WEEK_START.
    return Array.from({ length: 7 }, (_, index) => weekdayName.format(new Date(2024, 0, 1 + ((index + WEEK_START - 1) % 7))));
  }, [locale]);
  const today = formatLocalDate(new Date());
  const outOfRange = (day: string) => Boolean((min && day < min) || (max && day > max));

  const pick = (day: string) => {
    onValueChange(day);
    setOpen(false);
  };

  // One wrapper keeps the popover's focus guards out of the Field's spacing.
  return (
    <div>
      <Popover.Root
        open={open}
        onOpenChange={(next) => {
          if (next) setView(viewOf(value));
          setOpen(next);
        }}
      >
        <Popover.Trigger
          id={id}
          disabled={disabled}
          aria-describedby={describedBy}
          aria-invalid={invalid}
          className={cn(
            'flex h-8 w-full min-w-0 items-center gap-2 rounded-lg border border-input bg-transparent px-2.5 py-1 text-left text-base transition-colors outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive md:text-sm dark:bg-input/30',
            className,
          )}
        >
          <span className={cn('flex-1 truncate', !selected && 'text-muted-foreground')}>
            {selected ? value : labels.placeholder}
          </span>
          <CalendarDays aria-hidden className="size-4 shrink-0 text-muted-foreground" />
        </Popover.Trigger>
        <Popover.Portal>
          <Popover.Positioner side="bottom" align="start" sideOffset={6} collisionPadding={12} className={layer}>
            <Popover.Popup className="w-64 rounded-lg bg-popover p-2 text-popover-foreground shadow-xl outline-none ring-1 ring-foreground/10">
              <div className="mb-2 flex items-center justify-between">
                <button
                  type="button"
                  className={MONTH_BUTTON}
                  aria-label={labels.previousMonth}
                  onClick={() => setView((current) => addMonths(current.year, current.month, -1))}
                >
                  <ChevronLeft aria-hidden className="size-4" />
                </button>
                <span className="text-sm font-medium" aria-live="polite">
                  {monthTitle.format(new Date(view.year, view.month - 1, 1))}
                </span>
                <button
                  type="button"
                  className={MONTH_BUTTON}
                  aria-label={labels.nextMonth}
                  onClick={() => setView((current) => addMonths(current.year, current.month, 1))}
                >
                  <ChevronRight aria-hidden className="size-4" />
                </button>
              </div>
              <div className="grid grid-cols-7 gap-0.5 text-center text-xs text-muted-foreground">
                {weekdays.map((name, index) => <span key={index} className="py-1">{name}</span>)}
              </div>
              <div className="grid grid-cols-7 gap-0.5">
                {grid.map((cell) => {
                  const isSelected = cell.value === value;
                  return (
                    <button
                      key={cell.value}
                      type="button"
                      disabled={outOfRange(cell.value)}
                      aria-label={cell.value}
                      aria-pressed={isSelected}
                      aria-current={cell.value === today ? 'date' : undefined}
                      onClick={() => pick(cell.value)}
                      className={cn(
                        'h-8 rounded-md text-sm tabular-nums hover:bg-accent focus-visible:outline-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-30',
                        !cell.inMonth && 'text-muted-foreground/60',
                        cell.value === today && !isSelected && 'ring-1 ring-primary/60',
                        isSelected && 'bg-primary text-primary-foreground hover:bg-primary/90',
                      )}
                    >
                      {cell.day}
                    </button>
                  );
                })}
              </div>
              <div className="mt-2 flex items-center justify-between">
                <button
                  type="button"
                  className="rounded-md px-2 py-1 text-xs text-primary hover:bg-accent focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-40"
                  disabled={outOfRange(today)}
                  onClick={() => pick(today)}
                >
                  {labels.today}
                </button>
                {clearable ? (
                  <button
                    type="button"
                    className="rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent focus-visible:outline-2 focus-visible:outline-ring"
                    onClick={() => pick('')}
                  >
                    {labels.clear}
                  </button>
                ) : null}
              </div>
            </Popover.Popup>
          </Popover.Positioner>
        </Popover.Portal>
      </Popover.Root>
    </div>
  );
}

function viewOf(value: string): { year: number; month: number } {
  const parsed = parseDate(value);
  if (parsed) return { year: parsed.year, month: parsed.month };
  const now = new Date();
  return { year: now.getFullYear(), month: now.getMonth() + 1 };
}
