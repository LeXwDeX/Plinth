import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { CANVAS_TOOL_IDS } from '@/lib/workspace/tools';
import type { CanvasToolType } from '@/store/canvas-store';
import { CanvasToolRail } from './canvas-tool-rail';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));

const noop = () => {};
const render = (props: Partial<React.ComponentProps<typeof CanvasToolRail>> = {}) =>
  renderToStaticMarkup(
    <CanvasToolRail sessionId="s" activeTool={'select' as CanvasToolType} onToolChange={noop} onUploadImage={noop} {...props} />,
  );
const buttonOf = (html: string, id: string) => html.match(new RegExp(`<button[^>]*data-tool="${id}"[^>]*>`))?.[0] ?? '';
const hasClass = (tag: string, name: string) => (tag.match(/class="([^"]*)"/)?.[1] ?? '').split(/\s+/).includes(name);

describe('the 2D tool rail', () => {
  // Server rendering reads a zustand store's initial state, so these tests cover the rail as a fresh session shows it;
  // what a click changes in the store belongs to the store's own tests.
  it('is the shared rail on the canvas side, at the stage\'s top-left, with a button for every tool the code handles', () => {
    const html = render();
    expect(html).toContain('data-testid="canvas-tool-rail"');
    expect(html).toContain('data-rail="canvas"');
    expect(html).toContain('left-3 top-3');
    expect([...html.matchAll(/data-tool="([a-z]+)"/g)].map((match) => match[1])).toEqual([...CANVAS_TOOL_IDS]);
    expect(html).toContain('data-testid="canvas-tool-mask"');
  });

  it('presses the tool in use and no other tool', () => {
    for (const tool of ['select', 'brush', 'mask', 'eraser'] as const) {
      const html = render({ activeTool: tool });
      expect(buttonOf(html, tool), tool).toContain('aria-pressed="true"');
      expect(buttonOf(html, tool === 'select' ? 'hand' : 'select'), tool).toContain('aria-pressed="false"');
    }
  });

  it('shows the snap switch as the Schema default says, under the name 吸附', () => {
    const tag = buttonOf(render(), 'snap');
    expect(tag).toContain(`aria-pressed="${schema.canvas.frontend.snap.default_enabled}"`);
    expect(tag).toContain('aria-label="吸附"');
  });

  it('greys undo and redo out while there is nothing to undo or redo, and leaves them as plain actions', () => {
    const html = render();
    expect(buttonOf(html, 'undo')).toContain('disabled=""');
    expect(buttonOf(html, 'redo')).toContain('disabled=""');
    expect(buttonOf(html, 'undo')).not.toContain('aria-pressed="true"');
  });

  it('keeps the mask tool usable on a model that supports masks', () => {
    const tag = buttonOf(render({ maskSupported: true }), 'mask');
    expect(tag).not.toContain('disabled=""');
    expect(hasClass(tag, 'opacity-50')).toBe(false);
  });

  it('dims the mask tool on a model without masks but keeps it clickable when the caller offers a fix', () => {
    const tag = buttonOf(render({ maskSupported: false, onMaskUnsupported: noop }), 'mask');
    expect(tag).not.toContain('disabled=""');
    expect(hasClass(tag, 'opacity-50')).toBe(true);
  });

  it('disables the mask tool on a model without masks when nothing can offer a fix', () => {
    const tag = buttonOf(render({ maskSupported: false }), 'mask');
    expect(tag).toContain('disabled=""');
    expect(hasClass(tag, 'opacity-50')).toBe(true);
  });

  it('names the shape button 形状工具 and draws no flyout until it is opened', () => {
    const html = render();
    expect(html).not.toContain('left-full');
    expect(buttonOf(html, 'shape')).toContain('aria-label="形状工具"');
  });
});
