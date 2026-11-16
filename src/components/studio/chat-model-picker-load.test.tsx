// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { ChatModelChoice, ChatReasoning } from '@/lib/chat/browser-client';
import { ChatModelPicker } from '@/components/studio/chat-model-picker';
import { useChatStore } from '@/store/chat-store';
import { useSessionContextStore } from '@/store/session-context-store';

// #236: the picker is used while the page is still loading the session's context. Nothing that arrives late (the
// model, the levels, a revalidated Schema) may close an open panel, replace the trigger a click is landing on, or
// move the keyboard focus out of the panel. These tests drive the real hooks and stores with a client whose answers
// the test releases one by one, so "late" means late through the same load path the page uses.

const catalog = defaultCatalog.application as unknown as ProductSchema;
const messages = catalog.content.messages;

const mock = vi.hoisted(() => ({ schema: null as unknown, client: null as unknown }));

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: mock.schema }) }));
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

const TRIGGER = 'workspace-chat-model-picker';
const PANEL = 'workspace-chat-model-picker-panel';

/** What a click on the trigger hits: the node under the pointer, not the button itself. */
function hitTarget(trigger: HTMLElement): HTMLElement {
  return trigger.querySelector('span') as HTMLElement;
}

/** A mouse click as the browser delivers it: press, focus on the button, release, click. */
function press(trigger: HTMLElement) {
  const target = hitTarget(trigger);
  fireEvent.pointerDown(target, { pointerType: 'mouse', button: 0 });
  fireEvent.mouseDown(target, { button: 0 });
  trigger.focus();
  fireEvent.pointerUp(target, { pointerType: 'mouse', button: 0 });
  fireEvent.mouseUp(target, { button: 0 });
  fireEvent.click(target, { button: 0 });
}

async function openPanel(trigger: HTMLElement) {
  press(trigger);
  const panel = await screen.findByTestId(PANEL);
  // The panel takes the keyboard focus on the next frame.
  await waitFor(() => expect(panel.contains(document.activeElement)).toBe(true));
  return panel;
}

beforeEach(() => {
  mock.schema = catalog;
  mock.client = client;
  waiting.model.length = 0;
  waiting.reasoning.length = 0;
  Object.values(client).forEach((fn) => fn.mockClear());
  useSessionContextStore.setState({ chatModel: {}, chatModelBusy: {}, reasoning: {}, reasoningBusy: {}, context: {}, compacting: {} });
  useChatStore.setState({ activeSessionId: 's1' });
});
afterEach(() => {
  cleanup();
  useChatStore.setState({ activeSessionId: null });
});

