import { assetKind, type TrackedAssetKind } from '@/lib/workspace/task-projection';

/**
 * 会话版本链：服务端从任务冻结输入、3D 截图记录与字节同一性读出"谁由谁而来"。
 * 这里只把响应解析成严格类型，并在本地按创建时间给每个域（2D/3D）编版本号；
 * 服务端无法确认的来源一律保持"未知"，不在这里补全、猜测或合并。
 */

export type LineageEvidence = 'recorded' | 'same_bytes' | 'unknown';
export type LineageEdgeKind = 'source_task' | 'source_asset' | 'reference' | 'capture';
export type LineageDomain = 'image' | 'model' | 'file';

export interface LineageAsset {
  id: string;
  role: string;
  variant: string;
  deliveryProfile?: string;
  mediaType: string;
  ordinal: number;
  url: string;
  kind: TrackedAssetKind;
  /** A required output of the task, not an attachment; the stage shows a required model first (#326). */
  required?: boolean;
}

/**
 * `task` is a finished task; `capture` a screenshot of a 3D model and `upload` a picture the person
 * uploaded (neither has a task); `unknown` stands for a parent the session cannot identify.
 */
export type LineageNodeKind = 'task' | 'unknown' | 'capture' | 'upload';

export interface LineageNode {
  id: string;
  kind: LineageNodeKind;
  taskId: string;
  /** 产生该任务的能力（如 text-to-image、retexture）；服务端缺省时为空。 */
  capability: string;
  createdAt: string;
  assets: LineageAsset[];
  /** 属于哪个分组（三视图运行 ID）与在其中的槽位；不在分组里时为空。 */
  group: string;
  slot: string;
  /** What the task did beyond its capability ("inpaint" for a masked edit); empty otherwise. */
  operation: string;
  /** Capture nodes: the capture record and the real pixel size of the screenshot. */
  captureId: string;
  width: number;
  height: number;
  /** Session-wide ordinal supplied by the paged projection; legacy graphs omit it. */
  version?: number;
  /** 折叠之前做的格式转换（#330）：保留自己的节点与版本号，标为「格式转换」。 */
  conversion?: boolean;
  state?: string;
  progress?: number;
  failure?: { failureClass: string; detail: string; retryable: boolean; ambiguous: boolean } | null;
}

export interface LineageGroupMember {
  slot: string;
  label: string;
  taskId: string;
}

/** 服务端把几个任务记为同一个版本（三视图的正 / 侧 / 背）。 */
export interface LineageGroup {
  id: string;
  kind: string;
  label: string;
  complete: boolean;
  sourceSlot: string;
  createdAt: string;
  members: LineageGroupMember[];
}

export interface LineageEdge {
  from: string;
  to: string;
  kind: LineageEdgeKind;
  evidence: LineageEvidence;
  assetId: string;
  captureId: string;
}

