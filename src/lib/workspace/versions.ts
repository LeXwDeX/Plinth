import type { ProductSchema } from '@/lib/product-schema';
import { schemaText, taskStatePresentation } from '@/lib/product-schema';
import type { LineageAsset, LineageEdgeKind, LineageEvidence, LineageGraph, LineageNode } from '@/lib/workspace/lineage';
import { groupTileStatus, type ThreeViewGroup, type ThreeViewStatus } from '@/lib/workspace/three-view';
import { modelPreviewAsset } from '@/lib/workspace/task-projection';
import { mergedObjectId, type MergedVersion } from '@/lib/workspace/merge-version';
import type { TrackedTask } from '@/store/workspace-store';

/**
 * 版本条的数据：把服务端版本链（只含已成功的任务）与本会话跟踪中的任务
 * （排队、进行中、失败）合成一列"版本"。编号、分组和来源都来自服务端事实；
 * 这里不猜测来源——服务端无法确认的来源保持"未知"。
 */

export type VersionDomain = 'image' | 'model';
export type VersionStatus = 'ready' | 'pending' | 'running' | 'failed';
/** 连接线的种类：决定图标和文案；`unknown` 画虚线。 */
export type LinkClass = 'to_model' | 'capture' | 'process' | 'edit' | 'inpaint' | 'remove_background' | 'reference' | 'merge' | 'unknown';

/**
 * 版本从哪里来：任务的结果；3D 截图（没有任务）；用户上传的图（没有任务）；
 * 在画板上合并手绘修改而成（没有任务）。
 */
export type VersionOrigin = 'task' | 'capture' | 'upload' | 'merge';

export interface VersionParent {
  /** 来源版本的 key；来源未知时为空。 */
  key: string;
  known: boolean;
  kind: LineageEdgeKind;
  evidence: LineageEvidence;
  link: LinkClass;
  /** The capability that made the child; names the step on a processing link ("重拓扑"). */
  step: string;
}

export interface VersionSlot {
  slot: string;
  label: string;
  taskId: string;
  assetId: string;
}

/** One view of a three-view group on its tile: where it stands and which picture it has. */
export interface VersionView {
  key: string;
  label: string;
  status: ThreeViewStatus;
  taskId: string;
  assetId: string;
}

export interface VersionTile {
  key: string;
  /** 代表任务：普通版本是它自己，分组取来源槽位（正面）或第一个成员。 */
  taskId: string;
  taskIds: string[];
  domain: VersionDomain;
  /** 同一域内从 1 起的序号；失败的任务不占序号（为 0）。 */
  version: number;
  status: VersionStatus;
  /** Raw public task state; badge wording comes from Schema task_states. */
  state?: string;
  /** 0..1，仅 pending / running 有意义。 */
  progress: number;
  capability: string;
  createdAt: string;
  /** 候选图（图片版本）或模型文件（3D 版本）；附件经公开 Task 详情读取。失败 / 进行中的版本为空。 */
  assets: LineageAsset[];
  /** 3D 版本的缩略图。 */
  thumbnailUrl: string;
  /** 分组名（如"三视图"）；普通版本为空。 */
  groupLabel: string;
  groupComplete: boolean;
  slots: VersionSlot[];
  /** The group's views as the server projects them (all of them, finished or not); empty for an ordinary version. */
  views: VersionView[];
  parents: VersionParent[];
  failure: string;
  origin: VersionOrigin;
  /** "inpaint" for a masked edit; empty otherwise. */
  operation: string;
  /** Screenshots: the capture record this version is. */
  captureId: string;
  /** A format conversion made before conversions were folded (#330): it keeps its number and says what it is. */
  conversion?: boolean;
  /** Real pixel size when the server knows it (screenshots); null otherwise. */
  size: { width: number; height: number } | null;
}

