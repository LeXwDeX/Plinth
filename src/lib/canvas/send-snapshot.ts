/**
 * #286: every path that sends the artboard upstream (a chat turn, 发送到 3D, 用图片重贴图, the board sent
 * to chat as an attachment, merge, inpaint) first saves pending brush edits, then rasterises the artboard,
 * then uploads the picture as an input asset. Any of the three can fail, and before #286 each path told
 * the person one generic text (「上传图片失败，请重试」 and the like) without a report. Here each failure
 * keeps its step, so the person gets that step's Schema text and the client error report (#233) names the
 * path, the step, the export's sub-step and sizes, and the upload's request ID.
 */
import { reportClientError } from '@/lib/client-error-report';
import { reasonOf, requestIdOf, withErrorReference } from '@/lib/problem-reference';
import { schemaMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import { CANVAS_READ_STEPS, CanvasExportError } from './export-canvas';
import { sizeText } from '@/lib/size-text';

export type SnapshotStep = 'flush' | 'export' | 'upload';

/** Which path sent the board; it is part of the client error report's kind (`canvas-<source>-<step>`). */
export type SnapshotSource = 'chat-send' | 'board-to-3d' | 'retexture' | 'board-to-chat' | 'merge' | 'inpaint';

/** A brush edit could not be uploaded, so the board's nodes do not hold what is on screen. */
export class PendingEditsNotSaved extends Error {
  constructor() {
    super('pending canvas edits are not uploaded');
    this.name = 'PendingEditsNotSaved';
  }
}

/** The exported picture is larger than the Schema's input-asset upload limit; nothing was sent. */
export class SnapshotTooLarge extends Error {
  constructor(readonly bytes: number, readonly limit: number) {
    super(`artboard picture is ${bytes} bytes, upload limit ${limit} bytes`);
    this.name = 'SnapshotTooLarge';
  }
}

function describe(error: unknown): string {
  if (error instanceof CanvasExportError) {
    const detail = Object.entries(error.detail).map(([key, value]) => `${key}=${value}`).join(' ');
    return `${error.name}(${error.step}): ${error.message}${detail ? ` ${detail}` : ''}`;
  }
  if (error instanceof Error) return `${error.name}: ${error.message}`;
  return String(error);
}

export class SnapshotStepFailure extends Error {
  constructor(readonly step: SnapshotStep, readonly cause: unknown, readonly detail: Record<string, number> = {}) {
    super(`canvas snapshot ${step} failed: ${describe(cause)}${Object.entries(detail).map(([key, value]) => ` ${key}=${value}`).join('')}`);
    this.name = 'SnapshotStepFailure';
  }
}

/**
 * The flush step on its own, for paths that must save brush edits before deciding anything else
 * (merge hashes the layers, 重贴图 compares the frame with the picture). False is a failure: the
 * board would be sent without the strokes on screen.
 */
export async function saveBoardEdits(flush: () => Promise<boolean>): Promise<void> {
  let saved: boolean;
  try {
    saved = await flush();
  } catch (error) {
    throw new SnapshotStepFailure('flush', error);
  }
  if (!saved) throw new SnapshotStepFailure('flush', new PendingEditsNotSaved());
}

export interface SnapshotSteps {
  /** Uploads pending brush edits; false when some are still not uploaded. Omitted when the caller saved them already. */
  flush?: () => Promise<boolean>;
  exportPicture: () => Promise<Blob>;
  upload: (picture: Blob) => Promise<string>;
  /** The Schema's `runtime.input_assets.upload_max_bytes`: a larger picture is refused before the POST. */
  maximumBytes: number;
}

/** Runs the steps in order; a failure is wrapped with its step and nothing after it runs. */
export async function exportAndUploadSnapshot(steps: SnapshotSteps): Promise<{ picture: Blob; url: string }> {
  if (steps.flush) await saveBoardEdits(steps.flush);
  let picture: Blob;
  try {
    picture = await steps.exportPicture();
  } catch (error) {
    throw new SnapshotStepFailure('export', error);
  }
  const bytes = { bytes: picture.size };
  if (steps.maximumBytes > 0 && picture.size > steps.maximumBytes) {
    throw new SnapshotStepFailure('upload', new SnapshotTooLarge(picture.size, steps.maximumBytes), bytes);
  }
  try {
    return { picture, url: await steps.upload(picture) };
  } catch (error) {
    throw new SnapshotStepFailure('upload', error, bytes);
  }
}

/** The filename an uploaded board picture carries. */
export function snapshotFilename(schema: ProductSchema): string {
  return `${schema.canvas.frontend.export_filename_fallback}${schema.canvas.frontend.export_extension}`;
}

function stepOf(failure: unknown): { step: SnapshotStep; cause: unknown } {
  return failure instanceof SnapshotStepFailure
    ? { step: failure.step, cause: failure.cause }
    : { step: 'upload', cause: failure };
}

/** What the person is told: the failing step's Schema text; an upload failure keeps its request ID. */
export function snapshotFailureText(schema: ProductSchema, failure: unknown): string {
  const { step, cause } = stepOf(failure);
  if (step === 'flush') return schemaText(schema, 'ui.workspace.canvas.snapshot.flush_failed');
  if (step === 'export') {
    const reading = cause instanceof CanvasExportError && CANVAS_READ_STEPS.includes(cause.step);
    return schemaText(schema, reading ? 'ui.workspace.canvas.snapshot.read_failed' : 'ui.workspace.canvas.snapshot.export_failed');
  }
  if (cause instanceof SnapshotTooLarge) {
    return schemaMessage(schema, 'ui.workspace.canvas.snapshot.too_large', {
      size: sizeText(cause.bytes, true),
      limit: sizeText(cause.limit, false),
    });
  }
  return withErrorReference(reasonOf(cause) || schemaText(schema, 'ui.workspace.canvas.snapshot.upload_failed'), cause);
}

/** The client error report (#233): kind names the path and step, the message the cause and sizes, plus the request ID. */
export function snapshotFailureReport(source: SnapshotSource, failure: unknown): { kind: string; error: unknown; requestId: string } {
  const { step, cause } = stepOf(failure);
  return { kind: `canvas-${source}-${step}`, error: failure, requestId: requestIdOf(cause) };
}

/** Reports the failure to the server log and returns the text to show. */
export function reportSnapshotFailure(schema: ProductSchema, source: SnapshotSource, failure: unknown): string {
  const report = snapshotFailureReport(source, failure);
  reportClientError(report.kind, report.error, { requestId: report.requestId });
  return snapshotFailureText(schema, failure);
}
