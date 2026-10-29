import { create } from 'zustand';
import { reorderLayers, type LayerOp } from '@/lib/canvas/layer-order';
import { alignUpdates, distributeUpdates, type AlignMode, type DistributeAxis, type Rect } from '@/lib/canvas/align';
import type { AutoSelectMode } from '@/lib/canvas/auto-select';
import { resizeCanvasNodes, type ResizeAnchor } from '@/lib/canvas/canvas-resize';
import { persist } from 'zustand/middleware';
import type { InpaintSource } from '@/lib/canvas/inpaint-source';

/** 根据图片原始尺寸计算节点目标尺寸（保持比例，最大边不超过 maxEdge） */
export function computeNodeSize(
  naturalWidth: number,
  naturalHeight: number,
  maxEdge: number,
  defaultNodeSize: number,
): { width: number; height: number } {
  if (naturalWidth <= 0 || naturalHeight <= 0) {
    return { width: defaultNodeSize, height: defaultNodeSize };
  }
  const scale = Math.min(maxEdge / naturalWidth, maxEdge / naturalHeight, 1);
  return {
    width: Math.round(naturalWidth * scale),
    height: Math.round(naturalHeight * scale),
  };
}

// ── 类型 ──────────────────────────────────────────────

export type CanvasToolType =
  | 'select'
  | 'hand'
  | 'text'
  | 'shape'
  | 'pencil'
  | 'brush'
  | 'eraser'
  | 'crop'
  | 'mask'
  | 'marquee'
  | 'lasso'
  | 'zoom'
  | 'eyedropper';

export type ShapeType = 'rectangle' | 'arrow' | 'line' | 'ellipse';

export type CanvasBgType = string;

export interface CanvasNode {
  id: string;
  type: 'image' | 'text' | 'frame' | 'shape' | 'pencil';
  imageUrl: string;
  text?: string;
  taskId: string;
  /** Server-verified model capture provenance for a 3D-to-2D bitmap. */
  captureId?: string;
  x: number;
  y: number;
  width: number;
  height: number;
  zIndex: number;
  /** shape 节点的子类型 */
  shapeType?: ShapeType;
  /** pencil 节点的 SVG path data */
  pathData?: string;
  /** 边框线条粗细 */
  strokeWidth?: number;
  /** 线条颜色 */
  strokeColor?: string;
  /** 填充颜色（shape 节点） */
  fillColor?: string;
  /** 节点可见性，undefined / true 表示可见 */
  visible?: boolean;
  /** 节点锁定状态，锁定后不可拖拽/缩放/删除 */
  locked?: boolean;
  /** 旋转角度（度），konva 顺时针为正 */
  rotation?: number;
  /** 不透明度 0..1 */
  opacity?: number;
  /** 文字字号（画布单位）；缺省用画布默认字号。 */
  fontSize?: number;
  /** 源图像裁剪区域（图像像素坐标） */
  crop?: { x: number; y: number; width: number; height: number };
  /** 分组 ID，同 groupId 的节点作为一个整体选中/移动 */
  groupId?: string;
  /** 图层名称；缺省时图层面板按类型显示（局部重绘的结果图层带名称）。 */
  name?: string;
  /** 由哪个版本（任务 ID）合并而来：画布上的修改拍平成的新版本；随画布一起保存。 */
  mergedFrom?: string;
  /**
   * #246 第 2 阶段：新画布的初始位图图层在第一次落笔前没有图片（imageUrl 为空），只有 Schema 声明的
   * 填充（fillColor，透明时省略）。画笔第一次提交后换成真实图片并去掉该标记。
   */
  blank?: boolean;
  /** 合并发生的时间（ISO），版本条按它排序。 */
  mergedAt?: string;
  /** A local inpaint patch keeps the exact mask asset it was made with (viewable after reload). */
  inpaint?: InpaintSource;
}

/** A canvas size in whole pixels (#246). */
export interface CanvasSize {
  width: number;
  height: number;
}

export interface UndoEntry {
  nodes: CanvasNode[];
  descriptionKey: string;
  timestamp: number;
  /** The canvas size before this step; only a resize records it (#246). Undo restores it, redo re-applies it. */
  canvasSize?: CanvasSize;
}

export interface CanvasViewport {
  zoom: number;
  panX: number;
  panY: number;
}

interface CanvasViewportConfiguration {
  default_zoom: number;
  default_pan_x: number;
  default_pan_y: number;
  minimum_zoom: number;
  maximum_zoom: number;
  zoom_step: number;
  decimal_places: number;
  percentage_multiplier: number;
  fit_padding_pixels: number;
}

export function canvasDefaultViewport(
  configuration: CanvasViewportConfiguration,
): CanvasViewport {
  return {
    zoom: configuration.default_zoom,
    panX: configuration.default_pan_x,
    panY: configuration.default_pan_y,
  };
}

// ── Store ─────────────────────────────────────────────

interface CanvasStore {
  // 按 sessionId 隔离
  nodesBySession: Record<string, CanvasNode[]>;
  viewportBySession: Record<string, CanvasViewport>;
  selectedNodeIds: string[];

  // 工具模式
  activeTool: CanvasToolType;
  setActiveTool: (tool: CanvasToolType) => void;
  activeShapeType: ShapeType;
  setActiveShapeType: (type: ShapeType) => void;

  // 上次使用的样式（用于新建形状/画笔时继承）
  lastStyle: { strokeWidth: number; strokeColor: string; fillColor: string };
  setLastStyle: (style: Partial<{ strokeWidth: number; strokeColor: string; fillColor: string }>) => void;

  // 像素画笔尺寸（画布像素），与矢量线宽 lastStyle.strokeWidth 各自独立；按 Schema 的 brush 范围取整限幅。
  brushSize: number;
  setBrushSize: (size: number) => void;
  /** [ 和 ]：按 Schema 的比例（默认 10%）放大或缩小，至少变化 1 像素。 */
  stepBrushSize: (direction: 1 | -1) => void;

  // 背景色（前景色是 lastStyle.strokeColor）：X 交换，D 恢复 Schema 默认。
  backgroundColor: string;
  setBackgroundColor: (color: string) => void;
  swapColors: () => void;
  resetColors: () => void;

  // 移动工具的“自动选择：组 / 图层”。
  autoSelect: AutoSelectMode;
  setAutoSelect: (mode: AutoSelectMode) => void;

  /**
   * #149: set each time an action that would add picture layers is refused because the canvas already holds the
   * Schema's limit of them (`canvas.limits.maximum_bitmap_nodes`). The board turns each change into one notice.
   */
  bitmapLimitHit: { sessionId: string; seq: number } | null;

