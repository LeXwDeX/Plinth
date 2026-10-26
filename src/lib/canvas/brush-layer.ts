/**
 * The brush and eraser draw on a local bitmap of the target picture and upload it once, instead of
 * uploading every stroke. This file holds the two pieces that do not need React:
 *  - painting strokes onto a bitmap (also the replay that makes a local undo possible);
 *  - the committer that decides when the bitmap is uploaded (the tool is left, another picture is
 *    painted, or the pen has been still for the Schema's idle time) and keeps a failed upload retryable.
 */

export interface BrushStroke {
  /** x0, y0, x1, y1 … in the picture node's own coordinates (its top-left, unrotated). */
  points: number[];
  erase: boolean;
  /** Brush diameter in node units, fixed when the stroke was drawn. */
  size: number;
  color: string;
}

export interface BrushContext {
  save(): void;
  restore(): void;
  scale(x: number, y: number): void;
  beginPath(): void;
  moveTo(x: number, y: number): void;
  lineTo(x: number, y: number): void;
  stroke(): void;
  arc(x: number, y: number, radius: number, start: number, end: number): void;
  fill(): void;
  fillStyle: unknown;
  lineCap: string;
  lineJoin: string;
  lineWidth: number;
  strokeStyle: unknown;
  globalCompositeOperation: string;
}

/** Paints one stroke. `scale` converts node units to bitmap pixels. */
export function paintBrushStroke(context: BrushContext, stroke: BrushStroke, scale: { x: number; y: number }, eraserColor: string): void {
  if (stroke.points.length < 2) return;
  context.save();
  context.scale(scale.x, scale.y);
  context.lineCap = 'round';
  context.lineJoin = 'round';
  context.lineWidth = stroke.size;
  if (stroke.erase) {
    context.globalCompositeOperation = 'destination-out';
    context.strokeStyle = eraserColor;
  } else {
    context.globalCompositeOperation = 'source-over';
    context.strokeStyle = stroke.color;
  }
  context.beginPath();
  if (stroke.points.length === 2) {
    // A single tap is a filled dot of the brush size. A zero-length segment's
    // round cap is not drawn by every engine, so it is not used here.
    context.fillStyle = context.strokeStyle;
    context.arc(stroke.points[0], stroke.points[1], stroke.size / 2, 0, Math.PI * 2);
    context.fill();
    context.restore();
    return;
  }
  context.moveTo(stroke.points[0], stroke.points[1]);
  for (let index = 2; index < stroke.points.length; index += 2) context.lineTo(stroke.points[index], stroke.points[index + 1]);
  context.stroke();
  context.restore();
}

export interface BrushCommitterOptions {
  /** How long the pen must be still before the bitmap is uploaded. */
  idleMilliseconds: number;
  /** Uploads the bitmap as it is right now and applies it to the picture. Throws on failure. */
  commit: () => Promise<void>;
  /** Called once per failed upload; the strokes stay and the upload is tried again after the idle time. */
  onFailure: () => void;
  setTimer?: (callback: () => void, milliseconds: number) => unknown;
  clearTimer?: (handle: unknown) => void;
}

export interface BrushCommitter {
  /** A stroke was added (or undone): the bitmap differs from what was last uploaded. */
  touch(): void;
  /** Uploads now and resolves with whether everything is uploaded. Concurrent calls share one run. */
  flush(): Promise<boolean>;
  /** True while there are strokes not yet uploaded or an upload is running. */
  pending(): boolean;
  /** Forget everything (the picture is gone). */
  cancel(): void;
}

export function createBrushCommitter(options: BrushCommitterOptions): BrushCommitter {
  const setTimer = options.setTimer ?? ((callback, milliseconds) => setTimeout(callback, milliseconds));
  const clearTimer = options.clearTimer ?? ((handle) => clearTimeout(handle as ReturnType<typeof setTimeout>));
  let dirty = false;
  let running: Promise<boolean> | null = null;
  let timer: unknown = null;

  const stopTimer = () => {
    if (timer !== null) clearTimer(timer);
    timer = null;
  };
  const arm = () => {
    stopTimer();
    timer = setTimer(() => {
      timer = null;
      void flush();
    }, options.idleMilliseconds);
  };
  const drain = async (): Promise<boolean> => {
    // Strokes drawn while an upload is in flight set `dirty` again, so they are picked up by the next round.
    while (dirty) {
      dirty = false;
      try {
        await options.commit();
      } catch {
        dirty = true;
        options.onFailure();
        arm();
        return false;
      }
    }
    return true;
  };
  function flush(): Promise<boolean> {
    stopTimer();
    if (running) return running;
    if (!dirty) return Promise.resolve(true);
    running = drain().finally(() => {
      running = null;
    });
    return running;
  }
  return {
    touch() {
      dirty = true;
      arm();
    },
    flush,
    pending: () => dirty || running !== null,
    cancel() {
      stopTimer();
      dirty = false;
    },
  };
}
