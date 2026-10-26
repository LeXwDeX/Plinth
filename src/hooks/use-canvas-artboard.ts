import { useMemo } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { useGenerationParams, type GenerationParams } from '@/hooks/use-generation-params';
import { canvasArtboard, type CanvasArtboard, type CanvasPixels } from '@/lib/canvas/artboard';
import { artboardContent } from '@/lib/canvas/export-canvas';
import { canvasSizeFit, canvasTurnOutput, initialCanvasPixels, type CanvasSizeFit, type CanvasTurnOutput, type ReferenceTurnOutput } from '@/lib/workspace/canvas-size';
import { selectionForSession } from '@/lib/workspace/session-scope';
import { useCanvasStore } from '@/store/canvas-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';

export interface CanvasDelivery {
  /** The dashed frame in board coordinates: the canvas, one world unit per pixel (#246). */
  artboard: CanvasArtboard;
  /** The canvas pixel size: exports, the Agent's canvas snapshot and inpainting are exactly this size. */
  pixels: CanvasPixels;
  /** True once the canvas has its saved (or freshly initialised) size; before that the frame shows `initial`. */
  known: boolean;
  /** The size a new or legacy canvas starts with: what the chosen 2D model produces, else the Schema default. */
  initial: CanvasPixels;
  /** How the chosen 2D model's output size relates to the canvas size. */
  fit: CanvasSizeFit;
  /**
   * #284: the output size the next turn really sends when the artboard is its source (it holds something and the
   * composer still carries it): the server then sets the size from the canvas (#272), not from `fit`'s settings.
   * Null when the artboard is not the source or the model has no output-size setting the canvas sets.
   */
  turn: CanvasTurnOutput | null;
  /** The artboard is the next turn's source (it holds something, the chip was not dismissed, the stage is not 3D). */
  source: boolean;
  /**
   * #296: when the artboard is not the source and the composer carries one picture, the size an edit of that
   * picture sends if nobody chose a size (the chat column measures the picture and fills this in).
   */
  reference?: { pixels: CanvasPixels; turn: ReferenceTurnOutput } | null;
  /** The settings of the model the next turn runs (the size fit is read from these). */
  generation: GenerationParams['nextTurn'];
  /** Whether those settings have loaded (an initial size read before then would be the Schema default). */
  settled: boolean;
}

/**
 * 虚线画板 = 画布（#246）：像素尺寸随画布项目保存，世界坐标 1 单位 = 1 像素，画布以 Schema 锚点为中心。
 * 画布尺寸不再由所选模型推导；模型的输出尺寸只用于新画布/旧画布迁移的初始尺寸和尺寸冲突提示。
 */
export function useCanvasArtboard(sessionId: string | null, referenceContext?: boolean): CanvasDelivery {
  const { schema } = useProductSchema();
  const context = useStudioStore((state) => state.generationReferenceContext);
  const object = selectionForSession(useWorkspaceStore((state) => state.currentObject), sessionId);
  // The composer supplies attachments/pinned/skipped context. Without a composer, only a session-owned stage image counts.
  const hasReference = referenceContext ?? (context?.sessionId === sessionId ? context.hasReference : object?.kind === 'image');
  const generation = useGenerationParams('image', sessionId, hasReference);
  const saved = useCanvasStore((state) => (sessionId ? state.canvasSizeBySession[sessionId] : undefined));
  const nodes = useCanvasStore((state) => (sessionId ? state.nodesBySession?.[sessionId] : undefined));
  // The composer's chip was dismissed: the turn does not carry the artboard (chat-column's skipped rule).
  const skipped = context?.sessionId === sessionId && context.objectSkipped === true;
  const anchor = schema.canvas.frontend.artboard;
  const { offering, model, values } = generation.nextTurn;
  const sizeParams = model.sizeParams;
  const initial = useMemo(() => initialCanvasPixels(anchor, sizeParams, values), [anchor, sizeParams, values]);
  const width = saved?.width ?? initial.width;
  const height = saved?.height ?? initial.height;
  const known = saved !== undefined;
  const fit = useMemo(
    () => canvasSizeFit(offering, sizeParams, values, { width, height }, anchor),
    [anchor, height, offering, sizeParams, values, width],
  );
  // Stable while the size is: consumers key effects on the frame (placement, mask remap, the board registry).
  const geometry = useMemo(() => ({
    artboard: canvasArtboard(anchor, { width, height }),
    pixels: { width, height },
  }), [anchor, height, width]);
  // The same rule as the composer (currentObjectSource): an artboard that holds anything is the turn's source.
  const boardIsSource = !skipped && object?.kind !== 'model' && nodes !== undefined
    && artboardContent(nodes, geometry.artboard).length > 0;
  const turn = useMemo(
    () => (boardIsSource ? canvasTurnOutput(offering, values, { width, height }, anchor) : null),
    [anchor, boardIsSource, height, offering, values, width],
  );
  return {
    ...geometry,
    known,
    initial,
    fit,
    turn,
    source: boardIsSource,
    generation: generation.nextTurn,
    settled: generation.ready || generation.failed,
  };
}
