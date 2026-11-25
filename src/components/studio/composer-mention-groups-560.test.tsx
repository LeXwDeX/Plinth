// @vitest-environment jsdom
import React, { useState } from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { Composer, type ComposerProps } from './composer';

const schema = structuredClone(defaultCatalog.application);
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: null }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: () => ({ view: null, busy: false, error: null }) }));
vi.mock('@/components/workspace/harness-summary', () => ({ HarnessSummary: () => null }));
vi.mock('@/components/result/result-card-parts', () => ({ openResultDetail: vi.fn() }));
vi.mock('@/components/ui/hint-tooltip', () => ({ HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</> }));
// #405: the pickers moved into the settings dialog behind the gear; the composer only reaches them through it.
vi.mock('@/components/studio/composer-settings', () => ({ ComposerSettings: () => null }));
vi.mock('@/components/studio/model-picker', () => ({ GenerationModelNotice: () => null }));
vi.mock('@/components/studio/chat-model-picker', () => ({ ChatModelNotice: () => null }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => url }) }));

const noop = () => undefined;
const send = vi.fn();
const props: ComposerProps = {
  value: '', onChange: noop, onSend: send, onStop: noop, streaming: false, locked: false, canSend: true,
  chip: null, onDeselectChip: noop, images: [], onRemoveImage: noop, pinnedImage: null, onUnpin: noop,
  acceptedImages: undefined, onFiles: noop, onPasteImages: noop, editing: false, onCancelEdit: noop, sendCredits: null, mentions: [],
  params: {} as ComposerProps['params'],
  skill: { sessionId: 's1', catalog: null, effective: null, selectedId: null, unavailable: false, loading: false, failed: false, disabled: false, onSelect: noop, onCreated: noop },
};
function Editor(overrides: Partial<ComposerProps>) {
  const [value, setValue] = useState('');
  return <Composer {...props} {...overrides} value={value} onChange={setValue} />;
}
beforeEach(() => { vi.stubGlobal('React', React); send.mockClear(); });

// #560: the `@` list in sections ("画布中", "素材库"); the headings are not options and the keyboard does not stop on them.
const mentions: ComposerProps['mentions'] = [
  { handle: '@画布', kind: 'image', name: '画布', board: true, source: 'canvas', group: 'canvas', thumbnail: '' },
  { handle: '@img3', kind: 'image', source: 'history', group: 'canvas', thumbnail: '', url: '' },
  { handle: '@img7', kind: 'image', source: 'history', group: 'library', thumbnail: '', url: '' },
  { handle: '@model2', kind: 'model', source: 'history', group: 'library', thumbnail: '' },
];

describe('mention list sections (#560)', () => {
  const scrolled: string[] = [];
  beforeEach(() => {
    scrolled.length = 0;
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      configurable: true, writable: true,
      value(this: HTMLElement) { scrolled.push(this.hasAttribute('data-mention-heading') ? `heading:${this.textContent}` : this.getAttribute('data-handle') ?? '?'); },
    });
  });
  afterEach(() => { delete (HTMLElement.prototype as Partial<HTMLElement>).scrollIntoView; });
  const openList = (list: ComposerProps['mentions'] = mentions) => {
    render(<Editor mentions={list} />);
    const input = screen.getByRole('textbox');
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: '@', selectionStart: 1 } });
    return input;
  };

  it('labels each section with a heading that is not an option', () => {
    openList();
    const list = screen.getByRole('listbox');
    const sections = within(list).getAllByRole('group');
    expect(sections.map((section) => section.getAttribute('aria-labelledby') && document.getElementById(section.getAttribute('aria-labelledby')!)?.textContent))
      .toEqual(['画布中', '素材库']);
    expect(within(sections[0]).getAllByRole('option').map((option) => option.getAttribute('data-handle'))).toEqual(['@画布', '@img3']);
    expect(within(sections[1]).getAllByRole('option').map((option) => option.getAttribute('data-handle'))).toEqual(['@img7', '@model2']);
    // Only the four entries are options; the headings are presentation rows and the handles are the server's own.
    expect(within(list).getAllByRole('option').map((option) => option.getAttribute('data-handle'))).toEqual(['@画布', '@img3', '@img7', '@model2']);
    for (const heading of within(list).getAllByTestId('composer-mention-group-heading')) {
      expect(heading.getAttribute('role')).toBe('presentation');
      expect(heading.closest('[role="option"]')).toBeNull();
    }
    expect(within(list).getAllByRole('option')[0].getAttribute('aria-selected')).toBe('true');
  });

  it('moves through the options across sections, never onto a heading, and picks the highlighted handle', () => {
    const input = openList();
    const selected = () => screen.getAllByRole('option').findIndex((option) => option.getAttribute('aria-selected') === 'true');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(selected()).toBe(1);
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(selected()).toBe(2);
    fireEvent.keyDown(input, { key: 'ArrowUp' });
    fireEvent.keyDown(input, { key: 'ArrowUp' });
    fireEvent.keyDown(input, { key: 'ArrowUp' });
    expect(selected()).toBe(3);
    fireEvent.keyDown(input, { key: 'Enter' });
    expect((input as HTMLTextAreaElement).value).toBe('@model2 ');
  });

  it('brings a section heading into view with the first option of its section, wrap included', () => {
    const input = openList();
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(scrolled).toEqual(['@img3']);
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(scrolled).toEqual(['@img3', 'heading:素材库', '@img7']);
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    // Wrapping back to the canvas entry also shows the canvas heading above it.
    expect(scrolled).toEqual(['@img3', 'heading:素材库', '@img7', '@model2', 'heading:画布中', '@画布']);
    fireEvent.mouseEnter(screen.getAllByRole('option')[2]);
    expect(scrolled).toHaveLength(6);
  });

  it('shows one section when the filter leaves one, and no heading for a list without groups', () => {
    const input = openList();
    fireEvent.change(input, { target: { value: '@img7', selectionStart: 5 } });
    expect(within(screen.getByRole('listbox')).getAllByTestId('composer-mention-group-heading').map((heading) => heading.textContent)).toEqual(['素材库']);
    cleanup();
    openList(mentions.map(({ group: _group, ...rest }) => rest));
    expect(within(screen.getByRole('listbox')).queryAllByRole('group')).toEqual([]);
    expect(screen.getAllByRole('option')).toHaveLength(4);
  });
});
