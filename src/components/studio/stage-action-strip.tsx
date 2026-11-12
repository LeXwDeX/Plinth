'use client';

import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { ChevronLeft, ChevronRight } from 'lucide-react';

import { cn } from '@/lib/utils';

/**
 * The stage header's action row. It takes the room the identity group leaves and scrolls sideways inside the
 * header when the actions do not fit (no scrollbar drawn). An arrow button appears at each end that has hidden
 * actions and pages the row by most of its width; it disappears once that end is reached.
 */
export function StageActionStrip({
  children, previousLabel, nextLabel,
}: { children: ReactNode; previousLabel: string; nextLabel: string }) {
  const rowRef = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState({ previous: false, next: false });

  const measure = useCallback(() => {
    const row = rowRef.current;
    if (!row) return;
    const previous = row.scrollLeft > 1;
    const next = row.scrollLeft + row.clientWidth < row.scrollWidth - 1;
    setEdges((current) => (current.previous === previous && current.next === next ? current : { previous, next }));
  }, []);

  useEffect(() => {
    const row = rowRef.current;
    if (!row) return undefined;
    measure();
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure);
    observer?.observe(row);
    for (const child of Array.from(row.children)) observer?.observe(child);
    return () => observer?.disconnect();
  }, [measure, children]);

  const page = (direction: -1 | 1) => {
    const row = rowRef.current;
    if (!row) return;
    row.scrollBy({ left: direction * Math.max(80, row.clientWidth * 0.7), behavior: 'smooth' });
  };

  const arrow = 'flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring';
  return (
    <div className="flex min-w-0 flex-1 items-center justify-end" data-testid="stage-action-strip">
      {edges.previous ? (
        <button type="button" className={arrow} aria-label={previousLabel} title={previousLabel} onClick={() => page(-1)} data-testid="stage-actions-previous">
          <ChevronLeft aria-hidden className="size-4" />
        </button>
      ) : null}
      <div
        ref={rowRef}
        onScroll={measure}
        className="flex min-w-0 items-center overflow-x-auto overflow-y-hidden [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
        data-testid="stage-actions"
      >
        {children}
      </div>
      {edges.next ? (
        <button type="button" className={cn(arrow)} aria-label={nextLabel} title={nextLabel} onClick={() => page(1)} data-testid="stage-actions-next">
          <ChevronRight aria-hidden className="size-4" />
        </button>
      ) : null}
    </div>
  );
}
