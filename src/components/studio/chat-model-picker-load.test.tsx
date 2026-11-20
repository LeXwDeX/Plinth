// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { ChatModelChoice, ChatReasoning } from '@/lib/chat/browser-client';
import { ChatModelPicker, useChatModelSettings } from '@/components/studio/chat-model-picker';
import { ComposerSettings } from '@/components/studio/composer-settings';
import { useChatStore } from '@/store/chat-store';
import { useComposerSettingsStore } from '@/store/composer-settings-store';
import { useSessionContextStore } from '@/store/session-context-store';

// #236: the chat model and levels are used while the page is still loading the session's context. Nothing that arrives
// late (the model, the levels, a revalidated Schema) may close what is open, replace the nodes a click is landing on, or
// move the keyboard focus. #405: the picker's panel is a tab of the settings dialog, so the same promises are kept at
// both levels: the content keeps its nodes and the focus (ChatTab), and the dialog stays open on its tab (ComposerSettings).
// These tests drive the real hooks and stores with a client whose answers the test releases one by one, so "late" means
// late through the same load path the page uses.

const catalog = defaultCatalog.application as unknown as ProductSchema;
const messages = catalog.content.messages;

const mock = vi.hoisted(() => ({ schema: null as unknown, client: null as unknown }));

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: mock.schema }) }));
// The dialog's other tabs are stand-ins here: only the chat model tab is under test.
vi.mock('@/components/studio/model-picker', () => ({
  useDomainModelPick: (domain: string) => ({ section: { title: domain }, short: domain, full: domain }),
  generationModelPair: () => '',
  ModelPicker: () => null,
}));
vi.mock('@/components/studio/skill-picker', () => ({ SkillPicker: () => null }));
vi.mock('@/components/studio/params-popover', () => ({ ParamsPanel: () => null, paramsSummary: () => '' }));
// Every call builds a new client, like the page does when the Schema object is replaced by a revalidation.
vi.mock('@/lib/chat/browser-client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/chat/browser-client')>();
  return { ...actual, createChatBrowserClient: () => ({ ...(mock.client as object) }) };
});

interface Deferred<T> { resolve: (value: T) => void }
function deferred<T>(): { promise: Promise<T>; control: Deferred<T> } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, control: { resolve } };
}

const solo: ChatModelChoice = {
  selected: 'LLM', chosen: '', defaultId: 'LLM', fallback: false, options: [{ id: 'LLM', model: 'deepseek' }],
};
const two: ChatModelChoice = {
  selected: 'LLM', chosen: '', defaultId: 'LLM', fallback: false,
  options: [{ id: 'LLM', model: 'deepseek' }, { id: 'GLM', model: 'glm-5.3' }],
};
const levels: ChatReasoning = { effort: 'high', levels: ['low', 'high', 'max'], defaultLevel: 'high' };

const waiting = {
  model: [] as Array<Deferred<ChatModelChoice>>,
  reasoning: [] as Array<Deferred<ChatReasoning>>,
};
const client = {
  getChatModel: vi.fn(() => { const next = deferred<ChatModelChoice>(); waiting.model.push(next.control); return next.promise; }),
  getReasoning: vi.fn(() => { const next = deferred<ChatReasoning>(); waiting.reasoning.push(next.control); return next.promise; }),
  getContext: vi.fn(() => new Promise<never>(() => undefined)),
  setChatModel: vi.fn(async (_session: string, model: string) => ({ ...two, selected: model, chosen: model })),
  setReasoning: vi.fn(async (_session: string, level: string) => ({ ...levels, effort: level })),
};

/** The server answers every outstanding request of this kind; later requests wait for the next call. */
const answer = {
  model: async (choice: ChatModelChoice) => { await act(async () => { waiting.model.splice(0).forEach((request) => request.resolve(choice)); }); },
  reasoning: async (value: ChatReasoning) => { await act(async () => { waiting.reasoning.splice(0).forEach((request) => request.resolve(value)); }); },
};


const PANEL = 'workspace-chat-model-picker-panel';
const GEAR = 'composer-settings-open';
const CHAT_TAB = 'composer-settings-tab-chat';

/** The chat model tab as the settings dialog builds it (#405): the settings are always read, the content shows once there is any. */
function ChatTab() {
  const settings = useChatModelSettings();
  return (
    <>
      <output data-testid="chat-value">{settings.value}</output>
      {settings.offered ? <ChatModelPicker settings={settings} /> : null}
    </>
  );
}
const value = () => screen.getByTestId('chat-value').textContent;

