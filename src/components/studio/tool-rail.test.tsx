import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Circle } from 'lucide-react';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { CANVAS_TOOL_IDS, VIEWER_TOOL_IDS } from '@/lib/workspace/tools';
import { ToolRail, type ToolHandler } from './tool-rail';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));

const noop = () => {};
const handlersFor = (ids: readonly string[], overrides: Record<string, Partial<ToolHandler>> = {}): Record<string, ToolHandler> =>
  Object.fromEntries(ids.map((id) => [id, { run: noop, ...overrides[id] }]));
const canvas = (expanded: boolean, overrides: Record<string, Partial<ToolHandler>> = {}, handlers = handlersFor(CANVAS_TOOL_IDS, overrides)) =>
  renderToStaticMarkup(
    <ToolRail
      rail="canvas" label={messages['ui.studio.canvas.tools']} handlers={handlers} expanded={expanded} onExpandedChange={noop}
      testId="canvas-tool-rail" itemTestId={(id) => `canvas-tool-${id}`} className="left-3 top-3"
    />,
  );
const viewer = (expanded: boolean, overrides: Record<string, Partial<ToolHandler>> = {}) =>
  renderToStaticMarkup(
    <ToolRail
      rail="viewer" label={messages['ui.studio.stage.view.label']} handlers={handlersFor(VIEWER_TOOL_IDS, overrides)} expanded={expanded}
      onExpandedChange={noop} testId="stage-3d-views" itemTestId={(id) => `stage-3d-view-${id}`} className="left-6 top-6"
    />,
  );
const count = (html: string, fragment: string) => html.split(fragment).length - 1;
const buttonOf = (html: string, id: string) => html.match(new RegExp(`<button[^>]*data-tool="${id}"[^>]*>`))?.[0] ?? '';
/** Whether a tag carries a class by its exact name (Tailwind variants such as `disabled:opacity-50` are other classes). */
const hasClass = (tag: string, name: string) => (tag.match(/class="([^"]*)"/)?.[1] ?? '').split(/\s+/).includes(name);

describe('the collapsed rail', () => {
  const html = canvas(false, { brush: { pressed: true }, select: { pressed: false } });

  it('is a vertical toolbar named by the caller, floating where the caller says, 38px wide', () => {
    expect(html).toContain('role="toolbar"');
    expect(html).toContain('aria-orientation="vertical"');
    expect(html).toContain('aria-label="画板工具"');
    expect(html).toContain('data-testid="canvas-tool-rail"');
    expect(html).toContain('data-expanded="false"');
    expect(html).toContain('width:38px');
    expect(html).toContain('left-3 top-3');
  });

  it('draws the Schema sections in order, split by a thin line, with the toggle after one more line', () => {
    const sections = [...html.matchAll(/data-section="([a-z]+)"/g)].map((match) => match[1]);
    expect(sections).toEqual(['select', 'draw', 'snap', 'history']);
    // Three lines between four sections, one before the toggle.
    expect(count(html, 'data-testid="tool-rail-separator"')).toBe(4);
    expect(count(html, 'role="separator"')).toBe(4);
    expect(html.lastIndexOf('tool-rail-separator')).toBeLessThan(html.indexOf('data-testid="tool-rail-toggle"'));
  });

  it('draws one icon-only 30x28 button per tool in the Schema order, named for screen readers', () => {
    const order = [...html.matchAll(/data-tool="([a-z]+)"/g)].map((match) => match[1]);
    expect(order).toEqual([...CANVAS_TOOL_IDS]);
    for (const [id, label] of [['select', '选择'], ['mask', '蒙版'], ['brush', '画笔'], ['snap', '吸附'], ['undo', '撤销'], ['zoom', '缩放工具']]) {
      expect(buttonOf(html, id), id).toContain(`aria-label="${label}"`);
    }
    expect(hasClass(buttonOf(html, 'brush'), 'w-[30px]')).toBe(true);
    expect(html).toContain('data-testid="canvas-tool-brush"');
    expect(count(html, 'tool-rail-label')).toBe(0);
    expect(count(html, '<svg')).toBe(CANVAS_TOOL_IDS.length + 1);
  });

  it('marks the current tool pressed in the primary colour and every other toggle not pressed', () => {
    expect(buttonOf(html, 'brush')).toContain('aria-pressed="true"');
    expect(hasClass(buttonOf(html, 'brush'), 'bg-primary/15')).toBe(true);
    expect(hasClass(buttonOf(html, 'brush'), 'text-primary')).toBe(true);
    expect(buttonOf(html, 'select')).toContain('aria-pressed="false"');
    expect(hasClass(buttonOf(html, 'select'), 'bg-primary/15')).toBe(false);
    expect(hasClass(buttonOf(html, 'select'), 'text-muted-foreground')).toBe(true);
    // A plain action is not a toggle.
    expect(buttonOf(html, 'upload')).not.toContain('aria-pressed');
    expect(count(html, 'aria-pressed="true"')).toBe(1);
  });

  it('has no native title tooltip: the hover hint is the only one', () => {
    expect(html).not.toContain(' title=');
    expect(canvas(true)).not.toContain(' title=');
    expect(viewer(false)).not.toContain(' title=');
  });

  it('ends with a » toggle that offers to expand', () => {
    const toggle = html.match(/<button[^>]*data-testid="tool-rail-toggle"[^>]*>[\s\S]*?<\/button>/)?.[0] ?? '';
    expect(toggle).toContain('aria-label="展开"');
    expect(toggle).toContain('aria-expanded="false"');
    expect(toggle).toContain('lucide-chevrons-right');
    expect(toggle).not.toContain('收起');
    expect(html.indexOf('data-testid="tool-rail-toggle"')).toBeGreaterThan(html.indexOf('data-tool="redo"'));
  });
});

