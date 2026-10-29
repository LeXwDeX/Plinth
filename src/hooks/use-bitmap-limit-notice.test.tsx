// @vitest-environment jsdom
import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { configureCanvasBitmapLimit, useCanvasStore, type CanvasNode } from '@/store/canvas-store';
import { BITMAP_LIMIT_TOAST_ID } from '@/lib/canvas/bitmap-limit';
import { useBitmapLimitNotice } from './use-bitmap-limit-notice';

const toastError = vi.hoisted(() => {
  // The canvas store persists to localStorage; this environment has none.
  const memory = new Map<string, string>();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => memory.get(key) ?? null, setItem: (key: string, value: string) => { memory.set(key, value); },
      removeItem: (key: string) => { memory.delete(key); }, clear: () => memory.clear(),
    },
  });
  return vi.fn();
});
vi.mock('sonner', () => ({ toast: { error: toastError } }));
const schema = defaultCatalog.application as unknown as ProductSchema;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));

const picture = (id: string): CanvasNode => ({ id, type: 'image', imageUrl: `/${id}`, taskId: '', x: 0, y: 0, width: 10, height: 10, zIndex: 1 });
const limit = schema.canvas.limits.maximum_bitmap_nodes;

describe('the picture-limit notice (#149)', () => {
  beforeEach(() => {
    toastError.mockClear();
    useCanvasStore.setState({
      nodesBySession: { s: Array.from({ length: limit }, (_, index) => picture(`p${index}`)) },
      selectedNodeIds: [], _undoStackBySession: {}, _redoStackBySession: {}, bitmapLimitHit: null,
    });
  });
  afterEach(() => { cleanup(); configureCanvasBitmapLimit(Number.POSITIVE_INFINITY); });

  it('hands the store the Schema limit and shows one notice naming it for each refused add', () => {
    renderHook(() => useBitmapLimitNotice());
    expect(toastError).not.toHaveBeenCalled();
    act(() => { expect(useCanvasStore.getState().addNode('s', { ...picture('extra') })).toBe(false); });
    expect(toastError).toHaveBeenCalledTimes(1);
    const [message, options] = toastError.mock.calls[0] as [string, { id: string }];
    expect(message).toContain(String(limit));
    expect(message).toContain('删除');
    expect(message).toContain('新建');
    expect(options.id).toBe(BITMAP_LIMIT_TOAST_ID);
    act(() => { useCanvasStore.getState().addNode('s', { ...picture('extra-2') }); });
    expect(toastError).toHaveBeenCalledTimes(2);
  });

  it('adds nothing to the notice when the add fits', () => {
    useCanvasStore.setState({ nodesBySession: { s: [] } });
    renderHook(() => useBitmapLimitNotice());
    act(() => { expect(useCanvasStore.getState().addNode('s', { ...picture('first') })).toBe(true); });
    expect(toastError).not.toHaveBeenCalled();
  });
});
