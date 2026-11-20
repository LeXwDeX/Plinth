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
schema.chat = { ...schema.chat, agent_limits: { max_tool_calls_per_reply: 20 } };

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
  chip: null, chipSkipped: false, onToggleChip: noop, images: [], onRemoveImage: noop, pinnedImage: null, onUnpin: noop,
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

describe('per-attachment purpose controls', () => {
  it('shows separate purposes, toggles one, sets all, and keeps a pinned choice separate', () => {
    const onSend = vi.fn();
    function Controlled() {
      const [purposes, setPurposes] = React.useState<('style' | 'layout' | 'input' | null)[]>(['style', 'layout']);
      const [pinned, setPinned] = React.useState<'style' | 'layout' | 'input' | null>('input');
      return <Composer {...props} onSend={onSend} images={['/a.png', '/b.png']} pinnedImage="/board.png"
        attachmentPurposes={purposes} pinnedPurpose={pinned}
        onAttachmentPurposeAt={(index, purpose) => setPurposes((items) => items.map((item, i) => i === index ? purpose : item))}
        onPinnedPurpose={setPinned} onAttachmentPurpose={(purpose) => setPurposes([purpose, purpose])} />;
    }
    const view = render(<Controlled />);
    const chips = view.getAllByTestId('composer-attachment-chip');
    expect(chips[0].textContent).toContain(schema.content.messages['ui.studio.composer.attachment.purpose_style']);
    expect(chips[1].textContent).toContain(schema.content.messages['ui.studio.composer.attachment.purpose_layout']);
    expect(chips[2].textContent).toContain(schema.content.messages['ui.studio.composer.attachment.purpose_input']);
    const rows = within(view.getByTestId('composer-attachment-purposes')).getAllByTestId('composer-attachment-purpose-row');
    const label = (purpose: string) => schema.content.messages[`ui.studio.composer.attachment.purpose_${purpose}`];
    expect(rows.every((row) => row.classList.contains('flex-wrap'))).toBe(true);
    fireEvent.click(within(rows[1]).getByRole('button', { name: label('input') }));
    expect(chips[0].textContent).toContain(label('style'));
    expect(chips[1].textContent).toContain(label('input'));
    fireEvent.click(within(rows[1]).getByRole('button', { name: label('input') }));
    expect(chips[1].textContent).not.toContain(label('input'));
    fireEvent.click(within(view.getByTestId('composer-attachment-purpose')).getByRole('radio', { name: label('layout') }));
    expect(chips[0].textContent).toContain(label('layout'));
    expect(chips[1].textContent).toContain(label('layout'));
    expect(chips[2].textContent).toContain(label('input'));
    fireEvent.click(within(rows[2]).getByRole('button', { name: label('input') }));
    expect(chips[2].textContent).not.toContain(label('input'));
    expect(onSend).not.toHaveBeenCalled();
  });

  it('disables controls that have no corresponding change handler', () => {
    const view = render(<Composer {...props} images={['/a.png']} pinnedImage="/board.png" />);
    for (const button of within(view.getByTestId('composer-attachment-purposes')).getAllByRole('button')) {
      expect((button as HTMLButtonElement).disabled).toBe(true);
    }
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
