import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { ViewMode } from '@/lib/workspace/view-mode';
import { Stage3DViews } from './stage-3d-views';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));

const noop = () => {};
const render = (mode: ViewMode, supported: boolean, loaded = true) =>
  renderToStaticMarkup(<Stage3DViews mode={mode} onChange={noop} loaded={loaded} supported={supported} />);
const buttonOf = (html: string, id: string) => html.match(new RegExp(`<button[^>]*data-tool="${id}"[^>]*>`))?.[0] ?? '';

describe('the 3D view bar', () => {
  it('is the shared rail on the viewer side: texture, clay and wire, no UV, collapsed by default', () => {
    const html = render('texture', true);
    expect(html).toContain('data-testid="stage-3d-views"');
    expect(html).toContain('data-rail="viewer"');
    expect(html).toContain('data-expanded="false"');
    expect([...html.matchAll(/data-tool="([a-z]+)"/g)].map((match) => match[1])).toEqual(['texture', 'clay', 'wire']);
    expect(html).toContain('data-testid="stage-3d-view-wire"');
    expect(html).not.toMatch(/uv/i);
    expect(html).toContain('left-6 top-6');
    expect(html).toContain(`aria-label="${messages['ui.studio.stage.view.label']}"`);
  });

  it('presses the view in use and no other', () => {
    for (const mode of ['texture', 'clay', 'wire'] as const) {
      const html = render(mode, true);
      for (const id of ['texture', 'clay', 'wire']) {
        expect(buttonOf(html, id), `${mode}/${id}`).toContain(`aria-pressed="${id === mode}"`);
      }
    }
  });

  it('can use every view when the viewer reads the model\'s materials', () => {
    const html = render('texture', true);
    for (const id of ['texture', 'clay', 'wire']) expect(buttonOf(html, id), id).not.toContain('disabled=""');
  });

  it('keeps texture and greys clay and wire out when the materials cannot be read', () => {
    const html = render('texture', false);
    expect(buttonOf(html, 'texture')).not.toContain('disabled=""');
    expect(buttonOf(html, 'clay')).toContain('disabled=""');
    expect(buttonOf(html, 'wire')).toContain('disabled=""');
  });

  it('greys clay and wire out while the model has not loaded, whatever the materials turn out to be (#264)', () => {
    for (const supported of [true, false]) {
      const html = render('texture', supported, false);
      expect(buttonOf(html, 'texture'), `${supported}`).not.toContain('disabled=""');
      expect(buttonOf(html, 'clay'), `${supported}`).toContain('disabled=""');
      expect(buttonOf(html, 'wire'), `${supported}`).toContain('disabled=""');
    }
  });
});