  // 节点 CRUD
  /** Returns false, and adds nothing, when the node is a picture and the canvas is at its picture limit (#149). */
  addNode: (sessionId: string, node: Omit<CanvasNode, 'zIndex'>) => boolean;
  /** #246: a blank canvas layer below every other layer (the brush landed where no picture is), one undo step. */
  addCanvasLayer: (sessionId: string, node: Omit<CanvasNode, 'zIndex'>) => boolean;
  removeNode: (sessionId: string, nodeId: string) => void;
  /**
   * Stamps the visible layers into one new top layer as a single undo step: the new node goes on top and
   * the stamped layers are hidden, not deleted, so they can be shown again.
   */
  stampNodes: (sessionId: string, hideIds: readonly string[], node: Omit<CanvasNode, 'zIndex'>) => boolean;
  /**
   * `recordHistory: false` when the caller already recorded its own step (a drag, a transform, a brush
   * commit); otherwise edits within the throttle window of the previous one share a single step.
   */
  updateNode: (sessionId: string, nodeId: string, update: Partial<Pick<CanvasNode, 'x' | 'y' | 'width' | 'height' | 'rotation' | 'opacity' | 'crop' | 'imageUrl'>>, options?: {
    recordHistory?: boolean;
    /** Brush strokes drawn before the layer was locked still land on it; nothing else sets this. */
    ignoreLock?: boolean;
  }) => void;
  updateNodeStyle: (sessionId: string, nodeId: string, style: Partial<Pick<CanvasNode, 'strokeWidth' | 'strokeColor' | 'fillColor'>>) => void;
  /** `recordHistory: false` skips the undo snapshot (a drag already pushed its own); repeated nudges within the throttle window share one step. */
  updateNodes: (sessionId: string, updates: Array<{ id: string; x: number; y: number }>, options?: { recordHistory?: boolean }) => void;
  /** Applies per-node style patches (selection property controls); `recordHistory: false` for the rest of a gesture whose first change already pushed an undo entry. */
  patchNodes: (sessionId: string, updates: Array<{ id: string; patch: Partial<Pick<CanvasNode, 'strokeColor' | 'strokeWidth' | 'fillColor' | 'opacity' | 'fontSize'>> }>, options?: { recordHistory?: boolean }) => void;
  /** Layer order of the selection (group siblings included) as one undo step. */
  reorderSelected: (sessionId: string, op: LayerOp) => void;
  updateNodeText: (sessionId: string, nodeId: string, text: string) => void;
  bringToFront: (sessionId: string, nodeId: string) => void;
  sendToBack: (sessionId: string, nodeId: string) => void;
  bringForward: (sessionId: string, nodeId: string) => void;
  sendBackward: (sessionId: string, nodeId: string) => void;
  clearSession: (sessionId: string) => void;
  toggleNodeVisibility: (sessionId: string, nodeId: string) => void;
  reorderNodes: (sessionId: string, nodeId: string, newIndex: number) => void;
  toggleNodeLock: (sessionId: string, nodeId: string) => void;
  toggleSelectedLock: (sessionId: string) => void;
  /** Shows or hides every member of a group as one step. */
  toggleGroupVisibility: (sessionId: string, groupId: string) => void;
  /** Locks every member of a group (unlocks when all are locked) as one step. */
  toggleGroupLock: (sessionId: string, groupId: string) => void;
  /** Applies a layers-panel drag: new bottom-to-top order and group membership, one undo step. */
  arrangeLayers: (sessionId: string, order: readonly string[], groupIds: Readonly<Record<string, string | undefined>>) => void;
  /** Ctrl+J: copies of the selected layers directly above their originals, selected afterwards. Returns the new ids. */
  duplicateSelected: (sessionId: string) => string[];
  /** Aligns the selection (to the frame when it is one layer and `target` is given); one undo step, none when nothing moves. */
  alignSelected: (sessionId: string, mode: AlignMode, target?: Rect | null) => void;
  distributeSelected: (sessionId: string, axis: DistributeAxis) => void;

  // Undo/Redo
  _undoStackBySession: Record<string, UndoEntry[]>;
  _redoStackBySession: Record<string, UndoEntry[]>;
  _lastUndoPushTime: Record<string, number>;
  /** The undo entry that edits within the throttle window keep folding into, per session. */
  _coalesceEntry: Record<string, UndoEntry | undefined>;
  _pushUndo: (sessionId: string, descriptionKey: string) => void;
  undo: (sessionId: string) => void;
  redo: (sessionId: string) => void;
  canUndo: (sessionId: string) => boolean;
  canRedo: (sessionId: string) => boolean;
  getUndoHistory: (sessionId: string) => UndoEntry[];
  /** The history panel: go to the state after `position` steps (0 = how the session opened). Undone steps stay redoable. */
  goToHistory: (sessionId: string, position: number) => void;

  // 视口控制
  setViewport: (sessionId: string, update: Partial<CanvasViewport>) => void;
  resetViewport: (sessionId: string) => void;
  zoomIn: (sessionId: string) => void;
  zoomOut: (sessionId: string) => void;
  fitToScreen: (sessionId: string, containerWidth: number, containerHeight: number) => void;

  // 选中
  selectNode: (nodeId: string | null) => void;
  toggleSelectNode: (nodeId: string) => void;
  addToSelection: (nodeId: string) => void;
  deselectNode: (nodeId: string) => void;
  selectNodes: (nodeIds: string[]) => void;
  clearSelection: () => void;

  // 批量操作
  removeSelectedNodes: (sessionId: string) => void;
  copySelectedNodes: (sessionId: string) => void;
  /** Pastes the internal clipboard; with `at` (a board point) the pasted group is centred there instead of offset. */
  pasteNodes: (sessionId: string, offsetX?: number, offsetY?: number, at?: { x: number; y: number }) => void;
  groupSelectedNodes: (sessionId: string) => void;
  ungroupSelectedNodes: (sessionId: string) => void;

  // 内部剪贴板；_clipboardId 是这一次复制的标记，写进系统剪贴板，粘贴时用来判断谁更新。
  _clipboard: CanvasNode[];
  _clipboardId: string;

  // Getter
  getNodes: (sessionId: string) => CanvasNode[];
  getViewport: (sessionId: string) => CanvasViewport;

  // ── 持久化相关 ──
  projectBySession: Record<string, string>;  // sessionId → projectId
  setProjectForSession: (sessionId: string, projectId: string) => void;
  getProjectId: (sessionId: string) => string | undefined;
  /** Replaces the session's nodes with the server's and drops its undo and redo records; true when there were any. */
  loadNodesFromDB: (sessionId: string, nodes: CanvasNode[]) => boolean;
  _savedHashBySession: Record<string, string>;
  _setSavedHash: (sessionId: string, hash: string) => void;
  // 本地已保存内容对应的服务端 revision 基线（FEA-01）：本地有未保存
  // 改动时，加载不得 adopting 服务端最新 revision，自动保存仍以基线
  // 提交，让服务端乐观锁有机会返回 409。
  _savedRevisionBySession: Record<string, number>;
  _setSavedRevision: (sessionId: string, revision: number) => void;
  bgStyleBySession: Record<string, CanvasBgType>;
  setBgStyleForSession: (sessionId: string, bg: CanvasBgType) => void;
  /**
   * 已放置结果清单（#239/#262），与节点一起保存。null：旧画布未登记；undefined：未加载。
   * `_placedDirtyBySession` 为 true 时即使节点没变也要保存一次。
   */
  placedResultsBySession: Record<string, string[] | null>;
  _placedDirtyBySession: Record<string, boolean>;
  /** 加载或冲突重载时以服务端为准（不标脏）。 */
  loadPlacedResults: (sessionId: string, placed: string[] | null) => void;
  /** 本地新放置或登记后写入，并标脏等待保存。 */
  setPlacedResults: (sessionId: string, placed: string[]) => void;
  /** 保存成功后，若清单未再变化则清除脏标记。 */
  _markPlacedSaved: (sessionId: string, saved: string[]) => void;
  /**
   * 画布像素尺寸（#246），与节点一起保存。undefined：尚未加载或尚未初始化/迁移。
   * `_canvasSizeDirtyBySession` 为 true 时即使节点没变也要保存一次。
   */
  canvasSizeBySession: Record<string, CanvasSize>;
  _canvasSizeDirtyBySession: Record<string, boolean>;
  /** 加载或冲突重载时以服务端为准（不标脏）；null 清除（旧画布等待迁移）。 */
  loadCanvasSize: (sessionId: string, size: CanvasSize | null) => void;
  /**
   * 改画布尺寸（#246）：只改边界，节点保持像素大小，并保持相对所选锚点的位置（见 resizeCanvasNodes；
   * 中心锚点时节点不动）。扩展补空白；缩小时越界内容留在节点上，返回被新边界截到的图层 ID。
   * 尚未落笔的初始图层随画布边界变化。一个撤销步骤；尺寸未变时什么也不做（返回空数组）。
   */
  resizeCanvas: (sessionId: string, size: CanvasSize, options: CanvasResizeOptions) => string[];
  /** 初始化或旧画布一次性迁移：替换节点并写入尺寸，不进撤销栈，标脏等待保存。 */
  initializeCanvas: (sessionId: string, nodes: CanvasNode[], size: CanvasSize) => void;
  /** 保存成功后，若尺寸未再变化则清除脏标记。 */
  _markCanvasSizeSaved: (sessionId: string, saved: CanvasSize) => void;
  /** Drag-snapping toggle chosen by the viewer; null means "use the Schema default". Persisted. */
  snapEnabledOverride: boolean | null;
  setSnapEnabled: (enabled: boolean) => void;
  /** #246: the resize anchor the viewer picked; null means "use the Schema default". Persisted. */
  resizeAnchorOverride: ResizeAnchor | null;
  setResizeAnchor: (anchor: ResizeAnchor) => void;
}

