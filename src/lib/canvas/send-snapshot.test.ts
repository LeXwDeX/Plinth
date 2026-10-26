import { afterEach, describe, expect, it, vi } from 'vitest';

const reported = vi.hoisted(() => vi.fn());
vi.mock('@/lib/client-error-report', () => ({ reportClientError: reported }));

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { configureErrorReference, RequestFailure } from '@/lib/problem-reference';
import type { ProductSchema } from '@/lib/product-schema';
import { CanvasExportError } from './export-canvas';
import {
  exportAndUploadSnapshot,
  PendingEditsNotSaved,
  SnapshotStepFailure,
  reportSnapshotFailure,
  saveBoardEdits,
  SnapshotTooLarge,
  snapshotFailureReport,
  snapshotFailureText,
} from './send-snapshot';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;
const schema = {
  content: { messages },
  runtime: {},
} as unknown as ProductSchema;

const picture = new Blob(['png-bytes'], { type: 'image/png' });

function steps(overrides: Partial<Parameters<typeof exportAndUploadSnapshot>[0]> = {}) {
  return {
    flush: vi.fn(async () => true),
    exportPicture: vi.fn(async () => picture),
    upload: vi.fn(async () => '/api/v1/assets/asset_1'),
    maximumBytes: 1024,
    ...overrides,
  };
}

async function failureOf(promise: Promise<unknown>): Promise<SnapshotStepFailure> {
  const failure = await promise.then(() => null, (error: unknown) => error);
  expect(failure).toBeInstanceOf(SnapshotStepFailure);
  return failure as SnapshotStepFailure;
}