const noop = () => undefined;
/** The real dialog around the real chat model tab; the other tabs are stand-ins. */
function Dialog() {
  return (
    <ComposerSettings
      locked={false}
      skill={{ sessionId: 's1', catalog: null, effective: null, selectedId: null, unavailable: false, loading: false, failed: false, disabled: false, onSelect: noop, onCreated: noop }}
      params={{ params: { domain: 'image', parameterStates: {} }, domains: [], hasSession: true, skill: null, effective: {}, canvas: null } as never}
    />
  );
}

/** A level row is the node a click or the keyboard is on: it must be the same node before and after late data. */
const levelRow = (name: string) => within(screen.getByTestId(PANEL)).getByRole('menuitemradio', { name });

beforeEach(() => {
  mock.schema = catalog;
  mock.client = client;
  waiting.model.length = 0;
  waiting.reasoning.length = 0;
  Object.values(client).forEach((fn) => fn.mockClear());
  useSessionContextStore.setState({ chatModel: {}, chatModelBusy: {}, reasoning: {}, reasoningBusy: {}, context: {}, compacting: {} });
  useChatStore.setState({ activeSessionId: 's1' });
  useComposerSettingsStore.setState({ open: false, tab: 'models' });
});
afterEach(() => {
  cleanup();
  useChatStore.setState({ activeSessionId: null });
});

describe('the tab while the session context is still loading (#236)', () => {
  it('keeps its nodes and the focus when the model name arrives after the levels (single model)', async () => {
    render(<ChatTab />);
    // The levels answer first: the tab shows with the thinking level alone.
    await answer.reasoning(levels);
    const panel = await screen.findByTestId(PANEL);
    expect(value()).toBe('high');
    const row = levelRow('low');
    row.focus();

    await answer.model(solo);
    expect(screen.getByTestId(PANEL)).toBe(panel);
    expect(levelRow('low')).toBe(row);
    expect(document.activeElement).toBe(row);
    expect(value()).toBe('deepseek · high');
    expect(screen.queryByTestId('workspace-chat-model-section')).toBeNull();
  });

  it('turns into two columns when the model list arrives, keeping the row that had the focus (two models)', async () => {
    render(<ChatTab />);
    await answer.reasoning(levels);
    const panel = await screen.findByTestId(PANEL);
    expect(panel.getAttribute('data-layout')).toBeNull();
    const row = levelRow('max');
    row.focus();

    await answer.model(two);
    expect(screen.getByTestId(PANEL)).toBe(panel);
    expect(panel.getAttribute('data-layout')).toBe('columns');
    expect(within(panel).getByTestId('workspace-chat-model-section')).toBeTruthy();
    // The levels table kept its rows, so the row that had the focus still has it.
    expect(levelRow('max')).toBe(row);
    expect(document.activeElement).toBe(row);
    expect(value()).toBe('deepseek · high');
  });

  it('adds the levels column when they arrive after the model list (two models)', async () => {
    render(<ChatTab />);
    await answer.model(two);
    const panel = await screen.findByTestId(PANEL);
    expect(within(panel).queryByTestId('workspace-reasoning-section')).toBeNull();
    const modelRow = within(panel).getByRole('menuitemradio', { name: /glm-5.3/ });
    modelRow.focus();

    await answer.reasoning(levels);
    expect(screen.getByTestId(PANEL)).toBe(panel);
    expect(within(panel).getByTestId('workspace-reasoning-section')).toBeTruthy();
    expect(within(panel).getByRole('menuitemradio', { name: /glm-5.3/ })).toBe(modelRow);
    expect(document.activeElement).toBe(modelRow);
  });

  it('keeps the tab as it is when the Schema is revalidated and every answer is read again', async () => {
    const view = render(<ChatTab />);
    await answer.reasoning(levels);
    await answer.model(two);
    const panel = await screen.findByTestId(PANEL);
    const row = levelRow('low');
    row.focus();
    const reads = client.getReasoning.mock.calls.length;

    // A revalidation hands the page an equal Schema in a new object: a new client, the effects run again and the
    // answers are read again. While those reads are on their way the tab shows what it already had.
    mock.schema = structuredClone(catalog);
    view.rerender(<ChatTab />);
    await waitFor(() => expect(client.getReasoning.mock.calls.length).toBeGreaterThan(reads));
    expect(screen.getByTestId(PANEL)).toBe(panel);
    expect(levelRow('low')).toBe(row);

    await answer.reasoning({ ...levels });
    await answer.model({ ...two });
    expect(screen.getByTestId(PANEL)).toBe(panel);
    expect(levelRow('low')).toBe(row);
    expect(document.activeElement).toBe(row);
  });

  it('names only the level until the model name arrives, without a dangling separator', async () => {
    render(<ChatTab />);
    await answer.reasoning(levels);
    await screen.findByTestId(PANEL);
    expect(value()).toBe('high');
    await answer.model(solo);
    expect(value()).toBe('deepseek · high');
  });

  it('keeps the same row nodes under a click while the data arrives', async () => {
    render(<ChatTab />);
    await answer.reasoning(levels);
    await screen.findByTestId(PANEL);
    const row = levelRow('max');
    await answer.model(solo);
    // A click is dropped when the node its press landed on is replaced before the release, so loading must only
    // change what the nodes say.
    expect(levelRow('max')).toBe(row);
    await answer.model(two);
    expect(levelRow('max')).toBe(row);
  });

  it('sends a level pick to the server once, and the table stays for the next pick', async () => {
    render(<ChatTab />);
    await answer.reasoning(levels);
    await screen.findByTestId(PANEL);
    await answer.model(solo);
    fireEvent.click(levelRow('low'));
    await waitFor(() => expect(client.setReasoning).toHaveBeenCalledTimes(1));
    expect(client.setReasoning).toHaveBeenCalledWith('s1', 'low');
    // #405: nothing is closed by a pick; the same table is still there.
    expect(screen.getByTestId(PANEL)).toBeTruthy();
    expect(screen.getAllByRole('menuitemradio')).toHaveLength(3);
  });
});

