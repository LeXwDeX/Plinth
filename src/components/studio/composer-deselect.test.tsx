// @vitest-environment jsdom
import React from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { Composer, type ComposerProps } from '@/components/studio/composer';
import { useChatStore } from '@/store/chat-store';

// #557: the Agent can see every object of the session by its handle, so there is no "leave the current object out
// this turn" switch any more. The selection is the default target: the chip stays (picture + kind badge) and its ✕
// deselects; a handle written in the box still wins over it.
const schema = defaultCatalog.application;
const messages = schema.content.messages as Record<string, string>;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema, text: (key: string) => (schema.content.messages as Record<string, string>)[key] ?? '' }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: null }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: () => ({ view: null, busy: false, error: null }) }));
vi.mock('@/components/workspace/harness-summary', () => ({ HarnessSummary: () => null }));
vi.mock('@/components/result/result-card-parts', () => ({ openResultDetail: vi.fn() }));
vi.mock('@/components/studio/composer-settings', () => ({ ComposerSettings: () => <button type="button" data-testid="composer-settings-open" /> }));
vi.mock('@/components/studio/model-picker', () => ({ GenerationModelNotice: () => null }));
vi.mock('@/components/studio/chat-model-picker', () => ({ ChatModelNotice: () => null }));
vi.mock('@/components/studio/context-meter', () => ({ ContextMeter: () => <div data-testid="composer-context" /> }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => url }) }));

const noop = () => undefined;
const props: ComposerProps = {
  value: '', onChange: noop, onSend: noop, onStop: noop, streaming: false, locked: false, canSend: true,
  chip: null, onDeselectChip: noop, images: [], onRemoveImage: noop, pinnedImage: null, onUnpin: noop,
  acceptedImages: undefined, onFiles: noop, onPasteImages: noop, editing: false, onCancelEdit: noop, sendCredits: null, mentions: [],
  params: {} as ComposerProps['params'],
  skill: { sessionId: 's1', catalog: null, effective: null, selectedId: null, unavailable: false, loading: false, failed: false, disabled: false, onSelect: noop, onCreated: noop },
};
const chip = { kind: 'image' as const, thumbnail: '/t.png', label: '2D v3', handle: '@img5' };

beforeEach(() => { vi.stubGlobal('React', React); useChatStore.setState({ activeSessionId: 's1', streamingBuffers: {} }); });
afterEach(() => { cleanup(); useChatStore.setState({ activeSessionId: null, streamingBuffers: {} }); });

describe('the current object chip (#557)', () => {
  it('has a ✕ that deselects the object, and no "leave it out this turn" strip', () => {
    const onDeselectChip = vi.fn();
    render(<Composer {...props} chip={chip} onDeselectChip={onDeselectChip} />);
    const object = screen.getByTestId('composer-object-chip');
    fireEvent.click(within(object).getByRole('button', { name: messages['ui.studio.composer.chip.deselect'] }));
    expect(onDeselectChip).toHaveBeenCalledTimes(1);
    expect(document.body.textContent).not.toContain('本轮不带');
    expect(document.body.textContent).not.toContain('只看对话文字');
    expect(object.getAttribute('data-skipped')).toBe('false');
  });

  it('shows no chip once the object is deselected (the parent passes none)', () => {
    render(<Composer {...props} chip={null} />);
    expect(screen.queryByTestId('composer-object-chip')).toBeNull();
    expect(document.body.textContent).not.toContain('本轮不带');
  });

  it('still says a typed handle takes the turn from the selection', () => {
    render(<Composer {...props} chip={chip} value="把 @img2 变成夜景" />);
    const object = screen.getByTestId('composer-object-chip');
    expect(object.getAttribute('data-overridden-by')).toBe('@img2');
    expect(object.textContent).toContain('@img2');
  });

  it('lets a typed handle take the turn from the board chip too, which has no handle of its own', () => {
    render(<Composer {...props} chip={{ kind: 'image', thumbnail: '/t.png', label: messages['ui.studio.composer.chip.canvas'], handle: '' }} value="把 @img2 变成夜景" />);
    expect(screen.getByTestId('composer-object-chip').getAttribute('data-overridden-by')).toBe('@img2');
  });
});

describe('the Schema words of the chip (#557)', () => {
  const zh = messages;
  const en = (schema as unknown as { localization: { bundles: { en: { messages: Record<string, string> } } } }).localization.bundles.en.messages;

  it('drops the skip and restore words in both languages and names the deselect', () => {
    for (const key of ['off_title', 'off_sub', 'restore', 'skip']) {
      expect(zh[`ui.studio.composer.chip.${key}`]).toBeUndefined();
      expect(en[`ui.studio.composer.chip.${key}`]).toBeUndefined();
    }
    expect(zh['ui.studio.composer.chip.deselect']).toBe('取消选中');
    expect(en['ui.studio.composer.chip.deselect']).toBe('Deselect');
  });
});

describe('the send (#557)', () => {
  const chat = readFileSync(resolve(process.cwd(), 'src/components/studio/chat-column.tsx'), 'utf8');

  it('keeps no per-turn skip state; a deselected object sends no selection and no board snapshot', () => {
    expect(chat).not.toContain('skippedObject');
    expect(chat).not.toContain('chipSkipped');
    expect(chat).toContain('deselectedObjectRef.current === objectKey(sessionObject)');
    // Choosing an object again (any new current object) selects it again.
    expect(chat).toMatch(/useEffect\(\(\) => \{\s*setDeselectedObject\(null\);\s*\}, \[currentObject/);
  });
});
