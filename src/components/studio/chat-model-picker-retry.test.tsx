// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { formatSchemaText } from '@/lib/product-schema';
import { ChatBrowserError, type ChatModelChoice, type ChatReasoning } from '@/lib/chat/browser-client';
import { ChatModelPicker, useChatModelSettings } from '@/components/studio/chat-model-picker';
import { useChatStore } from '@/store/chat-store';
import { useSessionContextStore } from '@/store/session-context-store';

// #314: the first read of a session's chat model or thinking level can fail (a rate limit, a network blip). The picker
// must not vanish for good: it reads again a bounded number of times, again when the window regains focus or the network
// comes back, says so while it has nothing to show, and lets the person retry. #405: the picker is a tab of the settings
// dialog now; ChatTab is that tab as the dialog builds it (the settings always read, the content shown once there is any),
// and `value` (what the trigger used to say) is what the gear's marker shows for the chat model. These tests drive the real hooks and
// stores with a client whose answers each test scripts; the Schema's retry waits are shortened to milliseconds, and
// every state a test looks at in between is held by a read the test has not answered yet, not by timing.

const catalog = defaultCatalog.application as unknown as ProductSchema;
const messages = catalog.content.messages;
const RETRIES = 3;

function schemaWithFastRetries(): ProductSchema {
  const schema = structuredClone(catalog);
  schema.runtime.frontend.session_read_retry = {
    max_retries: RETRIES,
    initial_delay_milliseconds: 20,
    max_backoff_milliseconds: 40,
    max_retry_after_milliseconds: 400,
  };
  return schema;
}

const mock = vi.hoisted(() => ({ schema: null as unknown, client: null as unknown }));

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: mock.schema }) }));
vi.mock('@/lib/chat/browser-client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/chat/browser-client')>();
  return { ...actual, createChatBrowserClient: () => ({ ...(mock.client as object) }) };
});

const solo: ChatModelChoice = {
  selected: 'LLM', chosen: '', defaultId: 'LLM', fallback: false, options: [{ id: 'LLM', model: 'deepseek' }],
};
const two: ChatModelChoice = {
  selected: 'LLM', chosen: '', defaultId: 'LLM', fallback: false,
  options: [{ id: 'LLM', model: 'deepseek' }, { id: 'GLM', model: 'glm-5.3' }],
};
const levels: ChatReasoning = { effort: 'high', levels: ['low', 'high', 'max'], defaultLevel: 'high' };
const limited = (retryAfterSeconds?: number) => new ChatBrowserError('', 429, '', retryAfterSeconds);

const client = {
  getChatModel: vi.fn(async (_session: string, _signal?: AbortSignal): Promise<ChatModelChoice> => solo),
  getReasoning: vi.fn(async (_session: string, _signal?: AbortSignal): Promise<ChatReasoning> => levels),
  getContext: vi.fn(() => new Promise<never>(() => undefined)),
  setChatModel: vi.fn(async (_session: string, model: string) => ({ ...two, selected: model, chosen: model })),
  setReasoning: vi.fn(async (_session: string, level: string) => ({ ...levels, effort: level })),
};
type Read = typeof client.getChatModel | typeof client.getReasoning;

const PANEL = 'workspace-chat-model-picker-panel';
const MODEL_RETRY = 'workspace-chat-model-retry';
const LEVELS_RETRY = 'workspace-reasoning-retry';

const unavailable = (reasonKey: string) => formatSchemaText(messages['ui.unavailable.label'], { reason: messages[reasonKey] });

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

/** A read the test answers later. */
function later<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

const calls = (read: Read, session?: string) => read.mock.calls.filter(([id]) => session === undefined || id === session).length;
const wait = (milliseconds: number) => act(async () => { await new Promise((resolve) => setTimeout(resolve, milliseconds)); });

/** Waits for a read to be asked `expected` times, then long past the whole schedule: no more come. */
async function expectBound(read: Read, expected: number, session?: string) {
  await waitFor(() => expect(calls(read, session)).toBe(expected));
  await wait(300);
  expect(calls(read, session)).toBe(expected);
}

/** The content is there as soon as there is anything to show; nothing to open. */
const openPanel = () => screen.findByTestId(PANEL);

beforeEach(() => {
  mock.schema = schemaWithFastRetries();
  mock.client = client;
  client.getChatModel.mockReset();
  client.getReasoning.mockReset();
  client.getChatModel.mockImplementation(async () => solo);
  client.getReasoning.mockImplementation(async () => levels);
  useSessionContextStore.setState({
    chatModel: {}, chatModelBusy: {}, chatModelFailed: {}, reasoning: {}, reasoningBusy: {}, reasoningFailed: {}, context: {}, compacting: {},
  });
  useChatStore.setState({ activeSessionId: 's1' });
});
afterEach(() => {
  cleanup();
  useChatStore.setState({ activeSessionId: null });
});

