import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { layerName, TEXT_LAYER_NAME_CHARACTERS } from './layer-name';

const messages = defaultCatalog.application.content.messages as unknown as Record<string, string>;
const text = (key: string) => messages[key] ?? '';

describe('layerName', () => {
  it('uses the name a layer was given, whatever its type', () => {
    expect(layerName({ type: 'image', name: '局部重绘 @img3' }, text)).toBe('局部重绘 @img3');
    expect(layerName({ type: 'text', name: '标题', text: '正文' }, text)).toBe('标题');
  });

  it('calls a text layer by its first 12 characters', () => {
    expect(TEXT_LAYER_NAME_CHARACTERS).toBe(12);
    expect(layerName({ type: 'text', text: '一二三四五六七八九十甲乙丙丁' }, text)).toBe('一二三四五六七八九十甲乙');
    expect(layerName({ type: 'text', text: '短' }, text)).toBe('短');
  });

  it('falls back to the type word for an empty text layer and for every other type', () => {
    expect(layerName({ type: 'text', text: '' }, text)).toBe('文字');
    expect(layerName({ type: 'text' }, text)).toBe('文字');
    for (const [type, word] of [['image', '图片'], ['shape', '形状'], ['pencil', '笔迹'], ['frame', '画框']] as const) {
      expect(layerName({ type }, text), type).toBe(word);
    }
  });

  it('shows an untouched default name in the reader\'s language and keeps a chosen name', () => {
    const persisted = { 'ui.canvas.layers.initial_name': ['画布图层', 'Canvas layer'] };
    const english = (key: string) => (key === 'ui.canvas.layers.initial_name' ? 'Canvas layer' : text(key));
    expect(layerName({ type: 'image', name: '画布图层' }, english, persisted)).toBe('Canvas layer');
    expect(layerName({ type: 'image', name: 'Canvas layer' }, text, persisted)).toBe(messages['ui.canvas.layers.initial_name']);
    expect(layerName({ type: 'image', name: '局部重绘 @img3' }, english, persisted)).toBe('局部重绘 @img3');
    // Without the declaration the saved name is shown as it is.
    expect(layerName({ type: 'image', name: 'Canvas layer' }, text)).toBe('Canvas layer');
  });

  it('is the rule the layers panel and the floating bar both read', async () => {
    const { readFileSync } = await import('node:fs');
    const panel = readFileSync(new URL('../../components/canvas/layers-panel.tsx', import.meta.url), 'utf8');
    const bar = readFileSync(new URL('../../components/workspace/canvas-image-actions.tsx', import.meta.url), 'utf8');
    expect(panel).toContain("from '@/lib/canvas/layer-name'");
    expect(bar).toContain("from '@/lib/canvas/layer-name'");
    expect(panel).not.toContain('ui.canvas.layers.type_');
    expect(panel).toContain('persisted_default_texts');
    expect(bar).toContain('persisted_default_texts');
    expect(bar).not.toContain('ui.canvas.layers.type_');
  });
});
