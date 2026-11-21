import type { ProductSchema } from '@/lib/product-schema';

import type {
  AssetListBoard,
  AssetListBoardCheck,
  AssetListBoardItem,
  AssetListBoardRegion,
  AssetListBoardSteps,
} from './client';

/**
 * #329: the acceptance board's browser reading. The wire is the server's own
 * spelling; this module only groups the snapshot's checks, resolves which
 * asset a cell shows and names the filters. Nothing here re-derives a
 * judgment: unknown is shown as unknown, never as a pass.
 */

/** The board's filters, in the order the header shows them. */
export type AssetListBoardFilter = 'all' | 'review' | 'accepted' | 'returned' | 'failed' | 'skipped';

export const ASSET_LIST_BOARD_FILTERS: ReadonlyArray<{ id: AssetListBoardFilter; labelKey: string }> = [
  { id: 'all', labelKey: 'ui.asset_list.board.filter_all' },
  { id: 'review', labelKey: 'ui.asset_list.board.filter_review' },
  { id: 'accepted', labelKey: 'ui.asset_list.board.filter_accepted' },
  { id: 'returned', labelKey: 'ui.asset_list.board.filter_returned' },
  { id: 'failed', labelKey: 'ui.asset_list.board.filter_failed' },
  { id: 'skipped', labelKey: 'ui.asset_list.board.filter_skipped' },
];

/** The item states each filter keeps; `all` keeps every item. */
export function filterBoardItems(
  items: readonly AssetListBoardItem[],
  filter: AssetListBoardFilter,
): AssetListBoardItem[] {
  return filter === 'all' ? [...items] : items.filter((item) => item.state === filter);
}

export function boardFilterCount(
  items: readonly AssetListBoardItem[],
  filter: AssetListBoardFilter,
): number {
  return filterBoardItems(items, filter).length;
}

/** One item's acceptance criteria, grouped by the judgment they came from. */
export interface BoardCheckGroup {
  kind: '3d' | '2d';
  checks: AssetListBoardCheck[];
}

export function boardCheckGroups(item: AssetListBoardItem): BoardCheckGroup[] {
  const groups: BoardCheckGroup[] = [];
  const model = item.review?.model3d?.checks;
  if (Array.isArray(model) && model.length > 0) groups.push({ kind: '3d', checks: model });
  const image = item.review?.image2d?.checks;
  if (Array.isArray(image) && image.length > 0) groups.push({ kind: '2d', checks: image });
  return groups;
}

export interface BoardCheckCounts {
  pass: number;
  fail: number;
  unknown: number;
  total: number;
}

/** Counts every judged criterion of the item; unknown stays its own bucket. */
export function boardCheckCounts(item: AssetListBoardItem): BoardCheckCounts {
  const counts: BoardCheckCounts = { pass: 0, fail: 0, unknown: 0, total: 0 };
  for (const group of boardCheckGroups(item)) {
    for (const check of group.checks) {
      counts.total += 1;
      if (check.status === 'pass') counts.pass += 1;
      else if (check.status === 'fail') counts.fail += 1;
      else counts.unknown += 1;
    }
  }
  return counts;
}

/** The copy key of one judged status; anything not pass/fail reads as not judged. */
export function checkStatusKey(status: string): string {
  if (status === 'pass') return 'ui.asset_list.board.check_pass';
  if (status === 'fail') return 'ui.asset_list.board.check_fail';
  return 'ui.asset_list.board.check_unknown';
}

/** A measured value as one line; structured values are shown as their JSON. */
export function checkActualText(value: unknown): string {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return '';
  }
}

/** Whether the item plans or has taken any 3D step; a pure 2D item shows no step line. */
export function itemShowsSteps(item: AssetListBoardItem): boolean {
  if (item.planned_model3d_step_count > 0) return true;
  if ((item.steps?.used ?? 0) > 0) return true;
  return (item.review?.steps?.model3d ?? 0) > 0;
}

/** The item's step standing: the live asset reading first, then the review snapshot. */
export function boardSteps(item: AssetListBoardItem): AssetListBoardSteps | null {
  const live = item.steps;
  if (live && typeof live.used === 'number') {
    return {
      used: live.used,
      model3d: item.review?.steps?.model3d ?? 0,
      cap: live.cap,
      extra: live.extra,
      mode: live.mode,
    };
  }
  const steps = item.review?.steps;
  if (!steps || typeof steps.used !== 'number') return null;
  return steps;
}

/** The item's step limit under the cap mode: the owner's cap plus the grant. */
export function stepLimit(steps: AssetListBoardSteps | null): number {
  if (!steps || !steps.cap) return 0;
  return steps.cap + (steps.extra ?? 0);
}

/** Whether the item used up its steps (the runner's step_cap block, or used >= limit). */
export function stepsExhausted(item: AssetListBoardItem): boolean {
  if (item.block === 'step_cap') return true;
  const steps = boardSteps(item);
  const limit = stepLimit(steps);
  return limit > 0 && Boolean(steps) && (steps as AssetListBoardSteps).used >= limit;
}

