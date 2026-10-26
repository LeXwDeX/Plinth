import { readFileSync } from 'node:fs';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';

const mock = vi.hoisted(() => ({
  nodes: [] as Array<Record<string, unknown>>,
  selected: [] as string[],
  draft: null as null | Record<string, unknown>,
  canvasSize: { width: 3840, height: 2160 } as { width: number; height: number } | undefined,
}));

const messages: Record<string, string> = {
  ...(defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages,
  'ui.canvas.layers.draft_mask_title': '当前画布的草稿蒙版',
  'ui.canvas.layers.draft_mask_count': '当前画布有 {count} 笔草稿蒙版。',
  'ui.canvas.layers.draft_mask_empty': '当前画布没有草稿蒙版。',
  'ui.canvas.layers.draft_mask_scope': '草稿蒙版属于整张画布，不属于某个图层。',
  'ui.canvas.layers.draft_mask_preview_alt': '当前画布草稿蒙版预览',
  'ui.canvas.layers.inpaint_mask_view': '查看重绘蒙版',
  'ui.canvas.layers.inpaint_mask_title': '这个重绘图层使用的蒙版',
};

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({
    schema: defaultCatalog.application,
    text: (key: string) => messages[key] ?? '',
  }),
}));

vi.mock('@/store/canvas-store', () => {
  const noop = () => undefined;
  const state = {
    selectedNodeIds: mock.selected,
    selectNodes: noop,
    toggleNodeVisibility: noop,
    toggleNodeLock: noop,
    toggleGroupVisibility: noop,
    toggleGroupLock: noop,
    arrangeLayers: noop,
    removeNode: noop,
  };
  return {
    useCanvasStore: (selector: (value: typeof state & {
      nodesBySession: { s1: Array<Record<string, unknown>> };
      canvasSizeBySession: { s1: { width: number; height: number } | undefined };
    }) => unknown) => selector({
      ...state,
      nodesBySession: { s1: mock.nodes },
      canvasSizeBySession: { s1: mock.canvasSize },
    }),
  };
});

vi.mock('@/store/mask-store', () => ({
  useMaskStore: (selector: (value: { draft: Record<string, unknown> | null }) => unknown) => selector({ draft: mock.draft }),
}));

import { CanvasLayersPanel } from './layers-panel';

const maskAssetId = 'asset_0123456789abcdef0123456789abcdef';
const image = (id: string, inpaint?: Record<string, unknown>) => ({
  id, type: 'image', imageUrl: `/api/v1/assets/${id}`, taskId: 'uploaded',
  x: 0, y: 0, width: 100, height: 100, zIndex: 1, ...(inpaint ? { inpaint } : {}),
});

describe('layers panel mask visibility (#309)', () => {
  it('offers a mask entry only for a layer with a valid persisted source', () => {
    mock.nodes = [
      image('result', { maskAssetId, width: 3840, height: 2160 }),
      image('plain'),
      image('broken', { maskAssetId: 'x', width: 1, height: 1 }),
    ];
    mock.draft = null;
    const html = renderToStaticMarkup(<CanvasLayersPanel sessionId="s1" />);
    expect((html.match(/aria-label="查看重绘蒙版"/g) ?? [])).toHaveLength(1);
    expect(html).toContain('data-node="result"');
  });

  it('distinguishes the current canvas draft from a layer mask and states its ownership', () => {
    mock.nodes = [image('plain')];
    mock.draft = { sessionId: 's1', strokes: [{ points: [1, 1], size: 8 }], redo: [], current: null };
    const html = renderToStaticMarkup(<CanvasLayersPanel sessionId="s1" />);
    expect(html).toContain('data-testid="canvas-draft-mask"');
    expect(html).toContain('当前画布有 1 笔草稿蒙版。');
    expect(html).toContain('草稿蒙版属于整张画布，不属于某个图层。');
  });

  it('does not borrow another session\'s parked draft', () => {
    mock.nodes = [];
    mock.draft = { sessionId: 's2', strokes: [{ points: [1, 1], size: 8 }], redo: [], current: null };
    const html = renderToStaticMarkup(<CanvasLayersPanel sessionId="s1" />);
    expect(html).toContain('当前画布没有草稿蒙版。');
    expect(html).not.toContain('当前画布有 1 笔草稿蒙版。');
  });
});

it('keeps the submitted draft until acceptance and then clears only that draft', () => {
  const board = readFileSync(new URL('./canvas-board.tsx', import.meta.url), 'utf8');
  const complete = board.slice(board.indexOf('const completeMask = () =>'), board.indexOf('completeMaskRef.current = completeMask'));
  expect(complete).toContain('const submittedDraft = maskState;');
  expect(complete).toContain('onSubmitted: () => {');
  expect(complete).toContain('sameMaskDraft(current, submittedDraft)');
  expect((complete.match(/clearDraft\(\)/g) ?? [])).toHaveLength(1);
  expect(complete.indexOf('onInpaintRequest')).toBeLessThan(complete.indexOf("onToolChange('select')"));
});
