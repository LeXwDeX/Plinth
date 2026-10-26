import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { useBrushLayers, type BrushLayersOptions } from './use-brush-layers';
import { useCanvasStore, type CanvasNode } from '@/store/canvas-store';

const SESSION = 'brush-lock-session';
const stubContext = new Proxy({}, { get: () => () => undefined, set: () => true });

class FakeImage {
  complete = true;
  naturalWidth = 10;
  naturalHeight = 10;
  crossOrigin = '';
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  set src(_value: string) { queueMicrotask(() => this.onload?.()); }
}

function picture(): CanvasNode {
  return { id: 'p', type: 'image', taskId: '', imageUrl: '/old', x: 0, y: 0, width: 10, height: 10, zIndex: 1 };
}

function mountHook(options: BrushLayersOptions) {
  let captured: ReturnType<typeof useBrushLayers> | null = null;
  function Probe() {
    captured = useBrushLayers(options);
    return null;
  }
  renderToStaticMarkup(<Probe />);
  return captured as unknown as ReturnType<typeof useBrushLayers>;
}

describe('brush commit on a locked layer', () => {
  beforeEach(() => {
    vi.stubGlobal('document', {
      createElement: () => ({ width: 0, height: 0, getContext: () => stubContext, toBlob: (done: (blob: Blob) => void) => done(new Blob(['png'])) }),
    });
    vi.stubGlobal('Image', FakeImage);
    useCanvasStore.setState({
      nodesBySession: { [SESSION]: [picture()] }, _undoStackBySession: {}, _redoStackBySession: {}, _lastUndoPushTime: {}, _coalesceEntry: {},
    });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    useCanvasStore.setState({ nodesBySession: {} });
  });

  it('lands the strokes drawn before the lock on the locked layer, keeping it locked', async () => {
    const images = new Map<string, HTMLImageElement>([['/old', new FakeImage() as unknown as HTMLImageElement]]);
    const hook = mountHook({
      sessionId: SESSION, images, eraserColor: '#000000', idleMilliseconds: 60_000, blankBitmapMaximumPixels: 16_777_216, filename: 'brush.png',
      upload: async () => '/api/v1/assets/painted', redraw: () => undefined, onUploadFailed: vi.fn(),
    });
    const node = useCanvasStore.getState().getNodes(SESSION)[0];
    expect(hook.addStroke(node, { points: [1, 1, 5, 5], erase: false, size: 2, color: '#ff0000' })).toBe(true);
    // The layer is locked after the stroke was drawn, before it was uploaded.
    useCanvasStore.getState().toggleNodeLock(SESSION, 'p');
    expect(useCanvasStore.getState().getNodes(SESSION)[0].locked).toBe(true);
    await expect(hook.flushAll()).resolves.toBe(true);
    const after = useCanvasStore.getState().getNodes(SESSION)[0];
    expect(after.imageUrl).toBe('/api/v1/assets/painted');
    expect(after.locked).toBe(true);
  });
});

describe('brush on the unpainted initial layer (#246 phase 2)', () => {
  const created: Array<{ width: number; height: number }> = [];
  beforeEach(() => {
    created.length = 0;
    vi.stubGlobal('document', {
      createElement: () => {
        const canvas = { width: 0, height: 0, getContext: () => stubContext, toBlob: (done: (blob: Blob) => void) => done(new Blob(['png'])) };
        created.push(canvas);
        return canvas;
      },
    });
    vi.stubGlobal('Image', FakeImage);
    const blank: CanvasNode = { id: 'layer', type: 'image', taskId: 'upload', imageUrl: '', x: -424, y: -624, width: 2048, height: 2048, zIndex: 0, blank: true, name: '画布图层' };
    useCanvasStore.setState({
      nodesBySession: { [SESSION]: [blank] }, _undoStackBySession: {}, _redoStackBySession: {}, _lastUndoPushTime: {}, _coalesceEntry: {},
    });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    useCanvasStore.setState({ nodesBySession: {} });
  });

  const options = (maximum: number): BrushLayersOptions => ({
    sessionId: SESSION, images: new Map(), eraserColor: '#000000', idleMilliseconds: 60_000, blankBitmapMaximumPixels: maximum,
    filename: 'brush.png', upload: async () => '/api/v1/assets/painted', redraw: () => undefined, onUploadFailed: vi.fn(),
  });

  it('paints without any picture loaded, in a bitmap of the layer pixels, and the commit makes it an ordinary picture layer', async () => {
    const hook = mountHook(options(16_777_216));
    const node = useCanvasStore.getState().getNodes(SESSION)[0];
    expect(hook.addStroke(node, { points: [100, 100, 200, 200], erase: false, size: 12, color: '#ff0000' })).toBe(true);
    expect(created[0]).toMatchObject({ width: 2048, height: 2048 });
    // Until the upload, a resize must treat this layer as content (the board registers this list).
    expect(hook.bitmapNodeIds()).toEqual(['layer']);
    await expect(hook.flushAll()).resolves.toBe(true);
    const after = useCanvasStore.getState().getNodes(SESSION)[0];
    expect(after).toMatchObject({ imageUrl: '/api/v1/assets/painted', name: '画布图层' });
    expect(after.blank).toBeUndefined();
    // One brush step: undo makes the layer blank again.
    useCanvasStore.getState().undo(SESSION);
    expect(useCanvasStore.getState().getNodes(SESSION)[0]).toMatchObject({ imageUrl: '', blank: true });
  });

  it('caps the bitmap of a large canvas at the Schema maximum, keeping the ratio', () => {
    const hook = mountHook(options(1_048_576));
    const node = useCanvasStore.getState().getNodes(SESSION)[0];
    expect(hook.addStroke(node, { points: [1, 1], erase: false, size: 4, color: '#000000' })).toBe(true);
    expect(created[0]).toMatchObject({ width: 1024, height: 1024 });
  });
});
