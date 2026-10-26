import type { CanvasProject } from './browser-client';
import type { CurrentObject } from '@/store/workspace-store';
import type { VersionTile } from '@/lib/workspace/versions';
import { resultKey, type AutoJoinCandidate } from '@/lib/workspace/canvas-auto-join';

/** A create receipt is not a permanent property: the subsequent read must still be untouched. */
export function canRestoreFirstCanvasImage(
  creation: CanvasProject, project: CanvasProject, localNodeCount: number, localDirty: boolean,
): boolean {
  return creation.created === true && creation.id === project.id && creation.sessionId === project.sessionId &&
    typeof creation.revision === 'number' && creation.revision > 0 && creation.revision === project.revision &&
    Array.isArray(project.nodes) && project.nodes.length === 0 && localNodeCount === 0 && !localDirty;
}

/** Seed only the selected verified task image, never the rest of the session's history. */
export function firstCanvasImage(
  selection: CurrentObject | null, sessionId: string, tiles: readonly VersionTile[],
): AutoJoinCandidate | null {
  if (!selection || selection.restored !== true || selection.kind !== 'image' || selection.sessionId !== sessionId) return null;
  for (const tile of tiles) {
    // An inpaint task's raw picture is never a first-open layer, with or without a local job: its result
    // only ever lands through the patch job, and a retry keeps the server's frozen `inpaint` operation.
    if (tile.domain !== 'image' || tile.status !== 'ready' || tile.origin !== 'task' || tile.operation === 'inpaint') continue;
    const asset = tile.assets.find((item) => item.kind === 'image' && item.url &&
      (selection.assetId ? item.id === selection.assetId : item.url === selection.url) &&
      (tile.slots.length > 0
        ? tile.slots.some((slot) => slot.taskId === selection.taskId && slot.assetId === item.id)
        : tile.taskId === selection.taskId));
    // Auto-join registers group assets under the representative task; the placed node still belongs to
    // the selected slot's actual source task, just like an explicit version selection.
    if (asset) return { key: resultKey(tile.taskId, asset.id), taskId: selection.taskId, url: asset.url };
  }
  return null;
}
