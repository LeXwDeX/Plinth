// @vitest-environment jsdom
import React from 'react';
import { cleanup, render, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { Composer, type ComposerProps } from '@/components/studio/composer';
import { PICKER_UPPERCASE_CLASS } from '@/components/studio/table-popover';
import type { ProductSchema } from '@/lib/product-schema';
import { useChatStore } from '@/store/chat-store';
import { useSessionContextStore } from '@/store/session-context-store';

// #99 / #108 layout contract, asserted on the rendered composer instead of on its source text. jsdom has no
// layout engine and the stylesheet is not loaded, so the contract is checked the way the stylesheet reads it:
// the grid's declared column tracks (`grid-cols-N`, `@[40rem]:grid-cols-N`) and each child's declared span
// (`col-span-full`, `@[40rem]:col-span-N`) are read off the rendered elements, the grid's auto-placement is
// replayed, and the resulting rows are compared with the design (docs/product.md §4.2). The pickers themselves
// are stand-ins that carry their own test id; the context meter is the real component.
const schema = defaultCatalog.application as unknown as ProductSchema;
const mock = vi.hoisted(() => ({ chatModelPicker: true }));

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner' } }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: () => ({ view: null, busy: false, error: null }) }));
vi.mock('@/components/workspace/harness-summary', () => ({ HarnessSummary: () => null }));
vi.mock('@/components/result/result-card-parts', () => ({ openResultDetail: vi.fn() }));
vi.mock('@/components/ui/hint-tooltip', () => ({ HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</> }));
vi.mock('@/components/studio/model-picker', () => ({
  ModelPicker: () => <button type="button" data-testid="picker-model" />,
  GenerationModelNotice: () => null,
}));
vi.mock('@/components/studio/skill-picker', () => ({ SkillPicker: () => <button type="button" data-testid="picker-skill" /> }));
vi.mock('@/components/studio/agent-scope-picker', () => ({ AgentScopePicker: () => <button type="button" data-testid="picker-agent-scope" /> }));
vi.mock('@/components/studio/chat-model-picker', () => ({
  ChatModelPicker: () => (mock.chatModelPicker ? <button type="button" data-testid="picker-chat-model" /> : null),
  ChatModelNotice: () => null,
}));
vi.mock('@/components/studio/reasoning-picker', () => ({ ReasoningPicker: () => <button type="button" data-testid="picker-reasoning" /> }));
vi.mock('@/components/studio/params-popover', () => ({ ParamsPopover: () => <button type="button" data-testid="picker-params" /> }));

const noop = () => undefined;
const props: ComposerProps = {
  value: '', onChange: noop, onSend: noop, onStop: noop, streaming: false, locked: false, canSend: true,
  chip: null, chipSkipped: false, onToggleChip: noop, images: [], onRemoveImage: noop, pinnedImage: null, onUnpin: noop,
  acceptedImages: undefined, onFiles: noop, onPasteImages: noop, editing: false, onCancelEdit: noop, sendCredits: null, mentions: [],
  params: {} as ComposerProps['params'],
  skill: { sessionId: 's1', catalog: null, effective: null, selectedId: null, unavailable: false, loading: false, failed: false, disabled: false, onSelect: noop, onCreated: noop },
};

const WIDE = '@[40rem]:';

/** The declared track count at a width: the largest `grid-cols-N` of that width's prefix. */
function tracks(grid: Element, prefix: string): number | null {
  const found = [...grid.classList]
    .filter((name) => (prefix ? name.startsWith(prefix) : !name.includes(':')) && /grid-cols-\d+$/.test(name))
    .map((name) => Number(name.slice(name.lastIndexOf('-') + 1)));
  return found.length ? found[found.length - 1] : null;
}

/** How many tracks a child covers at a width; `full` is the whole row. */
function span(child: Element, prefix: string, trackCount: number): number {
  const names = [...child.classList].filter((name) => (prefix ? name.startsWith(prefix) : !name.includes(':')) && name.includes('col-span-')).map((name) => name.slice(name.lastIndexOf('col-span-') + 9));
  const last = names[names.length - 1];
  if (last === undefined) return 1;
  return last === 'full' ? trackCount : Number(last);
}

/** Grid auto-placement (row flow, no dense packing) of the children, as `testid` rows. */
function rows(grid: Element, wide: boolean): string[][] {
  const count = (wide ? tracks(grid, WIDE) : null) ?? tracks(grid, '') ?? 1;
  const placed: string[][] = [];
  let row: string[] = [];
  let used = 0;
  for (const child of [...grid.children]) {
    const covers = Math.min(span(child, wide ? WIDE : '', count) || 1, count);
    if (used + covers > count) { placed.push(row); row = []; used = 0; }
    row.push(child.getAttribute('data-testid') ?? '?');
    used += covers;
  }
  if (row.length) placed.push(row);
  return placed;
}

function open(options: { chatModelPicker: boolean }) {
  mock.chatModelPicker = options.chatModelPicker;
  useChatStore.setState({ activeSessionId: 's1', streamingBuffers: {} });
  useSessionContextStore.setState({ context: { s1: { tokens: 356_000, window: 1_000_000, compactions: [] } }, compacting: {} });
  const view = render(<Composer {...props} />);
  return within(view.container).getByTestId('composer-pickers');
}

beforeEach(() => { vi.stubGlobal('React', React); });
afterEach(() => {
  cleanup();
  useChatStore.setState({ activeSessionId: null, streamingBuffers: {} });
  useSessionContextStore.setState({ context: {}, compacting: {} });
});

describe('the composer picker grid (#99)', () => {
  it('declares two tracks when narrow and four when the column is at least 40rem wide', () => {
    const grid = open({ chatModelPicker: true });
    expect(tracks(grid, '')).toBe(2);
    expect(tracks(grid, WIDE)).toBe(4);
    // The container query that switches the tracks is on the grid's wrapper, not on the page.
    expect(grid.parentElement?.classList.contains('@container')).toBe(true);
  });

  it('renders the pickers in reading order, once each, with the context row last', () => {
    const grid = open({ chatModelPicker: true });
    const order = [...grid.children].map((child) => child.getAttribute('data-testid'));
    expect(order).toEqual(['picker-model', 'picker-skill', 'picker-agent-scope', 'composer-params-slot', 'picker-chat-model', 'picker-reasoning', 'composer-context']);
    expect(within(grid).getByTestId('composer-params-slot').querySelector('[data-testid="picker-params"]')).not.toBeNull();
  });

  it('pairs them left to right: models | Skill, Agent mode | parameters, chat model | thinking, then the context row', () => {
    const grid = open({ chatModelPicker: true });
    expect(rows(grid, false)).toEqual([
      ['picker-model', 'picker-skill'],
      ['picker-agent-scope', 'composer-params-slot'],
      ['picker-chat-model', 'picker-reasoning'],
      ['composer-context'],
    ]);
    expect(rows(grid, true)).toEqual([
      ['picker-model', 'picker-skill', 'picker-agent-scope', 'composer-params-slot'],
      ['picker-chat-model', 'picker-reasoning', 'composer-context'],
    ]);
  });

  it('lets the context row fill the whole narrow row and two wide tracks, one picker high', () => {
    const grid = open({ chatModelPicker: true });
    const context = within(grid).getByTestId('composer-context');
    expect(span(context, '', 2)).toBe(2);
    expect(span(context, WIDE, 4)).toBe(2);
    // The figure and the compaction action sit on the row's left and right edges.
    expect(context.classList.contains('justify-between')).toBe(true);
    expect(context.classList.contains('h-7')).toBe(true);
    expect(within(context).getByTestId('composer-context-meter')).toBeTruthy();
    expect(within(context).getByTestId('composer-context-compact')).toBeTruthy();
  });

  it('moves the thinking level up into the chat model slot when only one chat model is offered, leaving no hole before a picker', () => {
    const grid = open({ chatModelPicker: false });
    expect([...grid.children].map((child) => child.getAttribute('data-testid'))).not.toContain('picker-chat-model');
    expect(rows(grid, false)).toEqual([
      ['picker-model', 'picker-skill'],
      ['picker-agent-scope', 'composer-params-slot'],
      ['picker-reasoning'],
      ['composer-context'],
    ]);
    expect(rows(grid, true)).toEqual([
      ['picker-model', 'picker-skill', 'picker-agent-scope', 'composer-params-slot'],
      ['picker-reasoning', 'composer-context'],
    ]);
  });

  it('keeps the parameters button in the bottom grid, beside the Agent mode, and out of the chips and the attachment box', () => {
    const grid = open({ chatModelPicker: true });
    const composer = grid.closest('[data-testid="studio-composer"]') as HTMLElement;
    // The only parameters button is the one in the grid: none in the chips or the attachment box above it.
    const all = composer.querySelectorAll('[data-testid="picker-params"]');
    expect(all).toHaveLength(1);
    expect(all[0].closest('[data-testid="composer-pickers"]')).toBe(grid);
    expect(all[0].closest('[data-testid="composer-chips"], [data-testid="composer-attachments"]')).toBeNull();
    expect(within(grid).getAllByTestId('picker-model')).toHaveLength(1);
  });

  it('puts nothing at an explicit column, so a hidden picker cannot leave a gap', () => {
    const grid = open({ chatModelPicker: true });
    for (const child of grid.children) {
      expect([...child.classList].some((name) => /(^|:)col-start-/.test(name))).toBe(false);
    }
  });
});

describe('the context meter keeps the shared uppercase treatment (#108)', () => {
  it('carries the shared class on the figure and on the compaction action', () => {
    const grid = open({ chatModelPicker: true });
    const classes = (id: string) => within(grid).getByTestId(id).className;
    expect(classes('composer-context-meter')).toContain(PICKER_UPPERCASE_CLASS);
    expect(classes('composer-context-compact')).toContain(PICKER_UPPERCASE_CLASS);
  });
});