describe('a first read that fails is read again until the picker shows (#314)', () => {
  it('says the thinking level could not be read, then shows it when the retry succeeds after a rate limit', async () => {
    const retry = later<ChatReasoning>();
    client.getReasoning.mockRejectedValueOnce(limited()).mockImplementationOnce(() => retry.promise);
    render(<ChatTab />);
    // The failed read leaves the picker saying so; it does not stay away.
    await screen.findByTestId(PANEL);
    await waitFor(() => expect(calls(client.getReasoning)).toBe(2));
    expect(value()).toBe('deepseek');
    expect(useSessionContextStore.getState().reasoningFailed.s1).toBe(true);

    await act(async () => { retry.resolve(levels); });
    expect(value()).toBe('deepseek · high');
    expect(useSessionContextStore.getState().reasoningFailed.s1).toBeUndefined();
    await wait(100);
    expect(calls(client.getReasoning)).toBe(2);
  });

  it('shows the whole picker when both reads fail first and the retries find the server again', async () => {
    const models = later<ChatModelChoice>();
    const thinking = later<ChatReasoning>();
    client.getChatModel.mockRejectedValueOnce(new TypeError('Failed to fetch')).mockImplementationOnce(() => models.promise);
    client.getReasoning.mockRejectedValueOnce(limited()).mockImplementationOnce(() => thinking.promise);
    render(<ChatTab />);
    await screen.findByTestId(PANEL);
    await waitFor(() => expect(calls(client.getChatModel)).toBe(2));
    await waitFor(() => expect(calls(client.getReasoning)).toBe(2));
    expect(value()).toBe(messages['ui.studio.chat_model.load_failed_short']);

    await act(async () => { models.resolve(two); thinking.resolve(levels); });
    expect(value()).toBe('deepseek · high');
  });

  it('waits for the Retry-After the server sent instead of using its retries up inside the limit', async () => {
    // The backoff alone would read again after 20 ms; the server asked for 0.3 s (the Schema here caps a wait at 0.4 s).
    client.getReasoning.mockRejectedValueOnce(limited(0.3));
    render(<ChatTab />);
    await screen.findByTestId(PANEL);
    await wait(120);
    expect(calls(client.getReasoning)).toBe(1);
    await waitFor(() => expect(calls(client.getReasoning)).toBe(2), { timeout: 2000 });
    await waitFor(() => expect(value()).toBe('deepseek · high'));
  });
});

describe('a read that keeps failing asks a bounded number of times (#314)', () => {
  it('stops at the Schema bound while the picker keeps saying the read failed', async () => {
    client.getChatModel.mockRejectedValue(limited());
    client.getReasoning.mockRejectedValue(limited());
    render(<ChatTab />);
    await expectBound(client.getChatModel, 1 + RETRIES);
    await expectBound(client.getReasoning, 1 + RETRIES);
    expect(value()).toBe(messages['ui.studio.chat_model.load_failed_short']);
  });

  it('reads again when the window regains focus or the network comes back, and stops again at the bound', async () => {
    client.getReasoning.mockRejectedValue(limited());
    render(<ChatTab />);
    await expectBound(client.getReasoning, 1 + RETRIES);

    await act(async () => { window.dispatchEvent(new Event('focus')); });
    expect(calls(client.getReasoning)).toBe(2 + RETRIES);
    // The woken read starts a fresh cycle, and that one is bounded too.
    await expectBound(client.getReasoning, 2 + 2 * RETRIES);

    client.getReasoning.mockImplementation(async () => levels);
    await act(async () => { window.dispatchEvent(new Event('online')); });
    await waitFor(() => expect(value()).toBe('deepseek · high'));
    // Nothing failed any more: another focus reads nothing.
    const reads = calls(client.getReasoning);
    await act(async () => { window.dispatchEvent(new Event('focus')); });
    await wait(100);
    expect(calls(client.getReasoning)).toBe(reads);
  });
});

