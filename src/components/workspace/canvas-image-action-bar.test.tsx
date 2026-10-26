import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { CanvasImageActionBar } from '@/components/workspace/canvas-image-actions';
import type { ProductSchema } from '@/lib/product-schema';
import { shortcutCaps } from '@/lib/workspace/stage-actions';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));

const noop = () => {};
const bar = (layer: { type: string; name?: string; text?: string; locked?: boolean }) => renderToStaticMarkup(
  <CanvasImageActionBar layer={layer} onDuplicate={noop} onDownload={noop} onDelete={noop} />,
);
const buttons = (html: string) => html.match(/<button[^>]*>/g) ?? [];

describe('the floating bar over a selected layer', () => {
  it('leads with the layer name and holds exactly three icon buttons: 复制一份, 下载这一层, 删除这一层', () => {
    const html = bar({ type: 'image' });
    expect(html).toContain('data-testid="canvas-layer-bar"');
    expect(html).toMatch(/data-testid="canvas-layer-bar-name"[^>]*>图片</);
    expect(html.indexOf('canvas-layer-bar-name')).toBeLessThan(html.indexOf('canvas-layer-duplicate'));
    expect(buttons(html)).toHaveLength(3);
    for (const [testId, key] of [
      ['canvas-layer-duplicate', 'ui.canvas.menu.duplicate'],
      ['canvas-layer-download', 'ui.canvas.action.download'],
      ['canvas-layer-delete', 'ui.canvas.action.delete_layer'],
    ] as const) {
      const button = buttons(html).find((item) => item.includes(`data-testid="${testId}"`)) ?? '';
      expect(button, testId).toContain(`aria-label="${messages[key]}"`);
    }
    expect(messages['ui.canvas.menu.duplicate']).toBe('复制一份');
    expect(messages['ui.canvas.action.download']).toBe('下载这一层');
    expect(messages['ui.canvas.action.delete_layer']).toBe('删除这一层');
  });

  it('calls a named layer by its name and a text layer by its first characters', () => {
    expect(bar({ type: 'image', name: '局部重绘 1' })).toMatch(/data-testid="canvas-layer-bar-name"[^>]*>局部重绘 1</);
    expect(bar({ type: 'text', text: '一二三四五六七八九十一二三四五' })).toMatch(/data-testid="canvas-layer-bar-name"[^>]*>一二三四五六七八九十一二</);
  });

  it('uses no native title: the hint is a HintTooltip around each icon', () => {
    expect(bar({ type: 'image' })).not.toContain('title=');
  });

  it('offers nothing that calls a model, sends to the chat or converts to 3D (those are on the stage toolbar)', () => {
    const html = bar({ type: 'image' });
    const testIds = [...html.matchAll(/data-testid="([^"]+)"/g)].map((match) => match[1]);
    expect(testIds).toEqual(['canvas-layer-bar', 'canvas-layer-bar-name', 'canvas-layer-duplicate', 'canvas-layer-download', 'canvas-layer-delete']);
    for (const key of ['ui.canvas.action.bgremoval', 'ui.canvas.action.send_to_chat', 'ui.canvas.action.send_to_3d']) {
      expect(messages[key], key).toBeUndefined();
    }
  });

  it('switches off delete for a locked layer, and only that', () => {
    const locked = buttons(bar({ type: 'image', locked: true }));
    expect(locked.find((item) => item.includes('canvas-layer-delete'))).toContain('disabled=""');
    expect(locked.find((item) => item.includes('canvas-layer-duplicate'))).not.toContain('disabled=""');
    expect(locked.find((item) => item.includes('canvas-layer-download'))).not.toContain('disabled=""');
    expect(buttons(bar({ type: 'image' })).find((item) => item.includes('canvas-layer-delete'))).not.toContain('disabled=""');
  });

  it('words the hints from the Schema: the duplicate shortcut comes from keyboard.duplicate_shortcut', () => {
    expect(shortcutCaps(schema, 'duplicate_shortcut', { mac: false })).toEqual(['Ctrl', 'J']);
    expect(shortcutCaps(schema, 'duplicate_shortcut', { mac: true })).toEqual(['⌘', 'J']);
    for (const key of ['duplicate_hint', 'download_hint', 'delete_hint', 'delete_locked', 'label']) {
      expect(messages[`ui.canvas.layer_bar.${key}`], key).toBeTruthy();
    }
  });
});
