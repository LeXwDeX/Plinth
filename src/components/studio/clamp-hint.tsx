'use client';

import type { ReactNode } from 'react';

import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useClampedText } from '@/hooks/use-clamped-text';
import { cn } from '@/lib/utils';

/**
 * #546: a control whose text is clamped to a few lines (a choice option's description, a queued message) shows the
 * whole text as its hover hint, and on keyboard focus, only when some of it is cut. Mark the clamped text inside with
 * `data-clamp`. The hint never takes the focus or the pointer (`passive`), so clicking and arrow keys work as before;
 * a touch screen without hover keeps the clamped text as it was.
 */
export function ClampHint({ name, hint, children, className }: { name: string; hint?: string; children: ReactNode; className?: string }) {
  const { ref, clamped, measure } = useClampedText<HTMLSpanElement>();
  return (
    <HintTooltip name={name} hint={hint} suppressed={!clamped} passive className={cn('min-w-0', className)} triggerProps={{ onPointerEnter: measure, onFocus: measure }}>
      <span ref={ref} className="flex w-full min-w-0">{children}</span>
    </HintTooltip>
  );
}
