'use client';

import { useRef, useState, type DragEvent, type ReactNode } from 'react';
import { X } from 'lucide-react';

import { HintTooltip, type HintContent } from '@/components/ui/hint-tooltip';
import { cn } from '@/lib/utils';

/**
 * One chip above the composer. Every kind (current object, mask, branch, parameters,
 * attachment, Skill, "not this turn") is the same size: 40px high, a 32px square on the
 * left (thumbnail or icon), a name over a short description, and the remove button at the
 * right. `accent` marks what is active for the next turn; a grey chip is dropped or overridden.
 *
 * #405: a chip takes its width from its content and is never wider than half of its row (less half of the 6px gap
 * between chips), so two sit side by side and more wrap onto the next line. A description that only repeats the
 * name is left out (one line, centred); text that does not fit is cut, and hovering the chip shows all of it.
 */
export const CHIP_HEIGHT_CLASS = 'h-10';

/** The widest a chip gets: half of its row, so that two chips share a line. */
export const CHIP_WIDTH_CAP_CLASS = 'max-w-[calc(50%-3px)]';

export interface ContextChipProps {
  /** What goes in the 32px square: a thumbnail, an icon, or a drawing. */
  square: ReactNode;
  title: ReactNode;
  /** The title as plain text, when `title` is not a string (a name with a kind badge); the hover text and the merge of a repeated second line use it. */
  label?: string;
  subtitle?: ReactNode;
  accent?: boolean;
  /** Greyed out: the chip does not apply to the next turn. */
  muted?: boolean;
  remove?: { label: string; onClick: () => void; icon?: ReactNode; pressed?: boolean; testId?: string };
  testId?: string;
  /** The hover hint of the whole chip (what it does to the next turn). */
  hint?: HintContent;
  data?: Record<string, string | number | boolean | undefined>;
  /** Makes the picture and name a button (the attachment preview). */
  open?: { label: string; onClick: () => void; testId?: string };
  /** Briefly highlighted: the person tried to add what this chip already is. */
  flash?: boolean;
  /** Makes the chip a drag source (an attachment dragged onto the canvas). */
  onDragStart?: (event: DragEvent<HTMLElement>) => void;
}

const FRAME = 'flex max-w-full items-center gap-1.5 rounded-md border py-[3px] pl-[3px] pr-1 text-xs';

export function chipFrameClass(accent: boolean, muted: boolean): string {
  return cn(
    CHIP_HEIGHT_CLASS, FRAME,
    accent && !muted ? 'border-primary/40 bg-primary/10 text-foreground' : 'border-border bg-surface text-muted-foreground',
    muted && 'opacity-70',
  );
}

/** The 32px square. */
export function ChipSquare({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span className={cn('relative flex size-8 shrink-0 items-center justify-center overflow-hidden rounded-sm border border-border bg-background text-muted-foreground', className)}>
      {children}
    </span>
  );
}

/**
 * Two text lines: the name, then a short description. `single` leaves the description out (it only repeated the
 * name); the one line is centred in the chip by its frame.
 */
export function ChipText({ title, subtitle, accent = false, single = false }: { title: ReactNode; subtitle?: ReactNode; accent?: boolean; single?: boolean }) {
  return (
    <span className="flex min-w-0 flex-col gap-px text-left leading-tight">
      <span className="truncate font-medium text-foreground">{title}</span>
      {single ? null : <span className={cn('truncate text-[11px]', accent ? 'text-foreground' : 'text-muted-foreground')}>{subtitle ?? ' '}</span>}
    </span>
  );
}

/**
 * What a chip says: its description (null when it only repeats the name, or is empty) and everything as one string
 * for the hover text. The description repeats the name when it is the name's own ending, as a whole word (the
 * "画布" under "当前：画布"); a Skill called "ill" is not a repeat of "Skill".
 */