/** Where a resize keeps content and where the canvas is centred (the Schema's artboard anchor point). */
export interface CanvasResizeOptions {
  anchor: ResizeAnchor;
  center: { x: number; y: number };
  /** Layers holding a local brush bitmap right now: moved like content, never refitted to the frame. */
  painted?: ReadonlySet<string>;
}

interface CanvasHistoryConfiguration {
  maximum_depth: number;
  snapshot_throttle_milliseconds: number;
}

interface CanvasClipboardConfiguration {
  offset_x: number;
  offset_y: number;
}

interface CanvasFrontendConfiguration {
  viewport: CanvasViewportConfiguration;
  history: CanvasHistoryConfiguration;
  clipboard: CanvasClipboardConfiguration;
  style: {
    default_stroke_width: number;
    default_stroke_color: string;
    default_fill_color: string;
    default_background_color: string;
  };
  brush: {
    minimum_size: number;
    maximum_size: number;
    default_size: number;
  };
  keyboard: {
    brush_size_step_ratio: number;
  };
}

let canvasFrontendConfiguration: CanvasFrontendConfiguration | undefined;

/** `canvas.limits.maximum_bitmap_nodes`; without a configured Schema nothing is refused here (the server still is). */
let canvasBitmapLimit = Number.POSITIVE_INFINITY;

export function configureCanvasBitmapLimit(limit: number): void {
  canvasBitmapLimit = Number.isFinite(limit) && limit > 0 ? limit : Number.POSITIVE_INFINITY;
}

const pictureCount = (nodes: readonly Pick<CanvasNode, 'type'>[]) => nodes.filter((node) => node.type === 'image').length;

/** Whether adding `adding` pictures to `existing` goes past the limit; saved canvases count every picture layer, hidden ones too. */
function overBitmapLimit(existing: readonly Pick<CanvasNode, 'type'>[], adding: number): boolean {
  return adding > 0 && pictureCount(existing) + adding > canvasBitmapLimit;
}

/** The state patch of a refused add: one more notice for the board to show. */
function bitmapRefused(s: Pick<CanvasStore, 'bitmapLimitHit'>, sessionId: string): Pick<CanvasStore, 'bitmapLimitHit'> {
  return { bitmapLimitHit: { sessionId, seq: (s.bitmapLimitHit?.seq ?? 0) + 1 } };
}

export function configureCanvasFrontend(
  configuration: CanvasFrontendConfiguration,
): void {
  canvasFrontendConfiguration = configuration;
  useCanvasStore.setState((state) => {
    const backgroundColor = state.backgroundColor === '' ? { backgroundColor: configuration.style.default_background_color } : {};
    const brushSize = state.brushSize === 0 ? { brushSize: configuration.brush.default_size } : {};
    if (
      state.lastStyle.strokeWidth > 0 &&
      state.lastStyle.strokeColor !== '' &&
      state.lastStyle.fillColor !== ''
    ) {
      const unset = { ...backgroundColor, ...brushSize };
      return Object.keys(unset).length > 0 ? unset : state;
    }
    return {
      ...backgroundColor,
      ...brushSize,
      lastStyle: {
        strokeWidth: configuration.style.default_stroke_width,
        strokeColor: configuration.style.default_stroke_color,
        fillColor: configuration.style.default_fill_color,
      },
    };
  });
}

/**
 * Sessions whose oldest undo steps were dropped by the Schema's history cap. The history panel reads it so
 * its first row is not called the opening state once that state is no longer reachable. In memory only,
 * like the undo stacks it describes.
 */
const sessionsWithDroppedHistory = new Set<string>();

function noteHistoryDropped(sessionId: string): void {
  sessionsWithDroppedHistory.add(sessionId);
}

export function historyWasTruncated(sessionId: string): boolean {
  return sessionsWithDroppedHistory.has(sessionId);
}

function historyMaximumDepth(): number {
  return (
    canvasFrontendConfiguration?.history.maximum_depth ??
    Number.POSITIVE_INFINITY
  );
}

function historySnapshotThrottleMilliseconds(): number {
  return (
    canvasFrontendConfiguration?.history.snapshot_throttle_milliseconds ?? 0
  );
}

function clipboardOffsetX(): number {
  return canvasFrontendConfiguration?.clipboard.offset_x ?? 0;
}

function clipboardOffsetY(): number {
  return canvasFrontendConfiguration?.clipboard.offset_y ?? 0;
}

function requireCanvasFrontendConfiguration(): CanvasFrontendConfiguration {
  if (!canvasFrontendConfiguration) {
    throw new Error('Canvas frontend schema is not configured');
  }
  return canvasFrontendConfiguration;
}

function configuredDefaultViewport(): CanvasViewport {
  return canvasDefaultViewport(requireCanvasFrontendConfiguration().viewport);
}

function roundZoom(value: number): number {
  const decimalPlaces =
    requireCanvasFrontendConfiguration().viewport.decimal_places;
  const factor = 10 ** decimalPlaces;
  return Math.round(value * factor) / factor;
}

/** 深拷贝节点数组 */
function cloneNodes(nodes: CanvasNode[]): CanvasNode[] {
  return nodes.map((n) => structuredClone(n));
}

/**
 * 生成 UUID v4 字符串。
 * 优先使用 crypto.randomUUID()（需要安全上下文/HTTPS），
 * 不可用时降级为 Math.random() 实现，确保 HTTP 测试环境也能正常运行。
 */
export function generateId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  // Fallback UUID v4
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    const v = c === 'x' ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}

/** 获取节点列表中最大 zIndex */
function maxZIndex(nodes: CanvasNode[]): number {
  if (nodes.length === 0) return 0;
  return Math.max(...nodes.map((n) => n.zIndex));
}

/**
 * Records one undo step for an edit that repeats quickly (arrow-key nudges, repeated numeric changes):
 * edits within the throttle window of the previous one, with nothing else recorded in between, fold into
 * the same step. Returns the store fields to merge into the update.
 */