describe('the picker says it could not read, and lets the person retry (#314)', () => {
  it('names the failed model list in its column, and the retry brings the models into the open panel', async () => {
    client.getChatModel.mockRejectedValue(limited());
    render(<ChatTab />);
    await expectBound(client.getChatModel, 1 + RETRIES);
    // The thinking levels arrived; only the model column is unavailable.
    expect(value()).toBe('high');
    const panel = await openPanel();
    const column = within(panel).getByTestId('workspace-chat-model-section');
    expect(within(column).getByRole('alert').textContent).toBe(messages['ui.studio.chat_model.load_failed']);
    expect(within(column).getByTestId('unavailable-label').textContent).toBe(unavailable('ui.unavailable.reason.picker.chat_model_load_failed'));
    expect(within(column).getByTestId(MODEL_RETRY).textContent).toBe(messages['ui.studio.chat_model.retry']);
    expect(within(panel).getByTestId('workspace-reasoning-section')).toBeTruthy();
    expect(within(panel).queryByTestId(LEVELS_RETRY)).toBeNull();

    const retried = later<ChatModelChoice>();
    client.getChatModel.mockImplementationOnce(() => retried.promise);
    fireEvent.click(within(panel).getByTestId(MODEL_RETRY));
    await waitFor(() => expect(calls(client.getChatModel)).toBe(2 + RETRIES));
    // Held by the read on its way: still the failed column.
    expect(within(panel).getByRole('alert')).toBeTruthy();
    await act(async () => { retried.resolve(two); });
    expect(within(panel).getAllByRole('menuitemradio', { name: /deepseek|glm-5.3/ })).toHaveLength(2);
    // The panel stayed and the column turned into the model table.
    expect(screen.getByTestId(PANEL)).toBe(panel);
    expect(within(panel).queryByTestId(MODEL_RETRY)).toBeNull();
    expect(within(panel).queryByRole('alert')).toBeNull();
    expect(value()).toBe('deepseek · high');
  });

  it('names the failed thinking levels beside the model list and retries only them', async () => {
    client.getChatModel.mockImplementation(async () => two);
    client.getReasoning.mockRejectedValue(limited());
    render(<ChatTab />);
    await expectBound(client.getReasoning, 1 + RETRIES);
    expect(value()).toBe('deepseek');
    const panel = await openPanel();
    const column = within(panel).getByTestId('workspace-reasoning-section');
    expect(within(column).getByRole('alert').textContent).toBe(messages['ui.studio.reasoning.load_failed']);
    expect(within(column).getByTestId('unavailable-label').textContent).toBe(unavailable('ui.unavailable.reason.picker.reasoning_load_failed'));
    expect(within(column).getByTestId(LEVELS_RETRY).textContent).toBe(messages['ui.studio.chat_model.retry']);
    expect(within(panel).queryByTestId(MODEL_RETRY)).toBeNull();
    // The column is titled for the model it belongs to, as the levels table is.
    expect(within(column).getByText(formatSchemaText(messages['ui.studio.chat_model.levels_title'], { model: 'deepseek' }))).toBeTruthy();

    const modelReads = calls(client.getChatModel);
    client.getReasoning.mockImplementation(async () => levels);
    fireEvent.click(within(panel).getByTestId(LEVELS_RETRY));
    await waitFor(() => expect(within(panel).getByRole('menuitemradio', { name: /^high/ })).toBeTruthy());
    expect(calls(client.getChatModel)).toBe(modelReads);
    expect(screen.getByTestId(PANEL)).toBe(panel);
  });

  it('offers a retry for each part when both reads failed; the person\'s retry reads a fresh bounded cycle', async () => {
    client.getChatModel.mockRejectedValue(limited());
    client.getReasoning.mockRejectedValue(limited());
    render(<ChatTab />);
    await expectBound(client.getChatModel, 1 + RETRIES);
    await expectBound(client.getReasoning, 1 + RETRIES);
    const panel = await openPanel();
    expect(within(panel).getByTestId(MODEL_RETRY)).toBeTruthy();
    expect(within(panel).getByTestId(LEVELS_RETRY)).toBeTruthy();
    expect(within(panel).getAllByRole('alert').map((alert) => alert.textContent)).toEqual([
      messages['ui.studio.chat_model.load_failed'], messages['ui.studio.reasoning.load_failed'],
    ]);

    fireEvent.click(within(panel).getByTestId(LEVELS_RETRY));
    await expectBound(client.getReasoning, 2 * (1 + RETRIES));
    expect(calls(client.getChatModel)).toBe(1 + RETRIES);
  });

  it('says so in the tab when a read fails late, without replacing the panel or moving the focus', async () => {
    const models = later<ChatModelChoice>();
    client.getChatModel.mockImplementationOnce(() => models.promise);
    render(<ChatTab />);
    // The levels arrived first: the tab shows the level alone while the model list is still on its way.
    const panel = await openPanel();
    within(panel).getAllByRole('menuitemradio')[0].focus();
    const focused = document.activeElement;
    expect(within(panel).queryByTestId('workspace-chat-model-section')).toBeNull();

    // A long Retry-After, so the failed model read stays failed while the test looks at it; the retry finds two models.
    client.getChatModel.mockImplementation(async () => two);
    await act(async () => { models.reject(limited(0.3)); });
    await waitFor(() => expect(within(panel).getByTestId(MODEL_RETRY)).toBeTruthy());
    expect(screen.getByTestId(PANEL)).toBe(panel);
    expect(panel.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).toBe(focused);

    // And when the retry finds the models, the failed column turns into the table in the same panel.
    await waitFor(() => expect(within(panel).getAllByRole('menuitemradio', { name: /deepseek|glm-5.3/ }).length).toBeGreaterThan(0), { timeout: 2000 });
    expect(screen.getByTestId(PANEL)).toBe(panel);
  });

  it('keeps showing what it holds when a later read fails, without an unavailable state', async () => {
    useSessionContextStore.setState({ chatModel: { s1: two }, reasoning: { s1: levels } });
    client.getChatModel.mockRejectedValue(limited());
    client.getReasoning.mockRejectedValue(limited());
    render(<ChatTab />);
    await expectBound(client.getChatModel, 1 + RETRIES);
    await expectBound(client.getReasoning, 1 + RETRIES);
    expect(value()).toBe('deepseek · high');
    const panel = await openPanel();
    expect(within(panel).queryByRole('alert')).toBeNull();
    expect(within(panel).queryByTestId(MODEL_RETRY)).toBeNull();
    expect(within(panel).queryByTestId(LEVELS_RETRY)).toBeNull();
  });
});

