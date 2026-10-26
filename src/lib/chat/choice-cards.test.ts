import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';
import {
  choiceAnswerText,
  choiceCardPlacement,
  choiceOptionQuote,
  choiceOptionTitle,
  choiceStatusText,
  choicesRoute,
  fetchChoiceCards,
  parseChoiceCards,
  heldChoiceRelease,
  radioKeyTarget,
  type ChoiceCardModel,
} from './choice-cards';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;

const wire = {
  id: 'choice_1', assistant_message_id: 'assistant-1', tool: 'process_model', capability: 'mesh-segmentation',
  options: [
    { id: 'tripo/geometry/smartsegment-model', provider_name: 'Tripo3D', model_name: 'Smart Segmentation', credits_metered: true, estimated_credits: 40 },
    { id: 'hyper3d/bang/local-model', provider_name: 'Hyper3D', model_name: 'Bang', native_quote: { version: 1, unit: 'hyper3d.credits', unit_label: 'Hyper3D credits', base_amount: '0.5', extra_amount: '0', total_amount: '0.5' } },
    { id: 'free/x', provider_name: 'Free', model_name: 'X' },
  ],
  default_option_id: 'tripo/geometry/smartsegment-model', allow_default: true, state: 'pending', created_at: '2026-10-22T00:00:00Z',
};

const card = (patch: Partial<ChoiceCardModel> = {}): ChoiceCardModel => ({ ...parseChoiceCards({ choices: [wire] })[0], ...patch });

describe('choice cards (#321)', () => {
  it('reads the session route from the Schema and parses only well-formed cards', async () => {
    expect(choicesRoute(schema, 'a b')).toBe('/api/v1/chat/sessions/a%20b/choices');
    const parsed = parseChoiceCards({ choices: [wire, { id: '' }, { ...wire, id: 'choice_2', options: [] }, 'x',
      { ...wire, id: 'choice_3', state: 'answered', answer: { option_id: 'hyper3d/bang/local-model', use_default: false, source: 'card', answered_at: 'now' } }] });
    expect(parsed.map((item) => item.id)).toEqual(['choice_1', 'choice_3']);
    expect(parsed[0]).toMatchObject({ assistantMessageId: 'assistant-1', capability: 'mesh-segmentation', defaultOptionId: wire.default_option_id, allowDefault: true, state: 'pending', answer: null });
    expect(parsed[0].options[1].nativeQuote?.total_amount).toBe('0.5');
    expect(parsed[1].answer).toEqual({ optionId: 'hyper3d/bang/local-model', useDefault: false, source: 'card' });
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ choices: [wire] }), { status: 200 }));
    expect((await fetchChoiceCards(schema, 's1', undefined, fetcher as unknown as typeof fetch))[0].id).toBe('choice_1');
    expect(fetcher).toHaveBeenCalledWith('/api/v1/chat/sessions/s1/choices', expect.anything());
  });

  it('names and prices each option from the public Offering list, falling back to the stored card', () => {
    const live = { id: 'hyper3d/bang/local-model', provider_name: 'Hyper3D', model_name: 'Bang (local model)', price_mode: 'native',
      default_native_quote: wire.options[1].native_quote, available: true } as unknown as OfferingWire;
    const [tripo, bang, free] = card().options;
    expect(choiceOptionTitle(bang, [live])).toBe('Hyper3D · Bang (local model)');
    expect(choiceOptionTitle(tripo, [live])).toBe('Tripo3D · Smart Segmentation');
    const estimate = messages['ui.pricing.default_estimate'].replace('{amount}', '0.5 Hyper3D credits');
    expect(choiceOptionQuote(schema, bang, [live])).toBe(estimate);
    expect(choiceOptionQuote(schema, bang, undefined)).toBe(estimate);
    expect(choiceOptionQuote(schema, tripo, undefined)).toBe(`${messages['ui.workspace.harness.credits']}: 40`);
    expect(choiceOptionQuote(schema, free, undefined)).toBe(`${messages['ui.workspace.harness.credits']}: /`);
  });

  it('words the answer the turn sends and the status of a closed card from the Schema', () => {
    const value = card();
    expect(choiceAnswerText(schema, value, { optionId: 'hyper3d/bang/local-model' }))
      .toBe(messages['ui.chat.choice.answer_text'].replace('{title}', 'Hyper3D · Bang'));
    expect(choiceAnswerText(schema, value, { useDefault: true }))
      .toBe(messages['ui.chat.choice.answer_default_text'].replace('{title}', 'Tripo3D · Smart Segmentation'));
    expect(choiceStatusText(schema, value)).toBe('');
    expect(choiceStatusText(schema, card({ state: 'answered', answer: { optionId: 'free/x', useDefault: false, source: 'card' } })))
      .toBe(messages['ui.chat.choice.answered'].replace('{title}', 'Free · X'));
    expect(choiceStatusText(schema, card({ state: 'answered', answer: { optionId: wire.default_option_id, useDefault: true, source: 'card' } })))
      .toBe(messages['ui.chat.choice.answered_default'].replace('{title}', 'Tripo3D · Smart Segmentation'));
    expect(choiceStatusText(schema, card({ state: 'superseded' }))).toBe(messages['ui.chat.choice.superseded']);
  });

  it('moves through the options with the arrow keys, Home and End, wrapping at the ends', () => {
    expect(radioKeyTarget('ArrowDown', 0, 3)).toBe(1);
    expect(radioKeyTarget('ArrowRight', 2, 3)).toBe(0);
    expect(radioKeyTarget('ArrowUp', 0, 3)).toBe(2);
    expect(radioKeyTarget('ArrowLeft', 1, 3)).toBe(0);
    expect(radioKeyTarget('Home', 2, 3)).toBe(0);
    expect(radioKeyTarget('End', 0, 3)).toBe(2);
    expect(radioKeyTarget('a', 0, 3)).toBeNull();
  });

  it('places each card after its assistant message, and an open card whose message is not shown at the end', () => {
    const shown = card();
    const elsewhere = card({ id: 'choice_2', assistantMessageId: 'not-loaded' });
    const closedElsewhere = card({ id: 'choice_3', assistantMessageId: 'not-loaded', state: 'superseded' });
    const placement = choiceCardPlacement([shown, elsewhere, closedElsewhere], ['user-1', 'assistant-1']);
    expect(placement.byMessage.get('assistant-1')?.map((item) => item.id)).toEqual(['choice_1']);
    expect(placement.loose.map((item) => item.id)).toEqual(['choice_2']);
  });
});

// #335: an answer confirmed while the composer's pictures are still being read is held, then sent.
describe('heldChoiceRelease', () => {
  const ready = { sameScope: true, cardOpen: true, attachmentsBusy: false, streaming: false };
  it('sends the held answer once the pictures are read and no turn is running', () => {
    expect(heldChoiceRelease(ready)).toBe('send');
  });
  it('keeps waiting while pictures are read or uploaded, or a turn is running', () => {
    expect(heldChoiceRelease({ ...ready, attachmentsBusy: true })).toBe('wait');
    expect(heldChoiceRelease({ ...ready, streaming: true })).toBe('wait');
  });
  it('drops the answer when the person left the session or the card is no longer open', () => {
    expect(heldChoiceRelease({ ...ready, sameScope: false, attachmentsBusy: true })).toBe('drop');
    expect(heldChoiceRelease({ ...ready, cardOpen: false })).toBe('drop');
  });
});