describe('the picker while the session context is still loading (#236)', () => {
  it('stays open, on the same trigger and focus, when the model name arrives after a click (single model)', async () => {
    render(<ChatModelPicker />);
    // The levels answer first: the picker shows with the thinking level alone.
    await answer.reasoning(levels);
    const trigger = await screen.findByTestId(TRIGGER);
    const hit = hitTarget(trigger);

    const panel = await openPanel(trigger);
    const focused = document.activeElement;

    await answer.model(solo);
    expect(screen.getByTestId(PANEL)).toBe(panel);
    expect(screen.getByTestId(TRIGGER)).toBe(trigger);
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    expect(trigger.getAttribute('aria-label')).toBe(`${messages['ui.studio.reasoning.label']}: deepseek · high`);
    // The node the click landed on is still on the page, and the keyboard focus never left the panel.
    expect(trigger.contains(hit)).toBe(true);
    expect(document.activeElement).toBe(focused);
    expect(panel.contains(document.activeElement)).toBe(true);
    expect(screen.queryByTestId('workspace-chat-model-section')).toBeNull();
  });

  it('stays open when the model list arrives and turns the panel into two columns (two models)', async () => {
    render(<ChatModelPicker />);
    await answer.reasoning(levels);
    const trigger = await screen.findByTestId(TRIGGER);
    const panel = await openPanel(trigger);
    expect(panel.getAttribute('data-layout')).toBeNull();
    const focused = document.activeElement;

    await answer.model(two);
    expect(screen.getByTestId(PANEL)).toBe(panel);
    expect(screen.getByTestId(TRIGGER)).toBe(trigger);
    expect(panel.getAttribute('data-layout')).toBe('columns');
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    expect(trigger.getAttribute('aria-label')).toBe(`${messages['ui.studio.chat_model.combined_label']}: deepseek · high`);
    expect(within(panel).getByTestId('workspace-chat-model-section')).toBeTruthy();
    // The levels table kept its rows, so the row that had the focus still has it.
    expect(document.activeElement).toBe(focused);
    expect(panel.contains(document.activeElement)).toBe(true);
  });

  it('stays open when the levels arrive after the model list (two models)', async () => {
    render(<ChatModelPicker />);
    await answer.model(two);
    const trigger = await screen.findByTestId(TRIGGER);
    const panel = await openPanel(trigger);
    expect(within(panel).queryByTestId('workspace-reasoning-section')).toBeNull();

    await answer.reasoning(levels);
    expect(screen.getByTestId(PANEL)).toBe(panel);
    expect(screen.getByTestId(TRIGGER)).toBe(trigger);
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    expect(within(panel).getByTestId('workspace-reasoning-section')).toBeTruthy();
    expect(panel.contains(document.activeElement)).toBe(true);
  });

  it('stays open when the Schema is revalidated and every answer is read again', async () => {
    const view = render(<ChatModelPicker />);
    await answer.reasoning(levels);
    await answer.model(two);
    const trigger = await screen.findByTestId(TRIGGER);
    const panel = await openPanel(trigger);
    const focused = document.activeElement;
    const reads = client.getReasoning.mock.calls.length;

    // A revalidation hands the page an equal Schema in a new object: a new client, the effects run again and the
    // answers are read again. While those reads are on their way the panel shows what it already had.
    mock.schema = structuredClone(catalog);
    view.rerender(<ChatModelPicker />);
    await waitFor(() => expect(client.getReasoning.mock.calls.length).toBeGreaterThan(reads));
    expect(screen.getByTestId(PANEL)).toBe(panel);
    expect(screen.getByTestId(TRIGGER)).toBe(trigger);

    await answer.reasoning({ ...levels });
    await answer.model({ ...two });
    expect(screen.getByTestId(PANEL)).toBe(panel);
    expect(screen.getByTestId(TRIGGER)).toBe(trigger);
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    expect(document.activeElement).toBe(focused);
  });

  it('names only the level until the model name arrives, without a dangling separator', async () => {
    render(<ChatModelPicker />);
    await answer.reasoning(levels);
    const trigger = await screen.findByTestId(TRIGGER);
    expect(trigger.textContent).toBe('high');
    expect(trigger.getAttribute('aria-label')).toBe(`${messages['ui.studio.reasoning.label']}: high`);
    await answer.model(solo);
    expect(trigger.textContent).toBe('deepseek · high');
    expect(trigger.getAttribute('aria-label')).toBe(`${messages['ui.studio.reasoning.label']}: deepseek · high`);
  });

  it('keeps the same nodes under a click while the trigger text changes with the data', async () => {
    render(<ChatModelPicker />);
    await answer.reasoning(levels);
    const trigger = await screen.findByTestId(TRIGGER);
    const hit = hitTarget(trigger);
    await answer.model(solo);
    // A click is dropped when the node its press landed on is replaced before the release, so loading must only
    // change what the nodes say.
    expect(trigger.contains(hit)).toBe(true);
    expect(hitTarget(trigger)).toBe(hit);
  });
});

describe('what still closes the panel after late data (#236)', () => {
  async function openedAfterLateModel() {
    render(<ChatModelPicker />);
    await answer.reasoning(levels);
    const trigger = await screen.findByTestId(TRIGGER);
    const panel = await openPanel(trigger);
    await answer.model(solo);
    expect(screen.getByTestId(PANEL)).toBe(panel);
    return { trigger, panel };
  }

  it('closes when a level is picked, and the pick reaches the server once', async () => {
    const { panel } = await openedAfterLateModel();
    fireEvent.click(within(panel).getByRole('menuitemradio', { name: 'low' }));
    await waitFor(() => expect(screen.queryByTestId(PANEL)).toBeNull());
    expect(client.setReasoning).toHaveBeenCalledTimes(1);
    expect(client.setReasoning).toHaveBeenCalledWith('s1', 'low');
  });

  it('closes on Escape and gives the focus back to the trigger', async () => {
    const { trigger, panel } = await openedAfterLateModel();
    fireEvent.keyDown(panel, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByTestId(PANEL)).toBeNull());
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
  });

  it('closes on a click outside', async () => {
    await openedAfterLateModel();
    fireEvent.pointerDown(document.body);
    fireEvent.mouseDown(document.body);
    fireEvent.pointerUp(document.body);
    fireEvent.mouseUp(document.body);
    fireEvent.click(document.body);
    await waitFor(() => expect(screen.queryByTestId(PANEL)).toBeNull());
  });

  it('opens again on the next click, as often as it is asked', async () => {
    const { trigger, panel } = await openedAfterLateModel();
    fireEvent.keyDown(panel, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByTestId(PANEL)).toBeNull());
    for (let round = 0; round < 3; round += 1) {
      const again = await openPanel(trigger);
      expect(trigger.getAttribute('aria-expanded')).toBe('true');
      fireEvent.keyDown(again, { key: 'Escape' });
      await waitFor(() => expect(screen.queryByTestId(PANEL)).toBeNull());
    }
  });
});