export interface VersionTiles {
  tiles: VersionTile[];
  byTask: Map<string, string>;
  /** Capture record ID to the screenshot's version key. */
  byCapture: Map<string, string>;
  /** Recorded sources of file-only tasks, which have no media version tile of their own. */
  fileTaskParents: Map<string, VersionParent[]>;
}

export function domainOfCapability(schema: ProductSchema, capability: string): VersionDomain | null {
  const declared = schema.capabilities.find((item) => item.id === capability);
  if (!declared) return null;
  if (declared.domain === 'image') return 'image';
  if (declared.domain === 'model-3d') return 'model';
  return schema.runtime.frontend.model_processing_capabilities.includes(capability) ? 'model' : 'image';
}

function nodeDomain(node: LineageNode): VersionDomain | null {
  if (node.assets.some((asset) => asset.kind === 'model')) return 'model';
  if (node.assets.some((asset) => asset.kind === 'splat')) return 'model';
  if (node.assets.some((asset) => asset.kind === 'image')) return 'image';
  return null;
}

const KIND_PRIORITY: Record<LineageEdgeKind, number> = { capture: 0, source_task: 1, source_asset: 2, reference: 3 };

export function linkClass(parent: VersionDomain | null, child: VersionDomain, kind: LineageEdgeKind): LinkClass {
  if (!parent) return 'unknown';
  if (kind === 'capture') return 'capture';
  if (parent === 'image' && child === 'model') return 'to_model';
  if (parent === 'model' && child === 'image') return 'capture';
  if (parent === 'model' && child === 'model') return 'process';
  return 'edit';
}

/** Says what an edit between two pictures was: a masked repaint, a cut-out, a merge of hand-drawn changes, or a reference. */
function refineLink(base: LinkClass, parent: Pick<VersionTile, 'origin'>, child: Pick<VersionTile, 'origin' | 'operation' | 'capability'>): LinkClass {
  if (base !== 'edit') return base;
  if (child.operation === 'inpaint') return 'inpaint';
  if (child.capability === 'background-removal') return 'remove_background';
  if (child.origin === 'merge') return 'merge';
  if (parent.origin === 'upload') return 'reference';
  return 'edit';
}

/** 当前连线到哪个来源：已知优先，其次按来源种类的重要性。 */
export function primaryParent(tile: Pick<VersionTile, 'parents'>): VersionParent | null {
  const ranked = [...tile.parents].sort((left, right) =>
    Number(right.known) - Number(left.known) || KIND_PRIORITY[left.kind] - KIND_PRIORITY[right.kind]);
  return ranked[0] ?? null;
}

function failureText(schema: ProductSchema, task: TrackedTask): string {
  // The server's public failure projection already carries the Schema's own message for every class (failure.<class>).
  return task.failure?.detail || task.liveError || '';
}

/** The parent of a folded format conversion: the version it converted, when that version is on the strip. */
function conversionParents(byTask: ReadonlyMap<string, string>, sourceTaskId: string): VersionParent[] {
  const key = byTask.get(sourceTaskId);
  return key ? [{ key, known: true, kind: 'source_task', evidence: 'recorded', link: 'process', step: '' }] : [];
}

export interface BuildVersionsInput {
  schema: ProductSchema;
  graph: LineageGraph | null | undefined;
  tracked: readonly TrackedTask[];
  sessionId: string | null;
  /** The session's three-view groups: one tile each, even before any view has a picture. */
  threeViews?: readonly ThreeViewGroup[];
  /** Versions made on the board by merging canvas edits: free, no task, linked to the version they came from. */
  merged?: readonly MergedVersion[];
}

/** The prefix a merged version uses for "made from this screenshot": its `from` is `capture:<capture id>`. */
export const MERGED_FROM_CAPTURE = 'capture:';

/** Synthetic task ID of an uploaded picture on the stage (it has no task). */
export function uploadObjectId(assetId: string): string {
  return `upload:${assetId}`;
}

