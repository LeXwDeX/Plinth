// @vitest-environment jsdom
import React, { useState } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ChoiceCard } from '@/components/studio/choice-card';
import { applyChoiceAction } from '@/components/workspace/harness-choice-action';
import { runChoiceAnswer } from '@/lib/chat/choice-actions';
import { choiceSelectionAction, parseChoiceCards, type ChoiceCardModel, type ChoiceSelection } from '@/lib/chat/choice-cards';
import type { OfferingWire } from '@/lib/agent-offerings';
import { HarnessBrowserError, type HarnessSelection, type HarnessSelectionInput } from '@/lib/harness';
import { schemaText, type ProductSchema } from '@/lib/product-schema';

// #374: clicking an option of a direction card. The option's action (a function selection saved through the session
// Harness) runs before the answer goes out; a selection that cannot be saved sends nothing and leaves the card open.
// `Host` is the ChatColumn's own order of steps (chat-choice-wiring.test.ts pins that wiring) around the real card.

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: defaultCatalog.application }) }));
vi.mock('swr', () => ({ default: () => ({ data: undefined, isLoading: false }) }));

const saved: HarnessSelection = {
  session_id: 's1', revision: 7, digest: 'd7', operation_id: 'remesh', offering_id: 'tripo/decimate', offering_revision: 'r1',
  enabled_operations: ['retexture', 'remesh'], sources: [], fixed_parameters: {}, fillable_parameters: [], allow_prerequisites: true, updated_at: '2026-11-19T00:00:00Z',
};
const texture = {
  id: 'meshy/texture', revision: 'r7', provider_id: 'meshy', provider_name: 'Meshy', model_id: 'meshy-processing', model_name: 'Texture',
  capability: 'retexture', available: true, parameters: [],
} as unknown as OfferingWire;

const card = parseChoiceCards({ choices: [{
  id: 'choice_next', assistant_message_id: 'a1', tool: '', kind: 'next_step', capability: 'next_step', question: '接下来想怎么做？',
  options: [
    { id: 'keep', label: '保持当前功能' },
    { id: 'clear_and_draw', label: '清空功能选择，先出图', recommended: true, action: { type: 'clear_operation' } },
    { id: 'switch_retexture', label: '改做换贴图', action: { type: 'switch_operation', operation_id: 'retexture' } },
  ],
  default_option_id: '', allow_default: false, state: 'pending', created_at: '2026-11-19T00:00:00Z',
}] })[0];