/**
 * #330 step 5C: the extra-step bounds the Schema declares, or null when this
 * deployment cannot extend an item (no step policy, no route).
 */
export function stepExtraConfig(schema: ProductSchema): { default: number; maximum: number; totalMaximum: number } | null {
  const extra = schema.step_policy?.extra_steps;
  if (!extra || !schema.runtime.http.routes.project_asset_list_item_steps) return null;
  return { default: extra.default, maximum: extra.maximum, totalMaximum: extra.total_maximum };
}

/**
 * #330 step 5C: whether the board offers "give it more steps" for this item.
 * Only a capped asset with a running sub-session can use them, and only once
 * the item has used its limit; the button never appears for 2D-only items or
 * the agent/unlimited modes.
 */
export function canExtendSteps(schema: ProductSchema, item: AssetListBoardItem): boolean {
  if (!stepExtraConfig(schema) || !item.session_id) return false;
  const steps = boardSteps(item);
  if (!steps || steps.mode !== 'cap') return false;
  return stepsExhausted(item);
}

/** The single-slot viewer key of one item; the store keeps one viewer alive at a time. */
export function boardSlotKey(itemID: string): string {
  return `asset-list-board:${itemID}`;
}

/** A same-origin task asset path from the Schema's route; '' without a route or an id. */
export function taskAssetPath(schema: ProductSchema, taskID: string, assetID: string): string {
  const template = schema.runtime.http.routes.task_asset;
  if (!template || !taskID || !assetID) return '';
  return template
    .replace('{taskID}', encodeURIComponent(taskID))
    .replace('{assetID}', encodeURIComponent(assetID));
}

/** An input asset path (the crop's source picture); '' without a route or an id. */
export function inputAssetPath(schema: ProductSchema, assetID: string): string {
  const template = schema.runtime.http.routes.asset_item;
  if (!template || !assetID) return '';
  return template.replace('{assetID}', encodeURIComponent(assetID));
}

function mediaTypeFromRole(role: string): string {
  if (role === 'model' || role.startsWith('model')) return 'model/gltf-binary';
  if (role === 'other') return '';
  return 'image/png';
}

/** The stored media type of one result asset, from the snapshot first and the role after. */
export function artifactMediaType(item: AssetListBoardItem, assetID: string): string {
  const artifact = item.review?.artifacts?.find((candidate) => candidate.asset_id === assetID);
  if (artifact?.media_type) return artifact.media_type;
  const produced = item.result?.assets?.find((candidate) => candidate.id === assetID);
  return produced?.role ? mediaTypeFromRole(produced.role) : '';
}

/** The GLB a 3D cell rotates; '' when the result carries no model. */
export function model3DAsset(item: AssetListBoardItem): string {
  const judged = item.review?.model3d?.asset_id;
  if (judged) return judged;
  const artifact = item.review?.artifacts?.find(
    (candidate) => candidate.asset_id !== '' && (candidate.media_type ?? '').startsWith('model/'),
  );
  if (artifact) return artifact.asset_id;
  const produced = item.result?.assets?.find(
    (candidate) => candidate.id !== '' && (candidate.role === 'model' || candidate.role?.startsWith('model/')),
  );
  return produced?.id ?? '';
}

/** The picture a cell shows as its cover; '' when the item has no image. */
export function coverAsset(item: AssetListBoardItem): string {
  const candidates = [
    item.review?.cover_asset_id ?? '',
    item.representative_asset_id ?? '',
    ...(item.review?.artifacts ?? []).filter((artifact) => (artifact.media_type ?? '').startsWith('image/')).map((artifact) => artifact.asset_id),
    ...(item.result?.assets ?? []).filter((asset) => asset.id && mediaTypeFromRole(asset.role ?? '').startsWith('image/')).map((asset) => asset.id),
  ];
  for (const assetID of candidates) {
    if (assetID && artifactMediaType(item, assetID).startsWith('image/')) return assetID;
  }
  return '';
}

/** Every stored artifact of the item, one row each: the snapshot's rich list plus any unlisted result asset. */
export interface BoardArtifactView {
  assetID: string;
  name: string;
  role: string;
  mediaType: string;
  size: number;
}

export function boardArtifacts(item: AssetListBoardItem): BoardArtifactView[] {
  const rows: BoardArtifactView[] = [];
  const seen = new Set<string>();
  for (const artifact of item.review?.artifacts ?? []) {
    if (!artifact.asset_id || seen.has(artifact.asset_id)) continue;
    seen.add(artifact.asset_id);
    rows.push({
      assetID: artifact.asset_id,
      name: artifact.role || artifact.asset_id,
      role: artifact.role ?? '',
      mediaType: artifact.media_type ?? artifactMediaType(item, artifact.asset_id),
      size: artifact.size ?? 0,
    });
  }
  for (const produced of item.result?.assets ?? []) {
    if (!produced.id || seen.has(produced.id)) continue;
    seen.add(produced.id);
    rows.push({
      assetID: produced.id,
      name: produced.name || produced.role || produced.id,
      role: produced.role ?? '',
      mediaType: artifactMediaType(item, produced.id),
      size: 0,
    });
  }
  return rows;
}

