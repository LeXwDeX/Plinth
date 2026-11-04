/**
 * #91: the Konva side of the outside-dim reveal. Each layer of the cut-out mask is drawn by Konva itself
 * (`drawScene` with the node's absolute transform, opacity, crop and filters) into a scene canvas with the
 * veil layer's own pixel ratio and size, so the hole matches the board pixel for pixel at any zoom.
 */
import { SceneCanvas } from 'konva/lib/Canvas';
import type { Container } from 'konva/lib/Container';
import type { Node } from 'konva/lib/Node';

import type { PaintedNode, RevealPainter, RevealStep } from './outside-dim';

/** The two reusable scratch canvases (the mask, and one layer painted over it). */
export interface RevealScratch {
  mask: SceneCanvas | null;
  cover: SceneCanvas | null;
}

/** The parts of the veil layer's scene canvas the painter reads. */
interface VeilCanvas {
  pixelRatio: number;
  width: number;
  height: number;
}

type Native = CanvasRenderingContext2D;

function nativeOf(canvas: SceneCanvas): Native {
  return canvas.getContext()._context as Native;
}

function clear(canvas: SceneCanvas) {
  const context = nativeOf(canvas);
  context.save();
  context.setTransform(1, 0, 0, 1, 0, 0);
  context.clearRect(0, 0, canvas.width, canvas.height);
  context.restore();
}

/** A scratch canvas with the veil canvas's pixel ratio and bitmap size, cleared. */
function prepare(existing: SceneCanvas | null, veil: VeilCanvas): SceneCanvas {
  const canvas = existing ?? new SceneCanvas({ width: 0, height: 0, pixelRatio: veil.pixelRatio });
  const logicalWidth = veil.width / veil.pixelRatio;
  const logicalHeight = veil.height / veil.pixelRatio;
  if (canvas.pixelRatio !== veil.pixelRatio || canvas.width !== veil.width || canvas.height !== veil.height) {
    canvas.pixelRatio = veil.pixelRatio;
    canvas.setSize(logicalWidth, logicalHeight);
  }
  clear(canvas);
  return canvas;
}

/** The board's visible layers in paint order with their world bounding boxes (`#node-<id>` shapes). */
export function paintedNodes(stage: Container, ids: readonly string[]): PaintedNode[] {
  const painted: PaintedNode[] = [];
  for (const id of ids) {
    const node = stage.findOne(`#node-${id}`);
    if (!node || !node.isVisible()) continue;
    painted.push({ id, rect: node.getClientRect({ relativeTo: stage, skipShadow: true }) });
  }
  return painted;
}

export function konvaRevealPainter(stage: Container, veil: VeilCanvas, scratch: RevealScratch): RevealPainter {
  let mask: SceneCanvas | null = null;
  const find = (id: string): Node | undefined => stage.findOne(`#node-${id}`);
  return {
    begin() {
      if (typeof document === 'undefined' || veil.width <= 0 || veil.height <= 0) return false;
      mask = scratch.mask = prepare(scratch.mask, veil);
      return true;
    },
    paint(step: RevealStep) {
      const node = find(step.id);
      if (!mask || !node) return;
      if (step.reveal) {
        node.drawScene(mask);
        return;
      }
      // A layer over the selected one: paint it alone, then take its pixels out of the mask.
      const cover = scratch.cover = prepare(scratch.cover, veil);
      node.drawScene(cover);
      const context = nativeOf(mask);
      context.save();
      context.setTransform(1, 0, 0, 1, 0, 0);
      context.globalCompositeOperation = 'destination-out';
      context.globalAlpha = 1;
      context.drawImage(cover._canvas, 0, 0);
      context.restore();
    },
    result() {
      return mask?._canvas ?? null;
    },
  };
}