export function blankTile(partial: Pick<VersionTile, 'key' | 'taskId' | 'domain' | 'status'> & Partial<VersionTile>): VersionTile {
  return {
    taskIds: [], version: 0, progress: 1, capability: '', createdAt: '', assets: [], thumbnailUrl: '',
    groupLabel: '', groupComplete: true, slots: [], views: [], parents: [], failure: '',
    origin: 'task', operation: '', captureId: '', size: null,
    ...partial,
  };
}

export function buildVersionTiles({ schema, graph, tracked, sessionId, threeViews, merged }: BuildVersionsInput): VersionTiles {
  const tiles: VersionTile[] = [];
  const byKey = new Map<string, VersionTile>();
  const nodeTile = new Map<string, string>();
  const byTask = new Map<string, string>();
  const byCapture = new Map<string, string>();
  const fileTaskParents = new Map<string, VersionParent[]>();

  if (graph) {
    const groups = new Map(graph.groups.map((group) => [group.id, group]));
    const nodeById = new Map(graph.nodes.map((node) => [node.id, node]));
    const nodes = graph.nodes
      .filter((node) => node.kind !== 'unknown' && (node.assets.length > 0 || Boolean(node.state)))
      .sort((left, right) => left.createdAt.localeCompare(right.createdAt) || left.id.localeCompare(right.id));
    for (const node of nodes) {
      // File-only Tasks have their own detail entry in the task center. A
      // processing capability is not evidence of an image or model output.
      if (node.assets.length > 0 && node.assets.every(asset => asset.kind === 'other')) {
        if (node.taskId) fileTaskParents.set(node.taskId, []);
        continue;
      }
      const domain = nodeDomain(node) ?? domainOfCapability(schema, node.capability);
      if (!domain) continue;
      const outcome = node.state ? taskStatePresentation(schema, node.state).outcome : 'success';
      if (outcome === 'canceled') continue;
      const status: VersionStatus = outcome === 'failure' ? 'failed' : outcome === 'success' ? 'ready' : outcome === 'running' ? 'running' : 'pending';
      const origin: VersionOrigin = node.kind === 'capture' ? 'capture' : node.kind === 'upload' ? 'upload' : 'task';
      const group = status === 'ready' && origin === 'task' && node.group ? groups.get(node.group) : undefined;
      const assets = node.assets.filter((asset) => asset.kind === domain || (domain === 'model' && asset.kind === 'splat'));
      let key: string;
      if (origin === 'capture') key = `c:${node.captureId || node.id}`;
      else if (origin === 'upload') key = `u:${assets[0]?.id ?? node.id}`;
      else key = group ? `g:${group.id}` : `n:${node.id}`;
      let tile = byKey.get(key);
      if (!tile) {
        let taskId = node.taskId;
        if (origin === 'capture') {
          // A screenshot is a picture of its own; it names the model it was taken from.
          const source = graph.edges.find((edge) => edge.to === node.id && edge.kind === 'capture');
          taskId = (source ? nodeById.get(source.from)?.taskId : '') || `capture:${node.captureId}`;
        } else if (origin === 'upload') taskId = uploadObjectId(assets[0]?.id ?? node.id);
        const thumbnail = domain === 'model' ? modelPreviewAsset(node.assets) : undefined;
        tile = blankTile({
          key, taskId, domain, status, origin, state: node.state,
          capability: node.capability, createdAt: group?.createdAt || node.createdAt,
          thumbnailUrl: thumbnail?.url ?? '', groupLabel: group?.label ?? '', groupComplete: group?.complete ?? true,
          operation: node.operation, captureId: node.captureId, ...(node.conversion ? { conversion: true } : {}),
          version: node.version ?? 0,
          progress: Math.min(Math.max((node.progress ?? 0) / schema.runtime.frontend.percentage_multiplier, 0), 1),
          failure: node.failure?.detail ?? '',
          size: node.width > 0 && node.height > 0 ? { width: node.width, height: node.height } : null,
        });
        byKey.set(key, tile);
        tiles.push(tile);
      }
      if (origin === 'capture') {
        byCapture.set(node.captureId, key);
      } else {
        const ownTask = origin === 'upload' ? uploadObjectId(assets[0]?.id ?? node.id) : node.taskId;
        tile.taskIds.push(ownTask);
        byTask.set(ownTask, key);
      }
      tile.assets.push(...assets);
      if (group) {
        const member = group.members.find((entry) => entry.taskId === node.taskId);
        tile.slots.push({ slot: node.slot || member?.slot || '', label: member?.label ?? '', taskId: node.taskId, assetId: assets[0]?.id ?? '' });
        // The source slot (the front view) represents the group on the stage.
        if (group.sourceSlot && node.slot === group.sourceSlot) tile.taskId = node.taskId;
      }
      nodeTile.set(node.id, key);
    }

    // Parents: edges between different versions. Unknown evidence or a source
    // outside the loaded graph stays unknown; nothing is guessed.
    const unknownSeen = new Set<string>();
    const parentSeen = new Set<string>();
    for (const edge of graph.edges) {
      const childKey = nodeTile.get(edge.to);
      const child = childKey ? byKey.get(childKey) : undefined;
      const childNode = nodeById.get(edge.to);
      const parents = child?.parents ?? (childNode ? fileTaskParents.get(childNode.taskId) : undefined);
      if (!parents) continue;
      const childIdentity = child?.key ?? `file:${edge.to}`;
      const step = child?.capability ?? childNode?.capability ?? '';
      const parentKey = nodeTile.get(edge.from);
      const parent = parentKey ? byKey.get(parentKey) : undefined;
      if (parent && parent.key !== childKey && edge.evidence !== 'unknown') {
        const signature = `${childIdentity}|${parent.key}|${edge.kind}`;
        if (parentSeen.has(signature)) continue;
        parentSeen.add(signature);
        parents.push({ key: parent.key, known: true, kind: edge.kind, evidence: edge.evidence,
          link: child ? refineLink(linkClass(parent.domain, child.domain, edge.kind), parent, child) : 'unknown', step });
      } else if (!parent || edge.evidence === 'unknown') {
        if (unknownSeen.has(childIdentity)) continue;
        unknownSeen.add(childIdentity);
        parents.push({ key: '', known: false, kind: edge.kind, evidence: 'unknown', link: 'unknown', step: '' });
      }
    }

    // A format conversion is part of the version it converted (#330): no tile of its own, its detail names that version.
    for (const [taskId, sourceTaskId] of Object.entries(graph.conversions ?? {})) {
      fileTaskParents.set(taskId, conversionParents(byTask, sourceTaskId));
    }
  }

  addThreeViewTiles({ tiles, byKey, byTask, groups: threeViews ?? [] });
  addMergedTiles({ tiles, byKey, byTask, byCapture, merged: merged ?? [], label: schemaText(schema, 'ui.studio.merge.label') });

  // Tasks still in flight or failed live only in the tracker: the lineage
  // describes succeeded work.
  for (const task of tracked) {
    // Started from a version's Formats tab before the chain lists it: already a conversion of that version.
    if (task.formatOf && !byTask.has(task.taskId)) {
      if (!fileTaskParents.has(task.taskId)) fileTaskParents.set(task.taskId, conversionParents(byTask, task.formatOf));
      continue;
    }
    const existingKey = byTask.get(task.taskId);
    const existing = existingKey ? byKey.get(existingKey) : undefined;
    // The tracker updates status immediately, but never supplies a guessed source.
    if (existing && existing.status !== 'ready' && task.state && task.sessionId === sessionId) {
      const outcome = taskStatePresentation(schema, task.state).outcome;
      existing.state = task.state;
      existing.status = outcome === 'failure' ? 'failed' : outcome === 'pending' ? 'pending' : 'running';
      existing.progress = Math.min(Math.max(task.progress / Math.max(task.progressMaximum, 1), 0), 1);
      existing.failure = failureText(schema, task);
    }
    if (byTask.has(task.taskId) || fileTaskParents.has(task.taskId) || task.notFound) continue;
    if (sessionId && task.sessionId !== sessionId) continue;
    const observedAssets = task.assets.length ? task.assets : task.downloadables;
    if (observedAssets.length > 0 && observedAssets.every(asset => asset.kind === 'other')) continue;
    const domain = domainOfCapability(schema, task.capability);
    if (!domain) continue;
    const failure = failureText(schema, task);
    const outcome = task.state ? taskStatePresentation(schema, task.state).outcome : 'pending';
    if (outcome === 'canceled') continue;
    let status: VersionStatus;
    if (outcome === 'failure' || (task.terminal && failure !== '')) status = 'failed';
    else if (task.terminal) status = 'running'; // succeeded, waiting for the version chain to include it
    else status = outcome === 'running' ? 'running' : 'pending';
    const maximum = task.progressMaximum > 0 ? task.progressMaximum : 1;
    const progress = status === 'running' && task.terminal ? 1 : Math.min(Math.max(task.progress / maximum, 0), 1);
    const key = `t:${task.taskId}`;
    tiles.push(blankTile({
      key, taskId: task.taskId, taskIds: [task.taskId], domain, status, state: task.state, progress, capability: task.capability, failure,
      createdAt: task.startedAt ? new Date(task.startedAt).toISOString() : '',
      parents: [{ key: '', known: false, kind: 'source_task', evidence: 'unknown', link: 'unknown', step: task.capability }],
    }));
    byTask.set(task.taskId, key);
  }
  for (const tile of tiles) if (tile.status !== 'ready' && tile.parents.length === 0) {
    tile.parents.push({ key: '', known: false, kind: 'source_task', evidence: 'unknown', link: 'unknown', step: tile.capability });
  }
  return { tiles: numberTiles(tiles), byTask, byCapture, fileTaskParents };
}

