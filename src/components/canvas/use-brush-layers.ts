'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

import { createBrushCommitter, paintBrushStroke, type BrushCommitter, type BrushStroke } from '@/lib/canvas/brush-layer';
import { blankBitmapSize, blankLayerFill, isBlankLayer } from '@/lib/canvas/initial-layer';
import { decodePicture, pictureSize, type CanvasPicture, type PictureSource } from '@/lib/canvas/image-cache';
import { registerLocalBitmaps, registerPendingEdits } from '@/lib/canvas/pending-edits';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { useCanvasStore, type CanvasNode } from '@/store/canvas-store';

interface BrushLayer {
  /** The session the picture belongs to: an upload lands there even after the board moved to another session. */
  sessionId: string;
  nodeId: string;
  canvas: HTMLCanvasElement;
  context: CanvasRenderingContext2D;
  /** The picture under the strokes; null for an initial layer that has none yet (#246), drawn as `fill`. */
  base: CanvasPicture | null;
  baseCrop: { x: number; y: number; width: number; height: number };
  fill: string | null;
  /** Bitmap pixels per node unit. */
  scale: { x: number; y: number };
  strokes: BrushStroke[];
  redo: BrushStroke[];
  committer: BrushCommitter;
  /** The canvas step for this brush session has been recorded (later uploads ride on it). */
  recorded: boolean;
  /** The picture address the node is expected to have; anything else means someone else changed it. */
  expectedUrl: string;
}

export interface BrushLayersOptions {
  sessionId: string;
  /** The board's picture cache; uploaded results are put in it so the swap from bitmap to picture does not flash. */
  images: PictureSource;
  eraserColor: string;
  idleMilliseconds: number;
  /** Schema cap on the bitmap of an unpainted initial layer (#246). */
  blankBitmapMaximumPixels: number;
  filename: string;
  upload?: (blob: Blob, filename: string) => Promise<string>;
  /** Repaint the stage after the bitmap changed. */
  redraw: () => void;
  onUploadFailed: () => void;
}

function preload(url: string): Promise<CanvasPicture> {
  const source = safeAssetURL(url);
  if (!source) return Promise.reject(new Error('image load failed'));
  return decodePicture(source, new AbortController().signal);
}

function drawBase(layer: BrushLayer) {
  layer.context.clearRect(0, 0, layer.canvas.width, layer.canvas.height);
  if (!layer.base) {
    if (layer.fill) {
      layer.context.fillStyle = layer.fill;
      layer.context.fillRect(0, 0, layer.canvas.width, layer.canvas.height);
    }
    return;
  }
  layer.context.drawImage(
    layer.base,
    layer.baseCrop.x, layer.baseCrop.y, layer.baseCrop.width, layer.baseCrop.height,
    0, 0, layer.canvas.width, layer.canvas.height,
  );
}

/**
 * Brush and eraser strokes land on a local bitmap of the picture, which is shown at once, and are uploaded
 * together: when the tool is left, when another picture is painted, or when the pen has been still. The
 * bitmap stays the picture on screen until the uploaded one is in the cache, so a stroke that starts the
 * moment an upload finishes is drawn on the same bitmap and is never lost.
 */