function recordCoalesced(
  s: Pick<CanvasStore, '_undoStackBySession' | '_redoStackBySession' | '_lastUndoPushTime' | '_coalesceEntry'>,
  sessionId: string,
  before: CanvasNode[],
  descriptionKey: string,
) {
  const now = Date.now();
  const stack = s._undoStackBySession[sessionId] || [];
  const open = s._coalesceEntry[sessionId];
  const folds = open !== undefined
    && stack[stack.length - 1] === open
    && now - (s._lastUndoPushTime[sessionId] || 0) < historySnapshotThrottleMilliseconds();
  let entry = open;
  let undoStack = stack;
  if (!folds) {
    entry = { nodes: cloneNodes(before), descriptionKey, timestamp: now };
    undoStack = [...stack, entry];
    if (undoStack.length > historyMaximumDepth()) { undoStack = undoStack.slice(-historyMaximumDepth()); noteHistoryDropped(sessionId); }
  }
  return {
    _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
    _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
    _lastUndoPushTime: { ...s._lastUndoPushTime, [sessionId]: now },
    _coalesceEntry: { ...s._coalesceEntry, [sessionId]: entry },
  };
}

/** Brush diameter in whole pixels within the Schema's brush range (any value before the Schema is loaded). */
function clampBrushSize(size: number): number {
  const brush = canvasFrontendConfiguration?.brush;
  const rounded = Math.round(size);
  if (!brush) return Math.max(1, rounded);
  return Math.min(brush.maximum_size, Math.max(brush.minimum_size, rounded));
}

/** The nodes with new positions applied (locked nodes are never moved here). */
function withPositions(nodes: CanvasNode[], updates: ReadonlyArray<{ id: string; x: number; y: number }>): CanvasNode[] {
  const byId = new Map(updates.map((update) => [update.id, update]));
  return nodes.map((node) => {
    const update = byId.get(node.id);
    return update && !node.locked ? { ...node, x: update.x, y: update.y } : node;
  });
}

/** The size a resize step keeps for the opposite direction: the current one (only resize steps carry a size). */
function swappedSize(s: Pick<CanvasStore, 'canvasSizeBySession'>, sessionId: string, entry: UndoEntry): Pick<UndoEntry, 'canvasSize'> {
  const current = s.canvasSizeBySession[sessionId];
  return entry.canvasSize && current ? { canvasSize: current } : {};
}

/** The store fields that put a resize step's recorded size back; nothing for any other step. */
function restoredSize(s: Pick<CanvasStore, 'canvasSizeBySession' | '_canvasSizeDirtyBySession'>, sessionId: string, entry: UndoEntry) {
  if (!entry.canvasSize || !s.canvasSizeBySession[sessionId]) return {};
  return {
    canvasSizeBySession: { ...s.canvasSizeBySession, [sessionId]: entry.canvasSize },
    _canvasSizeDirtyBySession: { ...s._canvasSizeDirtyBySession, [sessionId]: true },
  };
}

/** One undo step for an edit that is applied in the same update; clears the redo stack. */
function recordStep(
  s: Pick<CanvasStore, '_undoStackBySession' | '_redoStackBySession'>,
  sessionId: string,
  before: CanvasNode[],
  descriptionKey: string,
) {
  const undoStack = [...(s._undoStackBySession[sessionId] || []), { nodes: cloneNodes(before), descriptionKey, timestamp: Date.now() }];
  if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
  return {
    _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
    _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
  };
}