/** The public task state owns badge wording; legacy non-task tiles use strip text. */
export function versionStatusLabel(schema: ProductSchema, tile: Pick<VersionTile, 'state' | 'status'>): string {
  if (tile.state) return taskStatePresentation(schema, tile.state).label;
  return tile.status === 'ready' ? '' : schemaText(schema, `ui.studio.strip.${tile.status}`);
}

const timeOf = (tile: VersionTile): number => Date.parse(tile.createdAt);

/**
 * Orders the versions by when they were made and numbers each domain in that order
 * (2D v1, 3D v1, 2D v2 ...). A version keeps its number when later work arrives, because
 * later work is later; work still in flight has no time yet and sits last. Failed work and
 * uploaded pictures take no number: neither is a step of the line the person is following.
 */
function numberTiles(tiles: VersionTile[]): VersionTile[] {
  const ordered = tiles
    .map((tile, index) => ({ tile, index, time: timeOf(tile) }))
    .sort((left, right) => {
      const leftKnown = Number.isFinite(left.time);
      const rightKnown = Number.isFinite(right.time);
      if (leftKnown !== rightKnown) return leftKnown ? -1 : 1;
      return (leftKnown && left.time !== right.time ? left.time - right.time : 0) || left.index - right.index;
    })
    .map((entry) => entry.tile);
  const counters: Record<VersionDomain, number> = { image: 0, model: 0 };
  let merges = 0;
  for (const tile of ordered) {
    if (tile.status !== 'ready' || tile.origin === 'upload') { tile.version = 0; continue; }
    // Canvas merges have no server task. Their own mN namespace keeps older
    // server pages from renumbering them or colliding with session ordinals.
    if (tile.origin === 'merge') { tile.version = ++merges; continue; }
    if (tile.version > 0) { counters[tile.domain] = Math.max(counters[tile.domain], tile.version); continue; }
    counters[tile.domain] += 1;
    tile.version = counters[tile.domain];
  }
  return ordered;
}