export interface LineageGraph {
  nodes: LineageNode[];
  edges: LineageEdge[];
  groups: LineageGroup[];
  truncated: boolean;
  nextCursor?: string;
  /**
   * 折叠进来源版本的格式转换任务（#330）：任务 ID → 它所属版本的任务 ID。这些任务没有节点、没有版本号；
   * 转换不改变模型，版本仍以来源的原始模型为准，转换出的文件属于该版本的「格式」页。
   */
  conversions?: Readonly<Record<string, string>>;
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

const NODE_KINDS: readonly LineageNodeKind[] = ['task', 'unknown', 'capture', 'upload'];
const EDGE_KINDS: readonly LineageEdgeKind[] = ['source_task', 'source_asset', 'reference', 'capture'];

function parseAsset(value: unknown): LineageAsset | null {
  const entry = record(value);
  const id = text(entry.id);
  const url = text(entry.url);
  if (!id || !url) return null;
  const role = text(entry.role);
  const mediaType = text(entry.media_type);
  return {
    id, role, mediaType, url,
    variant: text(entry.variant),
    deliveryProfile: text(entry.delivery_profile),
    ordinal: typeof entry.ordinal === 'number' ? entry.ordinal : 0,
    kind: assetKind(mediaType, role, text(entry.variant), text(entry.delivery_profile)),
    required: entry.required === true,
  };
}

/** 解析服务端响应；形状不对的条目被丢弃，指向不存在节点的边被丢弃。 */
export function parseLineage(payload: unknown): LineageGraph {
  const body = record(payload);
  const nodes: LineageNode[] = [];
  const seen = new Set<string>();
  for (const value of Array.isArray(body.nodes) ? body.nodes : []) {
    const entry = record(value);
    const id = text(entry.id);
    const kind = NODE_KINDS.find((candidate) => candidate === entry.kind);
    if (!id || !kind || seen.has(id)) continue;
    seen.add(id);
    const assets = (Array.isArray(entry.assets) ? entry.assets : [])
      .map(parseAsset)
      .filter((asset): asset is LineageAsset => asset !== null)
      .sort((left, right) => left.ordinal - right.ordinal);
    nodes.push({
      id, kind, taskId: text(entry.task_id), capability: text(entry.capability),
      createdAt: text(entry.created_at), assets, group: text(entry.group), slot: text(entry.slot),
      operation: text(entry.operation), captureId: text(entry.capture_id),
      width: typeof entry.pixel_width === 'number' ? entry.pixel_width : 0,
      height: typeof entry.pixel_height === 'number' ? entry.pixel_height : 0,
      ...(typeof entry.version === 'number' && entry.version > 0 ? { version: entry.version } : {}),
      ...(entry.conversion === true ? { conversion: true } : {}),
      state: text(entry.state), progress: typeof entry.progress === 'number' ? entry.progress : 0,
      failure: entry.failure ? { failureClass: text(record(entry.failure).class), detail: text(record(entry.failure).detail),
        retryable: record(entry.failure).retryable === true, ambiguous: record(entry.failure).ambiguous === true } : null,
    });
  }
  const edges: LineageEdge[] = [];
  for (const value of Array.isArray(body.edges) ? body.edges : []) {
    const entry = record(value);
    const from = text(entry.from);
    const to = text(entry.to);
    const kind = EDGE_KINDS.find((candidate) => candidate === entry.kind);
    if (!kind || !from || !seen.has(to) || (!('next_cursor' in body) && !seen.has(from))) continue;
    const evidence: LineageEvidence = entry.evidence === 'recorded' || entry.evidence === 'same_bytes' ? entry.evidence : 'unknown';
    edges.push({ from, to, kind, evidence, assetId: text(entry.asset_id), captureId: text(entry.capture_id) });
  }
  const groups: LineageGroup[] = [];
  const groupIds = new Set<string>();
  for (const value of Array.isArray(body.groups) ? body.groups : []) {
    const entry = record(value);
    const id = text(entry.id);
    if (!id || groupIds.has(id)) continue;
    groupIds.add(id);
    const members = (Array.isArray(entry.members) ? entry.members : []).flatMap((raw) => {
      const member = record(raw);
      const taskId = text(member.task_id);
      return taskId ? [{ slot: text(member.slot), label: text(member.label), taskId }] : [];
    });
    groups.push({
      id, kind: text(entry.kind), label: text(entry.label), complete: entry.complete === true,
      sourceSlot: text(entry.source_slot), createdAt: text(entry.created_at), members,
    });
  }
  const conversions: Record<string, string> = {};
  for (const value of Array.isArray(body.conversions) ? body.conversions : []) {
    const entry = record(value);
    const taskId = text(entry.task_id);
    const sourceTaskId = text(entry.source_task_id);
    if (taskId && sourceTaskId && taskId !== sourceTaskId) conversions[taskId] = sourceTaskId;
  }
  return { nodes, edges, groups, truncated: body.truncated === true,
    ...(Object.keys(conversions).length > 0 ? { conversions } : {}),
    ...('next_cursor' in body ? { nextCursor: text(body.next_cursor) } : {}) };
}

/** Pages are newest first. Keep pending cross-page edges; IDs, not names or dates, identify nodes. */
export function mergeLineagePages(pages: readonly LineageGraph[]): LineageGraph {
  const nodes = new Map<string, LineageNode>();
  const edges = new Map<string, LineageEdge>();
  const groups = new Map<string, LineageGroup>();
  const conversions: Record<string, string> = {};
  for (const page of pages) {
    for (const [task, source] of Object.entries(page.conversions ?? {})) conversions[task] ??= source;
    for (const node of page.nodes) if (!nodes.has(node.id)) nodes.set(node.id, node);
    for (const edge of page.edges) edges.set(JSON.stringify(edge), edge);
    for (const group of page.groups) {
      const previous = groups.get(group.id);
      if (!previous) groups.set(group.id, { ...group, members: [...group.members] });
      else for (const member of group.members) if (!previous.members.some((entry) => entry.taskId === member.taskId)) previous.members.push(member);
    }
  }
  const last = pages.at(-1);
  return { nodes: [...nodes.values()].sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.id.localeCompare(b.id)),
    edges: [...edges.values()], groups: [...groups.values()], truncated: last?.truncated ?? false, nextCursor: last?.nextCursor ?? '',
    ...(Object.keys(conversions).length > 0 ? { conversions } : {}) };
}

export function lineageDomain(node: Pick<LineageNode, 'assets'>): LineageDomain {
  if (node.assets.some((asset) => asset.kind === 'model')) return 'model';
  if (node.assets.some((asset) => asset.kind === 'image')) return 'image';
  return 'file';
}

/** 一个来源：已知任务（带它的域与版本号），或服务端无法确认的"未知"。 */
export interface ChainParent {
  edge: LineageEdge;
  /** 已知来源任务所在的链条目 ID；未知来源为空。 */
  entryId: string;
  domain: LineageDomain | null;
  version: number;
}

export interface ChainEntry {
  id: string;
  taskId: string;
  createdAt: string;
  domain: LineageDomain;
  /** 同一域内按创建时间的序号，从 1 起（2D v1、3D v1、2D v2 …）。 */
  version: number;
  assets: LineageAsset[];
  parents: ChainParent[];
}

/**
 * 按创建时间排成链：只有任务节点成为条目，"未知"节点只作为条目的未知来源出现。
 * 版本号按域独立计数，完全由本会话已知的任务顺序决定。
 */
export function buildChain(graph: LineageGraph): ChainEntry[] {
  const tasks = graph.nodes
    .filter((node) => node.kind === 'task' && node.assets.length > 0)
    .sort((left, right) => left.createdAt.localeCompare(right.createdAt) || left.id.localeCompare(right.id));
  const counters: Record<LineageDomain, number> = { image: 0, model: 0, file: 0 };
  const entries = new Map<string, ChainEntry>();
  for (const node of tasks) {
    const domain = lineageDomain(node);
    counters[domain] += 1;
    entries.set(node.id, {
      id: node.id, taskId: node.taskId, createdAt: node.createdAt, domain,
      version: node.version ?? counters[domain], assets: node.assets, parents: [],
    });
  }
  for (const edge of graph.edges) {
    const child = entries.get(edge.to);
    if (!child) continue;
    const parent = entries.get(edge.from);
    child.parents.push(parent && edge.evidence !== 'unknown'
      ? { edge, entryId: parent.id, domain: parent.domain, version: parent.version }
      : { edge: { ...edge, evidence: 'unknown' }, entryId: '', domain: null, version: 0 });
  }
  return [...entries.values()];
}
