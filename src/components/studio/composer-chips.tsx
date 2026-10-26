'use client';

import type { DragEvent, ReactNode } from 'react';
import { X } from 'lucide-react';

import { HintTooltip, type HintContent } from '@/components/ui/hint-tooltip';
import { cn } from '@/lib/utils';

/**
 * One chip above the composer. Every kind (current object, mask, branch, parameters,
 * attachment, Skill, "not this turn") is the same size: 40px high, a 32px square on the
 * left (thumbnail or icon), a name over a short description, and the remove button at the
 * right. `accent` marks what is active for the next turn; a grey chip is dropped or overridden.
 */
export const CHIP_HEIGHT_CLASS = 'h-10';

export interface ContextChipProps {
  /** What goes in the 32px square: a thumbnail, an icon, or a drawing. */
  square: ReactNode;
  title: ReactNode;
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

const FRAME = 'flex max-w-full items-center gap-2 rounded-md border py-[3px] pl-[3px] pr-1 text-xs';

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

/** Two text lines: the name, then a short description. */
export function ChipText({ title, subtitle, accent = false }: { title: ReactNode; subtitle?: ReactNode; accent?: boolean }) {
  return (
    <span className="flex min-w-0 flex-col gap-px text-left leading-tight">
      <span className="truncate font-medium text-foreground">{title}</span>
      <span className={cn('truncate text-[11px]', accent ? 'text-foreground' : 'text-muted-foreground')}>{subtitle ?? ' '}</span>
    </span>
  );
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

export function ContextChip({ square, title, subtitle, accent = false, muted = false, remove, testId, hint, data, flash = false, open, onDragStart }: ContextChipProps) {
  const dataAttributes = Object.fromEntries(Object.entries(data ?? {}).filter(([, value]) => value !== undefined).map(([name, value]) => [`data-${name}`, String(value)]));
  const chip = (
    <span data-testid={testId} className={cn(chipFrameClass(accent, muted), flash && 'animate-pulse ring-2 ring-primary')} data-flash={flash ? 'true' : undefined} {...dataAttributes} {...(onDragStart ? { draggable: true, onDragStart } : {})}>
      {open ? (
        <HintTooltip name={open.label} className="min-w-0">
          <button
            type="button" onClick={open.onClick} aria-label={open.label} data-testid={open.testId}
            className="flex min-w-0 items-center gap-2 rounded-sm text-left focus-visible:outline-2 focus-visible:outline-ring"
          >
            {square}
            <ChipText title={title} subtitle={subtitle} accent={accent && !muted} />
          </button>
        </HintTooltip>
      ) : (
        <>
          {square}
          <ChipText title={title} subtitle={subtitle} accent={accent && !muted} />
        </>
      )}
      {remove ? <ChipRemove {...remove} /> : null}
    </span>
  );
  return hint ? <HintTooltip {...hint} className="max-w-full">{chip}</HintTooltip> : chip;
}
