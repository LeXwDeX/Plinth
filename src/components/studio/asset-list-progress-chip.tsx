'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import { useAssetListRun } from '@/hooks/use-asset-list-run';
import type { AssetListClient } from '@/lib/asset-lists/client';
import { runProgressText, runStatusText } from '@/lib/asset-lists/run';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { useProjectStore } from '@/store/project-store';
import { useStudioStore } from '@/store/studio-store';

export interface AssetListProgressChipProps {
  /** Test seam; the studio chip follows the open session's project. */
  projectId?: string | null;
  /** Test seam; the real chip builds its client from the Schema. */
  client?: AssetListClient;
  /** Test seam; polling defaults to the Schema's task polling interval. */
  intervalMs?: number;
}

/**
 * #328: the studio top bar's progress chip. It follows the newest confirmed
 * list of the open project and shows its run summary; clicking opens the
 * project tab of the right panel, where the same card offers the controls.
 * Nothing is drawn without a project or a run.
 */
export function AssetListProgressChip({ projectId, client, intervalMs }: AssetListProgressChipProps = {}) {
  const { schema } = useProductSchema();
  const storedProjectId = useProjectStore((state) => state.activeProjectId);
  const setPanelTab = useStudioStore((state) => state.setPanelTab);
  const activeProjectId = projectId !== undefined ? projectId : storedProjectId;
  const { run } = useAssetListRun({
    projectId: activeProjectId,
    client,
    intervalMs,
    enabled: Boolean(schema.asset_list),
  });
  if (!schema.asset_list || !run) return null;
  const status = runStatusText(schema, run);
  const progress = runProgressText(schema, run);
  const label = `${status} · ${progress}`;
  const locate = () => {
    // The project tab is offered by both families; setting both also expands a
    // folded panel, so one click lands on the card from any stage context.
    setPanelTab('board', 'project');
    setPanelTab('model', 'project');
    if (typeof document !== 'undefined') {
      requestAnimationFrame(() => {
        document.querySelector('[data-testid="project-panel"]')?.scrollIntoView({ block: 'nearest' });
      });
    }
  };
  return (
    <button
      type="button"
      onClick={locate}
      aria-label={formatSchemaText(schemaText(schema, 'ui.common.label_value'), {
        label: schemaText(schema, 'ui.asset_list.run_status'),
        value: label,
      })}
      className="flex max-w-72 items-center gap-1.5 truncate rounded-full border border-input bg-panel-raised px-2 py-1 text-[11px] transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-ring"
      data-testid="asset-list-progress-chip"
      data-status={run.status}
    >
      <span className="shrink-0 font-medium" data-testid="asset-list-progress-chip-status">{status}</span>
      <span className="truncate tabular-nums text-muted-foreground" data-testid="asset-list-progress-chip-progress">
        {progress}
      </span>
    </button>
  );
}