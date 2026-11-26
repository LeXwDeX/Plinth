import { useMemo } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { useGenerationParams, type GenerationParams } from '@/hooks/use-generation-params';
import { canvasArtboard, type CanvasArtboard, type CanvasPixels } from '@/lib/canvas/artboard';
import { canvasRatioTurnOutput, canvasShapeOf, initialCanvasPixels, type CanvasRatioTurnOutput, type CanvasShape, type ReferenceTurnOutput } from '@/lib/workspace/canvas-size';
import { selectionForSession } from '@/lib/workspace/session-scope';
import { useCanvasStore } from '@/store/canvas-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';

export interface CanvasDelivery {
  /** The dashed frame in board coordinates: the canvas, one world unit per pixel (#246). */
  artboard: CanvasArtboard;
  /**
   * The canvas pixels: only editing precision (#653). Exports, the Agent's canvas snapshot and inpainting are this
   * size; the output size of a canvas turn is the model's (`plan`).
   */
  pixels: CanvasPixels;
  /** The canvas ratio, read from its pixels (#653): the canvas's truth, shown instead of its pixels. */
  ratio: CanvasShape;
  /** True once the canvas has its saved (or freshly initialised) size; before that the frame shows `initial`. */
  known: boolean;
  /** The size a new canvas starts with: the Schema default, 1:1 (#653). A legacy canvas is migrated from it. */
  initial: CanvasPixels;
  /**
   * #653: the output size a canvas turn would send with the chosen 2D model: its highest quality at the canvas
   * ratio, or at the nearest ratio it has (`nearest`). Null when the model has no output-size setting the canvas
   * sets, or the Agent chose the size (#614).
   */
  plan: CanvasRatioTurnOutput | null;
  /**
   * #284: `plan` when the artboard is the next turn's source (a canvas turn, #644): the server then sets the size
   * from the canvas (#272). Null otherwise.
   */
  turn: CanvasRatioTurnOutput | null;
  /** The artboard is the next turn's source: the composer sends a canvas turn (#644). */
  source: boolean;
  /**
   * #296: when the artboard is not the source and the composer carries one picture, the size an edit of that
   * picture sends if nobody chose a size (the chat column measures the picture and fills this in).
   */
  reference?: { pixels: CanvasPixels; turn: ReferenceTurnOutput } | null;
  /** The settings of the model the next turn runs (`plan` is read from these). */
  generation: GenerationParams['nextTurn'];
  /** Whether those settings have loaded (an initial size read before then would be the Schema default). */
  settled: boolean;
}

/**
 * 虚线画板 = 画布（#246）：像素尺寸随画布项目保存，世界坐标 1 单位 = 1 像素，画布以 Schema 锚点为中心。
 * #653：画布以比例为准，像素只作编辑精度；新画布按 Schema 默认（1:1）开始，与所选模型无关。画板轮的出图尺寸由
 * 当前模型决定（该比例下的最高画质），切换模型不改画布。
 */
export function useCanvasArtboard(sessionId: string | null, referenceContext?: boolean): CanvasDelivery {
  const { schema } = useProductSchema();
  const context = useStudioStore((state) => state.generationReferenceContext);
  const object = selectionForSession(useWorkspaceStore((state) => state.currentObject), sessionId);
  // The composer supplies attachments/pinned/skipped context. Without a composer, only a session-owned stage image counts.
  const hasReference = referenceContext ?? (context?.sessionId === sessionId ? context.hasReference : object?.kind === 'image');
  const generation = useGenerationParams('image', sessionId, hasReference);
  const saved = useCanvasStore((state) => (sessionId ? state.canvasSizeBySession[sessionId] : undefined));
  const anchor = schema.canvas.frontend.artboard;
  const { offering, values, parameterStates } = generation.nextTurn;
  const initial = useMemo(() => initialCanvasPixels(anchor), [anchor]);
  const width = saved?.width ?? initial.width;
  const height = saved?.height ?? initial.height;
  const known = saved !== undefined;
  const ratio = useMemo(() => canvasShapeOf({ width, height }, anchor), [anchor, height, width]);
  const plan = useMemo(
    () => canvasRatioTurnOutput(offering, values, { width, height }, anchor, parameterStates),
    [anchor, height, offering, parameterStates, values, width],
  );
  // Stable while the size is: consumers key effects on the frame (placement, mask remap, the board registry).
  const geometry = useMemo(() => ({
    artboard: canvasArtboard(anchor, { width, height }),
    pixels: { width, height },
  }), [anchor, height, width]);
  // #644: the board is the turn's source only on a canvas turn, as the composer decides it (以画板为主, a selection
  // or mask, `@画布`); a picture on the board alone no longer makes it one.
  const boardIsSource = context?.sessionId === sessionId && context.boardTurn === true;
  return {
    ...geometry,
    ratio,
    known,
    initial,
    plan,
    turn: boardIsSource ? plan : null,
    source: boardIsSource,
    generation: generation.nextTurn,
    settled: generation.ready || generation.failed,
  };
}
