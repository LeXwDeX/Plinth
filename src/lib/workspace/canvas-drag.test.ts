import { describe, expect, it } from 'vitest';

import { CANVAS_ITEM_MIME, canvasDragItem, dataURIFile, decodeCanvasDrag, encodeCanvasDrag, hasCanvasDrag, startCanvasDrag, uploadDragItem } from './canvas-drag';

describe('canvas drag payload', () => {
  const item = { taskId: 't1', assetId: 'a1', url: '/api/v1/tasks/t1/assets/a1', kind: 'image' as const, width: 1024, height: 768, label: '2D v1' };

  it('round-trips', () => {
    expect(decodeCanvasDrag(encodeCanvasDrag(item))).toEqual(item);
    expect(CANVAS_ITEM_MIME).toBe('application/x-artai-canvas-item');
  });

  it('drops optional parts that are absent or invalid', () => {
    const bare = { taskId: 't1', assetId: 'a1', url: '/u', kind: 'image' as const };
    expect(decodeCanvasDrag(JSON.stringify({ ...bare, width: -1, height: 'x', label: '' }))).toEqual(bare);
  });

  it('refuses anything that is not a complete image item', () => {
    expect(decodeCanvasDrag('')).toBeNull();
    expect(decodeCanvasDrag('not json')).toBeNull();
    expect(decodeCanvasDrag('[]')).toBeNull();
    expect(decodeCanvasDrag(JSON.stringify({ ...item, kind: 'model' }))).toBeNull();
    expect(decodeCanvasDrag(JSON.stringify({ ...item, assetId: '' }))).toBeNull();
    expect(decodeCanvasDrag(JSON.stringify({ taskId: 't' }))).toBeNull();
  });

  it('builds an item only when task, asset and url are all known', () => {
    expect(canvasDragItem({ taskId: 't', assetId: 'a', url: '/u', width: 0, label: '' })).toEqual({ taskId: 't', assetId: 'a', url: '/u', kind: 'image' });
    expect(canvasDragItem({ taskId: 't', url: '/u' })).toBeNull();
  });

  it('fills the data transfer with the item and a uri-list fallback, and sees it by type', () => {
    const data = new Map<string, string>();
    const calls: unknown[] = [];
    const transfer = { setData: (format: string, value: string) => { data.set(format, value); }, getData: (format: string) => data.get(format) ?? '', setDragImage: (...args: unknown[]) => { calls.push(args); } };
    startCanvasDrag(transfer, item, {} as Element);
    expect(decodeCanvasDrag(data.get(CANVAS_ITEM_MIME) ?? '')).toEqual(item);
    expect(data.get('text/uri-list')).toBe(item.url);
    expect(calls).toHaveLength(1);
    expect(hasCanvasDrag([...data.keys()])).toBe(true);
    expect(hasCanvasDrag(['Files'])).toBe(false);
    expect(hasCanvasDrag(null)).toBe(false);
  });

  it('carries an uploaded picture without a task, keeping its asset id when known', () => {
    expect(uploadDragItem({ url: '/api/v1/assets/a1', assetId: 'a1', label: 'up 1' })).toEqual({ taskId: '', assetId: 'a1', url: '/api/v1/assets/a1', kind: 'image', label: 'up 1', source: 'upload' });
    expect(uploadDragItem({ url: 'data:image/png;base64,AAAA' })).toEqual({ taskId: '', assetId: '', url: 'data:image/png;base64,AAAA', kind: 'image', source: 'upload' });
    expect(uploadDragItem({ url: '' })).toBeNull();
    // A task id smuggled into an upload is dropped.
    expect(decodeCanvasDrag(JSON.stringify({ kind: 'image', source: 'upload', url: '/u', taskId: 't9' }))?.taskId).toBe('');
    // Without the upload mark the strict task rule still applies.
    expect(decodeCanvasDrag(JSON.stringify({ kind: 'image', url: '/u', taskId: '', assetId: '' }))).toBeNull();
  });

  it('sets no uri-list text for a data URI attachment', () => {
    const data = new Map<string, string>();
    const item = uploadDragItem({ url: 'data:image/png;base64,AAAA' })!;
    startCanvasDrag({ setData: (format: string, value: string) => { data.set(format, value); }, setDragImage: () => undefined }, item);
    expect(data.has(CANVAS_ITEM_MIME)).toBe(true);
    expect(data.has('text/uri-list')).toBe(false);
  });

  it('turns a raster data URI into a file for the upload path, and nothing else', () => {
    const file = dataURIFile('data:image/png;base64,iVBORw0KGgo=', 'up');
    expect(file?.type).toBe('image/png');
    expect(file?.name).toBe('up.png');
    expect(file?.size).toBe(8);
    expect(dataURIFile('data:image/jpeg;base64,AAAA', 'x')?.name).toBe('x.jpg');
    expect(dataURIFile('data:image/svg+xml;base64,AAAA', 'x')).toBeNull();
    expect(dataURIFile('/api/v1/assets/a1', 'x')).toBeNull();
  });
});
