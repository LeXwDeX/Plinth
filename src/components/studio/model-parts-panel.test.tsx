import React from 'react';
import { readFileSync } from 'node:fs';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ModelPartsPanel } from '@/components/studio/model-parts-panel';
import type { ProductSchema } from '@/lib/product-schema';
import { panelTabIds } from '@/lib/workspace/panel-tabs';
import { useStudioStore } from '@/store/studio-store';

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => key }),
}));

// Server rendering reads a zustand store's initial snapshot, so the selectors read the live state instead.
vi.mock('@/store/studio-store', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/studio-store')>();
  const useStudioStore = Object.assign(
    (selector: (state: ReturnType<typeof actual.useStudioStore.getState>) => unknown) => selector(actual.useStudioStore.getState()),
    actual.useStudioStore,
  );
  return { ...actual, useStudioStore };
});

const object = { taskId: 't1', assetId: 'a1', kind: 'model' as const, url: '/m.glb' };

afterEach(() => {
  useStudioStore.setState({ stageParts: null, selectedPart: null, partExportRequest: null });
});

describe('model parts panel', () => {
  it('puts 部件 between the formats and the details of a model, and offers a picture no parts tab', () => {
    const schema = defaultCatalog.application as unknown as ProductSchema;
    expect(panelTabIds(schema, 'model')).toEqual(['operations', 'process', 'formats', 'parts', 'details']);
    expect(panelTabIds(schema, 'image')).not.toContain('parts');
    expect(panelTabIds(schema, 'board')).not.toContain('parts');
    const panel = readFileSync(new URL('./right-panel.tsx', import.meta.url), 'utf8');
    expect(panel).toContain('<ModelPartsPanel object={object} />');
  });

  it('asks for the model to load before it lists anything, and ignores parts of another model', () => {
    expect(renderToStaticMarkup(<ModelPartsPanel object={object} />)).toContain('模型加载后显示部件');
    useStudioStore.setState({ stageParts: { key: 'other:', parts: [{ id: 'p', name: 'x', triangles: 3 }] } });
    expect(renderToStaticMarkup(<ModelPartsPanel object={object} />)).toContain('模型加载后显示部件');
  });

  it('lists parts with names or numbers and triangles; only the chosen one offers an export', () => {
    useStudioStore.setState({
      stageParts: { key: 't1:a1', parts: [{ id: 'p1', name: 'head', triangles: 12345 }, { id: 'p2', name: '', triangles: 6 }] },
      selectedPart: { key: 't1:a1', id: 'p2' },
    });
    const html = renderToStaticMarkup(<ModelPartsPanel object={object} />);
    expect(html).toContain('head');
    expect(html).toContain('部件 2');
    expect(html).toContain('12,345 三角面');
    expect(html.match(/data-testid="model-part-export"/g)).toHaveLength(1);
    expect(html).toContain('导出 GLB');
  });

  it('says a single part is the whole model', () => {
    useStudioStore.setState({ stageParts: { key: 't1:a1', parts: [{ id: 'p1', name: '', triangles: 9 }] } });
    expect(renderToStaticMarkup(<ModelPartsPanel object={object} />)).toContain('只有一个部件');
  });
});