/** A merge is a version of its own, linked to the version (or screenshot) it came from. */
function addMergedTiles({ tiles, byKey, byTask, byCapture, merged, label }: {
  tiles: VersionTile[];
  byKey: Map<string, VersionTile>;
  byTask: Map<string, string>;
  byCapture: Map<string, string>;
  merged: readonly MergedVersion[];
  label: string;
}): void {
  for (const version of merged) {
    const key = `m:${version.nodeId}`;
    if (byKey.has(key)) continue;
    const id = mergedObjectId(version.nodeId);
    const source = version.from.startsWith(MERGED_FROM_CAPTURE)
      ? byCapture.get(version.from.slice(MERGED_FROM_CAPTURE.length))
      : byTask.get(version.from);
    const parent = source ? byKey.get(source) : undefined;
    const tile = blankTile({
      key, taskId: id, taskIds: [id], domain: 'image', status: 'ready', origin: 'merge',
      createdAt: version.at, groupLabel: label,
      assets: [{ id: version.nodeId, role: 'output', variant: '', mediaType: 'image/png', ordinal: 0, url: version.url, kind: 'image' }],
    });
    tile.parents = parent
      ? [{ key: parent.key, known: true, kind: 'source_asset', evidence: 'recorded', link: 'merge', step: '' }]
      : [{ key: '', known: false, kind: 'source_asset', evidence: 'unknown', link: 'unknown', step: '' }];
    tiles.push(tile);
    byKey.set(key, tile);
    byTask.set(id, key);
  }
}

