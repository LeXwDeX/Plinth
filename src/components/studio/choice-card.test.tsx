import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ChoiceCard } from '@/components/studio/choice-card';
import { parseChoiceCards, type ChoiceCardModel } from '@/lib/chat/choice-cards';

const messages = defaultCatalog.application.content.messages as Record<string, string>;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: defaultCatalog.application }) }));
vi.mock('swr', () => ({ default: () => ({ data: undefined, isLoading: false }) }));

const base = parseChoiceCards({ choices: [{
  id: 'choice_1', assistant_message_id: 'a1', tool: 'process_model', capability: 'mesh-segmentation',
  options: [
    { id: 'tripo/geometry/smartsegment-model', provider_name: 'Tripo3D', model_name: 'Smart Segmentation', credits_metered: true, estimated_credits: 40 },
    { id: 'hyper3d/bang/rodin-task', provider_name: 'Hyper3D', model_name: 'Bang Rodin' },
  ],
  default_option_id: 'tripo/geometry/smartsegment-model', allow_default: true, state: 'pending', created_at: '2026-10-22T00:00:00Z',
}] })[0];
const render = (card: ChoiceCardModel, extra: { busy?: boolean; disabled?: boolean; error?: string } = {}) => renderToStaticMarkup(
  <ChoiceCard card={card} busy={extra.busy ?? false} disabled={extra.disabled ?? false} error={extra.error ?? null} onAnswer={() => {}} />,
);
const step = defaultCatalog.application.capabilities.find((item) => item.id === 'mesh-segmentation')!.label_cn;

describe('ChoiceCard (#321)', () => {
  it('asks the Schema question with a labelled single-choice list, the default preselected and marked', () => {
    const html = render(base);
    const question = messages['ui.chat.choice.offering_question'].replace('{step}', step);
    expect(html).toContain(question);
    expect(html).toMatch(/role="radiogroup"[^>]*aria-labelledby="[^"]+"/);
    expect(html.match(/role="radio"/g)).toHaveLength(2);
    expect(html).toMatch(/role="radio" aria-checked="true" tabindex="0"[^>]*data-option-id="tripo\/geometry\/smartsegment-model"/);
    expect(html).toMatch(/role="radio" aria-checked="false" tabindex="-1"[^>]*data-option-id="hyper3d\/bang\/rodin-task"/);
    expect(html).toContain(messages['ui.chat.choice.default_badge']);
    expect(html).toContain('Hyper3D · Bang Rodin');
    expect(html).toContain(`${messages['ui.workspace.harness.credits']}: 40`);
    expect(html).toContain(`>${messages['ui.chat.choice.confirm']}<`);
    expect(html).toContain(messages['ui.chat.choice.use_default'].replace('{title}', 'Tripo3D · Smart Segmentation'));
  });

  it('keeps a long default button inside the card and shows its whole text on hover (#321 C)', () => {
    const title = 'Tripo Smart Segmentation · 模型（很长很长的服务名称用来撑满卡片宽度）';
    const long = { ...base, options: base.options.map((option, index) => (index === 0 ? { ...option, modelName: title } : option)) };
    const html = render(long);
    const text = messages['ui.chat.choice.use_default'].replace('{title}', `Tripo3D · ${title}`);
    // The whole text is the hover hint's name (HintTooltip wraps the button), not a native title.
    const wrapper = html.match(/<span class="([^"]*)"[^>]*>(?=<button[^>]*data-testid="chat-choice-default")/);
    expect((wrapper?.[1] ?? '').split(' ')).toEqual(expect.arrayContaining(['inline-flex', 'min-w-0', 'max-w-full']));
    const button = html.match(/<button[^>]*data-testid="chat-choice-default"[^>]*>(.*?)<\/button>/);
    expect(button?.[0]).not.toContain('title=');
    const classes = (button?.[0].match(/class="([^"]*)"/)?.[1] ?? '').split(' ');
    expect(classes).toEqual(expect.arrayContaining(['max-w-full', 'min-w-0', 'shrink']));
    expect(classes).not.toContain('shrink-0');
    expect(button?.[1]).toContain(`<span class="truncate">${text}</span>`);
  });

  it('offers no default button when the card does not allow one, and disables the buttons while busy', () => {
    expect(render({ ...base, allowDefault: false })).not.toContain('data-testid="chat-choice-default"');
    expect(render(base, { busy: true })).toMatch(/<button[^>]*disabled=""[^>]*data-testid="chat-choice-confirm"/);
    expect(render(base, { disabled: true })).toMatch(/<button[^>]*disabled=""[^>]*data-testid="chat-choice-confirm"/);
    expect(render(base, { error: 'nope' })).toMatch(/role="alert"[^>]*>nope</);
  });

  // #335: an answer that did not reach the server says so on the card and can be sent again.
  it('shows the Schema failure words as an alert and leaves the card answerable', () => {
    const failed = messages['ui.chat.choice.failed'];
    const english = (defaultCatalog.application.localization.bundles.en.messages as Record<string, string>)['ui.chat.choice.failed'];
    expect(failed).toBeTruthy();
    expect(english).toBeTruthy();
    const html = render(base, { error: failed });
    expect(html).toMatch(new RegExp(`role="alert"[^>]*data-testid="chat-choice-error"[^>]*>${failed}<`));
    expect(html).not.toMatch(/<button[^>]*disabled=""[^>]*data-testid="chat-choice-confirm"/);
    expect(html).not.toMatch(/role="radiogroup"[^>]*aria-disabled="true"/);
  });

  // #335: 确认 pressed while the composer's pictures are still being read used to do nothing at all.
  it('says the answer waits for the pictures being read, and keeps the buttons locked meanwhile', () => {
    const waiting = renderToStaticMarkup(<ChoiceCard card={base} busy={false} waiting disabled={false} error={null} onAnswer={() => {}} />);
    expect(waiting).toMatch(new RegExp(`role="status"[^>]*data-testid="chat-choice-waiting"[^>]*>${messages['ui.chat.choice.waiting_attachments']}<`));
    expect(waiting).toMatch(/<button[^>]*disabled=""[^>]*data-testid="chat-choice-confirm"/);
    expect(waiting).toMatch(/<button[^>]*disabled=""[^>]*data-testid="chat-choice-default"/);
    expect(waiting).toMatch(/role="radiogroup"[^>]*aria-disabled="true"/);
    expect(render(base)).not.toContain('data-testid="chat-choice-waiting"');
  });

  it('shows an answered card read-only with its answer, and a superseded card as closed', () => {
    const answered = render({ ...base, state: 'answered', answer: { optionId: 'hyper3d/bang/rodin-task', useDefault: false, source: 'card' } });
    expect(answered).toContain(messages['ui.chat.choice.answered'].replace('{title}', 'Hyper3D · Bang Rodin'));
    expect(answered).not.toContain('data-testid="chat-choice-confirm"');
    expect(answered).not.toContain('data-testid="chat-choice-default"');
    expect(answered).toMatch(/role="radio" aria-checked="true"[^>]*aria-disabled="true"[^>]*data-option-id="hyper3d\/bang\/rodin-task"/);
    const closed = render({ ...base, state: 'superseded' });
    expect(closed).toContain(messages['ui.chat.choice.superseded']);
    expect(closed).not.toContain('data-testid="chat-choice-confirm"');
  });
});
