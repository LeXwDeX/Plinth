import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';

import { HintBubble, HintTooltip } from './hint-tooltip';

const tooltipSource = readFileSync(new URL('./tooltip.tsx', import.meta.url), 'utf8');
const hintSource = readFileSync(new URL('./hint-tooltip.tsx', import.meta.url), 'utf8');

it('writes the bubble top to bottom: name with shortcut, what it does, what it acts on, the cost line', () => {
  const html = renderToStaticMarkup(
    <HintBubble name="大图" shortcut={['空格']} hint="全窗口查看这张图" target="作用于：舞台上当前这张图" cost={{ text: '免费，不调用模型', ai: false }} />,
  );
  const order = ['大图', '空格', '全窗口查看这张图', '作用于：舞台上当前这张图', '免费，不调用模型'].map((text) => html.indexOf(text));
  expect(order.every((index) => index >= 0)).toBe(true);
  expect([...order].sort((a, b) => a - b)).toEqual(order);
  expect(html).toContain('<kbd');
  expect(html).not.toContain('hint-reason');
});

it('draws the cost line in the AI colour only for a control that calls a model', () => {
  const calling = renderToStaticMarkup(<HintBubble name="局部重绘" cost={{ text: '✦ 会调用模型，积分和批准在对话里', ai: true }} />);
  expect(calling).toContain('text-ai-mark');
  expect(calling).toContain('data-ai="true"');
  const free = renderToStaticMarkup(<HintBubble name="比较" cost={{ text: '免费，不调用模型', ai: false }} />);
  expect(free).not.toContain('text-ai-mark');
  expect(free).toContain('data-ai="false"');
});

it('puts the reason a control is disabled before its name', () => {
  const html = renderToStaticMarkup(<HintBubble name="转 3D" reason="正面图完成后才能转 3D。" hint="用这张图发一轮图生 3D" />);
  expect(html.indexOf('正面图完成后才能转 3D。')).toBeGreaterThan(-1);
  expect(html.indexOf('正面图完成后才能转 3D。')).toBeLessThan(html.indexOf('转 3D</span>'));
  expect(html).toContain('data-testid="hint-reason"');
});

it('omits the lines it was not given', () => {
  const html = renderToStaticMarkup(<HintBubble name="只有名字" />);
  expect(html).not.toContain('<kbd');
  expect(html).not.toContain('hint-text');
  expect(html).not.toContain('hint-target');
  expect(html).not.toContain('hint-cost');
});

it('wraps the control in a span that carries the hover and focus listeners, so a disabled button still shows its hint', () => {
  const html = renderToStaticMarkup(
    <HintTooltip name="导出画板" reason="虚线框内还没有内容可导出">
      <button type="button" disabled>导出</button>
    </HintTooltip>,
  );
  expect(html).toMatch(/^<span[^>]*class="[^"]*inline-flex[^"]*"[^>]*><button[^>]*disabled=""[^>]*>导出<\/button><\/span>$/);
  // Closed, the bubble is not in the page.
  expect(html).not.toContain('hint-bubble');
});

it('passes className to the wrapper, so a corner button keeps its place and a stretched one its width', () => {
  const corner = renderToStaticMarkup(
    <HintTooltip name="全屏" className="absolute right-2 top-2"><button type="button">x</button></HintTooltip>,
  );
  expect(corner).toMatch(/^<span[^>]*class="[^"]*inline-flex[^"]*absolute right-2 top-2[^"]*"/);
  const stretched = renderToStaticMarkup(
    <HintTooltip name="模型: GPT Image 2" className="w-full min-w-0"><button type="button">x</button></HintTooltip>,
  );
  expect(stretched).toContain('w-full min-w-0');
});

it('can make the tooltip wrapper itself a labeled, focusable target', () => {
  const html = renderToStaticMarkup(
    <HintTooltip name="OBJ" reason="OBJ 暂不可用。" triggerProps={{ role: 'img', tabIndex: 0, 'aria-label': '查看原因：OBJ' }}>
      <span className="truncate">OBJ 暂不可用。</span>
    </HintTooltip>,
  );
  expect(html).toMatch(/^<span[^>]*class="[^"]*inline-flex[^"]*"[^>]*role="img"[^>]*tabindex="0"[^>]*aria-label="查看原因：OBJ"[^>]*data-slot="tooltip-trigger"><span class="truncate">OBJ 暂不可用。<\/span><\/span>$/);
});

it('keeps the line breaks and wraps anywhere in a long name, so a truncated value can show whole', () => {
  const html = renderToStaticMarkup(<HintBubble name={'a-very-long-file-name.glb\n尺寸: 2048'} />);
  expect(html).toContain('whitespace-pre-wrap');
  expect(html).toContain('[overflow-wrap:anywhere]');
});

it('never opens for an empty name: a cell with no full value has nothing to say', () => {
  expect(hintSource).toContain("disabled={content.name.trim() === ''}");
});

it('keeps the opening rules in the primitive: 350ms hover, below with 8px, 260px wide, inside the window', () => {
  expect(tooltipSource).toContain('const HOVER_OPEN_DELAY_MS = 350');
  expect(tooltipSource).toContain('delay = HOVER_OPEN_DELAY_MS');
  expect(tooltipSource).toContain('side = "bottom"');
  expect(tooltipSource).toContain('sideOffset = 8');
  expect(tooltipSource).toContain('collisionPadding = 8');
  expect(tooltipSource).toContain('max-w-[260px]');
  expect(tooltipSource).not.toMatch(/\bz-50\b/);
  // Each hint carries its own provider, so the 350ms rule holds wherever the control sits.
  expect(hintSource).toContain('<TooltipProvider>');
  expect(hintSource).toContain('arrow={false}');
});
