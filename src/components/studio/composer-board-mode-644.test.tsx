// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { Composer, type ComposerProps } from '@/components/studio/composer';
import { useChatStore } from '@/store/chat-store';

// #644: the 以画板为主 toggle in the box. Off by default; a selection, a mask or `@画布` shows it on and locked;
// the person can turn it on for one message. Its text comes from the Schema.
const schema = defaultCatalog.application;
const messages = schema.content.messages as Record<string, string>;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema, text: (key: string) => (schema.content.messages as Record<string, string>)[key] ?? '' }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: null }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: () => ({ view: null, busy: false, error: null }) }));
vi.mock('@/components/workspace/harness-summary', () => ({ HarnessSummary: () => null }));
vi.mock('@/components/result/result-card-parts', () => ({ openResultDetail: vi.fn() }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children, ...content }: { children: React.ReactNode } & Record<string, unknown>) => (
    <span data-testid="hint-wrapper" data-name={String(content.name ?? '')} data-hint={String(content.hint ?? '')}>{children}</span>
  ),
}));
vi.mock('@/components/studio/composer-settings', () => ({ ComposerSettings: () => null }));
vi.mock('@/components/studio/model-picker', () => ({ GenerationModelNotice: () => null }));
vi.mock('@/components/studio/chat-model-picker', () => ({ ChatModelNotice: () => null }));
vi.mock('@/components/studio/context-meter', () => ({ ContextMeter: () => null }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => url }) }));

const noop = () => undefined;
const props: ComposerProps = {
  value: '', onChange: noop, onSend: noop, onStop: noop, streaming: false, locked: false, canSend: true,
  chip: null, onDeselectChip: noop, images: [], onRemoveImage: noop, pinnedImage: null, onUnpin: noop,
  acceptedImages: undefined, onFiles: noop, onPasteImages: noop, editing: false, onCancelEdit: noop, sendCredits: null, mentions: [],
  params: {} as ComposerProps['params'],
  skill: { sessionId: 's1', catalog: null, effective: null, selectedId: null, unavailable: false, loading: false, failed: false, disabled: false, onSelect: noop, onCreated: noop },
};

beforeEach(() => { vi.stubGlobal('React', React); useChatStore.setState({ activeSessionId: 's1', streamingBuffers: {} }); });
afterEach(() => { cleanup(); useChatStore.setState({ activeSessionId: null, streamingBuffers: {} }); });

const toggle = () => screen.getByTestId('composer-board-mode');
const hintOf = (element: HTMLElement) => element.closest('[data-testid="hint-wrapper"]')?.getAttribute('data-hint');

describe('the 以画板为主 toggle (#644)', () => {
  it('is not shown when the session has no board to send', () => {
    render(<Composer {...props} />);
    expect(screen.queryByTestId('composer-board-mode')).toBeNull();
  });

  it('is off by default and says the board takes no part', () => {
    const onToggle = vi.fn();
    render(<Composer {...props} boardMode={{ on: false, forced: false, onToggle }} />);
    expect(toggle().getAttribute('aria-pressed')).toBe('false');
    expect(toggle().textContent).toContain(messages['ui.studio.composer.board_mode.label']);
    expect(hintOf(toggle())).toBe(messages['ui.studio.composer.board_mode.hint_off']);
    fireEvent.click(toggle());
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it('shows on for a message the person switched on, and can be switched off again', () => {
    const onToggle = vi.fn();
    render(<Composer {...props} boardMode={{ on: true, forced: false, onToggle }} />);
    expect(toggle().getAttribute('aria-pressed')).toBe('true');
    expect(hintOf(toggle())).toBe(messages['ui.studio.composer.board_mode.hint_on']);
    expect((toggle() as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(toggle());
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it('shows on and cannot be turned off while a selection, a mask or @画布 makes the message a canvas turn', () => {
    const onToggle = vi.fn();
    render(<Composer {...props} boardMode={{ on: true, forced: true, onToggle }} />);
    expect(toggle().getAttribute('aria-pressed')).toBe('true');
    expect((toggle() as HTMLButtonElement).disabled).toBe(true);
    expect(hintOf(toggle())).toBe(messages['ui.studio.composer.board_mode.hint_forced']);
    fireEvent.click(toggle());
    expect(onToggle).not.toHaveBeenCalled();
  });

  it('has its text in both language packs of the Schema', () => {
    const english = (schema.localization.bundles as Record<string, { messages: Record<string, string> }>).en.messages;
    for (const key of ['label', 'hint_off', 'hint_on', 'hint_forced']) {
      expect(messages[`ui.studio.composer.board_mode.${key}`]).toBeTruthy();
      expect(english[`ui.studio.composer.board_mode.${key}`]).toBeTruthy();
    }
  });
});
