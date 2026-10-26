import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { CapabilitySchema, ProductSchema } from '@/lib/product-schema';
import { hintContentOf, processStepView, stageActionViews } from '@/lib/workspace/stage-actions';
import { ActionButton } from './action-button';
import { HintBubble } from '@/components/ui/hint-tooltip';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));

const view = (kind: 'image' | 'model' | 'board', id: string) => stageActionViews(schema, kind).find((item) => item.id === id)!;
const noop = () => {};

it('draws a model-calling text button with its name, the ✦ after the text and a dot for narrow stages', () => {
  const html = renderToStaticMarkup(<ActionButton view={view('image', 'inpaint')} onAct={noop} />);
  expect(html).toContain('aria-label="局部重绘"');
  expect(html).toContain('data-testid="stage-inpaint"');
  expect(html).toContain('data-ai="true"');
  // Label and ✦ appear together from the stage width the label allows; the dot covers the narrower widths.
  expect(html).toMatch(/<span class="hidden @5xl:inline"[^>]*>局部重绘<\/span>/);
  expect(html).toMatch(/<span aria-hidden="true" class="[^"]*text-\[10px\][^"]*text-ai-mark[^"]*hidden @5xl:inline"[^>]*>✦<\/span>/);
  expect(html).toContain('@5xl:hidden');
  expect(html).toContain('bg-ai-mark');
  expect(html).toContain('<svg');
});

it('draws no ✦ and no dot on a button that does not call a model', () => {
  const html = renderToStaticMarkup(<ActionButton view={view('model', 'capture_2d')} onAct={noop} />);
  expect(html).toContain('aria-label="截图到 2D"');
  expect(html).toContain('data-testid="stage-capture"');
  expect(html).toContain('data-ai="false"');
  expect(html).not.toContain('✦');
  expect(html).not.toContain('bg-ai-mark');
});

it('shows only a dot at the icon\'s corner on an icon-only button that calls a model', () => {
  const html = renderToStaticMarkup(<ActionButton view={{ ...view('image', 'inpaint'), group: 'output' }} iconOnly onAct={noop} />);
  expect(html).toContain('aria-label="局部重绘"');
  expect(html).toContain('bg-ai-mark');
  expect(html).not.toContain('✦');
  expect(html).not.toContain('data-testid="action-label"');
});

it('keeps the large view and the other output buttons icon-only with their names for screen readers', () => {
  for (const [kind, id, label, testId] of [
    ['image', 'enlarge', '大图', 'stage-enlarge'],
    ['image', 'compare', '比较', 'stage-compare'],
    ['image', 'send_chat', '发送到对话', 'stage-send-chat'],
    ['image', 'export_board', '导出画板', 'stage-export-board'],
  ] as const) {
    const html = renderToStaticMarkup(<ActionButton view={view(kind, id)} iconOnly onAct={noop} />);
    expect(html, id).toContain(`aria-label="${label}"`);
    expect(html, id).toContain(`data-testid="${testId}"`);
    expect(html, id).not.toContain('data-testid="action-label"');
    expect(html, id).not.toContain('✦');
  }
});

it('staggers where a label appears by its length so the toolbar does not crowd', () => {
  const long = renderToStaticMarkup(<ActionButton view={view('image', 'retexture_with_image')} onAct={noop} />);
  expect(long).toContain('hidden @6xl:inline');
  expect(long).not.toContain('@5xl:');
  const merge = renderToStaticMarkup(<ActionButton view={view('board', 'merge')} onAct={noop} />);
  expect(merge).toContain('hidden @7xl:inline');
});

it('reports a pressed toggle and a disabled button, which keeps its hover hint wrapper', () => {
  const pressed = renderToStaticMarkup(<ActionButton view={view('image', 'compare')} iconOnly pressed onAct={noop} />);
  expect(pressed).toContain('aria-pressed="true"');
  expect(pressed).toContain('bg-primary/15');
  const disabled = renderToStaticMarkup(<ActionButton view={view('image', 'export_board')} iconOnly disabled disabledReason="虚线框内还没有内容可导出" onAct={noop} />);
  expect(disabled).toMatch(/^<span[^>]*inline-flex[^>]*><button[^>]*disabled=""/);
  expect(disabled).not.toContain('aria-pressed');
});

it('draws a neutral processing navigation step without AI marker, dot or tooltip colour', () => {
  const capability = schema.capabilities.find((item) => item.id === 'rigging') as CapabilitySchema;
  const html = renderToStaticMarkup(<ActionButton view={processStepView(schema, capability)} onAct={noop} />);
  expect(html).toContain('aria-label="绑骨"');
  expect(html).toContain('data-testid="stage-process-rigging"');
  expect(html).toContain('data-ai="false"');
  expect(html).not.toContain('data-ai="true"');
  expect(html).not.toContain('data-testid="ai-mark"');
  expect(html).not.toContain('data-testid="ai-dot"');
  expect(html).not.toContain('✦');
  expect(html).not.toContain('bg-ai-mark');
  expect(html).toContain('<svg');
  const hint = renderToStaticMarkup(<HintBubble {...hintContentOf(schema, processStepView(schema, capability))} />);
  expect(hint).toContain('data-ai="false"');
  expect(hint).not.toContain('text-ai-mark');
  expect(hint).toContain(messages['ui.studio.hint.cost.process_panel']);
});

it('follows an explicit Schema processing marker switch without changing capability identity', () => {
  const revised = structuredClone(schema);
  Object.assign(revised.canvas.frontend, { processing_action: { calls_model: true, cost_hint: 'process_panel', group: 'produce' } });
  const capability = schema.capabilities.find((item) => item.id === 'format-conversion') as CapabilitySchema;
  const step = processStepView(revised, capability);
  const html = renderToStaticMarkup(<ActionButton view={step} onAct={noop} />);
  expect(html).toContain('data-testid="stage-process-format-conversion"');
  expect(html).toContain('data-testid="ai-mark"');
  expect(html).toContain('data-testid="ai-dot"');
  const hint = renderToStaticMarkup(<HintBubble {...hintContentOf(revised, step)} />);
  expect(hint).toContain('data-ai="true"');
  expect(hint).toContain('text-ai-mark');
});

it('has no native title tooltip: the hover hint is the only one', () => {
  const html = renderToStaticMarkup(<ActionButton view={view('image', 'inpaint')} onAct={noop} />);
  expect(html).not.toContain(' title=');
});