/** The task the item's produced artifacts belong to; '' before the item produced one. */
export function boardTaskID(item: AssetListBoardItem): string {
  return item.review?.task_id || item.result?.task_id || '';
}

/** A normalized crop region as the drawer prints it. */
export function regionText(region: AssetListBoardRegion): {
  x: string; y: string; width: string; height: string;
  x0: string; y0: string; x1: string; y1: string;
} {
  const fixed = (value: number) => (Number.isFinite(value) ? String(Math.round(value * 10_000) / 10_000) : '0');
  const rect = region.pixel_rect;
  return {
    x: fixed(region.x), y: fixed(region.y), width: fixed(region.width), height: fixed(region.height),
    x0: rect ? String(rect.x0) : '0', y0: rect ? String(rect.y0) : '0',
    x1: rect ? String(rect.x1) : '0', y1: rect ? String(rect.y1) : '0',
  };
}

/** The board's own items, tolerant of an older server that omits the array. */
export function boardItems(board: AssetListBoard | null): AssetListBoardItem[] {
  return Array.isArray(board?.items) ? board.items : [];
}

/** #329d: one declared return reason, visible for the given item kinds. */
export interface BoardReturnReason {
  id: string;
  label: string;
  kinds: string[];
}

/**
 * #329d: the reasons the picker offers for one kind. The server refuses a
 * reason outside the item's kinds, so the picker never shows one; an empty
 * `kinds` means the reason applies to every kind.
 */
export function returnReasonsForKind(schema: ProductSchema, kind: string): BoardReturnReason[] {
  const declared = schema.asset_list?.review?.return_reasons ?? [];
  return declared.filter((reason) => reason.kinds.length === 0 || reason.kinds.includes(kind));
}

/** The 1-based position of one item on the board; 0 when it is not listed. */
export function boardItemIndex(items: readonly AssetListBoardItem[], itemID: string): number {
  const index = items.findIndex((item) => item.id === itemID);
  return index < 0 ? 0 : index + 1;
}

/** The accepted items; the batch archive packs exactly these. */
export function acceptedBoardItems(items: readonly AssetListBoardItem[]): AssetListBoardItem[] {
  return items.filter((item) => item.state === 'accepted');
}

/**
 * #329d: whether the board should keep re-reading while it is open. A running
 * run is producing, and items in working or review keep the run running, so
 * the board stays current until every item is terminal; a paused or finished
 * list with no such item stops polling.
 */
export function boardPollingActive(board: AssetListBoard | null): boolean {
  if (!board) return false;
  if (board.list.run?.status === 'running') return true;
  return board.items.some((item) => item.state === 'working' || item.state === 'review');
}

/**
 * #329d: the item ids a batch download takes. An explicit selection of
 * accepted items wins; otherwise the accepted items the current filter shows.
 * The archive route narrows to exactly these ids.
 */
export function boardDownloadScope(
  items: readonly AssetListBoardItem[],
  filter: AssetListBoardFilter,
  selectedIDs: ReadonlySet<string>,
): string[] {
  const accepted = new Map(acceptedBoardItems(items).map((item) => [item.id, item.id]));
  const selected = [...selectedIDs].filter((id) => accepted.has(id));
  if (selected.length > 0) return selected;
  return filterBoardItems(items, filter)
    .filter((item) => item.state === 'accepted')
    .map((item) => item.id);
}

/** #329d: the crop provenance of one group's source picture. */
export interface BoardCropGroup {
  sourceAssetID: string;
  sourceWidth: number;
  sourceHeight: number;
  items: AssetListBoardItem[];
}

/** The items that carry both a crop source picture and its region. */
export function boardCropItems(items: readonly AssetListBoardItem[]): AssetListBoardItem[] {
  return items.filter((item) => Boolean(item.source?.source_input_asset_id) && Boolean(item.region));
}

/**
 * #329d: the crop items grouped by their source picture, in board order. One
 * list may cut from several pictures; each group draws its own boxes.
 */
export function boardCropGroups(items: readonly AssetListBoardItem[]): BoardCropGroup[] {
  const groups: BoardCropGroup[] = [];
  const bySource = new Map<string, BoardCropGroup>();
  for (const item of boardCropItems(items)) {
    const sourceAssetID = item.source?.source_input_asset_id ?? '';
    let group = bySource.get(sourceAssetID);
    if (!group) {
      group = {
        sourceAssetID,
        sourceWidth: item.source?.source_width ?? 0,
        sourceHeight: item.source?.source_height ?? 0,
        items: [],
      };
      bySource.set(sourceAssetID, group);
      groups.push(group);
    }
    group.items.push(item);
  }
  return groups;
}