function addThreeViewTiles({ tiles, byKey, byTask, groups }: {
  tiles: VersionTile[];
  byKey: Map<string, VersionTile>;
  byTask: Map<string, string>;
  groups: readonly ThreeViewGroup[];
}): void {
  for (const group of groups) {
    const key = `g:${group.runId}`;
    let tile = byKey.get(key);
    if (!tile) {
      // The chain only knows finished tasks; a group still working (or waiting for the chain to catch up) is a tile of its own.
      const status = groupTileStatus(group);
      const tileStatus: VersionStatus = status === 'ready' ? 'running' : status;
      const front = group.slots.find((slot) => slot.key === group.modelSource.slot) ?? group.slots[0];
      tile = blankTile({
        key, taskId: front?.taskId ?? '', taskIds: group.slots.flatMap((slot) => (slot.taskId ? [slot.taskId] : [])),
        domain: 'image', status: tileStatus, progress: tileStatus === 'pending' ? 0 : 0.5, createdAt: group.createdAt,
        groupLabel: group.label, groupComplete: group.complete,
      });
      const source = group.sourceAssetId
        ? tiles.find((other) => other.assets.some((asset) => asset.id === group.sourceAssetId))
        : undefined;
      if (source) {
        tile.parents.push({ key: source.key, known: true, kind: 'source_asset', evidence: 'recorded', link: refineLink(linkClass(source.domain, 'image', 'source_asset'), source, tile), step: '' });
      }
      tiles.push(tile);
      byKey.set(key, tile);
    }
    tile.groupLabel = group.label || tile.groupLabel;
    tile.groupComplete = group.complete;
    tile.views = group.slots.map((slot) => ({
      key: slot.key, label: slot.label, status: slot.status, taskId: slot.taskId, assetId: slot.outputAssetId,
    }));
    for (const slot of group.slots) if (slot.taskId) byTask.set(slot.taskId, key);
  }
}

