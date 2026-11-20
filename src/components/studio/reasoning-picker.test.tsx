import React from 'react';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { reasoningSection } from '@/components/studio/reasoning-picker';

const catalog = defaultCatalog.application as unknown as ProductSchema;
const messages = catalog.content.messages;

const deepseek = { effort: 'high', levels: ['low', 'high', 'max'], defaultLevel: 'high' };

describe('the thinking level section (#322, merged into the model entry in #176)', () => {
  it('lists the model levels verbatim, tags the default one and selects the current one', () => {
    const section = reasoningSection(catalog, { ...deepseek, effort: 'max' }, () => undefined);
    expect(section.head).toEqual([messages['ui.studio.reasoning.col.level']]);
    expect(section.rows.map((row) => row.key)).toEqual(['low', 'high', 'max']);
    expect(section.rows.map((row) => row.cells[0].text)).toEqual(['low', 'high', 'max']);
    expect(section.rows.map((row) => row.cells[0].tag ?? '')).toEqual(['', messages['ui.studio.reasoning.default_tag'], '']);
    expect(section.selectedKey).toBe('max');
    expect(section.hint).toBe(messages['ui.studio.reasoning.hint']);
    expect(section.foot).toBe(messages['ui.studio.reasoning.note']);
  });

  it('is off with the Schema reason when the model offers no levels', () => {
    const section = reasoningSection(catalog, { effort: '', levels: [], defaultLevel: '' }, () => undefined);
    expect(section.disabledReason).toBe(messages['ui.studio.reasoning.unavailable']);
    expect(section.disabledLabel).toContain(messages['ui.unavailable.reason.picker.reasoning_none']);
  });
});

describe('the composer settings (#322, #176, #405)', () => {
  it('has one combined model-and-thinking tab and no separate thinking picker, and the composer keeps only the status row under the box', async () => {
    const { readFileSync } = await import('node:fs');
    const composer = readFileSync(new URL('./composer.tsx', import.meta.url), 'utf8');
    const settings = readFileSync(new URL('./composer-settings.tsx', import.meta.url), 'utf8');
    // Under the box there is only the status row (context usage and tool calls); the pickers are behind the gear.
    const bottom = composer.slice(composer.indexOf('<ChatModelNotice />'));
    expect(bottom).not.toContain('AgentScopePicker');
    expect(bottom).not.toContain('<ChatModelPicker');
    expect(bottom).not.toContain('<ReasoningPicker');
    expect(bottom.indexOf('<GenerationModelNotice />')).toBeGreaterThan(-1);
    expect(bottom.indexOf('<GenerationModelNotice />')).toBeLessThan(bottom.indexOf('<ContextMeter />'));
    // The dialog has one chat model tab (model and levels together) and no tab of its own for the thinking level.
    expect(settings).toContain('<ChatModelPicker settings={chat} />');
    expect(settings).not.toContain('<ReasoningPicker');
    expect(settings.match(/<TabsContent value=/g)).toHaveLength(4);
  });
});
