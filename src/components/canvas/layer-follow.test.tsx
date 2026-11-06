// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import { bindLayerFollow } from '@/lib/canvas/layer-follow';
import { stageFitInsets } from '@/lib/workspace/tool-rail-layout';

const mock = vi.hoisted(() => ({
  nodes: [] as Array<Record<string, unknown>>,
  selectNodes: vi.fn(),
}));
vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: catalog.application, text: (key: string) => key }),
}));
vi.mock('@/hooks/use-picture-handles', () => ({ useNodeHandles: () => new Map() }));
vi.mock('@/store/mask-store', () => ({ useMaskStore: (selector: (s: { draft: null }) => unknown) => selector({ draft: null }) }));
vi.mock('@/store/canvas-store', () => ({
  useCanvasStore: (selector: (s: Record<string, unknown>) => unknown) => selector({
    nodesBySession: { s1: mock.nodes }, selectedNodeIds: [], selectNodes: mock.selectNodes,
    canvasSizeBySession: {}, toggleNodeVisibility: vi.fn(), toggleNodeLock: vi.fn(),
    toggleGroupVisibility: vi.fn(), toggleGroupLock: vi.fn(), arrangeLayers: vi.fn(), removeNode: vi.fn(),
  }),
}));
import { CanvasLayersPanel } from './layers-panel';

const config = catalog.application.canvas.frontend.viewport;
const size = { width: 800, height: 600 };
const insets = stageFitInsets(false);
const node = (id: string, x: number, groupId?: string) => ({
  id, type: 'image', imageUrl: '/image.png', x, y: 100, width: 100, height: 100, zIndex: 1, groupId,
});

afterEach(() => { cleanup(); mock.selectNodes.mockClear(); });

describe('layer panel follow', () => {
  it('follows only an ordinary row, even after a manual pan, and not a canvas or group selection', () => {
    mock.nodes = [node('away', 1200, 'g'), node('other', 1600, 'g')];
    const viewport = { zoom: 1, panX: 0, panY: 0 };
    const onSelect = vi.fn();
    const unbind = bindLayerFollow('s1', (id) => ({
      node: mock.nodes.find((item) => item.id === id) as never, viewport, size, config, insets,
    }), onSelect);
    const view = render(<CanvasLayersPanel sessionId="s1" />);
    const group = view.getByTestId('layer-group-row');
    fireEvent.click(within(group).getByRole('button', { pressed: false }));
    expect(mock.selectNodes).toHaveBeenLastCalledWith(['away', 'other']);
    expect(onSelect).not.toHaveBeenCalled();
    mock.selectNodes(['away']); // canvas selection does not emit a panel request
    expect(onSelect).not.toHaveBeenCalled();
    fireEvent.click(within(view.getAllByTestId('layer-row')[0]).getByRole('button', { pressed: false }));
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect.mock.calls[0][0]).toEqual(expect.objectContaining({ zoom: config.default_zoom }));
    unbind();
  });

  it('does not move a fully visible layer, including exact inset boundaries, but fits covered and rotated layers', () => {
    let selected = node('n', insets.left);
    const onSelect = vi.fn();
    const unbind = bindLayerFollow('s1', () => ({ node: selected as never, viewport: { zoom: 1, panX: 0, panY: 0 }, size, config, insets }), onSelect);
    const request = () => window.dispatchEvent(new CustomEvent('canvas:layer-follow', { detail: { sessionId: 's1', nodeId: 'n' } }));
    request();
    expect(onSelect).toHaveBeenLastCalledWith(null);
    selected = node('n', insets.left - 1);
    request();
    expect(onSelect.mock.lastCall?.[0]).toEqual(expect.objectContaining({ zoom: expect.any(Number) }));
    selected = { ...node('n', 100), rotation: 90 } as typeof selected;
    request();
    expect(onSelect.mock.lastCall?.[0]).toEqual(expect.objectContaining({ zoom: expect.any(Number) }));
    unbind();
  });

  it('clamps fit zoom and ignores other sessions or missing geometry', () => {
    const onSelect = vi.fn();
    let current = { ...node('n', 5000), width: 100000, height: 100000 };
    const unbind = bindLayerFollow('s1', () => ({ node: current as never,
      viewport: { zoom: 1, panX: 0, panY: 0 }, size, config, insets }), onSelect);
    window.dispatchEvent(new CustomEvent('canvas:layer-follow', { detail: { sessionId: 'other', nodeId: 'n' } }));
    expect(onSelect).not.toHaveBeenCalled();
    window.dispatchEvent(new CustomEvent('canvas:layer-follow', { detail: { sessionId: 's1', nodeId: 'n' } }));
    expect(onSelect.mock.lastCall?.[0].zoom).toBe(config.minimum_zoom);
    current = { ...current, width: 0, height: 0 };
    window.dispatchEvent(new CustomEvent('canvas:layer-follow', { detail: { sessionId: 's1', nodeId: 'n' } }));
    expect(onSelect).toHaveBeenCalledTimes(1);
    unbind();
  });

  it('can follow a horizontal line whose geometric height is zero', () => {
    const onSelect = vi.fn();
    const unbind = bindLayerFollow('s1', () => ({ node: { ...node('n', 5000), type: 'shape', height: 0 } as never,
      viewport: { zoom: 1, panX: 0, panY: 0 }, size, config, insets }), onSelect);
    window.dispatchEvent(new CustomEvent('canvas:layer-follow', { detail: { sessionId: 's1', nodeId: 'n' } }));
    expect(onSelect.mock.lastCall?.[0]).toEqual(expect.objectContaining({ zoom: expect.any(Number) }));
    expect(onSelect).toHaveBeenCalledTimes(1);
    unbind();
  });
});