describe('a read follows the session it was made for (#314)', () => {
  /** Reads that stay open until the test answers them, each with the signal it was given. */
  function hang() {
    const open: Array<{ session: string; signal: AbortSignal; reject: (error: unknown) => void }> = [];
    client.getReasoning.mockImplementation((session: string, signal?: AbortSignal) => new Promise<ChatReasoning>((_resolve, reject) => {
      open.push({ session, signal: signal!, reject });
    }));
    return open;
  }

  it('cancels the read of the session that was left; its late failure neither shows nor retries', async () => {
    const open = hang();
    render(<ChatTab />);
    await waitFor(() => expect(open).toHaveLength(1));
    expect(open[0].signal.aborted).toBe(false);

    await act(async () => { useChatStore.setState({ activeSessionId: 's2' }); });
    await waitFor(() => expect(open.filter((read) => read.session === 's2')).toHaveLength(1));
    expect(open[0].signal.aborted).toBe(true);
    expect(open[1].signal.aborted).toBe(false);

    // The refusal of the cancelled read arrives anyway (it had been answered as the page moved on).
    await act(async () => { open[0].reject(limited()); });
    await wait(300);
    expect(calls(client.getReasoning, 's1')).toBe(1);
    expect(useSessionContextStore.getState().reasoningFailed.s1).toBeUndefined();
    // The session on the screen is still waiting for its own first answer and shows nothing of s1's failure.
    expect(calls(client.getReasoning, 's2')).toBe(1);
    expect(screen.queryByTestId(PANEL)).toBeNull();
  });

  it('cancels the retry a left session was waiting for', async () => {
    // s1 is rate limited for 0.3 s, so its retry is waiting when the session changes; s2 answers at once.
    client.getReasoning.mockImplementation(async (session: string) => {
      if (session === 's1') throw limited(0.3);
      return levels;
    });
    render(<ChatTab />);
    await waitFor(() => expect(calls(client.getReasoning, 's1')).toBe(1));
    await act(async () => { useChatStore.setState({ activeSessionId: 's2' }); });
    await waitFor(() => expect(calls(client.getReasoning, 's2')).toBe(1));
    await wait(600);
    expect(calls(client.getReasoning, 's1')).toBe(1);
    expect(calls(client.getReasoning, 's2')).toBe(1);

    // Coming back to s1 reads it again at once, as opening a session does.
    await act(async () => { useChatStore.setState({ activeSessionId: 's1' }); });
    await waitFor(() => expect(calls(client.getReasoning, 's1')).toBe(2));
  });

  it('cancels the read and the waiting retry when the picker leaves the page', async () => {
    const open = hang();
    const first = render(<ChatTab />);
    await waitFor(() => expect(open).toHaveLength(1));
    first.unmount();
    expect(open[0].signal.aborted).toBe(true);

    client.getReasoning.mockRejectedValue(limited(0.3));
    const second = render(<ChatTab />);
    await waitFor(() => expect(client.getReasoning).toHaveBeenCalledTimes(2));
    second.unmount();
    await wait(600);
    expect(client.getReasoning).toHaveBeenCalledTimes(2);
    // The listeners went with it: a focus reads nothing.
    await act(async () => { window.dispatchEvent(new Event('focus')); });
    expect(client.getReasoning).toHaveBeenCalledTimes(2);
  });
});
