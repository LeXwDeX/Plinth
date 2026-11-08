'use client';

import { ChevronDown } from 'lucide-react';
import type { ReactNode } from 'react';

import { cn } from '@/lib/utils';

export interface CollapsibleSectionProps {
  /** The row's name; each section of the 功能 tab uses one. */
  title: ReactNode;
  /** The current state shown on the right of the row while it is closed. */
  summary?: ReactNode;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** id of the body the trigger points at (`aria-controls`). */
  id: string;
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
  'data-testid'?: string;
}

/**
 * One collapsible group of the right panel: a chevron row and its body. The 功能 tab is a
 * stack of these — 风格锁定, 功能, 执行服务, 来源 and 参数设置 all share this UE.
 */
export function CollapsibleSection({ title, summary, open, onOpenChange, id, children, className, bodyClassName, ...rest }: CollapsibleSectionProps) {
  return <section className={cn('shrink-0 border-b border-border', className)} data-testid={rest['data-testid']}>
    <button
      type="button"
      aria-expanded={open}
      aria-controls={id}
      onClick={() => onOpenChange(!open)}
      className="flex w-full items-center gap-1.5 px-3 py-2.5 text-left text-sm font-semibold text-foreground hover:bg-muted/50 focus-visible:outline-2 focus-visible:outline-ring"
    >
      <ChevronDown aria-hidden className={cn('size-4 shrink-0 transition-transform', !open && '-rotate-90')} />
      <span className="shrink-0">{title}</span>
      {summary ? <span className="ml-auto min-w-0 truncate text-xs font-normal text-muted-foreground">{summary}</span> : null}
    </button>
    <div id={id} hidden={!open} className={cn('border-t border-border', bodyClassName)}>{children}</div>
  </section>;
}