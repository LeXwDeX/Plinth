import type { CanvasNode, CanvasSize } from '@/store/canvas-store';
import { samePixels } from './artboard';
import { computeNodesHash } from './node-persistence';

/** The parts of the canvas store a board save writes besides the nodes. */
export interface BoardSaveState {
  canvasSizeBySession: Record<string, CanvasSize>;
  _canvasSizeDirtyBySession: Record<string, boolean>;
  placedResultsBySession: Record<string, string[] | null>;
  _placedDirtyBySession: Record<string, boolean>;
}

/**
 * What the board's autosave effect depends on besides the nodes (#246). The dirty flags alone are not enough:
 * a second size (or registration) picked while the first is still unsaved leaves them `true`, so the effect
 * would not run again and the newer value would wait for an unrelated node edit. The current size and the
 * placed-results list itself change with every pick. Compare element-wise (`useShallow`).
 */
export function boardAutosaveTrigger(state: BoardSaveState, sessionID: string):
  readonly [boolean, boolean, number, number, string[] | null | undefined] {
  const size = state.canvasSizeBySession[sessionID];
  return [
    state._placedDirtyBySession[sessionID] === true,
    state._canvasSizeDirtyBySession[sessionID] === true,
    size?.width ?? 0,
    size?.height ?? 0,
    state.placedResultsBySession[sessionID],
  ];
}

/** What a board save sent. */
export interface BoardSaveRequest {
  nodesHash: string;
  size: CanvasSize | undefined;
  placed: string[] | null | undefined;
}

/**
 * After a 409 the server's canvas replaces the local one only while the local canvas is still exactly what
 * the failed save sent. A node edit, size or registration made since is newer than that request: it keeps its
 * own pending save, which meets the conflict itself.
 */
export function conflictReloadApplies(
  state: BoardSaveState & { getNodes: (sessionID: string) => CanvasNode[] },
  sessionID: string,
  request: BoardSaveRequest,
): boolean {
  const size = state.canvasSizeBySession[sessionID];
  return computeNodesHash(state.getNodes(sessionID)) === request.nodesHash &&
    (size === request.size || samePixels(size, request.size)) &&
    state.placedResultsBySession[sessionID] === request.placed;
}
