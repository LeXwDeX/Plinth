import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { ChatModelChoice } from '@/lib/chat/browser-client';
import { ChatModelNotice, ChatModelPicker, chatModelSection } from '@/components/studio/chat-model-picker';
import { useChatStore } from '@/store/chat-store';
import { useSessionContextStore } from '@/store/session-context-store';

const catalog = defaultCatalog.application as unknown as ProductSchema;
const messages = catalog.content.messages;
const mock = vi.hoisted(() => ({ schema: null as unknown }));

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: mock.schema }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ name, children }: { name: string; children: React.ReactNode }) => <span data-hint-name={name}>{children}</span>,
}));
vi.mock('@/store/chat-store', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/chat-store')>();
  return { ...actual, useChatStore: Object.assign((selector: (state: ReturnType<typeof actual.useChatStore.getState>) => unknown) => selector(actual.useChatStore.getState()), actual.useChatStore) };
});
vi.mock('@/store/session-context-store', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/session-context-store')>();
  return { ...actual, useSessionContextStore: Object.assign((selector: (state: ReturnType<typeof actual.useSessionContextStore.getState>) => unknown) => selector(actual.useSessionContextStore.getState()), actual.useSessionContextStore) };
});

const two: ChatModelChoice = {
  selected: 'GLM', chosen: 'GLM', defaultId: 'LLM', fallback: false,
  options: [{ id: 'LLM', model: 'deepseek' }, { id: 'GLM', model: 'glm-5.3' }],
};

function open(session: string | null, choice?: ChatModelChoice) {
  useChatStore.setState({ activeSessionId: session });
  useSessionContextStore.setState({ chatModel: session && choice ? { [session]: choice } : {}, chatModelBusy: {} });
}

beforeEach(() => { mock.schema = catalog; });
afterEach(() => open(null));

describe('the chat model picker (#322)', () => {
  it('shows the session model by its upstream name when two or more are offered', () => {
    open('s1', two);
    const html = renderToStaticMarkup(<ChatModelPicker />);
    expect(html).toContain('data-testid="workspace-chat-model-picker"');
    expect(html).toContain(`aria-label="${messages['ui.studio.chat_model.label']}: glm-5.3"`);
  });

  it('is hidden with a single model, without a session or on an older server', () => {
    open('s1', { ...two, selected: 'LLM', options: [two.options[0]] });
    expect(renderToStaticMarkup(<ChatModelPicker />)).toBe('');
    open(null);
    expect(renderToStaticMarkup(<ChatModelPicker />)).toBe('');
    open('s1', two);
    mock.schema = { ...catalog, runtime: { ...catalog.runtime, http: { ...catalog.runtime.http, routes: { ...catalog.runtime.http.routes, chat_session_model: undefined } } } };
    expect(renderToStaticMarkup(<ChatModelPicker />)).toBe('');
  });

  it('lists the offered models with the default tagged and the session model selected', () => {
    const section = chatModelSection(catalog, two, () => undefined);
    expect(section.rows.map((row) => row.cells[0].text)).toEqual(['deepseek', 'glm-5.3']);
    expect(section.rows[0].cells[0].tag).toBe(messages['ui.studio.chat_model.default_tag']);
    expect(section.rows[1].cells[0].tag).toBeUndefined();
    expect(section.selectedKey).toBe('GLM');
  });

  it('says so when the chosen model went offline, even with one model left', () => {
    open('s1', { selected: 'LLM', chosen: 'GLM', defaultId: 'LLM', fallback: true, options: [{ id: 'LLM', model: 'deepseek' }] });
    const html = renderToStaticMarkup(<ChatModelNotice />);
    expect(html).toContain('data-testid="workspace-chat-model-notice"');
    expect(html).toContain(messages['ui.studio.chat_model.fallback'].replace('{model}', 'deepseek'));
    open('s1', two);
    expect(renderToStaticMarkup(<ChatModelNotice />)).toBe('');
  });
});
