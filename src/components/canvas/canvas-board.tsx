'use client';

import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import {
  Arrow,
  Ellipse,
  Group,
  Image as KonvaImage,
  Layer,
  Line,
  Text as KonvaText,
  Path,
  Rect,
  Shape,
  Stage,
  Text,
  Transformer,
} from '@/lib/canvas/konva-runtime';
import type { KonvaEventObject } from 'konva/lib/Node';
import type Konva from 'konva';
import { toast } from 'sonner';

import { CanvasToolRail } from './canvas-tool-rail';
import { CanvasToolOptions } from './canvas-tool-options';
import { CanvasZoomControls } from './canvas-zoom-controls';
import { useBrushLayers } from './use-brush-layers';
import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { Surface } from '@/components/ui/surface';
import { sameMaskDraft, useMaskStore } from '@/store/mask-store';
import { artboardToWorld, inpaintGeometry, paintMask, rectangleRegion, worldToArtboard } from '@/lib/canvas/inpaint-export';
import { CanvasExportError, exportArtboardToBlob } from '@/lib/canvas/export-canvas';
import { CanvasImageCache, pictureKeys, pictureSize, splitPictureKey } from '@/lib/canvas/image-cache';
import { canvasText } from '@/lib/canvas/presentation';
import { flushPendingEdits } from '@/lib/canvas/pending-edits';
import { exportAndUploadSnapshot, reportSnapshotFailure } from '@/lib/canvas/send-snapshot';
import { canvasOwnsCopy, clipboardMarker } from '@/lib/canvas/clipboard';
import { matchesShortcut, matchingShortcut } from '@/lib/canvas/shortcut';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { readCanvasToken, useCanvasPalette } from '@/lib/canvas/canvas-palette';
import { artboardLabelBox, labelTextWidth } from '@/lib/canvas/artboard-label';
import {
  bindCanvasZoomGestures,
  fitArtboardViewport,
  fitContentOrArtboardViewport,
  fitViewportToRect,
  initialArtboardViewport,
  visibleContentBounds,
  viewportAfterResize,
  zoomAroundCenter,
  zoomKeepingFrame,
  zoomStepViewport,
} from '@/lib/canvas/viewport';
import { layerDraggable, pansView, resolveToolKey } from '@/lib/canvas/tool-keys';
import { clickTargets, selectionAfterClick } from '@/lib/canvas/auto-select';
import { outsideDimRects, visibleWorld } from '@/lib/canvas/outside-dim';
import { antsOutlines } from '@/lib/canvas/selection-preview';
import { brushCursorRadius, drawMaskOverlay, maskOverlaySteps, overlayTintKind, type MaskOverlayTarget } from '@/lib/canvas/mask-overlay';
import { sampleColor } from '@/lib/canvas/eyedropper';
import { brushTargetAt, type BrushTargetState } from '@/lib/canvas/brush-target';
import { blankLayerFill, brushCreatesCanvasLayer, canvasLayerNode, isBlankLayer } from '@/lib/canvas/initial-layer';
import { cropTargetAt, marqueeSelection } from '@/lib/canvas/canvas-pick';
import { artboardBackdrop, checkerTile } from '@/lib/canvas/artboard-backdrop';
import { nodeBox, unionRect, type AlignMode } from '@/lib/canvas/align';
import { useStudioStore } from '@/store/studio-store';
import { stageFitInsets } from '@/lib/workspace/tool-rail-layout';
import {
  canvasPointerToWorld,
  nodeContainsCanvasPoint,
  shapeGeometryFromDrag,
} from '@/lib/canvas/drawing-geometry';
import { createRecenterGesture, isEditableTarget } from '@/lib/canvas/recenter-key';
import { onHeldKeysLost } from '@/lib/canvas/held-keys';
import { panelShortcutTabs } from '@/lib/workspace/panel-tabs';
import { ownsNavigationKeys } from '@/lib/workspace/stage-keys';
import { artboardRatioLabel } from '@/lib/canvas/artboard';
import { snapRect, type SnapGuide } from '@/lib/canvas/snapping';
import { dragUpdates, movedUnion, snapTargetsFor, startDragSession, type DragSession } from '@/lib/canvas/multi-drag';
import { canvasMenuItems, layerShortcut, nodeMenuItems, selectableNodeIds, type CanvasMenuItemId } from '@/lib/canvas/context-menu';
import {
  ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuShortcut, ContextMenuSub, ContextMenuSubContent, ContextMenuSubTrigger, ContextMenuTrigger,
} from '@/components/ui/context-menu';
import { cn } from '@/lib/utils';
import {
  canvasDefaultViewport,
  configureCanvasFrontend,
  generateId,
  useCanvasStore,
  type CanvasNode,
  type CanvasToolType,
  type ShapeType,
} from '@/store/canvas-store';

export interface CanvasBoardProps {
  sessionId: string;
  /**
   * True when a Space tap belongs to someone else right now (the large view is open,
   * or a picture is selected and the tap opens it). The board then only pans on Space.
   */
  spaceTapTaken?: () => boolean;
  className?: string;
  activeTool: CanvasToolType;
  onToolChange: (tool: CanvasToolType) => void;
  onUploadImage: () => void;
  onImageGenerator: () => void;
  /** 合并可见图层（盖印）：Ctrl+Alt+Shift+E。 */
  onMergeVisible?: () => void;
  onUploadBlob?: (blob: Blob, filename: string) => Promise<string>;
  /** 提交重绘：整个虚线画板栅格化成的一张图（已上传）和同尺寸同坐标的蒙版。 */
  onInpaintRequest?: (request: { imageUrl: string; source: Blob; mask: Blob; frame: { x: number; y: number; width: number; height: number }; size: { width: number; height: number }; onSubmitted?: () => void }) => void;
  /** 所选模型是否支持蒙版局部重绘（按 Offering 参数声明判断）。 */
  maskSupported?: boolean;
  /** 点击蒙版工具但当前模型不支持时回调（由外层弹确认切换模型）。 */
  onMaskUnsupported?: () => void;
  /** 虚线画板区（世界坐标，Schema 驱动）；null 不渲染。 */
  artboard?: { x: number; y: number; width: number; height: number } | null;
  /** 发给模型的输出像素（所选尺寸）；重绘的图片与蒙版都按它输出。 */
  deliveryPixels?: { width: number; height: number } | null;
  /** 绘图区包含 Stage，不包含底部工具栏；供外层按实际可用区域适配。 */
  drawingAreaRef?: React.RefObject<HTMLDivElement | null>;
  /** 双击画板上的图片节点（舞台用它打开全窗口大图）。 */
  /** 下载画板上的图片节点（右键菜单“下载这一层”用，与图片操作条同一处理）。 */
  onDownloadImage?: (node: { imageUrl: string }) => void;
  onImageDoubleClick?: (node: { nodeId: string; imageUrl: string; taskId?: string }) => void;
}

interface DraftShape {
  shapeType: ShapeType;
  startX: number;
  startY: number;
  currentX: number;
  currentY: number;
}

interface DraftStroke {
  points: number[];
  erase: boolean;
}

type SelectionMode = 'replace' | 'add' | 'subtract';

interface SelectionDraft {
  kind: 'marquee' | 'lasso';
  mode: SelectionMode;
  x0: number;
  y0: number;
  x1: number;
  y1: number;
  /** Lasso path, x0, y0, x1, y1 ... */
  points: number[];
}

/** Shift adds to the selection, Alt takes away from it, no key replaces it (Photoshop). */
function selectionModeOf(event: { shiftKey?: boolean; altKey?: boolean }): SelectionMode {
  if (event.altKey) return 'subtract';
  if (event.shiftKey) return 'add';
  return 'replace';
}

const EMPTY_NODES: CanvasNode[] = [];

/** A mask stroke's points (artboard coordinates) as flat world coordinates for drawing. */
function maskStrokeWorldPoints(artboard: { x: number; y: number; width: number; height: number }, stroke: { points: number[] }): number[] {
  const world: number[] = [];
  for (let point = 0; point < stroke.points.length; point += 2) {
    const mapped = artboardToWorld(artboard, { x: stroke.points[point], y: stroke.points[point + 1] });
    world.push(mapped.x, mapped.y);
  }
  return world;
}

/** The board's picture cache (#287): it holds the current layers' pictures only; see `CanvasImageCache`. */
function useImageCache(nodes: readonly CanvasNode[]) {
  const [, bump] = useState(0);
  const [cache] = useState(() => new CanvasImageCache({ resolve: safeAssetURL, onChange: () => bump((value) => value + 1) }));
  // Keyed by the addresses, not the node objects: moving or restyling a layer does not touch the cache.
  const { referenced, wanted } = useMemo(() => pictureKeys(nodes), [nodes]);
  useEffect(() => {
    cache.sync(splitPictureKey(referenced), splitPictureKey(wanted));
  }, [cache, referenced, wanted]);
  useEffect(() => () => cache.clear(), [cache]);
  return cache;
}

/** 节点局部坐标 → 世界坐标（toNodeLocal 的逆变换）。 */
function toNodeWorld(node: CanvasNode, localX: number, localY: number) {
  const angle = ((node.rotation ?? 0) * Math.PI) / 180;
  const cos = Math.cos(angle);
  const sin = Math.sin(angle);
  return {
    x: node.x + localX * cos - localY * sin,
    y: node.y + localX * sin + localY * cos,
  };
}

/** 世界坐标 → 节点局部坐标（Konva 绕左上角 (x,y) 旋转，先平移再逆旋转）。 */
function toNodeLocal(node: CanvasNode, worldX: number, worldY: number) {
  const dx = worldX - node.x;
  const dy = worldY - node.y;
  const angle = ((node.rotation ?? 0) * Math.PI) / 180;
  const cos = Math.cos(angle);
  const sin = Math.sin(angle);
  return { x: dx * cos + dy * sin, y: -dx * sin + dy * cos };
}


