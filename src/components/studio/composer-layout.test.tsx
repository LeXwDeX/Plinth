// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { Composer, type ComposerProps } from '@/components/studio/composer';
import { PICKER_UPPERCASE_CLASS } from '@/components/studio/table-popover';
import type { ProductSchema } from '@/lib/product-schema';
import { useChatStore } from '@/store/chat-store';
import { useSessionContextStore } from '@/store/session-context-store';

// #99 / #108 / #405 layout contract, asserted on the rendered composer instead of on its source text. jsdom has no
// layout engine and the stylesheet is not loaded, so the contract is checked the way the stylesheet reads it: the
// order of the elements and the classes that give each its place. The settings dialog behind the gear is a stand-in
// that carries its own test id (its tabs are tested in composer-settings-405.test.tsx); the context meter is the
// real component.
const schema = { ...(defaultCatalog.application as unknown as ProductSchema) };

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner' } }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: () => ({ view: null, busy: false, error: null }) }));
vi.mock('@/components/workspace/harness-summary', () => ({ HarnessSummary: () => null }));
vi.mock('@/components/result/result-card-parts', () => ({ openResultDetail: vi.fn() }));
vi.mock('@/components/ui/hint-tooltip', () => ({ HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</> }));
vi.mock('@/components/studio/composer-settings', () => ({
  ComposerSettings: () => <button type="button" data-testid="composer-settings-open" />,
}));
vi.mock('@/components/studio/model-picker', () => ({ GenerationModelNotice: () => null }));
vi.mock('@/components/studio/chat-model-picker', () => ({ ChatModelNotice: () => null }));

const noop = () => undefined;
const props: ComposerProps = {
  value: '', onChange: noop, onSend: noop, onStop: noop, streaming: false, locked: false, canSend: true,
  chip: null, onDeselectChip: noop, images: [], onRemoveImage: noop, pinnedImage: null, onUnpin: noop,
  acceptedImages: undefined, onFiles: noop, onPasteImages: noop, editing: false, onCancelEdit: noop, sendCredits: null, mentions: [],
  params: {} as ComposerProps['params'],
  skill: { sessionId: 's1', catalog: null, effective: null, selectedId: null, unavailable: false, loading: false, failed: false, disabled: false, onSelect: noop, onCreated: noop },
};

function open() {
  useChatStore.setState({ activeSessionId: 's1', streamingBuffers: {} });
  useSessionContextStore.setState({ context: { s1: { tokens: 356_000, window: 1_000_000, compactions: [] } }, compacting: {} });
  return render(<Composer {...props} />);
}

const follows = (a: Element, b: Element) => Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);

beforeEach(() => { vi.stubGlobal('React', React); });
afterEach(() => {
  cleanup();
  useChatStore.setState({ activeSessionId: null, streamingBuffers: {} });
  useSessionContextStore.setState({ context: {}, compacting: {} });
});

// #697: an attached picture has no purpose control; the chips say only where the picture came from.
describe('attached pictures without a purpose control', () => {
  it('shows no purpose control or purpose text for uploaded or board pictures', () => {
    const view = render(<Composer {...props} images={['/a.png', '/b.png']} pinnedImage="/board.png" />);
    const chips = view.getAllByTestId('composer-attachment-chip');
    expect(chips).toHaveLength(3);
    for (const testId of ['composer-attachment-purposes', 'composer-attachment-purpose-row', 'composer-attachment-purpose']) {
      expect(view.queryByTestId(testId)).toBeNull();
    }
    expect(view.container.querySelector('[role=radiogroup]')).toBeNull();
    for (const purpose of ['参考风格', '参考构图', '当作输入图']) expect(view.container.textContent).not.toContain(purpose);
    expect(chips[0].textContent).toContain(schema.content.messages['ui.studio.composer.attachment.upload']);
    expect(chips[2].textContent).toContain(schema.content.messages['ui.studio.composer.attachment.from_board']);
  });
});

describe('the composer under the input box (#99, #405)', () => {
  it('has the settings gear right of the send button, in the same toolbar', () => {
    const view = open();
    const send = within(view.container).getByTestId('composer-send');
    const gear = within(view.container).getByTestId('composer-settings-open');
    expect(send.parentElement).toBe(gear.parentElement);
    expect(follows(send, gear)).toBe(true);
    // The gear is the last control of the toolbar: nothing sits between the send button and the box's edge but it.
    expect(gear.parentElement?.lastElementChild).toBe(gear);
    // One gear, and the toolbar is inside the box, above the status row.
    expect(within(view.container).getAllByTestId('composer-settings-open')).toHaveLength(1);
  });

  it('no longer carries the four pickers, or the grid that held them, under the box', () => {
    const view = open();
    const root = within(view.container);
    for (const id of ['composer-pickers', 'composer-params-slot', 'workspace-model-picker', 'workspace-skill-picker', 'composer-params', 'workspace-chat-model-picker']) {
      expect(root.queryByTestId(id), id).toBeNull();
    }
  });

  it('keeps the status row under the box, last, in the place the pickers shared with it', () => {
    const view = open();
    const composer = within(view.container).getByTestId('studio-composer');
    const row = within(composer).getByTestId('composer-context');
    expect(row.parentElement).toBe(composer);
    expect(composer.lastElementChild).toBe(row);
    expect(follows(within(composer).getByRole('textbox'), row)).toBe(true);
  });

  it('lets the status row fill the whole row, one control high, with no grid cell left in its classes', () => {
    const view = open();
    const context = within(view.container).getByTestId('composer-context');
    const classes = [...context.classList];
    expect(classes).toContain('h-7');
    expect(classes).toContain('w-full');
    expect(classes.some((name) => /(^|:)col-(span|start)-/.test(name))).toBe(false);
    // The figure and the compaction action sit together on the left, the tool call counter alone on the right edge (#275).
    const meter = within(context).getByTestId('composer-context-meter');
    const compact = within(context).getByTestId('composer-context-compact');
    const counter = within(context).getByTestId('composer-tool-calls');
    expect(follows(meter, compact)).toBe(true);
    expect(follows(compact, counter)).toBe(true);
  });
});

describe('the context meter keeps the shared uppercase treatment (#108)', () => {
  it('carries the shared class on the figure and on the compaction action', () => {
    const view = open();
    const classes = (id: string) => within(view.container).getByTestId(id).className;
    expect(classes('composer-context-meter')).toContain(PICKER_UPPERCASE_CLASS);
    expect(classes('composer-context-compact')).toContain(PICKER_UPPERCASE_CLASS);
  });
});