function Host({ model, save, send, offerings = async () => [texture] }: {
  model: ChoiceCardModel;
  save: (selection: HarnessSelectionInput) => Promise<unknown>;
  send: (selection: ChoiceSelection, text: string) => Promise<void>;
  offerings?: (capability: string) => Promise<OfferingWire[]>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const answer = async (selection: ChoiceSelection, text: string) => {
    setError(null);
    setBusy(true);
    try {
      const outcome = await runChoiceAnswer(choiceSelectionAction(model, selection), {
        apply: (action) => applyChoiceAction(action, { schema, harness: { selection: saved, save }, offerings }),
        stillHere: () => true,
        send: () => send(selection, text),
      });
      if (outcome.status === 'action_failed') setError(schemaText(schema, outcome.failureKey));
    } finally {
      setBusy(false);
    }
  };
  return <ChoiceCard card={model} busy={busy} disabled={false} error={error} onAnswer={(selection, text) => { void answer(selection, text); }} />;
}

const pick = (optionId: string) => fireEvent.click(document.querySelector(`[data-testid="chat-choice-option"][data-option-id="${optionId}"]`)!);
const confirm = () => fireEvent.click(screen.getByTestId('chat-choice-confirm'));
const order = (...mocks: Array<{ mock: { invocationCallOrder: number[] } }>) => mocks.map((mock) => mock.mock.invocationCallOrder[0]);

afterEach(cleanup);

describe('picking an option of a direction card (#374)', () => {
  it('saves the cleared function selection with the loaded revision before the answer is sent', async () => {
    const save = vi.fn(async (_selection: HarnessSelectionInput) => undefined);
    const send = vi.fn(async (_selection: ChoiceSelection, _text: string) => undefined);
    render(<Host model={card} save={save} send={send} />);
    pick('clear_and_draw');
    confirm();
    await waitFor(() => expect(send).toHaveBeenCalledTimes(1));
    expect(save).toHaveBeenCalledTimes(1);
    expect(save.mock.calls[0][0]).toMatchObject({ session_id: 's1', revision: 7, operation_id: undefined, offering_id: undefined, enabled_operations: ['retexture', 'remesh'] });
    // The save comes first; the answer is the card's plain "选择：<标题>" message with this option.
    const [saveAt, sendAt] = order(save, send);
    expect(saveAt).toBeLessThan(sendAt);
    expect(send.mock.calls[0][0]).toEqual({ optionId: 'clear_and_draw' });
    expect(send.mock.calls[0][1]).toBe(messages['ui.chat.choice.answer_text'].replace('{title}', '清空功能选择，先出图'));
    expect(screen.queryByTestId('chat-choice-error')).toBeNull();
  });

  it('saves a function switch with its service before the answer is sent', async () => {
    const save = vi.fn(async (_selection: HarnessSelectionInput) => undefined);
    const send = vi.fn(async (_selection: ChoiceSelection, _text: string) => undefined);
    render(<Host model={card} save={save} send={send} />);
    pick('switch_retexture');
    confirm();
    await waitFor(() => expect(send).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0][0]).toMatchObject({ operation_id: 'retexture', offering_id: 'meshy/texture', offering_revision: 'r7' });
    const [saveAt, sendAt] = order(save, send);
    expect(saveAt).toBeLessThan(sendAt);
  });

  it('answers an option without an action at once and saves nothing', async () => {
    const save = vi.fn(async (_selection: HarnessSelectionInput) => undefined);
    const send = vi.fn(async (_selection: ChoiceSelection, _text: string) => undefined);
    render(<Host model={card} save={save} send={send} />);
    // The first option is preselected: confirming it is the "keep" answer.
    confirm();
    await waitFor(() => expect(send).toHaveBeenCalledTimes(1));
    expect(send.mock.calls[0][0]).toEqual({ optionId: 'keep' });
    expect(save).not.toHaveBeenCalled();
  });

  it('sends no answer when the selection cannot be saved, says so on the card and lets the person try again', async () => {
    let conflict = true;
    const save = vi.fn(async (_selection: HarnessSelectionInput) => {
      if (conflict) throw new HarnessBrowserError('error.harness_conflict', 409);
      return undefined;
    });
    const send = vi.fn(async (_selection: ChoiceSelection, _text: string) => undefined);
    render(<Host model={card} save={save} send={send} />);
    pick('clear_and_draw');
    confirm();
    const alert = await screen.findByTestId('chat-choice-error');
    // The revision conflict has its own words, in the existing error line of the card.
    expect(alert.getAttribute('role')).toBe('alert');
    expect(alert.textContent).toBe(messages['ui.chat.choice.action_conflict']);
    expect(save).toHaveBeenCalledTimes(1);
    expect(send).not.toHaveBeenCalled();
    // The card stays answerable: the options and the confirm button are not locked.
    const button = screen.getByTestId('chat-choice-confirm') as HTMLButtonElement;
    expect(button.disabled).toBe(false);
    expect(document.querySelector('[role="radiogroup"]')!.getAttribute('aria-disabled')).toBeNull();
    // The next attempt clears the line and, with the selection saved this time, sends the answer.
    conflict = false;
    await act(async () => { fireEvent.click(button); });
    await waitFor(() => expect(send).toHaveBeenCalledTimes(1));
    expect(save).toHaveBeenCalledTimes(2);
    expect(screen.queryByTestId('chat-choice-error')).toBeNull();
  });

  it('names the reason when the function has no single service to adopt, and sends nothing', async () => {
    const save = vi.fn(async (_selection: HarnessSelectionInput) => undefined);
    const send = vi.fn(async (_selection: ChoiceSelection, _text: string) => undefined);
    const two = [texture, { ...texture, id: 'tripo/texture', revision: 'r2', model_id: 'tripo-texture' } as OfferingWire];
    render(<Host model={card} save={save} send={send} offerings={async () => two} />);
    pick('switch_retexture');
    confirm();
    const alert = await screen.findByTestId('chat-choice-error');
    expect(alert.textContent).toBe(messages['ui.chat.choice.action_service_choose']);
    expect(save).not.toHaveBeenCalled();
    expect(send).not.toHaveBeenCalled();
  });
});