export function chipLines(title: string, subtitle: string | undefined): { subtitle: string | null; full: string } {
  const second = (subtitle ?? '').trim();
  const before = title.slice(0, Math.max(0, title.length - second.length)).slice(-1);
  const repeated = second === '' || title === second || (title.endsWith(second) && !/[\p{L}\p{N}]/u.test(before));
  return { subtitle: repeated ? null : second, full: repeated ? title : `${title} · ${second}` };
}

export function ChipRemove({ label, onClick, icon, pressed, testId }: NonNullable<ContextChipProps['remove']>) {
  return (
    <HintTooltip name={label} className="shrink-0">
      <button
        type="button"
        onClick={onClick}
        aria-pressed={pressed}
        aria-label={label}
        data-testid={testId}
        className="flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
      >
        {icon ?? <X aria-hidden className="size-3" />}
      </button>
    </HintTooltip>
  );
}

export function ContextChip({ square, title, label, subtitle, accent = false, muted = false, remove, testId, hint, data, flash = false, open, onDragStart }: ContextChipProps) {
  const dataAttributes = Object.fromEntries(Object.entries(data ?? {}).filter(([, value]) => value !== undefined).map(([name, value]) => [`data-${name}`, String(value)]));
  const frameRef = useRef<HTMLSpanElement>(null);
  // Whether some of the text is cut off, read when the pointer or the keyboard arrives (only then can it have changed).
  const [cut, setCut] = useState(false);
  const measure = () => setCut(Array.from(frameRef.current?.querySelectorAll<HTMLElement>('.truncate') ?? []).some((node) => node.scrollWidth > node.clientWidth));
  const titleText = typeof title === 'string' ? title : label ?? '';
  const lines = chipLines(titleText, typeof subtitle === 'string' ? subtitle : undefined);
  // A description that only repeats the name is left out; a chip given none keeps its (blank) second line as before.
  const single = typeof subtitle === 'string' && subtitle.trim() !== '' && lines.subtitle === null;
  const reach = { onPointerEnter: measure, onFocus: measure };
  const wrapped = !open;
  const chip = (
    <span
      ref={frameRef}
      data-testid={testId}
      // The outermost element carries the cap; inside a hint wrapper the frame only fills the wrapper.
      className={cn(chipFrameClass(accent, muted), !wrapped && CHIP_WIDTH_CAP_CLASS, flash && 'animate-pulse ring-2 ring-primary')}
      data-flash={flash ? 'true' : undefined}
      {...dataAttributes}
      {...(onDragStart ? { draggable: true, onDragStart } : {})}
    >
      {open ? (
        <HintTooltip name={open.label} hint={cut && titleText ? lines.full : undefined} className="min-w-0" triggerProps={reach}>
          <button
            type="button" onClick={open.onClick} aria-label={open.label} data-testid={open.testId}
            className="flex min-w-0 items-center gap-1.5 rounded-sm text-left focus-visible:outline-2 focus-visible:outline-ring"
          >
            {square}
            <ChipText title={title} subtitle={subtitle} accent={accent && !muted} single={single} />
          </button>
        </HintTooltip>
      ) : (
        <>
          {square}
          <ChipText title={title} subtitle={subtitle} accent={accent && !muted} single={single} />
        </>
      )}
      {remove ? <ChipRemove {...remove} /> : null}
    </span>
  );
  if (open) return chip;
  const wrapperClass = cn('min-w-0', CHIP_WIDTH_CAP_CLASS);
  // A chip with its own hint keeps it (and gains the whole text when it is cut); any other chip shows the whole text, only when it is cut.
  if (hint) return <HintTooltip {...hint} hint={hint.hint ?? (cut && titleText ? lines.full : undefined)} className={wrapperClass} triggerProps={reach}>{chip}</HintTooltip>;
  return <HintTooltip name={titleText ? lines.full : ''} suppressed={!cut} className={wrapperClass} triggerProps={reach}>{chip}</HintTooltip>;
}
