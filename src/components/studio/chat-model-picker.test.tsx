import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { ChatModelChoice } from '@/lib/chat/browser-client';
import { ChatModelNotice, ChatModelPicker, chatModelSection, useChatModelSettings } from '@/components/studio/chat-model-picker';
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

const reasoning = { effort: 'high', levels: ['low', 'high', 'max'], defaultLevel: 'high' };

/**
 * The chat model tab as the settings dialog builds it (#405): the settings are read by the always-mounted dialog, and the
 * content is laid out only when there is anything to show. `value` and `changed` are what the gear's marker reads.
 */
function ChatTab() {
  const settings = useChatModelSettings();
  return (
    <>
      <output data-offered={String(settings.offered)} data-value={settings.value} data-changed={String(settings.changed)} />
      {settings.offered ? <ChatModelPicker settings={settings} /> : null}
    </>
  );
}
const tab = () => renderToStaticMarkup(<ChatTab />);
const settingsOf = (html: string) => ({
  offered: html.match(/data-offered="([^"]*)"/)?.[1],
  value: html.match(/data-value="([^"]*)"/)?.[1],
  changed: html.match(/data-changed="([^"]*)"/)?.[1],
});

function open(session: string | null, choice?: ChatModelChoice, levels = false) {
  useChatStore.setState({ activeSessionId: session });
  useSessionContextStore.setState({
    chatModel: session && choice ? { [session]: choice } : {},
    chatModelBusy: {},
    reasoning: session && levels ? { [session]: reasoning } : {},
    reasoningBusy: {},
  });
}

beforeEach(() => { mock.schema = catalog; });
afterEach(() => open(null));

describe('the chat model picker (#322)', () => {
  it('lists the offered models by their upstream names, the session model selected, when two or more are offered', () => {
    open('s1', two);
    const html = tab();
    expect(html).toContain('data-testid="workspace-chat-model-picker-panel"');
    expect(html).toContain('data-testid="workspace-chat-model-section"');
    expect(html).toContain('>deepseek<');
    expect(html).toMatch(/aria-checked="true"[^>]*>(?:(?!<\/button>).)*glm-5\.3/);
    // The value the gear shows for the chat model is the session model's name.
    expect(settingsOf(html)).toEqual({ offered: 'true', value: 'glm-5.3', changed: 'true' });
  });

  it('is left out with a single model, without a session or on an older server', () => {
    open('s1', { ...two, selected: 'LLM', options: [two.options[0]] });
    expect(settingsOf(tab()).offered).toBe('false');
    expect(tab()).not.toContain('workspace-chat-model-picker-panel');
    open(null);
    expect(settingsOf(tab()).offered).toBe('false');
    open('s1', two);
    mock.schema = { ...catalog, runtime: { ...catalog.runtime, http: { ...catalog.runtime.http, routes: { ...catalog.runtime.http.routes, chat_session_model: undefined } } } };
    expect(settingsOf(tab()).offered).toBe('false');
  });

  it('shows the model and its level in one entry, in two columns, when both are available', () => {
    open('s1', two, true);
    const html = tab();
    expect(settingsOf(html).value).toBe('glm-5.3 · high');
    expect(html).toContain('data-layout="columns"');
    expect(html).toContain('data-testid="workspace-chat-model-section"');
    expect(html).toContain('data-testid="workspace-reasoning-section"');
  });

  it('shows only the thinking levels with a single chat model, and names that model and the level as the value', () => {
    open('s1', { ...two, selected: 'LLM', options: [two.options[0]] }, true);
    const html = tab();
    expect(settingsOf(html)).toEqual({ offered: 'true', value: 'deepseek · high', changed: 'false' });
    expect(html).toContain('data-testid="workspace-reasoning-section"');
    expect(html).not.toContain('data-testid="workspace-chat-model-section"');
    expect(html).not.toContain('data-layout="columns"');
  });

  it('counts the chat model as changed only when it is not the administrator default', () => {
    open('s1', { ...two, selected: 'LLM' }, true);
    expect(settingsOf(tab()).changed).toBe('false');
    open('s1', two, true);
    expect(settingsOf(tab()).changed).toBe('true');
    // The default model with a thinking level off its default is a change too.
    open('s1', { ...two, selected: 'LLM' });
    useSessionContextStore.setState({ reasoning: { s1: { ...reasoning, effort: 'max' } } });
    expect(settingsOf(tab()).changed).toBe('true');
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
    // #94: icon and "无法使用，<短原因>" on the page, the full sentence in the hover detail.
    expect(html).toContain('data-testid="unavailable-icon"');
    expect(html).toContain(`data-testid="unavailable-label">无法使用，${messages['ui.unavailable.reason.picker.chat_model_offline']}<`);
    expect(html).toContain(`data-testid="unavailable-detail">${messages['ui.studio.chat_model.fallback'].replace('{model}', 'deepseek')}<`);
    open('s1', two);
    expect(renderToStaticMarkup(<ChatModelNotice />)).toBe('');
  });
});