export function useBrushLayers(options: BrushLayersOptions) {
  const layers = useRef(new Map<string, BrushLayer>());
  const active = useRef<string | null>(null);
  const [version, setVersion] = useState(0);
  const latest = useRef(options);
  latest.current = options;
  const bump = useCallback(() => {
    setVersion((value) => value + 1);
    latest.current.redraw();
  }, []);

  const dropLayer = useCallback((nodeId: string) => {
    const layer = layers.current.get(nodeId);
    if (!layer) return;
    layer.committer.cancel();
    layers.current.delete(nodeId);
    if (active.current === nodeId) active.current = null;
    bump();
  }, [bump]);

  const commitLayer = useCallback(async (layer: BrushLayer) => {
    const { upload, filename, images } = latest.current;
    const { sessionId } = layer;
    const store = useCanvasStore.getState();
    const node = store.getNodes(sessionId).find((item) => item.id === layer.nodeId);
    if (!node || !upload) {
      // The picture is gone (or nothing can upload): there is nothing to put the strokes on.
      layer.committer.cancel();
      layers.current.delete(layer.nodeId);
      bump();
      return;
    }
    const blob = await new Promise<Blob | null>((resolve) => layer.canvas.toBlob(resolve, 'image/png'));
    if (!blob) throw new Error('bitmap encoding failed');
    const url = await upload(blob, filename);
    const loaded = await preload(url);
    images.set(url, loaded);
    const current = useCanvasStore.getState();
    if (!current.getNodes(sessionId).some((item) => item.id === layer.nodeId)) return;
    layer.expectedUrl = url;
    if (!layer.recorded) {
      current._pushUndo(sessionId, 'history.brush');
      layer.recorded = true;
    }
    // A lock set after the strokes were drawn must not throw them away: they land on the locked layer too.
    current.updateNode(sessionId, layer.nodeId, { imageUrl: url, crop: undefined }, { recordHistory: false, ignoreLock: true });
  }, [bump]);

  const ensureLayer = useCallback((node: CanvasNode): BrushLayer | null => {
    const existing = layers.current.get(node.id);
    if (existing) return existing;
    // An unpainted initial layer (#246) starts from an empty bitmap of its own pixel size: no picture to load.
    const blank = isBlankLayer(node);
    const base = blank ? null : latest.current.images.get(node.imageUrl);
    const baseSize = pictureSize(base);
    if (!blank && !baseSize) return null;
    const crop = node.crop ?? (baseSize
      ? { x: 0, y: 0, width: baseSize.width, height: baseSize.height }
      : { x: 0, y: 0, ...blankBitmapSize(node, latest.current.blankBitmapMaximumPixels) });
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.round(crop.width));
    canvas.height = Math.max(1, Math.round(crop.height));
    const context = canvas.getContext('2d');
    if (!context) return null;
    const layer: BrushLayer = {
      sessionId: latest.current.sessionId, nodeId: node.id, canvas, context, base: base ?? null, baseCrop: crop,
      fill: blankLayerFill(node),
      scale: { x: canvas.width / node.width, y: canvas.height / node.height },
      strokes: [], redo: [],
      committer: createBrushCommitter({
        idleMilliseconds: latest.current.idleMilliseconds,
        commit: () => commitLayer(layer),
        onFailure: () => latest.current.onUploadFailed(),
      }),
      recorded: false,
      expectedUrl: node.imageUrl,
    };
    drawBase(layer);
    layers.current.set(node.id, layer);
    return layer;
  }, [commitLayer]);

  /** Uploads everything not yet uploaded; resolves true when nothing is left. */
  const flushAll = useCallback(async (): Promise<boolean> => {
    const results = await Promise.all([...layers.current.values()].map((layer) => layer.committer.flush()));
    return results.every(Boolean);
  }, []);

  /** Uploads and then lets go of a picture's bitmap; one that failed to upload stays so no stroke disappears. */
  const retire = useCallback(async (nodeId: string) => {
    const layer = layers.current.get(nodeId);
    if (!layer) return;
    if (await layer.committer.flush()) {
      // A stroke may have started again during the upload.
      if (!layer.committer.pending() && layers.current.get(nodeId) === layer) {
        layers.current.delete(nodeId);
        if (active.current === nodeId) active.current = null;
        bump();
      }
    }
  }, [bump]);

  const retireAll = useCallback(async () => {
    await Promise.all([...layers.current.keys()].map((nodeId) => retire(nodeId)));
  }, [retire]);

  /** Draws a finished stroke. Returns false when the picture is not loaded yet, so the stroke cannot start. */
  const addStroke = useCallback((node: CanvasNode, stroke: BrushStroke): boolean => {
    for (const id of [...layers.current.keys()]) if (id !== node.id) void retire(id);
    const layer = ensureLayer(node);
    if (!layer) return false;
    layer.strokes.push(stroke);
    layer.redo = [];
    paintBrushStroke(layer.context, stroke, layer.scale, latest.current.eraserColor);
    active.current = node.id;
    layer.committer.touch();
    bump();
    return true;
  }, [bump, ensureLayer, retire]);

  const replay = useCallback((layer: BrushLayer) => {
    drawBase(layer);
    for (const stroke of layer.strokes) paintBrushStroke(layer.context, stroke, layer.scale, latest.current.eraserColor);
    layer.committer.touch();
    bump();
  }, [bump]);

  /** Undoes the last local stroke of the picture being painted; false when there is none (and the tool stays put). */
  const undo = useCallback((): boolean => {
    const layer = active.current ? layers.current.get(active.current) : undefined;
    const stroke = layer?.strokes.pop();
    if (!layer || !stroke) return false;
    layer.redo.push(stroke);
    replay(layer);
    return true;
  }, [replay]);

  const redo = useCallback((): boolean => {
    const layer = active.current ? layers.current.get(active.current) : undefined;
    const stroke = layer?.redo.pop();
    if (!layer || !stroke) return false;
    layer.strokes.push(stroke);
    paintBrushStroke(layer.context, stroke, layer.scale, latest.current.eraserColor);
    layer.committer.touch();
    bump();
    return true;
  }, [bump]);

  const hasLayer = useCallback((nodeId: string) => layers.current.has(nodeId), []);
  const canvasFor = useCallback((nodeId: string): HTMLCanvasElement | null => layers.current.get(nodeId)?.canvas ?? null, []);

  // Every delivery waits for the bitmap to be uploaded first.
  const { sessionId } = options;
  useEffect(() => registerPendingEdits(sessionId, flushAll), [flushAll, sessionId]);
  // A resize asks which layers hold a local bitmap: those are content now, never refitted (#246).
  const bitmapNodeIds = useCallback(() => [...layers.current.values()]
    .filter((layer) => layer.sessionId === latest.current.sessionId).map((layer) => layer.nodeId), []);
  useEffect(() => registerLocalBitmaps(sessionId, bitmapNodeIds), [bitmapNodeIds, sessionId]);

  // Leaving the session: upload what is pending (best effort) and let go.
  useEffect(() => () => {
    const pending = [...layers.current.values()];
    for (const layer of pending) void layer.committer.flush();
    // The next session's brush undo must not reach this session's strokes.
    active.current = null;
  }, [sessionId]);

  /** A bitmap whose picture was changed by someone else (a reload, an undo) is dropped, not uploaded over it. */
  const reconcile = useCallback((nodes: readonly CanvasNode[]) => {
    for (const layer of [...layers.current.values()]) {
      // Another session's bitmap is not judged by this session's nodes; its own flush or reconcile settles it.
      if (layer.sessionId !== latest.current.sessionId) continue;
      const node = nodes.find((item) => item.id === layer.nodeId);
      if (!node || (node.imageUrl !== layer.expectedUrl && !layer.committer.pending())) dropLayer(layer.nodeId);
    }
  }, [dropLayer]);

  return { version, canvasFor, hasLayer, addStroke, undo, redo, retireAll, flushAll, reconcile, bitmapNodeIds };
}
