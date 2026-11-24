// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ChoiceCard } from '@/components/studio/choice-card';
import { parseChoiceCards } from '@/lib/chat/choice-cards';

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: defaultCatalog.application }) }));
vi.mock('swr', () => ({ default: () => ({ data: undefined, isLoading: false }) }));
// The hint's bubble is Base UI's; what is under test is what it is given and when it may open.
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children, name, hint, suppressed, triggerProps }: { children: React.ReactNode; name: string; hint?: string; suppressed?: boolean; triggerProps?: Record<string, unknown> }) => (
    <span data-testid="hint" data-name={name} data-hint={hint ?? ''} data-suppressed={String(Boolean(suppressed))} {...triggerProps}>{children}</span>
  ),
}));

const LONG = '用现有模型重拓扑，先和原版对比凳面、榫头和腿形，再按参考图补齐细节，最后统一导出。';
const card = parseChoiceCards({ choices: [{
  id: 'choice_next', assistant_message_id: 'a3', tool: '', kind: 'next_step', capability: 'next_step', question: '接下来想怎么做？',
  options: [
    { id: 'retopo', label: '保轮廓重拓扑', description: LONG, recommended: true },
    { id: 'short', label: '再出一版', description: '换个角度' },
  ],
  default_option_id: 'retopo', allow_default: false, state: 'pending', created_at: '2026-11-24T00:00:00Z',
}] })[0];

afterEach(cleanup);

function renderCard() {
  render(<ChoiceCard card={card} busy={false} disabled={false} error={null} onAnswer={async () => undefined} />);
  return screen.getAllByTestId('chat-choice-option');
}

describe('a choice option whose description is cut (#546)', () => {
  it('shows the whole title and description on hover, only when the description is cut', () => {
    const [option] = renderCard();
    const hint = option.parentElement!.closest('[data-testid="hint"]') as HTMLElement;
    expect(hint.getAttribute('data-name')).toBe('保轮廓重拓扑');
    expect(hint.getAttribute('data-hint')).toBe(LONG);
    expect(hint.getAttribute('data-suppressed')).toBe('true');
    const description = option.querySelector('[data-testid="chat-choice-description"]') as HTMLElement;
    Object.defineProperty(description, 'scrollHeight', { configurable: true, value: 54 });
    Object.defineProperty(description, 'clientHeight', { configurable: true, value: 30 });
    fireEvent.pointerEnter(hint);
    expect(hint.getAttribute('data-suppressed')).toBe('false');
  });

  it('opens on keyboard focus too, and keeps a short description without a hint', () => {
    const options = renderCard();
    const short = options[1].parentElement!.closest('[data-testid="hint"]') as HTMLElement;
    fireEvent.focus(options[1]);
    expect(short.getAttribute('data-suppressed')).toBe('true');
    const long = options[0].parentElement!.closest('[data-testid="hint"]') as HTMLElement;
    const description = options[0].querySelector('[data-clamp]') as HTMLElement;
    Object.defineProperty(description, 'scrollHeight', { configurable: true, value: 54 });
    Object.defineProperty(description, 'clientHeight', { configurable: true, value: 30 });
    fireEvent.focus(options[0]);
    expect(long.getAttribute('data-suppressed')).toBe('false');
  });

  it('still picks an option with a click and moves with the arrow keys', () => {
    const options = renderCard();
    fireEvent.click(options[1]);
    expect(options[1].getAttribute('aria-checked')).toBe('true');
    fireEvent.keyDown(options[1], { key: 'ArrowUp' });
    expect(screen.getAllByTestId('chat-choice-option')[0].getAttribute('aria-checked')).toBe('true');
  });
});
