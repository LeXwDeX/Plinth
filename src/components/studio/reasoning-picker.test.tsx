import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { ReasoningPicker, reasoningSection } from '@/components/studio/reasoning-picker';
import { useChatStore } from '@/store/chat-store';
import { useSessionContextStore } from '@/store/session-context-store';

const catalog = defaultCatalog.application as unknown as ProductSchema;
const messages = catalog.content.messages;
const mock = vi.hoisted(() => ({ schema: null as unknown }));

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: mock.schema }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ name, hint, reason, children }: { name: string; hint?: string; reason?: string; children: React.ReactNode }) => (
    <span data-hint-name={name} data-hint={hint ?? ''} data-reason={reason ?? ''}>{children}</span>
  ),
}));
// Server rendering reads a zustand store's initial snapshot, so the selectors read the live state instead.
vi.mock('@/store/chat-store', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/chat-store')>();
  return { ...actual, useChatStore: Object.assign((selector: (state: ReturnType<typeof actual.useChatStore.getState>) => unknown) => selector(actual.useChatStore.getState()), actual.useChatStore) };
});
vi.mock('@/store/session-context-store', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/session-context-store')>();
  return { ...actual, useSessionContextStore: Object.assign((selector: (state: ReturnType<typeof actual.useSessionContextStore.getState>) => unknown) => selector(actual.useSessionContextStore.getState()), actual.useSessionContextStore) };
});

const deepseek = { effort: 'high', levels: ['low', 'high', 'max'], defaultLevel: 'high' };

function open(session: string | null, reasoning?: typeof deepseek, busy = false) {
  useChatStore.setState({ activeSessionId: session });
  useSessionContextStore.setState({
    reasoning: session && reasoning ? { [session]: reasoning } : {},
    reasoningBusy: session && busy ? { [session]: true } : {},
  });
}

function wrapper(html: string): string {
  return html.match(/<span data-hint-name="[^"]*" data-hint="[^"]*" data-reason="[^"]*">/)?.[0] ?? '';
}
function trigger(html: string): string {
  return html.match(/<button[^>]*data-testid="workspace-reasoning-picker"[^>]*>/)?.[0] ?? '';
}

beforeEach(() => { mock.schema = catalog; });
afterEach(() => open(null));

describe('the thinking level picker (#322)', () => {
  it('shows the level of the session verbatim, with the Schema hint in its hover text', () => {
    open('s1', deepseek);
    const html = renderToStaticMarkup(<ReasoningPicker />);
    expect(trigger(html)).toContain(`aria-label="${messages['ui.studio.reasoning.label']}: high"`);
    expect(trigger(html)).not.toContain('disabled=""');
    expect(wrapper(html)).toContain(`data-hint="${messages['ui.studio.reasoning.hint']}"`);
    expect(wrapper(html)).toContain('data-reason=""');
  });

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
    open('s1', { effort: '', levels: [], defaultLevel: '' });
    const html = renderToStaticMarkup(<ReasoningPicker />);
    expect(trigger(html)).toContain('disabled=""');
    expect(wrapper(html)).toContain(`data-reason="${messages['ui.studio.reasoning.unavailable']}"`);
    expect(html).toContain(messages['ui.studio.reasoning.unavailable']);
  });

  it('is off without a session and while a switch is being saved', () => {
    open(null);
    expect(trigger(renderToStaticMarkup(<ReasoningPicker />))).toContain('disabled=""');
    open('s1', deepseek, true);
    expect(trigger(renderToStaticMarkup(<ReasoningPicker />))).toContain('disabled=""');
  });

  it('is hidden when the Schema has no thinking level route', () => {
    open('s1', deepseek);
    const schema = structuredClone(catalog);
    delete schema.runtime.http.routes.chat_session_reasoning;
    mock.schema = schema;
    expect(renderToStaticMarkup(<ReasoningPicker />)).toBe('');
  });
});

describe('the composer bottom row (#322)', () => {
  it('pairs the chat model with the thinking level and gives the context usage its own row', async () => {
    const { readFileSync } = await import('node:fs');
    const composer = readFileSync(new URL('./composer.tsx', import.meta.url), 'utf8');
    const bottom = composer.slice(composer.indexOf('data-testid="composer-pickers"'));
    expect(bottom.indexOf('<AgentScopePicker />')).toBeLessThan(bottom.indexOf('<ParamsPopover {...params}'));
    expect(bottom.indexOf('<ParamsPopover {...params}')).toBeLessThan(bottom.indexOf('<ChatModelPicker />'));
    expect(bottom.indexOf('<ChatModelPicker />')).toBeLessThan(bottom.indexOf('<ReasoningPicker />'));
    expect(bottom.indexOf('<ReasoningPicker />')).toBeLessThan(bottom.indexOf('<ContextMeter />'));
  });
});
