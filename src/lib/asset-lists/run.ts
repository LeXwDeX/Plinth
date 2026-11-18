import type { ProductSchema } from '@/lib/product-schema';
import { formatSchemaText, schemaText } from '@/lib/product-schema';

import type { AssetListRun } from './client';

/**
 * #328: the browser's reading of a run summary. The status and pause reason are
 * the server's wire spelling; the copy comes from `ui.asset_list.run_*` and the
 * counts are shown as the server counted them, never re-derived from items.
 */

/** stopped and completed end automatic production; the controls stay hidden then. */
export function runIsTerminal(run: AssetListRun | null | undefined): boolean {
  return run?.status === 'stopped' || run?.status === 'completed';
}

/** The run status line: running, the pause reason, stopped, completed or idle (waiting). */
export function runStatusText(schema: ProductSchema, run: AssetListRun): string {
  const key = (() => {
    switch (run.status) {
      case 'running': return 'ui.asset_list.run_running';
      case 'stopped': return 'ui.asset_list.run_stopped';
      case 'completed': return 'ui.asset_list.run_completed';
      case 'idle': return 'ui.asset_list.run_waiting';
      case 'paused':
        switch (run.pause_reason) {
          case 'presence': return 'ui.asset_list.run_paused_presence';
          case 'step_cap': return 'ui.asset_list.run_paused_step_cap';
          case 'failure': return 'ui.asset_list.run_paused_failure';
          default: return 'ui.asset_list.run_paused_user';
        }
      default: return '';
    }
  })();
  return key ? schemaText(schema, key) : run.status;
}

/**
 * The progress line. The counts partition the list: done is accepted + skipped,
 * attention is returned + failed (the person has to act on both), and todo,
 * working and review are named in their own placeholder.
 */
export function runProgressText(schema: ProductSchema, run: AssetListRun): string {
  const counts = run.item_counts;
  const total = counts.todo + counts.working + counts.review + counts.accepted
    + counts.returned + counts.failed + counts.skipped;
  return formatSchemaText(schemaText(schema, 'ui.asset_list.run_progress'), {
    done: counts.accepted + counts.skipped,
    total,
    working: counts.working,
    review: counts.review,
    attention: counts.returned + counts.failed,
  });
}