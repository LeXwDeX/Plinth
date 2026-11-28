// @vitest-environment jsdom
import React from 'react';
import { readFileSync } from 'node:fs';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { Composer, type ComposerProps } from '@/components/studio/composer';
import { useChatStore } from '@/store/chat-store';

// #721: attaching 2–4 pictures no longer opens the 是不是多个视角 question above the box. The way to multiview 3D is
// one button beside the attachment cards; nothing changes until the person presses it. Its text comes from the Schema.
const schema = defaultCatalog.application;
const messages = schema.content.messages as Record<string, string>;
const english = (schema.localization.bundles as Record<string, { messages: Record<string, string> }>).en.messages;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema, text: (key: string) => (schema.content.messages as Record<string, string>)[key] ?? '' }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: null }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: () => ({ view: null, busy: false, error: null }) }));
vi.mock('@/components/workspace/harness-summary', () => ({ HarnessSummary: () => null }));
vi.mock('@/components/result/result-card-parts', () => ({ openResultDetail: vi.fn() }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children, ...content }: { children: React.ReactNode } & Record<string, unknown>) => (
    <span data-testid="hint-wrapper" data-name={String(content.name ?? '')}>{children}</span>
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
const two = ['/api/v1/tasks/t1/assets/a1', '/api/v1/tasks/t1/assets/a2'];
const source = (relative: string) => readFileSync(new URL(relative, import.meta.url), 'utf8');

beforeEach(() => { vi.stubGlobal('React', React); useChatStore.setState({ activeSessionId: 's1', streamingBuffers: {} }); });
afterEach(() => { cleanup(); useChatStore.setState({ activeSessionId: null, streamingBuffers: {} }); });

describe('the 用作多视角 entry beside the attachments (#721)', () => {
  it('is a labelled, keyboard-reachable button among the attachment cards and opens the views on press', () => {
    const onToggle = vi.fn();
    render(<Composer {...props} images={two} multiview={{ model: 'Tripo H3', open: false, onToggle }} />);
    const entry = screen.getByTestId('composer-multiview-entry');
    expect(entry.tagName).toBe('BUTTON');
    expect(entry.closest('[data-testid="composer-chip-cards"]')).not.toBeNull();
    expect(entry.textContent).toContain(messages['ui.studio.multiview_upload.entry']);
    expect(entry.getAttribute('aria-label')).toBe(messages['ui.studio.multiview_upload.entry_hint'].replace('{model}', 'Tripo H3'));
    expect(entry.getAttribute('aria-expanded')).toBe('false');
    // Nothing is asked above the box: the question panel is not part of the composer.
    expect(screen.queryByTestId('studio-multiview-upload')).toBeNull();
    fireEvent.click(entry);
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it('says it is open while the views are being marked', () => {
    render(<Composer {...props} images={two} multiview={{ model: 'Tripo H3', open: true, onToggle: noop }} />);
    expect(screen.getByTestId('composer-multiview-entry').getAttribute('aria-expanded')).toBe('true');
  });

  it('is absent when the composer is not offered multiview (one picture, none, or no multiview model)', () => {
    render(<Composer {...props} images={['/api/v1/tasks/t1/assets/a1']} />);
    expect(screen.queryByTestId('composer-multiview-entry')).toBeNull();
    cleanup();
    render(<Composer {...props} images={[]} multiview={null} />);
    expect(screen.queryByTestId('composer-multiview-entry')).toBeNull();
  });

  it('has its text in both language packs of the Schema', () => {
    for (const key of ['entry', 'entry_hint']) {
      expect(messages[`ui.studio.multiview_upload.${key}`]).toBeTruthy();
      expect(english[`ui.studio.multiview_upload.${key}`]).toBeTruthy();
    }
    expect(messages['ui.studio.multiview_upload.entry_hint']).toContain('{model}');
    expect(english['ui.studio.multiview_upload.entry_hint']).toContain('{model}');
  });
});

describe('the chat column offers multiview without interrupting (#721)', () => {
  const chat = source('./chat-column.tsx');

  it('shows the views panel only after the entry was pressed for these pictures', () => {
    expect(chat).toContain('multiviewEntryOffered(multiviewTarget, attachedImages.length, Boolean(editing))');
    expect(chat).toMatch(/\{multiviewTarget && multiviewOpen \? \(\s+<MultiviewUploadPanel/);
    expect(chat).not.toMatch(/\{multiviewTarget && attachedImages\.length >= 2 && attachedImages\.length <= 4 && !editing \? \(\s+<MultiviewUploadPanel/);
    expect(chat).toContain('multiview={multiviewOffered && multiviewTarget ? {');
  });

  it('confirms the views as the old 是 did: one multiview send with the marked views', () => {
    expect(chat).toContain("void send(formatSchemaText(schemaText(schema, 'ui.studio.multiview_upload.turn'), { views: labels }), picked, undefined, undefined, undefined, undefined, undefined, { multiviewViews: views });");
  });

  it('sends a plain message from the box with no multiview claim', () => {
    const plain = chat.slice(chat.indexOf('<Composer'), chat.indexOf('onStop={stopTurn}'));
    expect(plain).toContain('void send(input, undefined, undefined, editing?.deleteIds');
    expect(plain).not.toContain('multiviewViews');
    expect(plain).not.toContain('multiview:');
  });
});
