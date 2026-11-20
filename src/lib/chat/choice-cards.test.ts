import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';
import {
  choiceActionLabel,
  choiceAnswerText,
  choiceCardHasDetails,
  choiceCardOfferingCapability,
  choiceCardPlacement,
  choiceOptionQuote,
  choiceOptionTitle,
  choiceSelectionAction,
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
    expect(parsed[0]).toMatchObject({ assistantMessageId: 'assistant-1', kind: '', question: '', capability: 'mesh-segmentation', defaultOptionId: wire.default_option_id, allowDefault: true, state: 'pending', answer: null });
    expect(parsed[0].options[1].nativeQuote?.total_amount).toBe('0.5');
    expect(parsed[1].answer).toEqual({ optionId: 'hyper3d/bang/local-model', useDefault: false, source: 'card' });
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ choices: [wire] }), { status: 200 }));
    expect((await fetchChoiceCards(schema, 's1', undefined, fetcher as unknown as typeof fetch))[0].id).toBe('choice_1');
    expect(fetcher).toHaveBeenCalledWith('/api/v1/chat/sessions/s1/choices', expect.anything());
  });

  // #103/#171: a generic card stores its choice kind in `capability`; it is not
  // an Offering capability and must never be fetched as one (the 400 L0 saw).
  it('reads a generic card kind and question and never prices its kind as a capability', () => {
    const generic = parseChoiceCards({ choices: [{
      id: 'choice_usage', assistant_message_id: 'a2', tool: 'update_production_brief',
      kind: 'usage_profile', question: '这个 3D 模型主要用来做什么？', capability: 'usage_profile',
      options: [
        { id: 'game_character', label: '游戏/实时·角色', description: '控制面数并保留 UV', recommended: true },
        { id: 'display', label: '展示/渲染' },
      ],
      default_option_id: 'game_character', allow_default: false, state: 'pending', created_at: '2026-11-07T00:00:00Z',
    }] })[0];
    expect(generic).toMatchObject({ kind: 'usage_profile', question: '这个 3D 模型主要用来做什么？', capability: 'usage_profile' });
    expect(choiceCardOfferingCapability(schema, generic)).toBeNull();
    expect(choiceCardOfferingCapability(schema, card())).toBe('mesh-segmentation');
    expect(choiceCardOfferingCapability(schema, card({ capability: 'not-a-capability' }))).toBeNull();
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

  it('reads the rich option fields and renders tiles only when one of them is present (#103)', () => {
    const rich = parseChoiceCards({ choices: [{ ...wire, options: [
      { id: 'a', provider_name: 'P', model_name: 'M', label: '快速草模', description: '几十秒出一个低面数草模', recommended: true, thumbnail_url: '/api/v1/tasks/t/assets/sketch' },
      { id: 'b', provider_name: 'P', model_name: 'M2' },
    ] }] })[0];
    expect(rich.options[0]).toMatchObject({ label: '快速草模', description: '几十秒出一个低面数草模', recommended: true, thumbnailUrl: '/api/v1/tasks/t/assets/sketch' });
    // #242: a card without a thumbnail keeps the text layout.
    expect(rich.options[1].thumbnailUrl).toBe('');
    expect(rich.options[1]).toMatchObject({ label: '', description: '', recommended: false });
    expect(choiceOptionTitle(rich.options[0], [])).toBe('快速草模');
    expect(choiceCardHasDetails(rich)).toBe(true);
    // An older card carries no rich field: its familiar list rendering stays.
    expect(choiceCardHasDetails(card())).toBe(false);
    expect(choiceCardHasDetails({ options: [{ ...card().options[0], label: 'only a label' }] })).toBe(true);
    expect(choiceCardHasDetails({ options: [{ ...card().options[0], description: 'only a description' }] })).toBe(true);
    expect(choiceCardHasDetails({ options: [{ ...card().options[0], recommended: true }] })).toBe(true);
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

  // #374: the direction card (next_step) names no target tool, has no default and lets an option carry an action the
  // page performs before the answer goes out.
  describe('direction card option actions (#374)', () => {
    const direction = {
      id: 'choice_next', assistant_message_id: 'assistant-2', tool: '', kind: 'next_step', capability: 'next_step',
      question: '接下来想怎么做？', allow_default: false, default_option_id: '', state: 'pending', created_at: '2026-11-19T00:00:00Z',
      options: [
        { id: 'clear_and_draw', label: '清空功能选择', description: '回到自由模式再继续', recommended: true, action: { type: 'clear_operation' } },
        { id: 'switch_retexture', label: '换贴图', action: { type: 'switch_operation', operation_id: 'retexture' } },
        { id: 'keep', label: '保持当前功能' },
      ],
    };
    const parsed = () => parseChoiceCards({ choices: [direction] })[0];

    it('reads each option action and leaves an option without one actionless', () => {
      const card = parsed();
      expect(card).toMatchObject({ kind: 'next_step', tool: '', question: '接下来想怎么做？', allowDefault: false, defaultOptionId: '' });
      expect(card.options[0].action).toEqual({ type: 'clear_operation' });
      expect(card.options[1].action).toEqual({ type: 'switch_operation', operationId: 'retexture' });
      expect(card.options[2]).not.toHaveProperty('action');
      // A card of this kind is never priced as an Offering capability.
      expect(choiceCardOfferingCapability(schema, card)).toBeNull();
      expect(choiceCardHasDetails(card)).toBe(true);
    });

    it('leaves out an option whose action this page cannot perform, instead of answering it as a plain option', () => {
      const card = parseChoiceCards({ choices: [{ ...direction, options: [
        direction.options[0],
        { id: 'unknown', label: '未知动作', action: { type: 'reboot_server' } },
        { id: 'no_target', label: '没有目标功能', action: { type: 'switch_operation' } },
        { id: 'malformed', label: '动作不是对象', action: 'clear_operation' },
        direction.options[2],
      ] }] })[0];
      expect(card.options.map((option) => option.id)).toEqual(['clear_and_draw', 'keep']);
      // A card with nothing left to answer is not shown at all.
      expect(parseChoiceCards({ choices: [{ ...direction, options: [{ id: 'x', label: 'x', action: { type: 'reboot_server' } }] }] })).toEqual([]);
    });

    it('finds the action of the option a selection picks, and none for an actionless option or an unknown id', () => {
      const card = parsed();
      expect(choiceSelectionAction(card, { optionId: 'clear_and_draw' })).toEqual({ type: 'clear_operation' });
      expect(choiceSelectionAction(card, { optionId: 'switch_retexture' })).toEqual({ type: 'switch_operation', operationId: 'retexture' });
      expect(choiceSelectionAction(card, { optionId: 'keep' })).toBeUndefined();
      expect(choiceSelectionAction(card, { optionId: 'gone' })).toBeUndefined();
      // "Use default" reads the default option like the answer text does.
      expect(choiceSelectionAction({ ...card, defaultOptionId: 'clear_and_draw', allowDefault: true }, { useDefault: true })).toEqual({ type: 'clear_operation' });
      expect(choiceSelectionAction(card, { useDefault: true })).toBeUndefined();
    });

    it('words each action from the Schema, naming the function from its public declaration', () => {
      const retexture = schema.chat.harness!.operations.find((operation) => operation.id === 'retexture')!;
      expect(choiceActionLabel(schema, { type: 'clear_operation' })).toBe(messages['ui.chat.choice.action_clear']);
      expect(choiceActionLabel(schema, { type: 'switch_operation', operationId: 'retexture' }))
        .toBe(messages['ui.chat.choice.action_switch'].replace('{operation}', retexture.label));
      // A function the Schema no longer lists reads like the Harness summary words it, never as a raw id.
      const gone = choiceActionLabel(schema, { type: 'switch_operation', operationId: 'removed_function' });
      expect(gone).toContain(messages['ui.unavailable.reason.harness.selection_gone']);
      expect(gone).not.toContain('removed_function');
    });

    it('declares every Schema word an action shows, in both languages', () => {
      const english = (defaultCatalog.application.localization.bundles.en.messages as Record<string, string>);
      for (const key of ['action_switch', 'action_clear', 'action_failed', 'action_conflict', 'action_unavailable', 'action_service_choose', 'action_service_none']) {
        expect(messages[`ui.chat.choice.${key}`], key).toBeTruthy();
        expect(english[`ui.chat.choice.${key}`], key).toBeTruthy();
      }
      expect(messages['ui.chat.choice.action_switch']).toContain('{operation}');
      expect(english['ui.chat.choice.action_switch']).toContain('{operation}');
    });
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
