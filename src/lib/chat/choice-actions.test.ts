import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { HarnessBrowserError } from '@/lib/harness';
import { ChoiceActionError, choiceActionFailureKey, originalWordsBefore, runChoiceAnswer, type ChoiceActionReason } from './choice-actions';

const messages = defaultCatalog.application.content.messages as Record<string, string>;
const english = defaultCatalog.application.localization.bundles.en.messages as Record<string, string>;

// #374: an option with an action saves the function selection first; the answer goes out only after it is saved.
describe('runChoiceAnswer', () => {
  const clear = { type: 'clear_operation' } as const;
  const run = (patch: Partial<Parameters<typeof runChoiceAnswer>[1]> = {}) => ({
    apply: vi.fn(async () => undefined),
    stillHere: vi.fn(() => true),
    send: vi.fn(async () => undefined),
    ...patch,
  });

  it('saves the selection first and sends the answer after it', async () => {
    const order: string[] = [];
    const steps = run({
      apply: vi.fn(async () => { await Promise.resolve(); order.push('save'); }),
      send: vi.fn(async () => { order.push('send'); }),
    });
    expect(await runChoiceAnswer(clear, steps)).toEqual({ status: 'sent' });
    expect(order).toEqual(['save', 'send']);
    expect(steps.apply).toHaveBeenCalledWith(clear);
  });

  it('sends nothing when the selection could not be saved, and says why', async () => {
    const steps = run({ apply: vi.fn(async () => { throw new HarnessBrowserError('stale', 409); }) });
    expect(await runChoiceAnswer(clear, steps)).toEqual({ status: 'action_failed', failureKey: 'ui.chat.choice.action_conflict' });
    expect(steps.send).not.toHaveBeenCalled();
    const refused = run({ apply: vi.fn(async () => { throw new ChoiceActionError('service_choose'); }) });
    expect(await runChoiceAnswer({ type: 'switch_operation', operationId: 'retexture' }, refused))
      .toEqual({ status: 'action_failed', failureKey: 'ui.chat.choice.action_service_choose' });
    expect(refused.send).not.toHaveBeenCalled();
  });

  it('sends an option without an action at once, saving nothing', async () => {
    const steps = run();
    expect(await runChoiceAnswer(undefined, steps)).toEqual({ status: 'sent' });
    expect(steps.apply).not.toHaveBeenCalled();
    expect(steps.stillHere).not.toHaveBeenCalled();
    expect(steps.send).toHaveBeenCalledTimes(1);
  });

  it('sends nothing into another session when the person left while the selection was saved', async () => {
    const steps = run({ stillHere: vi.fn(() => false) });
    expect(await runChoiceAnswer(clear, steps)).toEqual({ status: 'left' });
    expect(steps.apply).toHaveBeenCalledTimes(1);
    expect(steps.send).not.toHaveBeenCalled();
  });

  // #709: continue_on_canvas resends the person's previous message as a canvas turn, once, instead of the answer words.
  it('hands what the action prepared to the one send', async () => {
    const canvas = { type: 'continue_on_canvas' } as const;
    const steps = run({ apply: vi.fn(async () => ({ canvasTurnText: '把这个角色做成三视图' })) });
    expect(await runChoiceAnswer(canvas, steps)).toEqual({ status: 'sent' });
    expect(steps.send).toHaveBeenCalledTimes(1);
    expect(steps.send).toHaveBeenCalledWith({ canvasTurnText: '把这个角色做成三视图' });
    const refused = run({ apply: vi.fn(async () => { throw new ChoiceActionError('original_missing'); }) });
    expect(await runChoiceAnswer(canvas, refused)).toEqual({ status: 'action_failed', failureKey: 'ui.chat.choice.action_original_missing' });
    expect(refused.send).not.toHaveBeenCalled();
  });

  it('lets an error of the send itself reach the caller, which already words it', async () => {
    const boom = new Error('network');
    await expect(runChoiceAnswer(undefined, run({ send: vi.fn(async () => { throw boom; }) }))).rejects.toBe(boom);
  });
});

describe('choiceActionFailureKey', () => {
  it('gives each reason its own Schema words, in both languages', () => {
    const reasons: ChoiceActionReason[] = ['failed', 'conflict', 'unavailable', 'service_choose', 'service_none'];
    const keys = reasons.map((reason) => choiceActionFailureKey(new ChoiceActionError(reason)));
    expect(new Set(keys).size).toBe(reasons.length);
    for (const key of keys) {
      expect(messages[key], key).toBeTruthy();
      expect(english[key], key).toBeTruthy();
    }
  });

  it('reads a revision conflict of the save as its own case and any other failure as a plain one', () => {
    expect(choiceActionFailureKey(new HarnessBrowserError('stale', 409))).toBe('ui.chat.choice.action_conflict');
    expect(choiceActionFailureKey(new HarnessBrowserError('bad', 400))).toBe('ui.chat.choice.action_failed');
    expect(choiceActionFailureKey(new HarnessBrowserError('Harness session is not loaded', 0))).toBe('ui.chat.choice.action_failed');
    expect(choiceActionFailureKey(new Error('network'))).toBe('ui.chat.choice.action_failed');
    expect(choiceActionFailureKey('nope')).toBe('ui.chat.choice.action_failed');
  });
});

// #709: the words continue_on_canvas sends again are the person's own message the card's reply answered.
describe('originalWordsBefore', () => {
  const messagesOf = (...items: Array<[string, 'user' | 'assistant', string]>) => items.map(([id, role, content]) => ({ id, role, content }));

  it('is the user message right before the reply that opened the card', () => {
    const list = messagesOf(['u1', 'user', '先画一张'], ['a1', 'assistant', '好'], ['u2', 'user', '  开始干活吧。 '], ['a2', 'assistant', '我看到画板上有……']);
    expect(originalWordsBefore(list, 'a2')).toBe('开始干活吧。');
    expect(originalWordsBefore(list, 'a1')).toBe('先画一张');
  });

  it('is empty when the reply is not loaded or no words came before it', () => {
    expect(originalWordsBefore(messagesOf(['a1', 'assistant', 'hi']), 'a1')).toBe('');
    expect(originalWordsBefore(messagesOf(['u1', 'user', 'x'], ['a1', 'assistant', 'hi']), 'gone')).toBe('');
    expect(originalWordsBefore(messagesOf(['u1', 'user', '   '], ['a1', 'assistant', 'hi']), 'a1')).toBe('');
  });
});