describe('the expanded rail', () => {
  const html = canvas(true, { brush: { pressed: true } });

  it('shows each name beside its icon, as wide as the longest name between 96 and 112px', () => {
    expect(html).toContain('data-expanded="true"');
    expect(html).toContain('width:max-content');
    expect(html).toContain('min-width:96px');
    expect(html).toContain('max-width:112px');
    expect(html).not.toContain('width:38px');
    expect(html).toContain('w-full');
    expect(hasClass(buttonOf(html, 'brush'), 'w-[30px]')).toBe(false);
    expect(hasClass(buttonOf(html, 'brush'), 'w-full')).toBe(true);
    const labels = [...html.matchAll(/data-testid="tool-rail-label">([^<]*)</g)].map((match) => match[1]);
    expect(labels).toEqual(['选择', '平移工具', '矩形选框', '套索选区', '蒙版', '吸管', '缩放工具', '上传图片', '形状工具', '文字工具', '矢量铅笔', '画笔', '橡皮', '裁剪', '吸附', '撤销', '重做', '收起']);
  });

  it('ends with a « 收起 toggle that offers to collapse', () => {
    const toggle = html.match(/<button[^>]*data-testid="tool-rail-toggle"[^>]*>[\s\S]*?<\/button>/)?.[0] ?? '';
    expect(toggle).toContain('aria-label="收起"');
    expect(toggle).toContain('aria-expanded="true"');
    expect(toggle).toContain('lucide-chevrons-left');
    expect(toggle).toContain('>收起<');
  });

  it('keeps the current tool pressed and the sections split the same way', () => {
    expect(buttonOf(html, 'brush')).toContain('aria-pressed="true"');
    expect(count(html, 'data-testid="tool-rail-separator"')).toBe(4);
  });
});

describe('disabled and dimmed buttons', () => {
  it('keeps a disabled button inside its hover-hint wrapper so the hint can still say why', () => {
    const html = canvas(false, { undo: { disabled: true, reason: '没有可以撤销的操作。' }, redo: { disabled: true } });
    expect(html).toMatch(/<span[^>]*inline-flex[^>]*><button[^>]*data-tool="undo"/);
    expect(buttonOf(html, 'undo')).toContain('disabled=""');
    expect(buttonOf(html, 'redo')).toContain('disabled=""');
    expect(buttonOf(html, 'select')).not.toContain('disabled=""');
    // The bubble itself only opens on hover or focus.
    expect(html).not.toContain('没有可以撤销的操作。');
  });

  it('dims a button that stays clickable (the mask tool on a model without masks)', () => {
    const html = canvas(false, { mask: { dimmed: true, reason: '当前模型不支持局部重绘，点击切换' } });
    expect(hasClass(buttonOf(html, 'mask'), 'opacity-50')).toBe(true);
    expect(buttonOf(html, 'mask')).not.toContain('disabled=""');
    expect(hasClass(buttonOf(html, 'select'), 'opacity-50')).toBe(false);
  });
});

