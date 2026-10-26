'use client';

import { useMemo } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { historyRows } from '@/lib/canvas/history-view';
import { formatTime } from '@/lib/i18n/format';
import { activeLocale } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';
import { historyWasTruncated, useCanvasStore, type UndoEntry } from '@/store/canvas-store';

const NO_ENTRIES: UndoEntry[] = [];

/**
 * The history panel: one row per undo step (name and time). A click returns the canvas to that step;
 * the later steps turn grey and stay redoable until a new edit drops them.
 */
export function CanvasHistoryPanel({ sessionId }: { sessionId: string }) {
  const { schema, text } = useProductSchema();
  const undo = useCanvasStore((state) => state._undoStackBySession[sessionId] ?? NO_ENTRIES);
  const redo = useCanvasStore((state) => state._redoStackBySession[sessionId] ?? NO_ENTRIES);
  const goToHistory = useCanvasStore((state) => state.goToHistory);
  const rows = useMemo(() => historyRows(undo, redo, historyWasTruncated(sessionId)), [undo, redo, sessionId]);
  const names = schema.canvas.presentation.text;
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-testid="canvas-history-panel">
      <ol className="min-h-0 flex-1 overflow-y-auto px-1 py-1.5">
        {rows.map((row) => (
          <li key={row.position}>
            <button
              type="button"
              data-testid="history-row"
              data-state={row.state}
              data-position={row.position}
              aria-current={row.current ? 'step' : undefined}
              onClick={() => goToHistory(sessionId, row.position)}
              className={cn(
                'flex w-full items-center justify-between gap-2 rounded-lg px-2 py-1.5 text-left text-xs focus-visible:outline-2 focus-visible:outline-ring',
                row.current ? 'bg-primary/15 text-primary' : 'text-foreground hover:bg-muted',
                row.state === 'undone' && 'text-muted-foreground opacity-50',
              )}
            >
              <span className="truncate">{row.descriptionKey ? (names[row.descriptionKey] ?? row.descriptionKey) : text(row.truncated ? 'ui.canvas.history.earlier' : 'ui.canvas.history.initial')}</span>
              {row.timestamp !== null ? (
                <time className="shrink-0 tabular-nums text-[11px] text-muted-foreground" dateTime={new Date(row.timestamp).toISOString()}>
                  {formatTime(row.timestamp, activeLocale(schema))}
                </time>
              ) : null}
            </button>
          </li>
        ))}
      </ol>
      <p className="border-t border-border px-3 py-2 text-[11px] leading-4 text-muted-foreground" data-testid="history-note">
        {text('ui.canvas.history.note')}
      </p>
    </div>
  );
}
