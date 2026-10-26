/**
 * Keeps the workspace's current object and the pinned canvas reference honest when canvas nodes disappear
 * (undo, delete, a conflict reload). Pure: given the nodes before and after, say what to change.
 *
 *  - A merge result that vanished goes back to the picture it was merged from (undoing a merge makes
 *    the base the working picture again, and the next turn's reference is the base).
 *  - A pinned reference to a picture that left the canvas is released.
 *  - Any other current object that left the canvas is left alone: undo or the version strip brings it
 *    back, and the chat's "已选用" record is a server fact that is not touched.
 */
import type { CanvasNode } from '@/store/canvas-store';
import type { CurrentObject } from '@/store/workspace-store';
import { MERGED_OBJECT_PREFIX } from './merge-version';

export interface ObjectSyncInput {
  previous: readonly CanvasNode[];
  next: readonly CanvasNode[];
  currentObject: Pick<CurrentObject, 'taskId' | 'kind' | 'url'> | null;
  canvasReference: string | null;
  /** The object a merge result was made from, by the merge result's object id. */
  mergeBases: Readonly<Record<string, CurrentObject>>;
}

export interface ObjectSyncResult {
  /** Make this the current object again (the base of an undone merge). */
  restoreObject: CurrentObject | null;
  /** Release the pinned canvas reference. */
  clearReference: boolean;
}

export function reconcileObjectWithNodes(input: ObjectSyncInput): ObjectSyncResult {
  const result: ObjectSyncResult = { restoreObject: null, clearReference: false };
  const remaining = new Set(input.next.map((node) => node.id));
  const vanished = input.previous.filter((node) => !remaining.has(node.id));
  if (vanished.length === 0) return result;
  const stillShown = (url: string) => input.next.some((node) => node.type === 'image' && node.imageUrl === url);
  if (input.canvasReference && vanished.some((node) => node.imageUrl === input.canvasReference) && !stillShown(input.canvasReference)) {
    result.clearReference = true;
  }
  const object = input.currentObject;
  if (object?.kind === 'image' && object.taskId.startsWith(MERGED_OBJECT_PREFIX)) {
    const nodeId = object.taskId.slice(MERGED_OBJECT_PREFIX.length);
    if (vanished.some((node) => node.id === nodeId)) {
      const base = input.mergeBases[object.taskId];
      if (base) result.restoreObject = base;
      if (input.canvasReference === object.url) result.clearReference = true;
    }
  }
  return result;
}