export function CanvasBoard({
  sessionId,
  spaceTapTaken,
  className,
  activeTool,
  onToolChange,
  onMergeVisible,
  onUploadImage,
  onImageGenerator,
  onUploadBlob,
  onInpaintRequest,
  maskSupported = true,
  onMaskUnsupported,
  artboard = null,
  deliveryPixels = null,
  drawingAreaRef,
  onImageDoubleClick,
  onDownloadImage,
}: CanvasBoardProps) {
  const { schema } = useProductSchema();
  const palette = useCanvasPalette();
  const canvasSchema = schema.canvas;
  const keyboard = canvasSchema.frontend.keyboard;
  const drawing = canvasSchema.frontend.drawing;
  const nodes = useCanvasStore((state) => state.nodesBySession[sessionId] ?? EMPTY_NODES);
  const selectedIds = useCanvasStore((state) => state.selectedNodeIds);
  const clipboardSize = useCanvasStore((state) => state._clipboard.length);
  const viewport =
    useCanvasStore((state) => state.viewportBySession[sessionId]) ??
    canvasDefaultViewport(canvasSchema.frontend.viewport);
  const canvasBg =
    useCanvasStore((state) => state.bgStyleBySession[sessionId]) ??
    canvasSchema.defaults.bg_style;
  const activeShapeType = useCanvasStore((state) => state.activeShapeType);
  const lastStyle = useCanvasStore((state) => state.lastStyle);
  const autoSelect = useCanvasStore((state) => state.autoSelect);
  const maskErasing = useMaskStore((state) => state.erasing);
  const containerRef = useRef<HTMLDivElement>(null);
  const setDrawingAreaRef = useCallback((element: HTMLDivElement | null) => {
    containerRef.current = element;
    if (drawingAreaRef) drawingAreaRef.current = element;
  }, [drawingAreaRef]);
  const stageRef = useRef<React.ComponentRef<typeof Stage>>(null);
  const transformerRef = useRef<React.ComponentRef<typeof Transformer>>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [marquee, setMarquee] = useState<{ x0: number; y0: number; x1: number; y1: number } | null>(null);
  const [draftShape, setDraftShape] = useState<DraftShape | null>(null);
  const [draftStroke, setDraftStroke] = useState<DraftStroke | null>(null);
  // Marquee / lasso being drawn (world coordinates); `mode` is fixed when the drag starts from the modifier keys.
  const [selectionDraft, setSelectionDraft] = useState<SelectionDraft | null>(null);
  const [editing, setEditing] = useState<{ nodeId: string; value: string } | null>(null);
  const [brushStroke, setBrushStroke] = useState<{
    nodeId: string;
    erase: boolean;
    points: number[];
  } | null>(null);
  const [cropDraft, setCropDraft] = useState<{
    nodeId: string;
    x: number;
    y: number;
    width: number;
    height: number;
  } | null>(null);
  const cropTransformerRef = useRef<React.ComponentRef<typeof Transformer>>(null);
  // The crop frame has its own undo: Ctrl+Z walks its earlier rectangles and never reaches the canvas history.
  const cropHistory = useRef<{ past: NonNullable<typeof cropDraft>[]; future: NonNullable<typeof cropDraft>[] }>({ past: [], future: [] });
  // Canvas shortcuts act (and cancel the browser's own meaning) only while the pointer or focus is on the canvas.
  const pointerInside = useRef(false);
  // The mask draft lives in a store: the composer chip, the stage header and the question
  // before leaving the object all need to see it. One draft, belonging to its session.
  const maskDraft = useMaskStore((state) => state.draft);
  // The mask belongs to the canvas (artboard coordinates), not to any picture on it.
  const maskState = maskDraft && maskDraft.sessionId === sessionId ? maskDraft : null;
  // Another session's draft is parked, not lost; this session's comes back into view.
  useEffect(() => {
    useMaskStore.getState().activate(sessionId);
  }, [sessionId]);
  // The mask follows the dashed frame: when the frame moves or resizes the strokes move with the picture,
  // and a mask that no longer touches the frame is cleared (one undoable step) with a notice.
  const artboardX = artboard?.x;
  const artboardY = artboard?.y;
  const artboardWidth = artboard?.width;
  const artboardHeight = artboard?.height;
  useEffect(() => {
    if (artboardX === undefined || artboardY === undefined || !artboardWidth || !artboardHeight) return;
    const outcome = useMaskStore.getState().remapFrame(sessionId, { x: artboardX, y: artboardY, width: artboardWidth, height: artboardHeight });
    if (outcome === 'cleared') toast(schemaText(schema, 'ui.canvas.mask.frame_cleared'));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [artboardX, artboardY, artboardWidth, artboardHeight, sessionId]);
  const completeMaskRef = useRef<() => void>(() => {});
  const exitMaskRef = useRef<() => void>(() => {});
  // "局部重绘" with a selection already made (marquee / lasso / mask) submits it without going through the mask tool.
  const submitRequest = useMaskStore((state) => state.submitRequest);
  const seenSubmitRequest = useRef(submitRequest);
  useEffect(() => {
    if (submitRequest === seenSubmitRequest.current) return;
    seenSubmitRequest.current = submitRequest;
    completeMaskRef.current();
  }, [submitRequest]);
  const selectionSchema = canvasSchema.frontend.selection;
  const maskSchema = canvasSchema.frontend.mask;
  // #310: the mask's own Schema tint (a CSS token, re-read with the theme), not the lighter selection tint.
  const maskTintColor = useMemo(
    () => readCanvasToken(maskSchema.tint_color_token, palette.handle),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [maskSchema.tint_color_token, palette],
  );
  const outsideDim = canvasSchema.frontend.artboard.outside_dim;
  // #310: the tint over the editable area is ONE layer — every step (and the stroke being drawn) is painted
  // at full strength with the very `paintMask` that builds the submitted mask, then laid over the board once
  // with the Schema mask tint (`canvas.frontend.mask`), so overlapping or repeated strokes only widen the area.
  const maskStrokes = maskState?.strokes;
  const maskCurrent = maskState?.current;
  const maskOverlay = useMemo(() => maskOverlaySteps(maskStrokes, maskCurrent), [maskStrokes, maskCurrent]);
  const maskOverlayStyle = overlayTintKind(maskOverlay) === 'mask'
    ? { tintColor: maskTintColor, tintOpacity: maskSchema.tint_opacity }
    : { tintColor: selectionSchema.tint_color, tintOpacity: selectionSchema.tint_opacity };
  const maskOverlayScratch = useRef<HTMLCanvasElement | null>(null);
  const outlines = useMemo(() => (maskStrokes ? antsOutlines(maskStrokes) : []), [maskStrokes]);
  // Marching ants: the dash offset steps on a Schema tick while there is an outline to animate.
  const [antsOffset, setAntsOffset] = useState(0);
  const hasOutlines = outlines.length > 0 || selectionDraft !== null;
  useEffect(() => {
    if (!hasOutlines) return;
    const dashLength = selectionSchema.ants_dash.reduce((total, part) => total + part, 0);
    const timer = window.setInterval(() => setAntsOffset((value) => (value + 1) % dashLength), selectionSchema.ants_tick_milliseconds);
    return () => window.clearInterval(timer);
  }, [hasOutlines, selectionSchema.ants_dash, selectionSchema.ants_tick_milliseconds]);
  const [spaceDown, setSpaceDown] = useState(false);
  const dragSnapshot = useRef(false);
  const dragSession = useRef<DragSession | null>(null);
  // #280: the right-click menu is Base UI's ContextMenu around the stage, so it opens at the pointer with
  // context-menu dismissal (no hover-out close). The stage's own contextmenu handler runs first and records
  // whether a layer was hit; the menu reads it when it opens.
  const [contextMenu, setContextMenu] = useState<{ onNode: boolean } | null>(null);
  const contextMenuTarget = useRef<{ onNode: boolean } | null>(null);
  const pendingCollapse = useRef<{ id: string; ctrl: boolean } | null>(null);
  // The clone an Alt-drag is about to carry: its drag start must not duplicate again.
  const altCloneId = useRef<string | null>(null);
  const snapSchema = canvasSchema.frontend.snap;
  const snapEnabled = useCanvasStore((state) => state.snapEnabledOverride ?? snapSchema.default_enabled);
  const [snapGuides, setSnapGuides] = useState<SnapGuide[]>([]);
  const textSchema = canvasSchema.frontend.text;
  const labelSchema = canvasSchema.frontend.artboard.label;
  const artboardLabelColor = useMemo(
    () => readCanvasToken(labelSchema.color_token, palette.erase),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [labelSchema.color_token, palette],
  );
  const artboardLabelPlate = useMemo(
    () => readCanvasToken(labelSchema.background_token, palette.canvasBackground),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [labelSchema.background_token, palette],
  );
  // #246/#282: the label inside the frame names the canvas pixels and its ratio ("工作区：2048×2048，1:1"),
  // the size every export has.
  const artboardRatio = artboard
    ? artboardRatioLabel(artboard.width, artboard.height, labelSchema.common_ratios)
    : '';
  const artboardLabel = artboard && artboardRatio
    ? formatSchemaText(schemaText(schema, 'ui.canvas.size.label'), {
      width: Math.round(artboard.width), height: Math.round(artboard.height), ratio: artboardRatio,
    })
    : '';
  const artboardLabelWidth = useMemo(
    () => labelTextWidth(artboardLabel, labelSchema.font_size_pixels),
    [artboardLabel, labelSchema.font_size_pixels],
  );
  // #246: fitting the dashed frame keeps it clear of the floating rail and bars; it stays centred (#282).
  const railExpanded = useStudioStore((state) => state.railExpanded);
  const fitInsets = useMemo(() => stageFitInsets(railExpanded), [railExpanded]);
  const recenterGesture = useRef(createRecenterGesture(keyboard.space_tap_milliseconds));
  const contentBounds = useMemo(
    () => visibleContentBounds(nodes, canvasSchema),
    [canvasSchema, nodes],
  );
  const recenter = useCallback(() => {
    if (!artboard) return false;
    const view = fitContentOrArtboardViewport(
      contentBounds, artboard, size, canvasSchema.frontend.viewport, fitInsets,
    );
    if (!view) return false;
    useCanvasStore.getState().setViewport(sessionId, view);
    return true;
  }, [artboard, canvasSchema.frontend.viewport, contentBounds, fitInsets, sessionId, size]);
  const recenterRef = useRef(recenter);
  const spaceTapTakenRef = useRef(spaceTapTaken);
  spaceTapTakenRef.current = spaceTapTaken;
  recenterRef.current = recenter;
  // #282: the zoom gestures and the resize observer read the frame and drawing area from here.
  const framing = useRef({ artboard, size });
  framing.current = { artboard, size };
  // Palette changes with the theme; re-read the schema-named token when it does.
  const snapGuideColor = useMemo(
    () => readCanvasToken(snapSchema.guide_color_token, palette.primary),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [snapSchema.guide_color_token, palette],
  );
  const pinchDistance = useRef(0);

  const backdrop = useMemo(() => artboardBackdrop(canvasSchema), [canvasSchema]);
  const checkerImage = useMemo(() => (backdrop.kind === 'checker' ? checkerTile(backdrop) : null), [backdrop]);
  const backgrounds = canvasSchema.frontend.backgrounds;
  const background =
    backgrounds.find((candidate) => candidate.value === canvasBg) ??
    backgrounds[0];

  useEffect(() => {
    configureCanvasFrontend(canvasSchema.frontend);
  }, [canvasSchema.frontend]);

  useEffect(() => {
    const element = containerRef.current;
    if (!element) return;
    let previous = { width: element.clientWidth, height: element.clientHeight };
    const observer = new ResizeObserver(() => {
      const next = { width: element.clientWidth, height: element.clientHeight };
      // #282: a centred frame stays centred when the drawing area grows or shrinks.
      const state = useCanvasStore.getState();
      const current = state.viewportBySession[sessionId];
      const moved = current ? viewportAfterResize(current, previous, next, framing.current.artboard) : null;
      if (moved) state.setViewport(sessionId, moved);
      previous = next;
      setSize(next);
    });
    observer.observe(element);
    setSize(previous);
    return () => observer.disconnect();
  }, [sessionId]);

  useEffect(() => {
    if (!artboard || size.width <= 0 || size.height <= 0 || useCanvasStore.getState().viewportBySession[sessionId]) return;
    useCanvasStore.getState().setViewport(sessionId, initialArtboardViewport(artboard, size, canvasSchema.frontend.viewport, fitInsets));
  }, [artboard, canvasSchema.frontend.viewport, fitInsets, sessionId, size]);

  const images = useImageCache(nodes);
  // Konva draws at the device pixel ratio (its default); reduced layer copies are chosen against it (#291).
  const devicePixelRatio = typeof window === 'undefined' ? 1 : window.devicePixelRatio || 1;

  const sorted = useMemo(
    () => [...nodes].sort((a, b) => a.zIndex - b.zIndex),
    [nodes],
  );

  const toWorld = useCallback(
    (pointer: { x: number; y: number }) => {
      const stage = stageRef.current;
      return stage ? canvasPointerToWorld(stage, pointer) : pointer;
    },
    [],
  );

  useEffect(() => {
    const transformer = transformerRef.current;
    const stage = stageRef.current;
    if (!transformer || !stage) return;
    const selected = selectedIds
      .map((id) => stage.findOne(`#node-${id}`))
      .filter((node): node is NonNullable<typeof node> => Boolean(node))
      // A locked layer can be selected in the panel but offers no handles.
      .filter((node) => !(node.attrs.listening === false));
    transformer.nodes(selected);
    transformer.getLayer()?.batchDraw();
  }, [selectedIds, sorted, size]);

  useEffect(() => {
    const transformer = cropTransformerRef.current;
    const stage = stageRef.current;
    if (!transformer || !stage || !cropDraft) return;
    const node = stage.findOne('#crop-rect');
    if (!node) return;
    transformer.nodes([node as Konva.Node]);
    transformer.getLayer()?.batchDraw();
  }, [cropDraft]);

  const store = useCanvasStore;
  // Subscribed so a size shortcut redraws the brush preview without a pointer move (#245).
  const brushSize = useCanvasStore((state) => state.brushSize);

  useEffect(() => {
    const element = stageRef.current?.container();
    if (!element) return;
    const config = canvasSchema.frontend.viewport;
    return bindCanvasZoomGestures(element, config, {
      zoom: (pointer, factor) => {
        const state = store.getState();
        const current = state.viewportBySession[sessionId] ?? canvasDefaultViewport(config);
        // #282: a centred frame stays centred while zooming.
        state.setViewport(sessionId, zoomKeepingFrame(current, pointer, factor, config, framing.current.artboard, framing.current.size));
      },
      // The wheel and a two-finger swipe move the canvas under the pointer.
      pan: (dx, dy) => {
        const state = store.getState();
        const current = state.viewportBySession[sessionId] ?? canvasDefaultViewport(config);
        state.setViewport(sessionId, { panX: current.panX + dx, panY: current.panY + dy });
      },
    });
  }, [canvasSchema.frontend.viewport, sessionId, store]);

  const commitStroke = useCallback(
    (stroke: DraftStroke) => {
      if (stroke.points.length < drawing.pencil_minimum_points * 2) return;
      let minX = Infinity;
      let minY = Infinity;
      let maxX = -Infinity;
      let maxY = -Infinity;
      for (let index = 0; index < stroke.points.length; index += 2) {
        minX = Math.min(minX, stroke.points[index]);
        maxX = Math.max(maxX, stroke.points[index]);
        minY = Math.min(minY, stroke.points[index + 1]);
        maxY = Math.max(maxY, stroke.points[index + 1]);
      }
      const pad = drawing.pencil_padding;
      const relative = stroke.points.map((value, index) =>
        index % 2 === 0 ? value - minX + pad : value - minY + pad,
      );
      const pathData = `M ${relative
        .map((value, index) => (index % 2 === 0 ? `${value} ${relative[index + 1]}` : ''))
        .filter(Boolean)
        .join(' L ')}`;
      store.getState().addNode(sessionId, {
        id: generateId(),
        type: 'pencil',
        taskId: '',
        imageUrl: '',
        pathData,
        x: minX - pad,
        y: minY - pad,
        width: maxX - minX + pad * 2,
        height: maxY - minY + pad * 2,
        strokeWidth: lastStyle.strokeWidth,
        strokeColor: stroke.erase ? 'erase' : lastStyle.strokeColor,
      });
    },
    [lastStyle, sessionId, store],
  );

  const brush = useBrushLayers({
    sessionId,
    images,
    eraserColor: palette.eraserComposite,
    idleMilliseconds: drawing.brush_idle_commit_milliseconds,
    blankBitmapMaximumPixels: canvasSchema.frontend.initial_layer.maximum_bitmap_pixels,
    filename: `${canvasSchema.frontend.export_filename_fallback}-edit.png`,
    upload: onUploadBlob,
    redraw: () => stageRef.current?.batchDraw(),
    onUploadFailed: () => toast.error(schemaText(schema, 'ui.canvas.brush.upload_failed')),
  });
  const brushRef = useRef(brush);
  brushRef.current = brush;
  useEffect(() => {
    brush.reconcile(nodes);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodes]);
  // Leaving the brush for any other tool uploads the local bitmap as one step; leaving crop drops its frame.
  useEffect(() => {
    if (activeTool !== 'brush' && activeTool !== 'eraser') void brushRef.current.retireAll();
    if (activeTool !== 'crop') {
      setCropDraft(null);
      cropHistory.current = { past: [], future: [] };
    }
  }, [activeTool]);

  const commitBrushStroke = useCallback(
    (stroke: { nodeId: string; erase: boolean; points: number[] }) => {
      // A tap without movement is one point: it still paints a dot (#245).
      if (stroke.points.length < 2) return;
      const node = store.getState().getNodes(sessionId).find((item) => item.id === stroke.nodeId);
      if (!node || node.type !== 'image' || node.locked) return;
      brushRef.current.addStroke(node, {
        points: stroke.points,
        erase: stroke.erase,
        size: store.getState().brushSize,
        color: lastStyle.strokeColor || palette.ink,
      });
    },
    [lastStyle.strokeColor, palette.ink, sessionId, store],
  );

  // #245：位图就绪才能落笔（已有本地位图，或原图已加载）。悬停预览与落笔共用。
  const brushBitmapReady = useCallback((node: CanvasNode) => {
    // An unpainted initial layer (#246) has no picture to wait for.
    if (brush.hasLayer(node.id) || isBlankLayer(node)) return true;
    return pictureSize(node.imageUrl ? images.get(node.imageUrl) : undefined) !== null;
  }, [brush, images]);
  const [brushHover, setBrushHover] = useState<{ x: number; y: number; state: BrushTargetState; nodeId: string | null } | null>(null);
  // #310: the mask brush's footprint under the pointer (canvas units), shown before and while painting.
  const [maskHover, setMaskHover] = useState<{ x: number; y: number } | null>(null);
  useEffect(() => {
    if (activeTool !== 'mask') setMaskHover(null);
  }, [activeTool]);
  useEffect(() => {
    if (activeTool !== 'brush' && activeTool !== 'eraser') setBrushHover(null);
  }, [activeTool]);
  const brushHintShown = useRef<{ state: BrushTargetState; at: number } | null>(null);
  // 落笔没有作用时说明原因；同一原因 4 秒内只提示一次。
  const explainBrushTarget = useCallback((state: BrushTargetState) => {
    if (state === 'ready') return;
    const now = Date.now();
    const last = brushHintShown.current;
    if (last && last.state === state && now - last.at < 4000) return;
    brushHintShown.current = { state, at: now };
    toast.info(schemaText(schema, `ui.canvas.brush.target_${state}`));
  }, [schema]);

  const handleStageMouseDown = useCallback(
    (event: KonvaEventObject<MouseEvent | TouchEvent>) => {
      if (editing) setEditing(null);
      const stage = stageRef.current;
      if (!stage) return;
      const pointer = stage.getPointerPosition();
      if (!pointer) return;
      const world = toWorld(pointer);
      if (
        activeTool === 'hand' ||
        spaceDown ||
        ('button' in event.evt && event.evt.button === 1)
      )
        return;
      const target = event.target;
      const isStage = target === stage;
      const modifiers = event.evt as { shiftKey?: boolean; altKey?: boolean };
      if (activeTool === 'marquee' || activeTool === 'lasso') {
        setSelectionDraft({ kind: activeTool, mode: selectionModeOf(modifiers), x0: world.x, y0: world.y, x1: world.x, y1: world.y, points: [world.x, world.y] });
        return;
      }
      if (activeTool === 'zoom') {
        // Click zooms in one Schema step around the pointer; Alt+click zooms out.
        const config = canvasSchema.frontend.viewport;
        const current = store.getState().viewportBySession[sessionId] ?? canvasDefaultViewport(config);
        const next = zoomStepViewport(current, size, modifiers.altKey ? -1 : 1, config);
        store.getState().setViewport(sessionId, zoomKeepingFrame(current, pointer, next.zoom / current.zoom, config, framing.current.artboard, framing.current.size));
        return;
      }
      if (activeTool === 'eyedropper') {
        const color = sampleColor(
          nodes.map((node) => ({ ...node, crop: brush.canvasFor(node.id) ? undefined : node.crop })),
          world,
          (nodeId, sx, sy) => {
            const node = nodes.find((item) => item.id === nodeId);
            const source = brush.canvasFor(nodeId) ?? (node ? images.get(node.imageUrl) : undefined);
            if (!source) return null;
            try {
              const probe = document.createElement('canvas');
              probe.width = 1;
              probe.height = 1;
              const context = probe.getContext('2d', { willReadFrequently: true });
              if (!context) return null;
              context.drawImage(source, sx, sy, 1, 1, 0, 0, 1, 1);
              const data = context.getImageData(0, 0, 1, 1).data;
              return [data[0], data[1], data[2], data[3]];
            } catch {
              // A picture from another origin taints the bitmap; its colour cannot be read.
              return null;
            }
          },
          (nodeId) => {
            const local = brush.canvasFor(nodeId);
            if (local) return { width: local.width, height: local.height };
            const node = nodes.find((item) => item.id === nodeId);
            return pictureSize(node ? images.get(node.imageUrl) : undefined);
          },
        );
        // Alt+click picks the background colour instead of the foreground (as in Photoshop).
        if (color) {
          if (modifiers.altKey) store.getState().setBackgroundColor(color);
          else store.getState().setLastStyle({ strokeColor: color });
        }
        return;
      }
      if (activeTool === 'select') {
        if (isStage) {
          if (!('shiftKey' in event.evt && (event.evt.shiftKey || event.evt.ctrlKey || event.evt.metaKey)))
            store.getState().clearSelection();
          setMarquee({ x0: world.x, y0: world.y, x1: world.x, y1: world.y });
        }
        return;
      }
      if (activeTool === 'text') {
        // Keep the browser from moving focus off the editor that opens on this click.
        event.evt.preventDefault();
        const textId = generateId();
        const defaultText = schemaText(schema, 'ui.canvas.node.default_text');
        store.getState().addNode(sessionId, {
          id: textId,
          type: 'text',
          taskId: '',
          imageUrl: '',
          text: defaultText,
          x: world.x,
          y: world.y,
          width: drawing.text_node_width,
          height: drawing.text_node_height,
          // The node keeps its own size and colour; the Schema supplies the defaults once, here.
          fontSize: canvasSchema.frontend.style.default_font_size,
          strokeColor: lastStyle.strokeColor || textSchema.default_color,
        });
        setEditing({ nodeId: textId, value: defaultText });
        onToolChange('select');
        return;
      }
      if (activeTool === 'shape') {
        setDraftShape({
          shapeType: activeShapeType,
          startX: world.x,
          startY: world.y,
          currentX: world.x,
          currentY: world.y,
        });
        return;
      }
      if (activeTool === 'pencil') {
        setDraftStroke({ points: [world.x, world.y], erase: false });
        return;
      }
      if (activeTool === 'brush' || activeTool === 'eraser') {
        // #245：与悬停预览同一个目标规则——指针下最上层的可见图片。
        const target = brushTargetAt(sorted, world, brushBitmapReady);
        if (target.state === 'ready' && target.node) {
          const local = toNodeLocal(target.node, world.x, world.y);
          setBrushStroke({
            nodeId: target.node.id,
            erase: activeTool === 'eraser',
            points: [local.x, local.y],
          });
        } else if (artboard && brushCreatesCanvasLayer(target.state, activeTool, world, artboard, canvasSchema.frontend.initial_layer.enabled)) {
          // #246: no picture under the brush inside the canvas: a blank canvas layer below everything takes the stroke.
          const layer = canvasLayerNode(generateId(), artboard, canvasSchema, schemaText(schema, 'ui.canvas.layers.initial_name'));
          // At the picture limit the person was told (#149); the stroke has no layer to land on.
          if (!store.getState().addCanvasLayer(sessionId, layer)) return;
          const local = toNodeLocal({ ...layer, zIndex: 0 }, world.x, world.y);
          setBrushStroke({ nodeId: layer.id, erase: false, points: [local.x, local.y] });
        } else {
          explainBrushTarget(target.state);
        }
        return;
      }
      if (activeTool === 'mask') {
        useMaskStore.getState().beginStroke(sessionId, worldToArtboard(artboard, world), store.getState().brushSize, artboard ?? undefined, useMaskStore.getState().erasing);
        return;
      }
      if (activeTool === 'crop') {
        // An unpainted initial layer (#246) is not a picture to crop.
        const target = cropTargetAt(sorted, world);
        if (target) {
          cropHistory.current = { past: [], future: [] };
          setCropDraft({
            nodeId: target.id,
            x: target.x,
            y: target.y,
            width: target.width,
            height: target.height,
          });
        }
      }
    },
    [activeShapeType, activeTool, artboard, brush, brushBitmapReady, canvasSchema.frontend, canvasSchema.frontend.style.default_font_size, editing, explainBrushTarget, images, lastStyle, nodes, size, onToolChange, palette.ink, schema, sessionId, sorted, spaceDown, store, textSchema.default_color, toWorld],
  );

  const handleStageMouseMove = useCallback(
    (event: KonvaEventObject<MouseEvent | TouchEvent>) => {
      const stage = stageRef.current;
      if (!stage) return;
      const pointer = stage.getPointerPosition();
      if (!pointer) return;
      const world = toWorld(pointer);
      if (activeTool === 'brush' || activeTool === 'eraser') {
        const target = brushTargetAt(sorted, world, brushBitmapReady);
        // Where a stroke would create a canvas layer (#246) the brush is ready, not "nothing to paint".
        const creates = brushCreatesCanvasLayer(target.state, activeTool, world, artboard, canvasSchema.frontend.initial_layer.enabled);
        setBrushHover({ x: world.x, y: world.y, state: creates ? 'ready' : target.state, nodeId: target.node?.id ?? null });
      }
      if (activeTool === 'mask') setMaskHover({ x: world.x, y: world.y });
      if (marquee) {
        setMarquee((current) => (current ? { ...current, x1: world.x, y1: world.y } : current));
        return;
      }
      if (selectionDraft) {
        setSelectionDraft((current) => (current ? {
          ...current,
          x1: world.x,
          y1: world.y,
          points: current.kind === 'lasso' ? [...current.points, world.x, world.y] : current.points,
        } : current));
        return;
      }
      if (draftShape) {
        setDraftShape((current) => (current ? { ...current, currentX: world.x, currentY: world.y } : current));
        return;
      }
      if (draftStroke) {
        setDraftStroke((current) =>
          current ? { ...current, points: [...current.points, world.x, world.y] } : current,
        );
        return;
      }
      if (brushStroke) {
        const node = nodes.find((item) => item.id === brushStroke.nodeId);
        if (!node) return;
        const local = toNodeLocal(node, world.x, world.y);
        setBrushStroke((current) =>
          current ? { ...current, points: [...current.points, local.x, local.y] } : current,
        );
        return;
      }
      if (maskState?.current) {
        useMaskStore.getState().extendStroke(sessionId, worldToArtboard(artboard, world));
      }
    },
    [activeTool, artboard, brushBitmapReady, brushStroke, draftShape, draftStroke, marquee, nodes, maskState, selectionDraft, sessionId, sorted, toWorld],
  );

  const handleStageMouseUp = useCallback(() => {
    if (selectionDraft) {
      const draft = selectionDraft;
      setSelectionDraft(null);
      const threshold = drawing.selection_drag_threshold_pixels;
      const maskStore = useMaskStore.getState();
      const isClick = draft.kind === 'marquee'
        ? Math.abs(draft.x1 - draft.x0) <= threshold && Math.abs(draft.y1 - draft.y0) <= threshold
        : draft.points.length < 6;
      if (isClick) {
        // A click without a drag drops the selection (Photoshop); with Shift / Alt it leaves it alone.
        if (draft.mode === 'replace') maskStore.clearSelection(sessionId);
      } else if (artboard) {
        const region = draft.kind === 'marquee'
          ? rectangleRegion(worldToArtboard(artboard, { x: draft.x0, y: draft.y0 }), worldToArtboard(artboard, { x: draft.x1, y: draft.y1 }))
          : {
              points: draft.points.map((value, index) => (index % 2 === 0 ? value - artboard.x : value - artboard.y)),
              size: 0,
              region: true as const,
            };
        maskStore.commitRegion(sessionId, region, draft.mode, artboard);
      }
    }
    if (marquee) {
      const left = Math.min(marquee.x0, marquee.x1);
      const right = Math.max(marquee.x0, marquee.x1);
      const top = Math.min(marquee.y0, marquee.y1);
      const bottom = Math.max(marquee.y0, marquee.y1);
      if (
        right - left > drawing.selection_drag_threshold_pixels ||
        bottom - top > drawing.selection_drag_threshold_pixels
      ) {
        // A marquee never picks the unpainted initial layer (#246): it covers the whole canvas.
        store.getState().selectNodes(marqueeSelection(nodes, { left, top, right, bottom }));
      }
      setMarquee(null);
    }
    if (draftShape) {
      const start = { x: draftShape.startX, y: draftShape.startY };
      const end = { x: draftShape.currentX, y: draftShape.currentY };
      const geometry = shapeGeometryFromDrag(draftShape.shapeType, start, end);
      const directed = draftShape.shapeType === 'line' || draftShape.shapeType === 'arrow';
      const largeEnough = directed
        ? Math.hypot(end.x - start.x, end.y - start.y) > drawing.minimum_committed_shape_size
        : geometry.width > drawing.minimum_committed_shape_size &&
          geometry.height > drawing.minimum_committed_shape_size;
      if (largeEnough) {
        store.getState().addNode(sessionId, {
          id: generateId(),
          type: 'shape',
          shapeType: draftShape.shapeType,
          taskId: '',
          imageUrl: '',
          ...geometry,
          strokeWidth: lastStyle.strokeWidth,
          strokeColor: lastStyle.strokeColor,
          fillColor: lastStyle.fillColor,
        });
      }
      setDraftShape(null);
    }
    if (draftStroke) {
      commitStroke(draftStroke);
      setDraftStroke(null);
    }
    if (brushStroke) {
      commitBrushStroke(brushStroke);
      setBrushStroke(null);
    }
    if (maskState?.current) {
      useMaskStore.getState().endStroke(sessionId);
    }
  }, [artboard, brushStroke, maskState, commitBrushStroke, commitStroke, drawing.selection_drag_threshold_pixels, draftShape, draftStroke, lastStyle, marquee, nodes, selectionDraft, sessionId, store]);

  // Undo and redo of the crop frame: its own rectangles, never the canvas history.
  const cropUndo = useCallback((redo: boolean) => {
    setCropDraft((current) => {
      const history = cropHistory.current;
      const target = redo ? history.future.pop() : history.past.pop();
      if (!target || !current) return current;
      (redo ? history.past : history.future).push(current);
      return target;
    });
  }, []);
  /** Remembers the crop frame before a drag or resize starts, so each gesture is one local undo step. */
  const rememberCrop = useCallback(() => {
    setCropDraft((current) => {
      if (current) cropHistory.current = { past: [...cropHistory.current.past, current], future: [] };
      return current;
    });
  }, []);

  // The tabs of the right panel that have a key (F7 图层, Alt+F9 历史), read from the Schema.
  const panelShortcuts = useMemo(() => panelShortcutTabs(schema), [schema]);
  useEffect(() => {
    const down = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (isEditableTarget(target)) return;
      if (event.code === keyboard.space_code) {
        // A focused button/menu/slider keeps Space for itself: no pan, no recenter, no preventDefault.
        if (ownsNavigationKeys(target)) return;
        event.preventDefault();
        if (!event.repeat && !spaceTapTakenRef.current?.()) recenterGesture.current.press(performance.now());
        setSpaceDown(true);
        return;
      }
      // The pointer or focus is on the canvas: its shortcuts are the canvas's, not the browser's.
      const canvasActive = pointerInside.current || Boolean(containerRef.current?.contains(document.activeElement));
      if (canvasActive && matchingShortcut(event, keyboard.reserved_shortcuts)) event.preventDefault();
      const ownsCopy = () => canvasOwnsCopy({ canvasActive, selection: window.getSelection(), canvas: containerRef.current });
      const viewportConfig = canvasSchema.frontend.viewport;
      const currentView = () => store.getState().viewportBySession[sessionId] ?? canvasDefaultViewport(viewportConfig);
      // F7 / Alt+F9: switch the right panel to its 图层 / 历史 tab. They only switch; nothing is closed or hidden.
      const panelShortcut = panelShortcuts.find((entry) => matchesShortcut(event, entry.descriptor));
      if (panelShortcut) {
        event.preventDefault();
        useStudioStore.getState().setPanelTab('image', panelShortcut.id);
        return;
      }
      // Ctrl/Cmd+0 fits the dashed frame: the same fit as the toolbar's fit button.
      if ((event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey && event.key === keyboard.recenter_key) {
        const view = artboard ? fitArtboardViewport(artboard, size, viewportConfig, fitInsets) : null;
        if (view) {
          event.preventDefault();
          store.getState().setViewport(sessionId, view);
        }
        return;
      }
      if (canvasActive) {
        if (matchesShortcut(event, keyboard.generate_shortcut)) {
          event.preventDefault();
          onImageGenerator();
          return;
        }
        const zoomIn = matchesShortcut(event, keyboard.zoom_in_shortcut) || ((event.metaKey || event.ctrlKey) && !event.altKey && event.key === '+');
        if (zoomIn || matchesShortcut(event, keyboard.zoom_out_shortcut)) {
          event.preventDefault();
          store.getState().setViewport(sessionId, zoomStepViewport(currentView(), size, zoomIn ? 1 : -1, viewportConfig));
          return;
        }
        if (matchesShortcut(event, keyboard.zoom_actual_shortcut)) {
          event.preventDefault();
          store.getState().setViewport(sessionId, zoomAroundCenter(currentView(), size, 1, viewportConfig));
          return;
        }
        if (matchesShortcut(event, keyboard.zoom_selection_shortcut)) {
          event.preventDefault();
          const chosen = nodes.filter((node) => store.getState().selectedNodeIds.includes(node.id));
          const rect = chosen.length > 0 ? unionRect(chosen.map(nodeBox)) : artboard;
          const view = rect ? fitViewportToRect(rect, size, viewportConfig, fitInsets) : null;
          if (view) store.getState().setViewport(sessionId, view);
          return;
        }
        if (matchesShortcut(event, keyboard.cut_shortcut) && ownsCopy()) {
          event.preventDefault();
          store.getState().copySelectedNodes(sessionId);
          const id = store.getState()._clipboardId;
          if (id) void navigator.clipboard?.writeText(clipboardMarker(id)).catch(() => undefined);
          store.getState().removeSelectedNodes(sessionId);
          return;
        }
        if (matchesShortcut(event, keyboard.duplicate_shortcut)) {
          event.preventDefault();
          store.getState().duplicateSelected(sessionId);
          return;
        }
        if (matchesShortcut(event, keyboard.deselect_shortcut)) {
          event.preventDefault();
          // The painted / marqueed selection first; with none, the layer selection.
          if (!useMaskStore.getState().clearSelection(sessionId)) store.getState().clearSelection();
          return;
        }
        if (matchesShortcut(event, keyboard.invert_selection_shortcut)) {
          event.preventDefault();
          if (artboard) useMaskStore.getState().invertSelection(sessionId, artboard);
          return;
        }
      }
      // While painting a mask: Enter finishes it, Esc leaves the tool.
      if (activeTool === 'mask' && !event.metaKey && !event.ctrlKey && !event.altKey && !event.isComposing) {
        if (event.key === keyboard.mask_confirm_key) {
          event.preventDefault();
          completeMaskRef.current();
          return;
        }
        if (event.key === keyboard.mask_exit_key) {
          event.preventDefault();
          exitMaskRef.current();
          return;
        }
      }
      const key = event.key.toLowerCase();
      const meta = event.metaKey || event.ctrlKey;
      // Esc with a painted / marquee / lasso selection drops that selection first (as Ctrl+D does); the next
      // Esc does what it always did. The mask tool keeps its own exit flow above.
      if (!meta && !event.altKey && key === 'escape' && canvasActive && !event.defaultPrevented && activeTool !== 'mask') {
        if (useMaskStore.getState().clearSelection(sessionId)) {
          event.preventDefault();
          return;
        }
      }
      // Esc leaves the tool first; on the move tool it drops the layer selection.
      if (!meta && !event.altKey && key === 'escape' && canvasActive && !event.defaultPrevented) {
        if (activeTool !== 'select') onToolChange('select');
        else if (store.getState().selectedNodeIds.length > 0) store.getState().clearSelection();
        return;
      }
      // The mask, brush, eraser and crop tools own their undo/redo while they are on: one stroke or one
      // rectangle per step, and with nothing left in their own history the key does nothing; it never falls
      // through to the canvas history (which would remove the picture under the tool).
      const maskTool = activeTool === 'mask';
      const brushTool = activeTool === 'brush' || activeTool === 'eraser';
      const cropTool = activeTool === 'crop';
      const toolOwnsHistory = maskTool || brushTool || cropTool;
      const toolHistory = (redo: boolean) => {
        if (maskTool) {
          if (redo) useMaskStore.getState().redoStroke(sessionId);
          else useMaskStore.getState().undoStroke(sessionId);
        } else if (brushTool) {
          if (redo) brushRef.current.redo();
          else brushRef.current.undo();
        } else cropUndo(redo);
      };
      if (meta && key === keyboard.undo_key) {
        event.preventDefault();
        if (toolOwnsHistory) toolHistory(event.shiftKey);
        else if (event.shiftKey) store.getState().redo(sessionId);
        else store.getState().undo(sessionId);
        return;
      }
      if (meta && key === keyboard.alternate_redo_key) {
        event.preventDefault();
        if (toolOwnsHistory) toolHistory(true);
        else store.getState().redo(sessionId);
        return;
      }
      if (meta && key === keyboard.copy_key) {
        // Off the canvas, or with chat text selected, Ctrl/Cmd+C is the page's own copy (#281).
        if (!ownsCopy()) return;
        store.getState().copySelectedNodes(sessionId);
        // The marker lets the paste decide which clipboard is newer (see lib/canvas/clipboard).
        const id = store.getState()._clipboardId;
        if (id) void navigator.clipboard?.writeText(clipboardMarker(id)).catch(() => undefined);
        return;
      }
      // Ctrl+V is handled once, by the paste event (the workspace board): a picture on the system
      // clipboard or the copied layers, never both.
      if (meta && key === keyboard.paste_key) return;
      if (meta && key === keyboard.select_all_key) {
        // Off the canvas Ctrl/Cmd+A keeps its page meaning (selecting chat text, for instance).
        if (!canvasActive) return;
        event.preventDefault();
        store.getState().selectNodes(selectableNodeIds(nodes));
        return;
      }
      if (matchesShortcut(event, keyboard.lock_shortcut)) {
        event.preventDefault();
        store.getState().toggleSelectedLock(sessionId);
        return;
      }
      if (matchesShortcut(event, keyboard.merge_visible_shortcut)) {
        event.preventDefault();
        onMergeVisible?.();
        return;
      }
      if (meta && key === keyboard.group_key) {
        event.preventDefault();
        if (event.shiftKey) store.getState().ungroupSelectedNodes(sessionId);
        else store.getState().groupSelectedNodes(sessionId);
        return;
      }
      if (meta) {
        const layerOp = layerShortcut(event, {
          forward: keyboard.layer_forward_key,
          backward: keyboard.layer_backward_key,
        });
        if (layerOp) {
          event.preventDefault();
          store.getState().reorderSelected(sessionId, layerOp);
          return;
        }
      }
      if (keyboard.delete_keys.includes(key)) {
        // Backspace on a focused board must never also navigate the page back (WebKit does by default).
        event.preventDefault();
        // Inside the mask, brush or crop tool Delete belongs to the tool, never to the layers underneath.
        if (toolOwnsHistory) return;
        // The whole selection goes in one step.
        store.getState().removeSelectedNodes(sessionId);
        return;
      }
      if (!meta && !event.altKey && activeTool === 'select' && key.startsWith('arrow')) {
        // The large view spent ←/→ already (it runs on window capture and prevents the default): no nudge.
        if (event.defaultPrevented) return;
        const step = event.shiftKey ? keyboard.nudge_large_pixels : keyboard.nudge_pixels;
        const dx = key === 'arrowleft' ? -step : key === 'arrowright' ? step : 0;
        const dy = key === 'arrowup' ? -step : key === 'arrowdown' ? step : 0;
        // Locked layers do not move, and a nudge that moves nothing records no step.
        const moving = store.getState().selectedNodeIds.filter((id) => nodes.some((node) => node.id === id && !node.locked));
        if (moving.length > 0 && (dx !== 0 || dy !== 0)) {
          event.preventDefault();
          // Held or repeated presses within the history throttle window fold into one undo step.
          store.getState().updateNodes(sessionId, nodes.filter((node) => moving.includes(node.id)).map((node) => ({ id: node.id, x: node.x + dx, y: node.y + dy })));
        }
        return;
      }
      if (meta) return;
      if (event.altKey) return;
      const brushTools = activeTool === 'brush' || activeTool === 'eraser' || activeTool === 'pencil' || activeTool === 'mask';
      if (brushTools && (key === keyboard.brush_size_down_key || key === keyboard.brush_size_up_key)) {
        event.preventDefault();
        store.getState().stepBrushSize(key === keyboard.brush_size_up_key ? 1 : -1);
        return;
      }
      if (key === keyboard.swap_colors_key) {
        // In the mask tool X flips between painting and erasing the selection (Photoshop's quick mask).
        if (maskTool) useMaskStore.getState().toggleErasing();
        else store.getState().swapColors();
        return;
      }
      if (key === keyboard.default_colors_key) {
        if (maskTool) useMaskStore.getState().setErasing(false);
        else store.getState().resetColors();
        return;
      }
      const resolved = resolveToolKey(event, keyboard, { tool: activeTool, shapeType: store.getState().activeShapeType });
      if (!resolved) return;
      // The selection tools share the mask's draft, so only the tools that would drop it ask first.
      const keepsDraft = ['mask', 'marquee', 'lasso', 'zoom', 'eyedropper', 'hand'].includes(resolved.tool);
      if (maskTool && resolved.tool !== 'mask' && !keepsDraft && (useMaskStore.getState().draft?.strokes.length ?? 0) > 0) {
        // Leaving the mask for another tool runs the mask's exit flow first: confirm before discarding.
        useMaskStore.getState().ask({ kind: 'exit' });
        return;
      }
      if (resolved.shapeType) store.getState().setActiveShapeType(resolved.shapeType);
      if (resolved.tool === 'mask' && !maskSupported) onMaskUnsupported?.();
      else onToolChange(resolved.tool);
    };
    const up = (event: KeyboardEvent) => {
      if (event.code === keyboard.space_code) {
        setSpaceDown(false);
        if (!isEditableTarget(event.target) && recenterGesture.current.release(performance.now())) recenterRef.current();
      }
    };
    window.addEventListener('keydown', down);
    window.addEventListener('keyup', up);
    return () => {
      window.removeEventListener('keydown', down);
      window.removeEventListener('keyup', up);
    };
  }, [activeTool, artboard, canvasSchema.frontend.viewport, cropUndo, keyboard, maskSupported, nodes, onImageGenerator, onMaskUnsupported, onMergeVisible, onToolChange, panelShortcuts, sessionId, size, store]);

  // #263: Space held while the window loses focus (Cmd+Tab) or the page is hidden never sends its keyup here;
  // a stuck Space would keep every layer undraggable until Space is pressed again.
  useEffect(() => onHeldKeysLost(window, document, () => {
    setSpaceDown(false);
    recenterGesture.current.cancel();
  }), []);

  const cursor =
    activeTool === 'hand' || spaceDown
      ? 'grab'
      : activeTool === 'select'
        ? 'default'
        : activeTool === 'zoom'
          ? 'zoom-in'
          : activeTool === 'brush' || activeTool === 'eraser' || activeTool === 'mask'
            ? 'none'
            : 'crosshair';

  const patternStyle = useMemo(() => {
    if (!background) return { backgroundColor: palette.canvasBackground } as const;
    const base = { backgroundColor: background.background_color } as Record<string, string>;
    if (background.pattern === 'dots') {
      base.backgroundImage = `radial-gradient(circle, ${background.pattern_color} ${background.pattern_width}px, transparent ${background.pattern_width}px)`;
      base.backgroundSize = `${background.cell_size}px ${background.cell_size}px`;
    }
    if (background.pattern === 'grid') {
      base.backgroundImage = `linear-gradient(${background.pattern_color} 1px, transparent 1px), linear-gradient(90deg, ${background.pattern_color} 1px, transparent 1px)`;
      base.backgroundSize = `${background.cell_size}px ${background.cell_size}px`;
    }
    return base;
  }, [background]);

  // 蒙版和裁剪的"应用 / 取消"放进顶部参数条，不再在画布上叠悬浮按钮。
  const completeMask = () => {
    if (!maskState || maskState.strokes.length === 0 || !onInpaintRequest || !onUploadBlob || !artboard) return;
    // One picture and one mask, both of the artboard at the chosen output size, aligned pixel for pixel.
    const geometry = inpaintGeometry(artboard, deliveryPixels);
    const canvas = document.createElement('canvas');
    canvas.width = geometry.size.width;
    canvas.height = geometry.size.height;
    const context = canvas.getContext('2d');
    if (!context) return;
    paintMask(context, geometry, maskState.strokes, palette.eraserComposite);
    const strokes = maskState.strokes;
    const submittedDraft = maskState;
    void (async () => {
      try {
        // #286 (after #274): save brush edits, export the mask and the board, upload; each failure keeps its step.
        const exported: { mask?: Blob } = {};
        const { picture: source, url: imageUrl } = await exportAndUploadSnapshot({
          flush: () => flushPendingEdits(sessionId),
          exportPicture: async () => {
            const mask = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'));
            if (!mask) throw new CanvasExportError(canvasText(canvasSchema, 'error.export_failed'), 'encode', { ...geometry.size, reason: 'mask toBlob returned null' });
            exported.mask = mask;
            return exportArtboardToBlob(useCanvasStore.getState().getNodes(sessionId), canvasSchema, artboard, deliveryPixels);
          },
          upload: (picture) => onUploadBlob(picture, `${canvasSchema.frontend.export_filename_fallback}-inpaint.png`),
          maximumBytes: schema.runtime.input_assets.upload_max_bytes,
        });
        const mask = exported.mask;
        if (!mask) return;
        // The strokes may have changed while the picture uploaded; submit only the mask that matches it.
        // The uploaded picture is left behind (no delete-asset path is called); the person presses 完成 again.
        if (useMaskStore.getState().draft?.strokes !== strokes) {
          toast.error(schemaText(schema, 'ui.studio.mask.changed_during_upload'));
          return;
        }
        onInpaintRequest({
          imageUrl, source, mask, frame: { x: artboard.x, y: artboard.y, width: artboard.width, height: artboard.height }, size: geometry.size,
          // Keep the draft while the prompt dialog is open: cancel or a failed preparation must not lose the selection.
          // Only the exact draft this submission came from is cleared, and only once the task is accepted.
          onSubmitted: () => {
            const current = useMaskStore.getState().draft;
            if (sameMaskDraft(current, submittedDraft)) useMaskStore.getState().clearDraft();
          },
        });
        onToolChange('select');
      } catch (error) {
        // #274/#286: say which step failed, with the upload's request ID, and log it on the server (#233).
        toast.error(reportSnapshotFailure(schema, 'inpaint', error));
      }
    })();
  };
  completeMaskRef.current = completeMask;
  const exitMask = () => {
    if (maskState && maskState.strokes.length > 0) {
      // A painted mask is not thrown away by one key: the person is asked first.
      useMaskStore.getState().ask({ kind: 'exit' });
      return;
    }
    useMaskStore.getState().clearDraft();
    onToolChange('select');
  };
  exitMaskRef.current = exitMask;
  const keyLabel = (key: string) => schema.content.messages[`ui.studio.mask.key.${key}`] ?? key;
  const maskActions = onInpaintRequest && (maskState || activeTool === 'mask') ? (
    <>
      <Action
        type="button"
        size="sm"
        data-testid="mask-done"
        disabled={!maskState || maskState.strokes.length === 0 || onUploadBlob === undefined}
        onClick={completeMask}
      >
        {formatSchemaText(schemaText(schema, 'ui.studio.mask.done'), { key: keyLabel(keyboard.mask_confirm_key) })}
      </Action>
      <Action
        type="button"
        variant="ghost"
        size="sm"
        data-testid="mask-exit"
        onClick={exitMask}
        className="text-muted-foreground"
      >
        {formatSchemaText(schemaText(schema, 'ui.studio.mask.exit'), { key: keyLabel(keyboard.mask_exit_key) })}
      </Action>
    </>
  ) : null;
  const cropActions = cropDraft ? (
    <>

          <Action
            type="button"
            size="sm"
            onClick={() => {
              const node = nodes.find((item) => item.id === cropDraft.nodeId);
              if (node && node.type === 'image') {
                const naturalWidth = node.crop?.width ?? pictureSize(images.get(node.imageUrl))?.width ?? node.width;
                const scale = naturalWidth / node.width;
                // Position, size and crop change together: one undo step.
                store.getState().updateNode(sessionId, node.id, {
                  x: cropDraft.x,
                  y: cropDraft.y,
                  width: cropDraft.width,
                  height: cropDraft.height,
                  crop: {
                    x: (node.crop?.x ?? 0) + (cropDraft.x - node.x) * scale,
                    y: (node.crop?.y ?? 0) + (cropDraft.y - node.y) * scale,
                    width: cropDraft.width * scale,
                    height: cropDraft.height * scale,
                  },
                });
              }
              setCropDraft(null);
              onToolChange('select');
            }}
          >
            {schemaText(schema, 'ui.canvas.crop.apply')}
          </Action>
          <Action
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setCropDraft(null)}
            className="text-muted-foreground"
          >
            {schemaText(schema, 'ui.canvas.crop.cancel')}
          </Action>
    </>
  ) : null;
  const editingNode = editing ? nodes.find((item) => item.id === editing.nodeId && item.type === 'text') : undefined;
  const commitEditing = () => {
    if (!editing) return;
    store.getState().updateNodeText(sessionId, editing.nodeId, editing.value);
    setEditing(null);
  };

  const menuItems = useMemo(() => {
    if (!contextMenu) return [];
    const state = {
      nodes,
      selectedIds,
      clipboardCount: clipboardSize,
      canRecenter: Boolean(artboard),
      hasDownloadHandler: Boolean(onDownloadImage),
      hasArtboard: Boolean(artboard),
    };
    return contextMenu.onNode ? nodeMenuItems(state) : canvasMenuItems(state);
  }, [contextMenu, nodes, selectedIds, clipboardSize, artboard, onDownloadImage]);
  const menuSeparatorBefore = useMemo(() => new Set<CanvasMenuItemId>(['copy', 'group', 'lock', 'align_left', 'download']), []);
  const menuLabel = (id: CanvasMenuItemId) => schemaText(schema, ({
    front: 'ui.canvas.menu.front',
    forward: 'ui.canvas.layers.up',
    backward: 'ui.canvas.layers.down',
    back: 'ui.canvas.menu.back',
    copy: 'ui.canvas.menu.copy',
    cut: 'ui.canvas.menu.cut',
    duplicate: 'ui.canvas.menu.duplicate',
    align_left: 'ui.canvas.options.align_left',
    align_hcenter: 'ui.canvas.options.align_hcenter',
    align_right: 'ui.canvas.options.align_right',
    align_top: 'ui.canvas.options.align_top',
    align_vcenter: 'ui.canvas.options.align_vcenter',
    align_bottom: 'ui.canvas.options.align_bottom',
    paste: 'ui.canvas.menu.paste',
    delete: 'ui.canvas.action.delete',
    group: 'ui.canvas.menu.group',
    ungroup: 'ui.canvas.menu.ungroup',
    lock: 'ui.canvas.menu.lock',
    unlock: 'ui.canvas.menu.unlock',
    download: 'ui.canvas.action.download',
    recenter: 'ui.canvas.toolbar.recenter',
    select_all: 'ui.canvas.menu.select_all',
  } satisfies Record<CanvasMenuItemId, string>)[id]);
  const menuShortcut = (id: CanvasMenuItemId): string => {
    const forward = (keyboard.layer_forward_key).toUpperCase();
    const backward = (keyboard.layer_backward_key).toUpperCase();
    const mod = typeof navigator !== 'undefined' && /mac/i.test(navigator.platform) ? '\u2318' : 'Ctrl+';
    const isMac = typeof navigator !== 'undefined' && /mac/i.test(navigator.platform);
    const shortcutHint = (descriptor: string): string => descriptor.split(/\+(?=.)/).map((part) => {
      const name = part.toLowerCase();
      if (name === 'mod') return isMac ? '\u2318' : 'Ctrl+';
      if (name === 'ctrl') return isMac ? '\u2303' : 'Ctrl+';
      if (name === 'meta') return isMac ? '\u2318' : 'Win+';
      if (name === 'alt') return isMac ? '\u2325' : 'Alt+';
      if (name === 'shift') return isMac ? '\u21e7' : 'Shift+';
      return part.toUpperCase();
    }).join('');
    const shiftMark = typeof navigator !== 'undefined' && /mac/i.test(navigator.platform) ? '\u21e7' : 'Shift+';
    switch (id) {
      case 'front': return `${mod}${shiftMark}${forward}`;
      case 'forward': return `${mod}${forward}`;
      case 'backward': return `${mod}${backward}`;
      case 'back': return `${mod}${shiftMark}${backward}`;
      case 'copy': return `${mod}${keyboard.copy_key.toUpperCase()}`;
      case 'cut': return shortcutHint(keyboard.cut_shortcut);
      case 'duplicate': return shortcutHint(keyboard.duplicate_shortcut);
      case 'paste': return `${mod}${keyboard.paste_key.toUpperCase()}`;
      case 'group': return `${mod}${keyboard.group_key.toUpperCase()}`;
      case 'ungroup': return `${mod}${shiftMark}${keyboard.group_key.toUpperCase()}`;
      case 'select_all': return `${mod}${keyboard.select_all_key.toUpperCase()}`;
      case 'lock': case 'unlock': return shortcutHint(keyboard.lock_shortcut);
      default: return '';
    }
  };
  const runMenuItem = (id: CanvasMenuItemId) => {
    const state = store.getState();
    setContextMenu(null);
    switch (id) {
      case 'front': case 'forward': case 'backward': case 'back': state.reorderSelected(sessionId, id); break;
      case 'copy': state.copySelectedNodes(sessionId); break;
      case 'cut': {
        state.copySelectedNodes(sessionId);
        const marker = store.getState()._clipboardId;
        if (marker) void navigator.clipboard?.writeText(clipboardMarker(marker)).catch(() => undefined);
        state.removeSelectedNodes(sessionId);
        break;
      }
      case 'duplicate': state.duplicateSelected(sessionId); break;
      case 'align_left': case 'align_hcenter': case 'align_right': case 'align_top': case 'align_vcenter': case 'align_bottom':
        state.alignSelected(sessionId, id.slice('align_'.length) as AlignMode, artboard);
        break;
      case 'paste': state.pasteNodes(sessionId); break;
      case 'delete': state.removeSelectedNodes(sessionId); break;
      case 'group': state.groupSelectedNodes(sessionId); break;
      case 'ungroup': state.ungroupSelectedNodes(sessionId); break;
      case 'lock': case 'unlock': state.toggleSelectedLock(sessionId); break;
      case 'select_all': state.selectNodes(selectableNodeIds(nodes)); break;
      case 'recenter': recenter(); break;
      case 'download': {
        const image = nodes.find((node) => state.selectedNodeIds.includes(node.id) && node.type === 'image');
        if (image) onDownloadImage?.({ imageUrl: image.imageUrl });
        break;
      }
    }
  };

  return (
    <div className={cn('relative flex min-h-0 flex-col overflow-hidden', className)}>
      <div
        ref={setDrawingAreaRef}
        data-canvas-drawing-area
        className="relative min-h-0 flex-1 overflow-hidden"
        style={patternStyle}
        onMouseEnter={() => { pointerInside.current = true; }}
        onMouseLeave={() => { pointerInside.current = false; setBrushHover(null); setMaskHover(null); }}
        onPointerDownCapture={(event) => {
          // Konva keeps the browser from dropping a text selection made elsewhere (chat text): a press on the
          // canvas drops it, so the next Ctrl/Cmd+C copies layers, not that older text (see canvasOwnsCopy).
          if (isEditableTarget(event.target as HTMLElement)) return;
          const selection = window.getSelection();
          if (selection && !selection.isCollapsed && !containerRef.current?.contains(selection.anchorNode)) selection.removeAllRanges();
        }}
      >
      <ContextMenu
        open={contextMenu !== null}
        disabled={activeTool !== 'select'}
        onOpenChange={(open) => setContextMenu(open ? (contextMenuTarget.current ?? { onNode: false }) : null)}
      >
      {/* display: contents keeps the stage's layout; the trigger only takes the stage's contextmenu events. */}
      <ContextMenuTrigger render={<div className="contents" data-testid="canvas-context-trigger" />}>
      <Stage
        ref={stageRef}
        width={size.width}
        height={size.height}
        scaleX={viewport.zoom}
        scaleY={viewport.zoom}
        x={viewport.panX}
        y={viewport.panY}
        draggable={pansView(activeTool, spaceDown)}
        onDragMove={(event) => {
          const stage = stageRef.current;
          if (!stage || event.target !== stage) return;
          store.getState().setViewport(sessionId, { panX: stage.x(), panY: stage.y() });
        }}
        onDragEnd={(event) => {
          const stage = stageRef.current;
          if (!stage || event.target !== stage) return;
          store.getState().setViewport(sessionId, { panX: stage.x(), panY: stage.y() });
        }}
        onTouchStart={(event) => {
          const touches = event.evt.touches;
          if (touches.length === 2) {
            const dx = touches[0].clientX - touches[1].clientX;
            const dy = touches[0].clientY - touches[1].clientY;
            pinchDistance.current = Math.hypot(dx, dy);
            return;
          }
          // C1 触摸：单指与鼠标同管线（绘画/蒙版/选框/图元），并阻止
          // WebKit 把触摸当页面滚动。
          if (touches.length === 1) {
            event.evt.preventDefault();
            handleStageMouseDown(event);
          }
        }}
        onTouchMove={(event) => {
          const touches = event.evt.touches;
          if (touches.length === 1) {
            event.evt.preventDefault();
            handleStageMouseMove(event);
            return;
          }
          if (touches.length !== 2 || pinchDistance.current === 0) return;
          event.evt.preventDefault();
          const dx = touches[0].clientX - touches[1].clientX;
          const dy = touches[0].clientY - touches[1].clientY;
          const distance = Math.hypot(dx, dy);
          const stage = stageRef.current;
          if (!stage) return;
          const mid = {
            x: (touches[0].clientX + touches[1].clientX) / 2,
            y: (touches[0].clientY + touches[1].clientY) / 2,
          };
          const rect = stage.container().getBoundingClientRect();
          const pointer = { x: mid.x - rect.left, y: mid.y - rect.top };
          const viewportConfig = canvasSchema.frontend.viewport;
          const factor = distance / pinchDistance.current;
          const state = store.getState();
          const current = state.viewportBySession[sessionId] ?? canvasDefaultViewport(viewportConfig);
          state.setViewport(sessionId, zoomKeepingFrame(current, pointer, factor, viewportConfig, framing.current.artboard, framing.current.size));
          pinchDistance.current = distance;
        }}
        onTouchEnd={(event) => {
          if (event.evt.touches.length === 0) {
            handleStageMouseUp();
          }
          pinchDistance.current = 0;
        }}
        onMouseDown={(event) => { recenterGesture.current.pointerDown(); handleStageMouseDown(event); }}
        onMouseMove={handleStageMouseMove}
        onMouseUp={handleStageMouseUp}
        onContextMenu={(event) => {
          event.evt.preventDefault();
          if (activeTool !== 'select') return;
          const id = event.target.id?.() ?? '';
          const nodeId = id.startsWith('node-') ? id.slice('node-'.length) : null;
          const hit = nodeId ? nodes.find((node) => node.id === nodeId) : null;
          if (hit && !store.getState().selectedNodeIds.includes(hit.id)) store.getState().selectNode(hit.id);
          if (!hit) store.getState().clearSelection();
          contextMenuTarget.current = { onNode: Boolean(hit) };
          // A right-click while the menu is open moves it to the new point: show that target's commands.
          setContextMenu((current) => (current ? { onNode: Boolean(hit) } : current));
        }}
        style={{ cursor, touchAction: 'none' }}
      >
        <Layer>
          {artboard ? (
            // #246: the canvas area under every layer — a checkerboard where it exports transparent (screen-sized
            // squares at any zoom), or the export background colour.
            <Rect
              x={artboard.x}
              y={artboard.y}
              width={artboard.width}
              height={artboard.height}
              listening={false}
              perfectDrawEnabled={false}
              name="artboard-backdrop"
              {...(backdrop.kind === 'fill'
                ? { fill: backdrop.color }
                : checkerImage
                  ? {
                    fillPatternImage: checkerImage as unknown as HTMLImageElement,
                    fillPatternRepeat: 'repeat',
                    fillPatternScale: { x: 1 / viewport.zoom, y: 1 / viewport.zoom },
                    opacity: backdrop.opacity,
                  }
                  : {})}
            />
          ) : null}
          {sorted.map((node) => {
            if (node.visible === false) return null;
            const common = {
              id: `node-${node.id}`,
              x: node.x,
              y: node.y,
              rotation: node.rotation ?? 0,
              opacity: node.opacity ?? 1,
              draggable: layerDraggable(activeTool, spaceDown, node.locked),
              // A locked layer is out of the way of the pointer; the layer panel still selects it.
              listening: !node.locked,
              onMouseDown: (event: KonvaEventObject<MouseEvent>) => {
                if (activeTool !== 'select' || spaceDown) return;
                event.cancelBubble = true;
                pendingCollapse.current = null;
                const current = store.getState().selectedNodeIds;
                const ctrl = event.evt.ctrlKey || event.evt.metaKey;
                if (event.evt.shiftKey) {
                  // Shift adds or removes the clicked layer (or its whole group in "group" mode).
                  store.getState().selectNodes(selectionAfterClick(current, nodes, node.id, autoSelect, { shift: true, ctrl }));
                } else if (current.includes(node.id) && current.length > 1 && !ctrl) {
                  // Keep the multi-selection so it can be dragged; a click without a drag collapses it on mouseup.
                  pendingCollapse.current = { id: node.id, ctrl };
                } else {
                  // Auto-select: a member of a group selects the group; Ctrl/Cmd turns the mode around for this click.
                  store.getState().selectNodes(selectionAfterClick(current, nodes, node.id, autoSelect, { shift: false, ctrl }));
                }
              },
              onMouseUp: () => {
                const pending = pendingCollapse.current;
                if (pending && pending.id === node.id) store.getState().selectNodes(clickTargets(nodes, node.id, autoSelect, pending.ctrl));
                pendingCollapse.current = null;
              },
              onDragStart: (event: KonvaEventObject<DragEvent>) => {
                dragSnapshot.current = true;
                pendingCollapse.current = null;
                // A multi-node Transformer starts dragging the other attached nodes itself (#273): that is the
                // same drag, so it neither restarts the session (the node under the pointer keeps leading the
                // snap) nor records a second undo step.
                const running = dragSession.current;
                if (running && running.starts.has(node.id)
                  && stageRef.current?.findOne(`#node-${running.draggedId}`)?.isDragging()) return;
                if (altCloneId.current === node.id) {
                  // The clone carried by an Alt-drag: it moves like any dragged layer.
                  altCloneId.current = null;
                } else if (event.evt.altKey && !node.locked) {
                  // Alt-drag copies: the original stays, a duplicate (directly above it) takes the drag over.
                  const state = store.getState();
                  const chosen = sorted.filter((item) => state.selectedNodeIds.includes(item.id)).map((item) => item.id);
                  event.target.stopDrag();
                  event.target.position({ x: node.x, y: node.y });
                  const created = state.duplicateSelected(sessionId);
                  const cloneId = created[chosen.indexOf(node.id)];
                  if (cloneId) {
                    altCloneId.current = cloneId;
                    requestAnimationFrame(() => stageRef.current?.findOne(`#node-${cloneId}`)?.startDrag());
                  }
                  return;
                }
                dragSession.current = startDragSession(nodes, store.getState().selectedNodeIds, node.id);
                store.getState()._pushUndo(sessionId, 'history.move');
              },
              onDragEnd: () => {
                dragSession.current = null;
                setSnapGuides([]);
              },
              onDragMove: (event: KonvaEventObject<DragEvent>) => {
                const session = dragSession.current;
                const target = event.target;
                if (!session || session.draggedId !== node.id) return;
                let position = { x: target.x(), y: target.y() };
                if (snapEnabled && session.snappable) {
                  const targets = snapTargetsFor(nodes, session, { groupMemberTargets: snapSchema.group_member_targets });
                  if (artboard) targets.primary.unshift({ ...artboard, rotation: 0 });
                  const union = movedUnion(session, position);
                  const snapped = snapRect(union, targets.primary, snapSchema.threshold_pixels / viewport.zoom, targets.secondary);
                  position = {
                    x: position.x + snapped.x - union.x,
                    y: position.y + snapped.y - union.y,
                  };
                  target.x(position.x);
                  target.y(position.y);
                  setSnapGuides(snapped.guides);
                } else {
                  setSnapGuides([]);
                }
                store.getState().updateNodes(sessionId, dragUpdates(session, position), { recordHistory: false });
              },
              onTransformStart: () => {
                store.getState()._pushUndo(sessionId, 'history.transform');
              },
              onTransformEnd: (event: KonvaEventObject<Event>) => {
                const target = event.target;
                const scaleX = target.scaleX();
                const scaleY = target.scaleY();
                target.scaleX(1);
                target.scaleY(1);
                store.getState().updateNode(sessionId, node.id, {
                  x: target.x(),
                  y: target.y(),
                  width: Math.max(
                  canvasSchema.frontend.minimum_node_size,
                  Math.round(node.width * scaleX),
                ),
                  height: Math.max(
                    canvasSchema.frontend.minimum_node_size,
                    Math.round(node.height * scaleY),
                  ),
                  rotation: Math.round(target.rotation()),
                }, { recordHistory: false });
              },
              onDblClick: () => {
                // Painting tools see quick repeated strokes as double clicks; only the select tool opens things.
                if (activeTool !== 'select') return;
                if (node.type === 'text') {
                  setEditing({ nodeId: node.id, value: node.text ?? '' });
                } else if (node.type === 'image') {
                  onImageDoubleClick?.({ nodeId: node.id, imageUrl: node.imageUrl, taskId: node.taskId });
                }
              },
            };
            if (node.type === 'image' && isBlankLayer(node) && !brush.canvasFor(node.id)) {
              // An unpainted initial layer (#246) is only its fill. It stays out of the pointer's way, so dragging
              // on an empty canvas never moves it; the layers panel selects it like any layer.
              const fill = blankLayerFill(node, canvasSchema.frontend.export.transparent_background_value);
              return (
                <Rect key={node.id} {...common} listening={false} draggable={false}
                  width={node.width} height={node.height} fill={fill ?? undefined} perfectDrawEnabled={false} />
              );
            }
            if (node.type === 'image') {
              // A picture being painted on shows its local bitmap until the uploaded one has replaced it.
              const local = brush.canvasFor(node.id);
              const loaded = images.get(node.imageUrl);
              const loadedSize = pictureSize(loaded);
              if (!local && (!loaded || !loadedSize)) return null;
              // #291: a zoomed-out layer is drawn from a reduced copy of its picture (the original stays held).
              // The ratio is device pixels shown per original pixel over the part the crop shows, on the axis
              // that is stretched the most.
              const shownPerPixel = viewport.zoom * devicePixelRatio;
              const ratio = loadedSize ? Math.max(
                node.width * shownPerPixel / (node.crop?.width ?? loadedSize.width),
                node.height * shownPerPixel / (node.crop?.height ?? loadedSize.height),
              ) : 1;
              const displayed = local || !loadedSize ? undefined : images.display(node.imageUrl, ratio);
              const image = local ?? displayed ?? loaded;
              if (!image) return null;
              // A crop is in the original's pixels; a reduced copy needs it scaled to its own.
              const cropScale = displayed && displayed !== loaded && loadedSize ? (pictureSize(displayed)?.width ?? loadedSize.width) / loadedSize.width : 1;
              return (
                <KonvaImage
                  key={node.id}
                  {...common}
                  image={image}
                  width={node.width}
                  height={node.height}
                  crop={
                    // The local brush bitmap is already the cropped region; cropping it again would show a fragment.
                    node.crop && !local
                      ? {
                          x: node.crop.x * cropScale,
                          y: node.crop.y * cropScale,
                          width: node.crop.width * cropScale,
                          height: node.crop.height * cropScale,
                        }
                      : undefined
                  }
                  perfectDrawEnabled={false}
                  shadowEnabled={false}
                />
              );
            }
            if (node.type === 'text') {
              return (
                <Text
                  key={node.id}
                  {...common}
                  text={node.text ?? ''}
                  visible={editing?.nodeId !== node.id}
                  width={node.width}
                  fontSize={node.fontSize ?? schema.canvas.frontend.style.default_font_size}
                  fontFamily={textSchema.font_family}
                  lineHeight={textSchema.line_height}
                  fill={node.strokeColor || textSchema.default_color}
                  wrap="word"
                />
              );
            }
            if (node.type === 'pencil') {
              return (
                <Path
                  key={node.id}
                  {...common}
                  data={node.pathData ?? ''}
                  stroke={node.strokeColor === 'erase' ? background?.background_color : node.strokeColor}
                  strokeWidth={node.strokeWidth ?? 2}
                  lineCap="round"
                  lineJoin="round"
                />
              );
            }
            if (node.type === 'shape') {
              const stroke = node.strokeColor || palette.ink;
              const fill = node.fillColor && node.fillColor !== 'transparent' ? node.fillColor : undefined;
              if (node.shapeType === 'ellipse') {
                return (
                  <Ellipse
                    key={node.id}
                    {...common}
                    // offsetX/offsetY 把椭圆的 Konva 原点对齐到包围盒
                    // 左上角，与其他节点一致：node.x/y 存左上角，拖拽
                    // 与变换回写 target.x()/y() 无需按中心点换算。
                    offsetX={-node.width / 2}
                    offsetY={-node.height / 2}
                    radiusX={node.width / 2}
                    radiusY={node.height / 2}
                    stroke={stroke}
                    strokeWidth={node.strokeWidth ?? 2}
                    fill={fill}
                  />
                );
              }
              if (node.shapeType === 'line' || node.shapeType === 'arrow') {
                return (
                  <Arrow
                    key={node.id}
                    {...common}
                    points={[0, 0, node.width, node.height]}
                    stroke={stroke}
                    strokeWidth={node.strokeWidth ?? 2}
                    pointerLength={node.shapeType === 'arrow' ? 12 : 0}
                    pointerWidth={node.shapeType === 'arrow' ? 8 : 0}
                    fill={fill}
                  />
                );
              }
              return (
                <Rect
                  key={node.id}
                  {...common}
                  width={node.width}
                  height={node.height}
                  stroke={stroke}
                  strokeWidth={node.strokeWidth ?? 2}
                  fill={fill}
                  cornerRadius={2}
                />
              );
            }
            return null;
          })}
          {draftShape ? (() => {
            const geometry = shapeGeometryFromDrag(
              draftShape.shapeType,
              { x: draftShape.startX, y: draftShape.startY },
              { x: draftShape.currentX, y: draftShape.currentY },
            );
            const common = {
              ...geometry,
              stroke: lastStyle.strokeColor || palette.ink,
              strokeWidth: lastStyle.strokeWidth || 2,
              dash: [6, 4],
              listening: false,
            };
            if (draftShape.shapeType === 'line' || draftShape.shapeType === 'arrow') {
              return (
                <Arrow
                  {...common}
                  points={[0, 0, geometry.width, geometry.height]}
                  pointerLength={draftShape.shapeType === 'arrow' ? 12 : 0}
                  pointerWidth={draftShape.shapeType === 'arrow' ? 8 : 0}
                />
              );
            }
            if (draftShape.shapeType === 'ellipse') {
              return (
                <Ellipse
                  {...common}
                  offsetX={-geometry.width / 2}
                  offsetY={-geometry.height / 2}
                  radiusX={geometry.width / 2}
                  radiusY={geometry.height / 2}
                />
              );
            }
            return <Rect {...common} />;
          })() : null}
          {draftStroke ? (
            <Line
              points={draftStroke.points}
              stroke={lastStyle.strokeColor || palette.ink}
              strokeWidth={lastStyle.strokeWidth || 2}
              lineCap="round"
              lineJoin="round"
              listening={false}
            />
          ) : null}
          {marquee ? (
            <Rect
              x={Math.min(marquee.x0, marquee.x1)}
              y={Math.min(marquee.y0, marquee.y1)}
              width={Math.abs(marquee.x1 - marquee.x0)}
              height={Math.abs(marquee.y1 - marquee.y0)}
              stroke={palette.primary}
              strokeWidth={1 / viewport.zoom}
              fill={palette.selectionFill}
              listening={false}
            />
          ) : null}
          {maskState && artboard
            ? (
              // The mask belongs to the artboard: strokes started outside it are kept but not drawn there.
              <Group clipX={artboard.x} clipY={artboard.y} clipWidth={artboard.width} clipHeight={artboard.height} listening={false}>
              {maskOverlay.length > 0 ? (
                <Shape
                  name="mask-overlay"
                  listening={false}
                  sceneFunc={(context) => {
                    if (typeof document === 'undefined') return;
                    maskOverlayScratch.current ??= document.createElement('canvas');
                    drawMaskOverlay(
                      context._context as unknown as MaskOverlayTarget,
                      maskOverlayScratch.current,
                      artboard,
                      maskOverlay,
                      maskOverlayStyle,
                    );
                  }}
                />
              ) : null}
              </Group>
            )
            : null}
          {artboard ? outlines.map((points, index) => {
            const world = maskStrokeWorldPoints(artboard, { points });
            return (
              <Fragment key={`ants-${index}`}>
                <Line points={world} closed stroke={palette.canvasBackground} strokeWidth={1} strokeScaleEnabled={false} listening={false} />
                <Line points={world} closed stroke={palette.ink} strokeWidth={1} strokeScaleEnabled={false} dash={selectionSchema.ants_dash} dashOffset={antsOffset} listening={false} />
              </Fragment>
            );
          }) : null}
          {selectionDraft ? (
            <Fragment>
              {selectionDraft.kind === 'marquee' ? (
                <Rect
                  x={Math.min(selectionDraft.x0, selectionDraft.x1)}
                  y={Math.min(selectionDraft.y0, selectionDraft.y1)}
                  width={Math.abs(selectionDraft.x1 - selectionDraft.x0)}
                  height={Math.abs(selectionDraft.y1 - selectionDraft.y0)}
                  stroke={palette.ink}
                  strokeWidth={1}
                  strokeScaleEnabled={false}
                  dash={selectionSchema.ants_dash}
                  dashOffset={antsOffset}
                  listening={false}
                />
              ) : (
                <Line points={selectionDraft.points} stroke={palette.ink} strokeWidth={1} strokeScaleEnabled={false} dash={selectionSchema.ants_dash} dashOffset={antsOffset} listening={false} />
              )}
            </Fragment>
          ) : null}
          {brushStroke
            ? (() => {
                const node = nodes.find((item) => item.id === brushStroke.nodeId);
                if (!node) return null;
                const worldPoints: number[] = [];
                for (let index = 0; index < brushStroke.points.length; index += 2) {
                  const world = toNodeWorld(
                    node,
                    brushStroke.points[index],
                    brushStroke.points[index + 1],
                  );
                  worldPoints.push(world.x, world.y);
                }
                return (
                  <Line
                    points={worldPoints}
                    stroke={brushStroke.erase ? palette.erase : lastStyle.strokeColor || palette.ink}
                    strokeWidth={brushSize}
                    lineCap="round"
                    lineJoin="round"
                    opacity={brushStroke.erase ? 0.6 : 1}
                    listening={false}
                  />
                );
              })()
            : null}
          {/* #245：画笔/橡皮的目标轮廓与圆形范围（直径 = 笔刷尺寸，跟随缩放）；不可落笔时为虚线灰色。 */}
          {brushHover && (activeTool === 'brush' || activeTool === 'eraser') ? (() => {
            const target = brushHover.nodeId ? nodes.find((item) => item.id === brushHover.nodeId) : undefined;
            const ready = brushHover.state === 'ready';
            const color = ready ? palette.primary : palette.erase;
            const dash = ready ? undefined : [4 / viewport.zoom, 3 / viewport.zoom];
            const radius = brushCursorRadius(brushSize, viewport.zoom);
            return (
              <Group listening={false}>
                {target ? (
                  <Rect
                    x={target.x}
                    y={target.y}
                    width={target.width}
                    height={target.height}
                    rotation={target.rotation ?? 0}
                    stroke={color}
                    strokeWidth={1 / viewport.zoom}
                    dash={[6 / viewport.zoom, 4 / viewport.zoom]}
                    listening={false}
                  />
                ) : null}
                <Ellipse
                  name={`brush-cursor-${brushHover.state}`}
                  x={brushHover.x}
                  y={brushHover.y}
                  radiusX={radius}
                  radiusY={radius}
                  stroke={color}
                  strokeWidth={1.5 / viewport.zoom}
                  dash={dash}
                  listening={false}
                />
              </Group>
            );
          })() : null}
          {/* #310：蒙版笔刷的圆形范围（直径 = 笔刷尺寸，跟随缩放），落笔前和涂抹时都跟随指针；擦除时用擦除色。 */}
          {maskHover && activeTool === 'mask' ? (
            <Ellipse
              name="mask-brush-cursor"
              x={maskHover.x}
              y={maskHover.y}
              radiusX={brushCursorRadius(brushSize, viewport.zoom)}
              radiusY={brushCursorRadius(brushSize, viewport.zoom)}
              stroke={maskErasing ? palette.erase : palette.handle}
              strokeWidth={1.5 / viewport.zoom}
              listening={false}
            />
          ) : null}
          {cropDraft ? (
            <Rect
              id="crop-rect"
              x={cropDraft.x}
              y={cropDraft.y}
              width={cropDraft.width}
              height={cropDraft.height}
              stroke={palette.primary}
              strokeWidth={1.5 / viewport.zoom}
              dash={[6 / viewport.zoom, 4 / viewport.zoom]}
              draggable={!spaceDown}
              onDragMove={(event) =>
                setCropDraft((current) =>
                  current
                    ? { ...current, x: event.target.x(), y: event.target.y() }
                    : current,
                )
              }
            />
          ) : null}
          {cropDraft ? (
            <Transformer
              ref={cropTransformerRef}
              rotateEnabled={false}
              boundBoxFunc={(oldBox, newBox) =>
                newBox.width < 8 || newBox.height < 8 ? oldBox : newBox
              }
              borderStroke={palette.primary}
              anchorStroke={palette.primary}
              anchorFill={palette.canvasBackground}
              onTransformEnd={(event) => {
                const target = event.target;
                const scaleX = target.scaleX();
                const scaleY = target.scaleY();
                target.scaleX(1);
                target.scaleY(1);
                setCropDraft((current) =>
                  current
                    ? {
                        ...current,
                        x: target.x(),
                        y: target.y(),
                        width: Math.max(8, current.width * scaleX),
                        height: Math.max(8, current.height * scaleY),
                      }
                    : current,
                );
              }}
            />
          ) : null}
        </Layer>
        <Layer listening={false}>
          {artboard
            ? outsideDimRects(visibleWorld(viewport, size, 4), artboard).map((rect, index) => (
              <Rect key={`dim-${index}`} {...rect} fill={outsideDim.color} opacity={outsideDim.opacity} listening={false} />
            ))
            : null}
          {artboard ? (
            <Rect
              x={artboard.x}
              y={artboard.y}
              width={artboard.width}
              height={artboard.height}
              stroke={schema.canvas.frontend.export.frame.stroke_color}
              // Screen pixels whatever the canvas size or zoom (#246): with strokeScaleEnabled off, Konva draws
              // both the width and the dash after resetting the view transform.
              strokeWidth={canvasSchema.frontend.artboard.frame_stroke_width}
              dash={canvasSchema.frontend.artboard.frame_line_dash}
              strokeScaleEnabled={false}
            />
          ) : null}
          {artboard && artboardLabel ? (() => {
            // #282: inset in the frame's top-left corner on a translucent plate, clipped to the frame.
            const box = artboardLabelBox(artboard, viewport.zoom, artboardLabelWidth, labelSchema);
            return (
              <Group
                name="artboard-label"
                clipX={artboard.x}
                clipY={artboard.y}
                clipWidth={artboard.width}
                clipHeight={artboard.height}
                listening={false}
              >
                <Rect {...box.plate} fill={artboardLabelPlate} opacity={labelSchema.background_opacity} />
                <KonvaText
                  x={box.text.x}
                  y={box.text.y}
                  scaleX={box.text.scale}
                  scaleY={box.text.scale}
                  text={artboardLabel}
                  fontSize={labelSchema.font_size_pixels}
                  fill={artboardLabelColor}
                />
              </Group>
            );
          })() : null}
          {snapGuides.map((guide) => (
            <Line
              key={`${guide.orientation}:${guide.position}`}
              points={guide.orientation === 'vertical'
                ? [guide.position, guide.from, guide.position, guide.to]
                : [guide.from, guide.position, guide.to, guide.position]}
              stroke={snapGuideColor}
              strokeWidth={snapSchema.guide_stroke_width}
              strokeScaleEnabled={false}
            />
          ))}
        </Layer>
        {/* #311: the selection box and handles sit above the outside-canvas dim, so a picture outside the canvas
            (dimmed: it is not exported) still shows plainly that it is selected. Only the handles listen here. */}
        <Layer name="selection-handles">
          <Transformer
            ref={transformerRef}
            boundBoxFunc={(oldBox, newBox) => {
              if (
                newBox.width < canvasSchema.frontend.minimum_node_size ||
                newBox.height < canvasSchema.frontend.minimum_node_size
              ) {
                return oldBox;
              }
              return newBox;
            }}
            rotateEnabled
            enabledAnchors={[
              'top-left',
              'top-right',
              'bottom-left',
              'bottom-right',
            ]}
            borderStroke={palette.primary}
            anchorStroke={palette.primary}
            anchorFill={palette.canvasBackground}
          />
        </Layer>
      </Stage>
      </ContextMenuTrigger>
      <ContextMenuContent data-testid="canvas-context-menu" className="min-w-44">
        {contextMenu ? menuItems.map((item, index) => {
          // The six align commands live in one submenu, placed where the first of them would be.
          if (item.id.startsWith('align_')) {
            if (item.id !== 'align_left') return null;
            const alignItems = menuItems.filter((candidate) => candidate.id.startsWith('align_'));
            return (
              <Fragment key="align">
                <ContextMenuSeparator />
                <ContextMenuSub>
                  <ContextMenuSubTrigger disabled={!item.enabled} data-testid="canvas-menu-align">{schemaText(schema, 'ui.canvas.options.align')}</ContextMenuSubTrigger>
                  <ContextMenuSubContent>
                    {alignItems.map((alignItem) => (
                      <ContextMenuItem key={alignItem.id} disabled={!alignItem.enabled} onClick={() => runMenuItem(alignItem.id)}>
                        {menuLabel(alignItem.id)}
                      </ContextMenuItem>
                    ))}
                  </ContextMenuSubContent>
                </ContextMenuSub>
              </Fragment>
            );
          }
          return (
            <Fragment key={item.id}>
              {index > 0 && menuSeparatorBefore.has(item.id) ? <ContextMenuSeparator /> : null}
              <ContextMenuItem
                disabled={!item.enabled}
                variant={item.id === 'delete' ? 'destructive' : 'default'}
                onClick={() => runMenuItem(item.id)}
              >
                {menuLabel(item.id)}
                {menuShortcut(item.id) ? <ContextMenuShortcut>{menuShortcut(item.id)}</ContextMenuShortcut> : null}
              </ContextMenuItem>
            </Fragment>
          );
        }) : null}
      </ContextMenuContent>
      </ContextMenu>
      {editing && editingNode ? (
        // 文字就地编辑：文本框盖在画布上文字所在的位置，大小随缩放。
        <textarea
          autoFocus
          aria-label={schemaText(schema, 'ui.studio.canvas.edit_text')}
          value={editing.value}
          onChange={(event) => setEditing({ nodeId: editing.nodeId, value: event.target.value })}
          onFocus={(event) => event.currentTarget.select()}
          onBlur={commitEditing}
          onKeyDown={(event) => {
            if (event.key === 'Escape' || (event.key === 'Enter' && (event.ctrlKey || event.metaKey))) {
              event.preventDefault();
              commitEditing();
            }
          }}
          data-testid="canvas-text-editor"
          className="absolute z-float resize-none overflow-hidden rounded-sm border border-primary bg-transparent p-0 outline-none"
          style={{
            left: viewport.panX + editingNode.x * viewport.zoom,
            top: viewport.panY + editingNode.y * viewport.zoom,
            width: Math.max(editingNode.width * viewport.zoom, 96),
            height: Math.max(editingNode.height * viewport.zoom, 24 * viewport.zoom * 1.4),
            fontSize: (editingNode.fontSize ?? schema.canvas.frontend.style.default_font_size) * viewport.zoom,
            color: editingNode.strokeColor || textSchema.default_color,
            fontFamily: textSchema.font_family,
            lineHeight: textSchema.line_height,
            transform: editingNode.rotation ? `rotate(${editingNode.rotation}deg)` : undefined,
            transformOrigin: 'top left',
          }}
        />
      ) : null}
      <CanvasToolRail
        sessionId={sessionId}
        activeTool={activeTool}
        onToolChange={onToolChange}
        onUploadImage={onUploadImage}
        maskSupported={maskSupported}
        onMaskUnsupported={onMaskUnsupported}
      />
      <CanvasToolOptions
        sessionId={sessionId}
        activeTool={activeTool}
        artboard={artboard}
        hint={maskActions ? schemaText(schema, 'ui.canvas.mask.hint') : cropActions ? schemaText(schema, 'ui.studio.canvas.crop_hint') : undefined}
        actions={maskActions ?? cropActions}
      />
      <CanvasZoomControls sessionId={sessionId} containerRef={containerRef} artboard={artboard} onRecenter={artboard ? recenter : undefined} spaceTapTaken={spaceTapTaken} />
      </div>
    </div>
  );
}
