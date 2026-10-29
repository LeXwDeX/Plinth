// @vitest-environment jsdom
import { cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { CanvasBitmapLimitError } from '@/lib/canvas/bitmap-limit';
import { useCanvasStore, type CanvasNode } from '@/store/canvas-store';
import { useChatStore } from '@/store/chat-store';
import { useWorkspaceStore } from '@/store/workspace-store';
import { useModelCapture } from './use-model-capture';

const mocks = vi.hoisted(() => {
  const memory = new Map<string, string>();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => memory.get(key) ?? null, setItem: (key: string, value: string) => { memory.set(key, value); },
      removeItem: (key: string) => { memory.delete(key); }, clear: () => memory.clear(),
    },
  });
  return { upload: vi.fn(), persist: vi.fn() };
});
const schema = defaultCatalog.application as unknown as ProductSchema;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner' } }) }));
vi.mock('@/hooks/use-canvas-artboard', () => ({ useCanvasArtboard: () => ({ initial: { width: 1024, height: 1024 }, settled: true }) }));
vi.mock('@/lib/workspace/input-asset-upload', () => ({ uploadWorkspaceInputAssetResult: mocks.upload }));
vi.mock('@/lib/canvas/capture-persistence', () => ({ persistCapturedImageToCanvas: mocks.persist }));

const SESSION = 'session-1';
const limit = schema.canvas.limits.maximum_bitmap_nodes;
const picture = (id: string): CanvasNode => ({ id, type: 'image', imageUrl: `/${id}`, taskId: '', x: 0, y: 0, width: 10, height: 10, zIndex: 1 });

describe('capture to 2D on a full canvas (#149)', () => {
  beforeEach(() => {
    mocks.upload.mockReset();
    mocks.persist.mockReset();
    useChatStore.setState({ activeSessionId: SESSION, activeSessionRevision: 1 });
    useWorkspaceStore.setState({
      currentObject: { taskId: 'model-task', kind: 'model', url: '/m.glb', assetId: 'model-asset', sessionId: SESSION },
    });
    useCanvasStore.setState({ nodesBySession: { [SESSION]: Array.from({ length: limit }, (_, index) => picture(`p${index}`)) } });
  });
  afterEach(cleanup);

  it('throws the limit error before anything is uploaded', async () => {
    const { result } = renderHook(() => useModelCapture());
    const failure = await result.current(new Blob(['png']), {} as never).catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(CanvasBitmapLimitError);
    expect(failure).toMatchObject({ used: limit, limit });
    expect(mocks.upload).not.toHaveBeenCalled();
    expect(mocks.persist).not.toHaveBeenCalled();
  });

  it('goes on to upload while there is a free slot', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture('only')] } });
    mocks.upload.mockRejectedValue(new Error('stop here'));
    const { result } = renderHook(() => useModelCapture());
    await expect(result.current(new Blob(['png']), {} as never)).rejects.toThrow('stop here');
    expect(mocks.upload).toHaveBeenCalledTimes(1);
  });
});
