import React from 'react';
import { readFileSync } from 'node:fs';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { CanvasBackgroundMenu } from './canvas-zoom-controls';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages as Record<string, string>;
const backgrounds = schema.canvas.frontend.backgrounds;
const read = (relative: string) => readFileSync(new URL(relative, import.meta.url), 'utf8');

const render = (value: string) => renderToStaticMarkup(
  <CanvasBackgroundMenu
    options={backgrounds}
    value={value}
    label={messages['ui.canvas.toolbar.background']}
    optionLabel={(option) => messages[`ui.canvas.toolbar.background.${option}`]}
    onChoose={() => undefined}
  />,
);
const radios = (html: string) => [...html.matchAll(/<button[^>]*role="radio"[^>]*>[\s\S]*?<\/button>/g)].map((match) => match[0]);
const tagOf = (button: string) => button.match(/^<button[^>]*>/)?.[0] ?? '';
const classesOf = (button: string) => (tagOf(button).match(/class="([^"]*)"/)?.[1] ?? '').split(/\s+/);

describe('the canvas background menu (#277)', () => {
  it('is a radio group of the Schema backgrounds, named and labelled from the Schema', () => {
    const html = render(backgrounds[0].value);
    expect(html).toContain(`role="radiogroup" aria-label="${messages['ui.canvas.toolbar.background']}"`);
    expect(radios(html).map((button) => tagOf(button).match(/data-value="([^"]*)"/)?.[1])).toEqual(backgrounds.map((item) => item.value));
    for (const item of backgrounds) expect(html).toContain(messages[`ui.canvas.toolbar.background.${item.value}`]);
  });

  it.each(backgrounds.map((item) => item.value))('marks %s, and only it, as the current background with a check mark', (value) => {
    const buttons = radios(render(value));
    const checked = buttons.filter((button) => tagOf(button).includes('aria-checked="true"'));
    expect(checked).toHaveLength(1);
    expect(tagOf(checked[0])).toContain(`data-value="${value}"`);
    for (const button of buttons) {
      const isCurrent = button === checked[0];
      expect(tagOf(button)).toContain(`aria-checked="${isCurrent}"`);
      // The check mark lives in its own column, so it stays when the pointer or focus moves elsewhere.
      expect(/data-slot="background-check"[^>]*><svg/.test(button)).toBe(isCurrent);
    }
  });

  it('keeps hover and keyboard focus apart from the choice', () => {
    const buttons = radios(render('plain'));
    const current = buttons.find((button) => tagOf(button).includes('aria-checked="true"'))!;
    const other = buttons.find((button) => tagOf(button).includes('aria-checked="false"'))!;
    // Hover only tints a row that is not the choice; the choice keeps its own tint and does not change on hover.
    expect(classesOf(other)).toEqual(expect.arrayContaining(['hover:bg-muted', 'hover:text-foreground']));
    expect(classesOf(other)).not.toContain('bg-primary/15');
    expect(classesOf(current)).toContain('bg-primary/15');
    expect(classesOf(current).some((name) => name.startsWith('hover:'))).toBe(false);
    // Focus draws a ring on any row and never the check mark or the choice's tint.
    for (const button of buttons) expect(classesOf(button)).toEqual(expect.arrayContaining(['focus-visible:outline-2', 'focus-visible:outline-ring']));
  });

  it('is what the zoom bar opens, and a choice is saved with the canvas project', () => {
    const source = read('./canvas-zoom-controls.tsx');
    expect(source).toContain('<CanvasBackgroundMenu');
    expect(source).toContain('options={canvasSchema.frontend.backgrounds}');
    expect(source).toContain('client.updateProject(projectId, { bgStyle: value })');
    expect(source).toContain("toast.error(text('ui.canvas.background_save_failed'))");
    expect(messages['ui.canvas.background_save_failed']).toBeTruthy();
  });
});