export const useCanvasStore = create<CanvasStore>()(
  persist(
    (set, get) => ({
  nodesBySession: {},
  viewportBySession: {},
  selectedNodeIds: [],
  bitmapLimitHit: null,
  _clipboard: [],
  _clipboardId: '',
  activeTool: 'select',
  activeShapeType: 'rectangle',
  lastStyle: { strokeWidth: 0, strokeColor: '', fillColor: '' },

  // ── 持久化相关初始值 ──
  projectBySession: {},
  _savedHashBySession: {},
  _savedRevisionBySession: {},
  bgStyleBySession: {},
  placedResultsBySession: {},
  _placedDirtyBySession: {},
  canvasSizeBySession: {},
  _canvasSizeDirtyBySession: {},
  snapEnabledOverride: null,
  resizeAnchorOverride: null,

  // ── Undo/Redo 初始值 ──
  _undoStackBySession: {},
  _redoStackBySession: {},
  _lastUndoPushTime: {},
  _coalesceEntry: {},

  // ── 工具模式 ──

  setActiveTool: (tool) => set({ activeTool: tool }),
  setActiveShapeType: (type) => set({ activeShapeType: type }),
  setLastStyle: (style) => set((s) => ({ lastStyle: { ...s.lastStyle, ...style } })),
  // 0 = not configured yet: configureCanvasFrontend() sets the Schema's brush.default_size.
  brushSize: 0,
  setBrushSize: (size) => set({ brushSize: clampBrushSize(size) }),
  stepBrushSize: (direction) => set((s) => {
    const ratio = canvasFrontendConfiguration?.keyboard.brush_size_step_ratio ?? 0.1;
    const change = Math.max(1, Math.round(s.brushSize * ratio));
    return { brushSize: clampBrushSize(s.brushSize + direction * change) };
  }),

  backgroundColor: '',
  setBackgroundColor: (color) => set({ backgroundColor: color }),
  swapColors: () => set((s) => {
    const foreground = s.lastStyle.strokeColor;
    const background = s.backgroundColor || canvasFrontendConfiguration?.style.default_background_color || '';
    if (background === '') return s;
    return { lastStyle: { ...s.lastStyle, strokeColor: background }, backgroundColor: foreground };
  }),
  resetColors: () => set((s) => {
    const style = canvasFrontendConfiguration?.style;
    if (!style) return s;
    return { lastStyle: { ...s.lastStyle, strokeColor: style.default_stroke_color }, backgroundColor: style.default_background_color };
  }),

  autoSelect: 'group',
  setAutoSelect: (mode) => set({ autoSelect: mode }),

  // ── 节点 CRUD ──

  addNode: (sessionId, node) => {
    let added = true;
    set((s) => {
      const existing = s.nodesBySession[sessionId] || [];
      if (node.type === 'image' && overBitmapLimit(existing, 1)) {
        added = false;
        return bitmapRefused(s, sessionId);
      }
      const zIndex = maxZIndex(existing) + 1;
      const descriptionKey = [
        'image',
        'text',
        'frame',
        'shape',
        'pencil',
      ].includes(node.type)
        ? `history.add.${node.type}`
        : 'history.add.generic';
      // 推 undo
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey,
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: [...existing, { ...node, zIndex }],
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    });
    return added;
  },

  addCanvasLayer: (sessionId, node) => {
    let added = true;
    set((s) => {
      const existing = s.nodesBySession[sessionId] || [];
      if (overBitmapLimit(existing, node.type === 'image' ? 1 : 0)) {
        added = false;
        return bitmapRefused(s, sessionId);
      }
      // Below every other layer: pictures, text and shapes stay on top of the canvas (#246).
      const zIndex = existing.length > 0 ? Math.min(...existing.map((n) => n.zIndex)) - 1 : 0;
      return {
        nodesBySession: { ...s.nodesBySession, [sessionId]: [{ ...node, zIndex }, ...existing] },
        ...recordStep(s, sessionId, existing, 'history.add.image'),
      };
    });
    return added;
  },

  stampNodes: (sessionId, hideIds, node) => {
    let added = true;
    set((s) => {
      const existing = s.nodesBySession[sessionId] || [];
      if (overBitmapLimit(existing, node.type === 'image' ? 1 : 0)) {
        added = false;
        return bitmapRefused(s, sessionId);
      }
      const hidden = new Set(hideIds);
      const hiddenNodes = existing.map((item) => (hidden.has(item.id) ? { ...item, visible: false } : item));
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({ nodes: cloneNodes(existing), descriptionKey: 'history.merge', timestamp: Date.now() });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: { ...s.nodesBySession, [sessionId]: [...hiddenNodes, { ...node, zIndex: maxZIndex(existing) + 1 }] },
        selectedNodeIds: [node.id],
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    });
    return added;
  },

  removeNode: (sessionId, nodeId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const target = existing.find((n) => n.id === nodeId);
      if (target?.locked) return s;  // 锁定节点不可删除
      const filtered = existing.filter((n) => n.id !== nodeId);
      // 推 undo
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.delete',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: { ...s.nodesBySession, [sessionId]: filtered },
        selectedNodeIds: s.selectedNodeIds.filter((id) => id !== nodeId),
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  updateNode: (sessionId, nodeId, update, options) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const target = existing.find((n) => n.id === nodeId);
      if (!target || (target.locked && !options?.ignoreLock)) return s;
      // A picture arriving on an unpainted initial layer (#246) makes it an ordinary picture layer.
      const nodes = existing.map((n) => (n.id !== nodeId ? n
        : update.imageUrl && n.blank ? { ...n, ...update, blank: undefined, fillColor: undefined } : { ...n, ...update }));
      if (options?.recordHistory === false) {
        return { nodesBySession: { ...s.nodesBySession, [sessionId]: nodes } };
      }
      return { nodesBySession: { ...s.nodesBySession, [sessionId]: nodes }, ...recordCoalesced(s, sessionId, existing, 'history.move') };
    }),

  updateNodeStyle: (sessionId, nodeId, style) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.style',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: existing.map((n) =>
            n.id === nodeId ? { ...n, ...style } : n,
          ),
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  patchNodes: (sessionId, updates, options) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing || updates.length === 0) return s;
      const lockedIds = new Set(existing.filter((n) => n.locked).map((n) => n.id));
      const patches = new Map(updates.filter((u) => !lockedIds.has(u.id)).map((u) => [u.id, u.patch]));
      if (patches.size === 0) return s;
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      const record = options?.recordHistory !== false;
      if (record) {
        undoStack.push({ nodes: cloneNodes(existing), descriptionKey: 'history.style', timestamp: Date.now() });
        if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      }
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: existing.map((n) => (patches.has(n.id) ? { ...n, ...patches.get(n.id) } : n)),
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: record ? { ...s._redoStackBySession, [sessionId]: [] } : s._redoStackBySession,
      };
    }),

  reorderSelected: (sessionId, op) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const order = reorderLayers(existing, s.selectedNodeIds, op);
      if (!order) return s;
      const rank = new Map(order.map((id, index) => [id, index + 1]));
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({ nodes: cloneNodes(existing), descriptionKey: `history.${{ front: 'front', back: 'back', forward: 'forward', backward: 'backward' }[op]}`, timestamp: Date.now() });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: existing.map((n) => ({ ...n, zIndex: rank.get(n.id) ?? n.zIndex })),
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  updateNodeText: (sessionId, nodeId, text) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      // Committing text that did not change is not an edit: no step, no autosave.
      const current = existing.find((n) => n.id === nodeId);
      if (!current || (current.text ?? '') === text) return s;
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.edit_text',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: existing.map((n) =>
            n.id === nodeId ? { ...n, text } : n,
          ),
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  bringToFront: (sessionId, nodeId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const newZ = maxZIndex(existing) + 1;
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.front',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: existing.map((n) =>
            n.id === nodeId ? { ...n, zIndex: newZ } : n,
          ),
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  sendToBack: (sessionId, nodeId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const minZ = Math.min(...existing.map((n) => n.zIndex));
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.back',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: existing.map((n) =>
            n.id === nodeId ? { ...n, zIndex: minZ - 1 } : n,
          ),
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  bringForward: (sessionId, nodeId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const sorted = [...existing].sort((a, b) => a.zIndex - b.zIndex);
      const idx = sorted.findIndex((n) => n.id === nodeId);
      if (idx === -1 || idx === sorted.length - 1) return s; // already top
      // swap zIndex with the one above
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.forward',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      const aboveId = sorted[idx + 1].id;
      const curZ = sorted[idx].zIndex;
      const aboveZ = sorted[idx + 1].zIndex;
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: existing.map((n) => {
            if (n.id === nodeId) return { ...n, zIndex: aboveZ };
            if (n.id === aboveId) return { ...n, zIndex: curZ };
            return n;
          }),
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  sendBackward: (sessionId, nodeId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const sorted = [...existing].sort((a, b) => a.zIndex - b.zIndex);
      const idx = sorted.findIndex((n) => n.id === nodeId);
      if (idx === -1 || idx === 0) return s; // already bottom
      // swap zIndex with the one below
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.backward',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      const belowId = sorted[idx - 1].id;
      const curZ = sorted[idx].zIndex;
      const belowZ = sorted[idx - 1].zIndex;
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: existing.map((n) => {
            if (n.id === nodeId) return { ...n, zIndex: belowZ };
            if (n.id === belowId) return { ...n, zIndex: curZ };
            return n;
          }),
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  clearSession: (sessionId) =>
    set((s) => {
      const { [sessionId]: _n, ...restNodes } = s.nodesBySession;
      const { [sessionId]: _v, ...restViewports } = s.viewportBySession;
      return {
        nodesBySession: restNodes,
        viewportBySession: restViewports,
      };
    }),

  toggleNodeVisibility: (sessionId, nodeId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      // 推 undo 快照
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.visibility',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: existing.map((n) =>
            n.id === nodeId ? { ...n, visible: n.visible === false ? true : false } : n,
          ),
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  reorderNodes: (sessionId, nodeId, newIndex) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      // 按 zIndex 排序（升序）
      const sorted = [...existing].sort((a, b) => a.zIndex - b.zIndex);
      const oldIdx = sorted.findIndex((n) => n.id === nodeId);
      if (oldIdx === -1) return s;
      // 移动到新位置
      const [moved] = sorted.splice(oldIdx, 1);
      sorted.splice(newIndex, 0, moved);
      // 重新分配 zIndex
      const reindexed = sorted.map((n, i) => ({ ...n, zIndex: i + 1 }));
      // 推 undo 快照
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.reorder',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: { ...s.nodesBySession, [sessionId]: reindexed },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  toggleNodeLock: (sessionId, nodeId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      const target = existing?.find((n) => n.id === nodeId);
      if (!existing || !target) return s;
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: target.locked ? 'history.unlock' : 'history.lock',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: existing.map((n) => (n.id === nodeId ? { ...n, locked: !n.locked } : n)),
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  /** Locks or unlocks the selection as one step: all lock unless every selected layer is already locked. */
  toggleSelectedLock: (sessionId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      const selected = existing?.filter((n) => s.selectedNodeIds.includes(n.id)) ?? [];
      if (!existing || selected.length === 0) return s;
      const lock = !selected.every((n) => n.locked);
      const ids = new Set(selected.map((n) => n.id));
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({ nodes: cloneNodes(existing), descriptionKey: lock ? 'history.lock' : 'history.unlock', timestamp: Date.now() });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: { ...s.nodesBySession, [sessionId]: existing.map((n) => (ids.has(n.id) ? { ...n, locked: lock } : n)) },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  toggleGroupVisibility: (sessionId, groupId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      const members = existing?.filter((n) => n.groupId === groupId) ?? [];
      if (!existing || members.length === 0) return s;
      // Hide unless every member is already hidden, the same rule the lock toggle uses.
      const visible = members.every((n) => n.visible === false);
      return {
        nodesBySession: { ...s.nodesBySession, [sessionId]: existing.map((n) => (n.groupId === groupId ? { ...n, visible } : n)) },
        ...recordStep(s, sessionId, existing, 'history.visibility'),
      };
    }),

  toggleGroupLock: (sessionId, groupId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      const members = existing?.filter((n) => n.groupId === groupId) ?? [];
      if (!existing || members.length === 0) return s;
      const lock = !members.every((n) => n.locked);
      return {
        nodesBySession: { ...s.nodesBySession, [sessionId]: existing.map((n) => (n.groupId === groupId ? { ...n, locked: lock } : n)) },
        ...recordStep(s, sessionId, existing, lock ? 'history.lock' : 'history.unlock'),
      };
    }),

  arrangeLayers: (sessionId, order, groupIds) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const byId = new Map(existing.map((n) => [n.id, n]));
      // Anything the order does not name keeps its place above the named layers.
      const named = order.filter((id) => byId.has(id));
      const rest = [...existing].sort((a, b) => a.zIndex - b.zIndex).filter((n) => !named.includes(n.id)).map((n) => n.id);
      const nodes = [...named, ...rest].map((id, index) => {
        const node = byId.get(id) as CanvasNode;
        const next: CanvasNode = { ...node, zIndex: index + 1 };
        if (id in groupIds) {
          if (groupIds[id] === undefined) delete next.groupId;
          else next.groupId = groupIds[id];
        }
        return next;
      });
      return { nodesBySession: { ...s.nodesBySession, [sessionId]: nodes }, ...recordStep(s, sessionId, existing, 'history.reorder') };
    }),

  duplicateSelected: (sessionId) => {
    const created: string[] = [];
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const selected = new Set(s.selectedNodeIds);
      if (!existing.some((n) => selected.has(n.id))) return s;
      if (overBitmapLimit(existing, pictureCount(existing.filter((n) => selected.has(n.id))))) return bitmapRefused(s, sessionId);
      // A duplicated group becomes a group of its own; the copies never share the original's group.
      const groups = new Map<string, string>();
      const stacked: CanvasNode[] = [];
      for (const node of [...existing].sort((a, b) => a.zIndex - b.zIndex)) {
        stacked.push(node);
        if (!selected.has(node.id)) continue;
        const copy: CanvasNode = { ...structuredClone(node), id: generateId(), locked: false };
        if (node.groupId) {
          if (!groups.has(node.groupId)) groups.set(node.groupId, generateId());
          copy.groupId = groups.get(node.groupId);
        }
        created.push(copy.id);
        stacked.push(copy);
      }
      return {
        nodesBySession: { ...s.nodesBySession, [sessionId]: stacked.map((n, index) => ({ ...n, zIndex: index + 1 })) },
        selectedNodeIds: [...created],
        ...recordStep(s, sessionId, existing, 'history.duplicate'),
      };
    });
    return created;
  },

  alignSelected: (sessionId, mode, target) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const updates = alignUpdates(existing, s.selectedNodeIds, mode, target);
      if (updates.length === 0) return s;
      return { nodesBySession: { ...s.nodesBySession, [sessionId]: withPositions(existing, updates) }, ...recordStep(s, sessionId, existing, 'history.align') };
    }),

  distributeSelected: (sessionId, axis) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const updates = distributeUpdates(existing, s.selectedNodeIds, axis);
      if (updates.length === 0) return s;
      return { nodesBySession: { ...s.nodesBySession, [sessionId]: withPositions(existing, updates) }, ...recordStep(s, sessionId, existing, 'history.distribute') };
    }),

  // ── Undo/Redo ──

  _pushUndo: (sessionId, descriptionKey) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId] || [];
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey,
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  undo: (sessionId) =>
    set((s) => {
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      if (undoStack.length === 0) return s;
      const entry = undoStack.pop()!;
      const currentNodes = s.nodesBySession[sessionId] || [];
      const redoStack = [...(s._redoStackBySession[sessionId] || [])];
      redoStack.push({
        nodes: cloneNodes(currentNodes),
        descriptionKey: entry.descriptionKey,
        timestamp: entry.timestamp,
        ...swappedSize(s, sessionId, entry),
      });
      if (redoStack.length > historyMaximumDepth()) redoStack.shift();
      return {
        ...restoredSize(s, sessionId, entry),
        nodesBySession: { ...s.nodesBySession, [sessionId]: entry.nodes },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: redoStack },
        selectedNodeIds: [],
      };
    }),

  redo: (sessionId) =>
    set((s) => {
      const redoStack = [...(s._redoStackBySession[sessionId] || [])];
      if (redoStack.length === 0) return s;
      const entry = redoStack.pop()!;
      const currentNodes = s.nodesBySession[sessionId] || [];
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(currentNodes),
        descriptionKey: entry.descriptionKey,
        timestamp: entry.timestamp,
        ...swappedSize(s, sessionId, entry),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        ...restoredSize(s, sessionId, entry),
        nodesBySession: { ...s.nodesBySession, [sessionId]: entry.nodes },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: redoStack },
        selectedNodeIds: [],
      };
    }),

  canUndo: (sessionId) => (get()._undoStackBySession[sessionId]?.length ?? 0) > 0,
  canRedo: (sessionId) => (get()._redoStackBySession[sessionId]?.length ?? 0) > 0,
  getUndoHistory: (sessionId) => get()._undoStackBySession[sessionId] || [],
  goToHistory: (sessionId, position) =>
    set((s) => {
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      const redoStack = [...(s._redoStackBySession[sessionId] || [])];
      const target = Math.min(undoStack.length + redoStack.length, Math.max(0, Math.round(position)));
      if (target === undoStack.length) return s;
      let nodes = s.nodesBySession[sessionId] || [];
      const startSize = s.canvasSizeBySession[sessionId];
      let size = startSize;
      // A resize step swaps the size it recorded with the current one, like undo and redo do.
      const step = (entry: UndoEntry): UndoEntry => {
        const record: UndoEntry = { nodes: cloneNodes(nodes), descriptionKey: entry.descriptionKey, timestamp: entry.timestamp };
        if (entry.canvasSize && size) { record.canvasSize = size; size = entry.canvasSize; }
        nodes = entry.nodes;
        return record;
      };
      while (undoStack.length > target) redoStack.push(step(undoStack.pop() as UndoEntry));
      while (undoStack.length < target) undoStack.push(step(redoStack.pop() as UndoEntry));
      const sizeChanged = size !== startSize && size !== undefined;
      return {
        ...(sizeChanged ? {
          canvasSizeBySession: { ...s.canvasSizeBySession, [sessionId]: size as CanvasSize },
          _canvasSizeDirtyBySession: { ...s._canvasSizeDirtyBySession, [sessionId]: true },
        } : {}),
        nodesBySession: { ...s.nodesBySession, [sessionId]: nodes },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: redoStack },
        selectedNodeIds: [],
      };
    }),

  // ── 视口控制 ──

  setViewport: (sessionId, update) =>
    set((s) => {
      const current =
        s.viewportBySession[sessionId] || configuredDefaultViewport();
      return {
        viewportBySession: {
          ...s.viewportBySession,
          [sessionId]: { ...current, ...update },
        },
      };
    }),

  resetViewport: (sessionId) =>
    set((s) => ({
      viewportBySession: {
        ...s.viewportBySession,
        [sessionId]: configuredDefaultViewport(),
      },
    })),

  zoomIn: (sessionId) =>
    set((s) => {
      const frontend = requireCanvasFrontendConfiguration().viewport;
      const current =
        s.viewportBySession[sessionId] || canvasDefaultViewport(frontend);
      return {
        viewportBySession: {
          ...s.viewportBySession,
          [sessionId]: {
            ...current,
            zoom: roundZoom(
              Math.min(current.zoom + frontend.zoom_step, frontend.maximum_zoom),
            ),
          },
        },
      };
    }),

  zoomOut: (sessionId) =>
    set((s) => {
      const frontend = requireCanvasFrontendConfiguration().viewport;
      const current =
        s.viewportBySession[sessionId] || canvasDefaultViewport(frontend);
      return {
        viewportBySession: {
          ...s.viewportBySession,
          [sessionId]: {
            ...current,
            zoom: roundZoom(
              Math.max(current.zoom - frontend.zoom_step, frontend.minimum_zoom),
            ),
          },
        },
      };
    }),

  fitToScreen: (sessionId, containerWidth, containerHeight) => {
    const nodes = get().getNodes(sessionId);

    if (nodes.length === 0) {
      get().resetViewport(sessionId);
      return;
    }

    // 计算所有节点的 bounding box
    const frontend = requireCanvasFrontendConfiguration().viewport;
    const padding = frontend.fit_padding_pixels;
    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;

    for (const n of nodes) {
      minX = Math.min(minX, n.x);
      minY = Math.min(minY, n.y);
      maxX = Math.max(maxX, n.x + n.width);
      maxY = Math.max(maxY, n.y + n.height);
    }

    const bbWidth = maxX - minX;
    const bbHeight = maxY - minY;
    const availW = containerWidth - padding * 2;
    const availH = containerHeight - padding * 2;

    // 计算 zoom 使 bounding box 完全可见
    const zoom = roundZoom(
      Math.max(
        frontend.minimum_zoom,
        Math.min(
          frontend.maximum_zoom,
          Math.min(availW / bbWidth, availH / bbHeight),
        ),
      ),
    );

    // 居中 pan
    const bbCenterX = (minX + maxX) / 2;
    const bbCenterY = (minY + maxY) / 2;
    const panX = containerWidth / 2 - bbCenterX * zoom;
    const panY = containerHeight / 2 - bbCenterY * zoom;

    set((s) => ({
      viewportBySession: {
        ...s.viewportBySession,
        [sessionId]: { zoom, panX, panY },
      },
    }));
  },

  // ── 选中 ──

  selectNode: (nodeId) => set({ selectedNodeIds: nodeId ? [nodeId] : [] }),

  toggleSelectNode: (nodeId) =>
    set((s) => {
      const ids = s.selectedNodeIds;
      return {
        selectedNodeIds: ids.includes(nodeId)
          ? ids.filter((id) => id !== nodeId)
          : [...ids, nodeId],
      };
    }),

  addToSelection: (nodeId) =>
    set((s) => ({
      selectedNodeIds: s.selectedNodeIds.includes(nodeId)
        ? s.selectedNodeIds
        : [...s.selectedNodeIds, nodeId],
    })),

  deselectNode: (nodeId) =>
    set((s) => ({
      selectedNodeIds: s.selectedNodeIds.filter((id) => id !== nodeId),
    })),

  selectNodes: (nodeIds) => set({ selectedNodeIds: nodeIds }),

  clearSelection: () => set({ selectedNodeIds: [] }),

  removeSelectedNodes: (sessionId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const toRemove = new Set(s.selectedNodeIds);
      // 锁定节点不可删除
      const filtered = existing.filter((n) => !(toRemove.has(n.id) && !n.locked));
      if (filtered.length === existing.length) return s;
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.delete_many',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: { ...s.nodesBySession, [sessionId]: filtered },
        selectedNodeIds: [],
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  copySelectedNodes: (sessionId) => {
    const s = get();
    const existing = s.nodesBySession[sessionId] || [];
    const selected = existing.filter((n) => s.selectedNodeIds.includes(n.id));
    set({ _clipboard: cloneNodes(selected), _clipboardId: selected.length > 0 ? generateId() : '' });
  },

  pasteNodes: (
    sessionId,
    offsetX = clipboardOffsetX(),
    offsetY = clipboardOffsetY(),
    at,
  ) =>
    set((s) => {
      const clipboard = s._clipboard;
      if (clipboard.length === 0) return s;
      const existing = s.nodesBySession[sessionId] || [];
      if (overBitmapLimit(existing, pictureCount(clipboard))) return bitmapRefused(s, sessionId);
      let currentMaxZ = maxZIndex(existing);
      let dx = offsetX;
      let dy = offsetY;
      if (at) {
        const left = Math.min(...clipboard.map((n) => n.x));
        const top = Math.min(...clipboard.map((n) => n.y));
        const right = Math.max(...clipboard.map((n) => n.x + n.width));
        const bottom = Math.max(...clipboard.map((n) => n.y + n.height));
        dx = at.x - (left + right) / 2;
        dy = at.y - (top + bottom) / 2;
      }
      const newNodes: CanvasNode[] = clipboard.map((n) => ({
        ...n,
        id: generateId(),
        locked: false, // a pasted copy is a new, editable layer (Photoshop behaviour)
        x: n.x + dx,
        y: n.y + dy,
        zIndex: ++currentMaxZ,
      }));
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.paste',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: { ...s.nodesBySession, [sessionId]: [...existing, ...newNodes] },
        selectedNodeIds: newNodes.map((n) => n.id),
        _clipboard: newNodes.map((n) => ({ ...n })), // 更新剪贴板位置以支持连续粘贴偏移
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  groupSelectedNodes: (sessionId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const selectedIds = new Set(s.selectedNodeIds);
      if (selectedIds.size < 2) return s; // 至少 2 个节点才能成组
      const groupId = generateId();
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.group',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: existing.map((n) =>
            selectedIds.has(n.id) ? { ...n, groupId } : n,
          ),
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  ungroupSelectedNodes: (sessionId) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const selectedIds = new Set(s.selectedNodeIds);
      // 找到选中节点涉及的所有 groupId
      const groupIds = new Set<string>();
      for (const n of existing) {
        if (selectedIds.has(n.id) && n.groupId) groupIds.add(n.groupId);
      }
      if (groupIds.size === 0) return s;
      const undoStack = [...(s._undoStackBySession[sessionId] || [])];
      undoStack.push({
        nodes: cloneNodes(existing),
        descriptionKey: 'history.ungroup',
        timestamp: Date.now(),
      });
      if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
      return {
        nodesBySession: {
          ...s.nodesBySession,
          [sessionId]: existing.map((n) =>
            n.groupId && groupIds.has(n.groupId) ? { ...n, groupId: undefined } : n,
          ),
        },
        _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
        _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      };
    }),

  updateNodes: (sessionId, updates, options) =>
    set((s) => {
      const existing = s.nodesBySession[sessionId];
      if (!existing) return s;
      const updateMap = new Map(updates.map((u) => [u.id, u]));
      const nodes = existing.map((n) => {
        const u = updateMap.get(n.id);
        return u && !n.locked ? { ...n, x: u.x, y: u.y } : n;
      });
      if (options?.recordHistory === false) {
        return { nodesBySession: { ...s.nodesBySession, [sessionId]: nodes } };
      }
      return { nodesBySession: { ...s.nodesBySession, [sessionId]: nodes }, ...recordCoalesced(s, sessionId, existing, 'history.move_many') };
    }),

  // ── Getter ──

  getNodes: (sessionId) => get().nodesBySession[sessionId] || [],
  getViewport: (sessionId) =>
    get().viewportBySession[sessionId] || configuredDefaultViewport(),

  // ── 持久化 ──

  setProjectForSession: (sessionId, projectId) =>
    set((s) => ({
      projectBySession: { ...s.projectBySession, [sessionId]: projectId },
    })),

  getProjectId: (sessionId) => get().projectBySession[sessionId],

  // A load or 409 reload replaces local state with the server's: older undo
  // snapshots no longer describe it, and undoing one would autosave stale data.
  loadNodesFromDB: (sessionId, nodes) => {
    const before = get();
    const hadRecords = (before._undoStackBySession[sessionId]?.length ?? 0) > 0
      || (before._redoStackBySession[sessionId]?.length ?? 0) > 0;
    sessionsWithDroppedHistory.delete(sessionId);
    set((s) => ({
      nodesBySession: { ...s.nodesBySession, [sessionId]: nodes },
      _undoStackBySession: { ...s._undoStackBySession, [sessionId]: [] },
      _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      _coalesceEntry: { ...s._coalesceEntry, [sessionId]: undefined },
    }));
    return hadRecords;
  },

  _setSavedHash: (sessionId, hash) =>
    set((s) => ({
      _savedHashBySession: { ...s._savedHashBySession, [sessionId]: hash },
    })),

  _setSavedRevision: (sessionId, revision) =>
    set((s) => ({
      _savedRevisionBySession: {
        ...s._savedRevisionBySession,
        [sessionId]: revision,
      },
    })),

  setSnapEnabled: (enabled) => set({ snapEnabledOverride: enabled }),

  setBgStyleForSession: (sessionId, bg) =>
    set((s) => ({
      bgStyleBySession: { ...s.bgStyleBySession, [sessionId]: bg },
    })),

  loadPlacedResults: (sessionId, placed) =>
    set((s) => ({
      placedResultsBySession: { ...s.placedResultsBySession, [sessionId]: placed },
      _placedDirtyBySession: { ...s._placedDirtyBySession, [sessionId]: false },
    })),

  setPlacedResults: (sessionId, placed) =>
    set((s) => ({
      placedResultsBySession: { ...s.placedResultsBySession, [sessionId]: placed },
      _placedDirtyBySession: { ...s._placedDirtyBySession, [sessionId]: true },
    })),

  _markPlacedSaved: (sessionId, saved) =>
    set((s) => (s.placedResultsBySession[sessionId] === saved
      ? { _placedDirtyBySession: { ...s._placedDirtyBySession, [sessionId]: false } }
      : s)),

  loadCanvasSize: (sessionId, size) =>
    set((s) => {
      const { [sessionId]: _previous, ...rest } = s.canvasSizeBySession;
      return {
        canvasSizeBySession: size ? { ...rest, [sessionId]: { width: size.width, height: size.height } } : rest,
        _canvasSizeDirtyBySession: { ...s._canvasSizeDirtyBySession, [sessionId]: false },
      };
    }),

  resizeCanvas: (sessionId, size, options) => {
    let clipped: string[] = [];
    set((s) => {
      const before = s.canvasSizeBySession[sessionId];
      if (before && before.width === size.width && before.height === size.height) return s;
      const next = { width: size.width, height: size.height };
      // Nodes keep their pixel size and their place relative to the anchor; nothing is stretched or
      // cropped away. Without a known size there is nothing to keep content relative to.
      const existing = s.nodesBySession[sessionId] || [];
      const resized = before
        ? resizeCanvasNodes(existing, before, next, options.anchor, options.center, options.painted)
        : { nodes: existing, clipped: [] };
      clipped = resized.clipped;
      const history = before
        ? (() => {
          const undoStack = [...(s._undoStackBySession[sessionId] || []), {
            nodes: cloneNodes(s.nodesBySession[sessionId] || []),
            descriptionKey: 'history.canvas_size',
            timestamp: Date.now(),
            canvasSize: before,
          }];
          if (undoStack.length > historyMaximumDepth()) { undoStack.shift(); noteHistoryDropped(sessionId); }
          return {
            _undoStackBySession: { ...s._undoStackBySession, [sessionId]: undoStack },
            _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
            _coalesceEntry: { ...s._coalesceEntry, [sessionId]: undefined },
          };
        })()
        : {};
      return {
        ...history,
        ...(resized.nodes !== existing && before ? { nodesBySession: { ...s.nodesBySession, [sessionId]: resized.nodes } } : {}),
        canvasSizeBySession: { ...s.canvasSizeBySession, [sessionId]: next },
        _canvasSizeDirtyBySession: { ...s._canvasSizeDirtyBySession, [sessionId]: true },
      };
    });
    return clipped;
  },

  setResizeAnchor: (anchor) => set({ resizeAnchorOverride: anchor }),

  initializeCanvas: (sessionId, nodes, size) => {
    sessionsWithDroppedHistory.delete(sessionId);
    set((s) => ({
      nodesBySession: { ...s.nodesBySession, [sessionId]: nodes },
      _undoStackBySession: { ...s._undoStackBySession, [sessionId]: [] },
      _redoStackBySession: { ...s._redoStackBySession, [sessionId]: [] },
      _coalesceEntry: { ...s._coalesceEntry, [sessionId]: undefined },
      canvasSizeBySession: { ...s.canvasSizeBySession, [sessionId]: { width: size.width, height: size.height } },
      _canvasSizeDirtyBySession: { ...s._canvasSizeDirtyBySession, [sessionId]: true },
    }));
  },

  _markCanvasSizeSaved: (sessionId, saved) =>
    set((s) => {
      const current = s.canvasSizeBySession[sessionId];
      return current && current.width === saved.width && current.height === saved.height
        ? { _canvasSizeDirtyBySession: { ...s._canvasSizeDirtyBySession, [sessionId]: false } }
        : s;
    }),
}),
    {
      name: 'canvas-store',
      // 只持久化有意义的状态，排除临时性字段
      partialize: (state) => ({
        nodesBySession: state.nodesBySession,
        viewportBySession: state.viewportBySession,
        activeTool: state.activeTool,
        activeShapeType: state.activeShapeType,
        lastStyle: state.lastStyle,
        backgroundColor: state.backgroundColor,
        autoSelect: state.autoSelect,
        projectBySession: state.projectBySession,
        _savedHashBySession: state._savedHashBySession,
        _savedRevisionBySession: state._savedRevisionBySession,
        bgStyleBySession: state.bgStyleBySession,
        placedResultsBySession: state.placedResultsBySession,
        _placedDirtyBySession: state._placedDirtyBySession,
        canvasSizeBySession: state.canvasSizeBySession,
        _canvasSizeDirtyBySession: state._canvasSizeDirtyBySession,
        snapEnabledOverride: state.snapEnabledOverride,
        resizeAnchorOverride: state.resizeAnchorOverride,
      }),
    },
  ),
);