describe('exportAndUploadSnapshot (#286)', () => {
  afterEach(() => configureErrorReference(undefined));

  it('saves brush edits, exports and uploads, in that order', async () => {
    const order: string[] = [];
    const run = steps({
      flush: vi.fn(async () => { order.push('flush'); return true; }),
      exportPicture: vi.fn(async () => { order.push('export'); return picture; }),
      upload: vi.fn(async () => { order.push('upload'); return '/api/v1/assets/asset_1'; }),
    });
    await expect(exportAndUploadSnapshot(run)).resolves.toEqual({ picture, url: '/api/v1/assets/asset_1' });
    expect(order).toEqual(['flush', 'export', 'upload']);
    expect(run.upload).toHaveBeenCalledWith(picture);
  });

  it('does not send a stale board when a brush edit is still not uploaded', async () => {
    const run = steps({ flush: vi.fn(async () => false) });
    const failure = await failureOf(exportAndUploadSnapshot(run));
    expect(failure.step).toBe('flush');
    expect(failure.cause).toBeInstanceOf(PendingEditsNotSaved);
    expect(run.exportPicture).not.toHaveBeenCalled();
    expect(run.upload).not.toHaveBeenCalled();
    expect(snapshotFailureText(schema, failure)).toBe(messages['ui.workspace.canvas.snapshot.flush_failed']);
  });

  it('names reading a board picture as its own failure, and never uploads', async () => {
    const read = new CanvasExportError('导出失败', 'decode', { asset: '/api/v1/tasks/t/assets/a', bytes: 12981871, type: 'image/png' });
    const run = steps({ exportPicture: vi.fn(async () => { throw read; }) });
    const failure = await failureOf(exportAndUploadSnapshot(run));
    expect(failure.step).toBe('export');
    expect(run.upload).not.toHaveBeenCalled();
    expect(snapshotFailureText(schema, failure)).toBe(messages['ui.workspace.canvas.snapshot.read_failed']);
    const report = snapshotFailureReport('chat-send', failure);
    expect(report.kind).toBe('canvas-chat-send-export');
    expect(report.requestId).toBe('');
    // The report says where it failed and on what, never the picture itself.
    expect((report.error as Error).message).toContain('CanvasExportError(decode)');
    expect((report.error as Error).message).toContain('bytes=12981871');
    expect((report.error as Error).message).toContain('asset=/api/v1/tasks/t/assets/a');
  });

  it('tells drawing or encoding the picture apart from reading the pictures', async () => {
    const encode = new CanvasExportError('导出失败', 'encode', { width: 3840, height: 2160, reason: 'toBlob returned null' });
    const failure = await failureOf(exportAndUploadSnapshot(steps({ exportPicture: vi.fn(async () => { throw encode; }) })));
    expect(snapshotFailureText(schema, failure)).toBe(messages['ui.workspace.canvas.snapshot.export_failed']);
    expect(failure.message).toContain('toBlob returned null');
    // An unexpected export error is an export failure too.
    const other = await failureOf(exportAndUploadSnapshot(steps({ exportPicture: vi.fn(async () => { throw new RangeError('bad'); }) })));
    expect(snapshotFailureText(schema, other)).toBe(messages['ui.workspace.canvas.snapshot.export_failed']);
  });

  it('refuses a picture over the upload limit before any request, with its size and the limit', async () => {
    const large = { size: 52_400_000 } as Blob;
    const run = steps({ exportPicture: vi.fn(async () => large), maximumBytes: 52_428_800 - 1_000_000 });
    const failure = await failureOf(exportAndUploadSnapshot(run));
    expect(failure.step).toBe('upload');
    expect(failure.cause).toBeInstanceOf(SnapshotTooLarge);
    expect(run.upload).not.toHaveBeenCalled();
    expect(snapshotFailureText(schema, failure)).toBe(
      // Decimal MB with the unit, written like every other size (#327).
      messages['ui.workspace.canvas.snapshot.too_large'].replace('{size}', '52.4 MB').replace('{limit}', '51.42 MB'),
    );
  });

  it('keeps the refused upload request ID and the server reason for the person and the report', async () => {
    configureErrorReference('（错误 ID：{id}）');
    const refused = new RequestFailure('asset upload 400', 'req_08d39bb70c2ff5ed');
    const failure = await failureOf(exportAndUploadSnapshot(steps({ upload: vi.fn(async () => { throw refused; }) })));
    expect(failure.step).toBe('upload');
    expect(failure.message).toContain(`bytes=${picture.size}`);
    expect(snapshotFailureText(schema, failure)).toBe(`${messages['ui.workspace.canvas.snapshot.upload_failed']}（错误 ID：req_08d39bb70c2ff5ed）`);
    expect(snapshotFailureReport('board-to-3d', failure)).toMatchObject({ kind: 'canvas-board-to-3d-upload', requestId: 'req_08d39bb70c2ff5ed' });

    const reasoned = new RequestFailure('asset upload 400', 'req_0123456789abcdef', '图片格式不受支持');
    const withReason = await failureOf(exportAndUploadSnapshot(steps({ upload: vi.fn(async () => { throw reasoned; }) })));
    expect(snapshotFailureText(schema, withReason)).toBe('图片格式不受支持（错误 ID：req_0123456789abcdef）');
  });

  it('reports a request that never reached the server as an upload failure without an ID', async () => {
    const failure = await failureOf(exportAndUploadSnapshot(steps({ upload: vi.fn(async () => { throw new TypeError('Failed to fetch'); }) })));
    expect(snapshotFailureText(schema, failure)).toBe(messages['ui.workspace.canvas.snapshot.upload_failed']);
    expect(snapshotFailureReport('retexture', failure)).toMatchObject({ kind: 'canvas-retexture-upload', requestId: '' });
    expect(failure.message).toContain('TypeError: Failed to fetch');
  });

  it('has a Schema text for every step', () => {
    for (const key of ['flush_failed', 'read_failed', 'export_failed', 'too_large', 'upload_failed']) {
      expect(messages[`ui.workspace.canvas.snapshot.${key}`]).toBeTruthy();
    }
    expect(messages['ui.workspace.canvas.snapshot.too_large']).toContain('{size}');
    expect(messages['ui.workspace.canvas.snapshot.too_large']).toContain('{limit}');
  });

  it('lets a path save brush edits first and then export and upload without flushing again', async () => {
    await expect(saveBoardEdits(async () => true)).resolves.toBeUndefined();
    const stale = await saveBoardEdits(async () => false).then(() => null, (error: unknown) => error);
    expect(stale).toBeInstanceOf(SnapshotStepFailure);
    expect((stale as SnapshotStepFailure).step).toBe('flush');
    const thrown = await saveBoardEdits(async () => { throw new Error('boom'); }).then(() => null, (error: unknown) => error);
    expect((thrown as SnapshotStepFailure).step).toBe('flush');
    const run = steps();
    const { flush: _flush, ...withoutFlush } = run;
    await expect(exportAndUploadSnapshot(withoutFlush)).resolves.toEqual({ picture, url: '/api/v1/assets/asset_1' });
    expect(run.flush).not.toHaveBeenCalled();
  });

  it('reports each path under its own kind and returns the text to show', async () => {
    reported.mockClear();
    const refused = new RequestFailure('asset upload 400', 'req_08d39bb70c2ff5ed');
    const failure = await failureOf(exportAndUploadSnapshot(steps({ upload: vi.fn(async () => { throw refused; }) })));
    for (const source of ['chat-send', 'board-to-3d', 'retexture', 'board-to-chat', 'merge', 'inpaint'] as const) {
      expect(reportSnapshotFailure(schema, source, failure)).toBe(messages['ui.workspace.canvas.snapshot.upload_failed']);
    }
    expect(reported.mock.calls.map((call) => call[0])).toEqual([
      'canvas-chat-send-upload', 'canvas-board-to-3d-upload', 'canvas-retexture-upload',
      'canvas-board-to-chat-upload', 'canvas-merge-upload', 'canvas-inpaint-upload',
    ]);
    expect(reported).toHaveBeenCalledWith('canvas-inpaint-upload', failure, { requestId: 'req_08d39bb70c2ff5ed' });
  });
});