describe('what a handler can change', () => {
  it('replaces the Schema icon (the shape button shows the shape in use)', () => {
    const slotOf = (html: string) => html.match(/<div[^>]*data-tool-slot="shape"[\s\S]*?<\/button>/)?.[0] ?? '';
    expect(slotOf(canvas(false))).toMatch(/lucide-square[ "]/);
    expect(slotOf(canvas(false))).not.toContain('lucide-circle');
    expect(slotOf(canvas(false, { shape: { icon: Circle } }))).toMatch(/lucide-circle[ "]/);
    expect(slotOf(canvas(false, { shape: { icon: Circle } }))).not.toMatch(/lucide-square[ "]/);
  });

  it('puts a flyout inside the button\'s own slot, after the button', () => {
    const html = canvas(false, { shape: { flyout: <ul data-testid="flyout"><li>shape</li></ul> } });
    const slot = html.match(/<div[^>]*data-tool-slot="shape"[^>]*>[\s\S]*?data-testid="flyout"[\s\S]*?<\/div>/)?.[0] ?? '';
    expect(slot).toContain('relative');
    expect(slot.indexOf('data-tool="shape"')).toBeLessThan(slot.indexOf('data-testid="flyout"'));
  });

  it('draws no button for a Schema entry the caller gave no handler, and no line for a section left empty', () => {
    const withoutHistory = Object.fromEntries(Object.entries(handlersFor(CANVAS_TOOL_IDS)).filter(([id]) => id !== 'undo' && id !== 'redo'));
    const html = canvas(false, {}, withoutHistory);
    expect(html).not.toContain('data-tool="undo"');
    expect(html).not.toContain('data-section="history"');
    expect(count(html, 'data-testid="tool-rail-separator"')).toBe(3);
  });
});

describe('the 3D view bar', () => {
  it('is the same rail with one section: texture, clay and wire, then the toggle', () => {
    const html = viewer(false, { texture: { pressed: true }, clay: { pressed: false }, wire: { pressed: false } });
    expect(html).toContain('aria-label="显示方式"');
    expect(html).toContain('data-testid="stage-3d-views"');
    expect(html).toContain('left-6 top-6');
    expect([...html.matchAll(/data-tool="([a-z]+)"/g)].map((match) => match[1])).toEqual(['texture', 'clay', 'wire']);
    expect([...html.matchAll(/data-section="([a-z]+)"/g)].map((match) => match[1])).toEqual(['views']);
    expect(count(html, 'data-testid="tool-rail-separator"')).toBe(1);
    expect(buttonOf(html, 'texture')).toContain('aria-label="贴图"');
    expect(buttonOf(html, 'clay')).toContain('aria-label="白模"');
    expect(buttonOf(html, 'wire')).toContain('aria-label="线框"');
    expect(buttonOf(html, 'texture')).toContain('aria-pressed="true"');
    expect(buttonOf(html, 'clay')).toContain('data-testid="stage-3d-view-clay"');
    expect(html).not.toContain('UV');
  });

  it('greys clay and wire out when the model has no readable materials, and expands with names', () => {
    const html = viewer(true, { clay: { disabled: true, reason: '这个模型读不出材质，只能看贴图。' }, wire: { disabled: true } });
    expect(buttonOf(html, 'clay')).toContain('disabled=""');
    expect(buttonOf(html, 'wire')).toContain('disabled=""');
    expect(buttonOf(html, 'texture')).not.toContain('disabled=""');
    expect([...html.matchAll(/data-testid="tool-rail-label">([^<]*)</g)].map((match) => match[1])).toEqual(['贴图', '白模', '线框', '收起']);
  });
});