/** 某版本的祖先、自身和后代：当前分支。 */
export function branchOf(tiles: readonly VersionTile[], key: string | null): Set<string> {
  const branch = new Set<string>();
  if (!key) return branch;
  const byKey = new Map(tiles.map((tile) => [tile.key, tile]));
  const walkUp = (current: string) => {
    if (branch.has(current)) return;
    branch.add(current);
    for (const parent of byKey.get(current)?.parents ?? []) if (parent.known) walkUp(parent.key);
  };
  walkUp(key);
  const children = new Map<string, string[]>();
  for (const tile of tiles) for (const parent of tile.parents) {
    if (parent.known) children.set(parent.key, [...(children.get(parent.key) ?? []), tile.key]);
  }
  const walkDown = (current: string) => {
    for (const child of children.get(current) ?? []) {
      if (branch.has(child)) continue;
      branch.add(child);
      walkDown(child);
    }
  };
  walkDown(key);
  return branch;
}

/** 一列版本：同一来源的多个结果竖向堆叠，共享来源连线。 */
export interface VersionColumn {
  parent: VersionParent | null;
  /**
   * The link of every tile in the column, in order ("编辑", "局部重绘"): the results of one source
   * share a connector, so it carries one mark per result and says what each of them was.
   */
  links: VersionParent[];
  tiles: VersionTile[];
}

export function layoutColumns(tiles: readonly VersionTile[]): VersionColumn[] {
  const children = new Map<string, VersionTile[]>();
  for (const tile of tiles) {
    const parent = primaryParent(tile);
    if (parent?.known) children.set(parent.key, [...(children.get(parent.key) ?? []), tile]);
  }
  const columns: VersionColumn[] = [];
  const placed = new Set<string>();
  for (const tile of tiles) {
    if (placed.has(tile.key)) continue;
    const parent = primaryParent(tile);
    const siblings = parent?.known ? children.get(parent.key) ?? [tile] : [tile];
    const stack = siblings.length > 1 ? siblings : [tile];
    for (const member of stack) placed.add(member.key);
    const links: VersionParent[] = [];
    for (const member of stack) {
      const link = primaryParent(member);
      if (link) links.push(link);
    }
    columns.push({ parent, links, tiles: stack });
  }
  return columns;
}

/** At most this many results of one source show in its column; the rest fold into "+N". */
export const COLUMN_VISIBLE_LIMIT = 3;

export interface FoldedColumn {
  visible: VersionTile[];
  hidden: VersionTile[];
}

/**
 * A column of siblings that is too tall keeps its first results and folds the rest.
 * When it folds, one place goes to the "+N" control, and the places go first to
 * the tiles of the current branch (the current one before its relatives), so the
 * branch in use is never hidden. Order within the column is unchanged.
 */
export function foldColumn(
  tiles: readonly VersionTile[],
  branch: ReadonlySet<string>,
  currentKey: string | null,
  limit: number = COLUMN_VISIBLE_LIMIT,
): FoldedColumn {
  if (tiles.length <= limit) return { visible: [...tiles], hidden: [] };
  const room = Math.max(1, limit - 1);
  const rank = (tile: VersionTile) => (tile.key === currentKey ? 0 : branch.has(tile.key) ? 1 : 2);
  const chosen = new Set(
    tiles
      .map((tile, index) => ({ tile, index }))
      .sort((left, right) => rank(left.tile) - rank(right.tile) || left.index - right.index)
      .slice(0, room)
      .map((entry) => entry.tile.key),
  );
  return {
    visible: tiles.filter((tile) => chosen.has(tile.key)),
    hidden: tiles.filter((tile) => !chosen.has(tile.key)),
  };
}

/**
 * The version is older when something newer of the same kind exists after it: the
 * stage is then showing a past version, and continuing from it makes a branch
 * rather than extending the latest line.
 */
export function isOlderVersion(tiles: readonly VersionTile[], key: string | null): boolean {
  if (!key) return false;
  const index = tiles.findIndex((tile) => tile.key === key);
  if (index < 0) return false;
  const tile = tiles[index];
  if (tile.status !== 'ready') return false;
  return tiles.slice(index + 1).some((later) => later.domain === tile.domain && later.status !== 'failed' && later.origin !== 'upload');
}