describe('the settings dialog while the session context is still loading (#236, #405)', () => {
  async function openedOnChatTab() {
    render(<Dialog />);
    const gear = screen.getByTestId(GEAR);
    gear.focus();
    // The levels answer first: the chat model tab is offered from then on.
    await answer.reasoning(levels);
    fireEvent.click(gear);
    const dialog = await screen.findByRole('dialog');
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
    fireEvent.click(await screen.findByTestId(CHAT_TAB));
    await screen.findByTestId(PANEL);
    return { gear, dialog };
  }

  it('stays open on the same gear, tab and focus when the model name arrives after a click (single model)', async () => {
    const { gear, dialog } = await openedOnChatTab();
    const row = levelRow('low');
    row.focus();

    await answer.model(solo);
    expect(screen.getByRole('dialog')).toBe(dialog);
    expect(screen.getByTestId(GEAR)).toBe(gear);
    expect(gear.getAttribute('aria-expanded')).toBe('true');
    expect(screen.getByTestId(CHAT_TAB).getAttribute('aria-selected')).toBe('true');
    expect(levelRow('low')).toBe(row);
    expect(document.activeElement).toBe(row);
  });

  it('stays open on the same tab and focus when the model list arrives and turns the tab into two columns (two models)', async () => {
    const { gear, dialog } = await openedOnChatTab();
    const row = levelRow('low');
    row.focus();

    await answer.model(two);
    expect(screen.getByRole('dialog')).toBe(dialog);
    expect(screen.getByTestId(GEAR)).toBe(gear);
    expect(screen.getByTestId(CHAT_TAB).getAttribute('aria-selected')).toBe('true');
    expect(screen.getByTestId(PANEL).getAttribute('data-layout')).toBe('columns');
    expect(levelRow('low')).toBe(row);
    expect(document.activeElement).toBe(row);
  });

  it('closes on Escape and hands the focus back to the gear', async () => {
    const { gear, dialog } = await openedOnChatTab();
    await answer.model(solo);
    expect(screen.getByRole('dialog')).toBe(dialog);
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(gear));
    expect(gear.getAttribute('aria-expanded')).toBe('false');
  });

  it('opens again from the gear as often as it is asked, on the tab it was left on', async () => {
    const { gear, dialog } = await openedOnChatTab();
    await answer.model(solo);
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    for (let round = 0; round < 3; round += 1) {
      fireEvent.click(gear);
      const again = await screen.findByRole('dialog');
      await waitFor(() => expect(again.contains(document.activeElement)).toBe(true));
      expect(gear.getAttribute('aria-expanded')).toBe('true');
      expect(screen.getByTestId(CHAT_TAB).getAttribute('aria-selected')).toBe('true');
      expect(screen.getByTestId(PANEL)).toBeTruthy();
      fireEvent.keyDown(again, { key: 'Escape' });
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    }
  });
});
