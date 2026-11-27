// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { Composer, type ComposerProps } from '@/components/studio/composer';
import { CHIP_CARD_SQUARE_CLASS, CHIP_CARD_WIDTH_CLASS, CHIP_WIDTH_CLASS, ChipSquare, ContextChip, CutHint, chipLines } from '@/components/studio/composer-chips';
import { useChatStore } from '@/store/chat-store';

// #405 (the scope note): the strips that show what a turn is about (the picture on the stage or board, the pictures
// attached, the mask, the branch, the Skill) sit on a two-column grid and are all exactly as wide, say a repeated word
// once, and show their whole text on hover when it is cut. #697 removed the purpose rows that used to sit under them.
const schema = defaultCatalog.application;
const messages = schema.content.messages as Record<string, string>;
const hints: Array<Record<string, unknown>> = [];

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema, text: (key: string) => (schema.content.messages as Record<string, string>)[key] ?? '' }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: null }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: () => ({ view: null, busy: false, error: null }) }));
vi.mock('@/components/workspace/harness-summary', () => ({ HarnessSummary: () => null }));
vi.mock('@/components/result/result-card-parts', () => ({ openResultDetail: vi.fn() }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children, className, suppressed, triggerProps, ...content }: { children: React.ReactNode; className?: string; suppressed?: boolean; triggerProps?: React.HTMLAttributes<HTMLSpanElement> } & Record<string, unknown>) => {
    hints.push({ ...content, suppressed });
    return <span data-testid="hint-wrapper" data-name={String(content.name ?? '')} data-hint={String(content.hint ?? '')} data-suppressed={String(Boolean(suppressed))} className={className} {...triggerProps}>{children}</span>;
  },
}));
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

beforeEach(() => { vi.stubGlobal('React', React); hints.length = 0; useChatStore.setState({ activeSessionId: 's1', streamingBuffers: {} }); });
afterEach(() => { cleanup(); useChatStore.setState({ activeSessionId: null, streamingBuffers: {} }); });

describe('chipLines', () => {
  it('says a repeated word once: a second line already inside the first is left out', () => {
    expect(chipLines('当前：画布', '画布')).toEqual({ subtitle: null, full: '当前：画布' });
    expect(chipLines('Current: Canvas', 'Canvas')).toEqual({ subtitle: null, full: 'Current: Canvas' });
  });

  it('keeps a second line that says something else, and joins both for the full text', () => {
    expect(chipLines('当前：@img5', '2D v3')).toEqual({ subtitle: '2D v3', full: '当前：@img5 · 2D v3' });
  });

  it('does not mistake a name that merely ends the same way for a repeat', () => {
    expect(chipLines('Skill', 'ill')).toEqual({ subtitle: 'ill', full: 'Skill · ill' });
    expect(chipLines('Skill', 'S')).toEqual({ subtitle: 'S', full: 'Skill · S' });
    expect(chipLines('当前：@img5', '@img5')).toEqual({ subtitle: null, full: '当前：@img5' });
    expect(chipLines('画布', '画布')).toEqual({ subtitle: null, full: '画布' });
  });

  it('has no second line without one', () => {
    expect(chipLines('Skill', undefined)).toEqual({ subtitle: null, full: 'Skill' });
    expect(chipLines('Skill', '  ')).toEqual({ subtitle: null, full: 'Skill' });
  });
});

describe('one chip', () => {
  const chip = (extra: Partial<React.ComponentProps<typeof ContextChip>> = {}) => render(
    <ContextChip testId="chip" accent square={<ChipSquare>x</ChipSquare>} title="当前：画布" subtitle="画布" remove={{ label: '取消选中', onClick: noop }} {...extra} />,
  );

  it('fills the grid cell it is in, whichever element is the outermost one', () => {
    expect(CHIP_WIDTH_CLASS).toBe('w-full min-w-0');
    // Hint wrapper outermost: the wrapper is the grid item and the frame fills it, so both are as wide as the cell.
    chip();
    const frame = screen.getByTestId('chip');
    const wrapper = frame.parentElement as HTMLElement;
    expect(wrapper.getAttribute('data-testid')).toBe('hint-wrapper');
    for (const element of [wrapper, frame]) {
      expect(element.className).toMatch(/\bw-full\b/);
      expect(element.className).toMatch(/\bmin-w-0\b/);
      // No width of its own: not from its content, not capped.
      expect(element.className).not.toMatch(/(^|\s)(max-w-|w-(?!full\b))/);
    }
    cleanup();
    // A chip whose own button carries the hint (an attachment): the frame is outermost and is the grid item.
    chip({ open: { label: '预览附件 1', onClick: noop } });
    expect(screen.getByTestId('chip').className).toMatch(/\bw-full\b/);
    expect(screen.getByTestId('chip').className).toMatch(/\bmin-w-0\b/);
    expect(screen.getByTestId('chip').parentElement?.getAttribute('data-testid')).not.toBe('hint-wrapper');
  });

  it('keeps the remove button at the right edge: the text takes the room that is left', () => {
    chip();
    const frame = screen.getByTestId('chip');
    expect(frame.querySelector('.truncate.font-medium')?.parentElement?.className).toMatch(/\bflex-1\b/);
    expect(frame.lastElementChild?.tagName).toBe('SPAN');
    expect(within(frame).getByRole('button', { name: '取消选中' })).toBeTruthy();
    cleanup();
    chip({ open: { label: '预览附件 1', onClick: noop, testId: 'open' } });
    expect(screen.getByTestId('open').className).toMatch(/\bflex-1\b/);
    expect(screen.getByTestId('open').parentElement?.className).toMatch(/\bflex-1\b/);
  });

  it('merges a repeated second line into one and keeps the picture, the badge slot and the remove button', () => {
    chip({ title: <span className="flex min-w-0 items-center gap-1.5"><i data-testid="badge">2D</i><span className="truncate">当前：画布</span></span>, label: '当前：画布' });
    const frame = screen.getByTestId('chip');
    // Only the title line: the repeated word is not written again.
    expect(frame.querySelectorAll('.truncate.font-medium')).toHaveLength(1);
    expect(frame.querySelectorAll('.truncate.text-\\[11px\\]')).toHaveLength(0);
    expect(frame.textContent).toBe('x2D当前：画布');
    expect(within(frame).getByTestId('badge')).toBeTruthy();
    expect(within(frame).getByRole('button', { name: '取消选中' })).toBeTruthy();
  });

  it('keeps two lines when the second one says something else', () => {
    chip({ title: '当前：@img5', label: undefined, subtitle: '2D v3' });
    expect(screen.getByTestId('chip').textContent).toBe('x当前：@img52D v3');
    expect(screen.getByTestId('chip').querySelectorAll('.truncate')).toHaveLength(2);
  });

  it('is as high as every other chip whether it has one line or two', () => {
    chip({ title: '当前：画布', subtitle: '画布' });
    expect(screen.getByTestId('chip').className).toMatch(/\bh-10\b/);
    cleanup();
    chip({ title: '当前：@img5', subtitle: '2D v3' });
    expect(screen.getByTestId('chip').className).toMatch(/\bh-10\b/);
  });

  it('shows the whole text on hover only when it is cut', () => {
    chip({ title: '上传的图 1', subtitle: '上传 · 参考风格' });
    const wrapper = screen.getByTestId('chip').parentElement as HTMLElement;
    expect(wrapper.getAttribute('data-testid')).toBe('hint-wrapper');
    expect(wrapper.getAttribute('data-name')).toBe('上传的图 1 · 上传 · 参考风格');
    // Nothing is cut (jsdom has no layout: every width is 0), so there is nothing to add on hover.
    fireEvent.pointerEnter(wrapper);
    expect(wrapper.getAttribute('data-suppressed')).toBe('true');
    // The text does not fit: the hover hint opens with the whole text.
    for (const node of screen.getByTestId('chip').querySelectorAll('.truncate')) {
      Object.defineProperty(node, 'scrollWidth', { configurable: true, value: 120 });
      Object.defineProperty(node, 'clientWidth', { configurable: true, value: 80 });
    }
    fireEvent.pointerEnter(wrapper);
    expect(wrapper.getAttribute('data-suppressed')).toBe('false');
    fireEvent.pointerLeave(wrapper);
  });

  it('puts the whole text under the preview name of an attachment when it is cut', () => {
    chip({ title: '上传的图 1', subtitle: '上传 · 参考风格', open: { label: '预览附件 1', onClick: noop, testId: 'open' } });
    const wrapper = screen.getByTestId('open').parentElement as HTMLElement;
    expect(wrapper.getAttribute('data-name')).toBe('预览附件 1');
    expect(wrapper.getAttribute('data-hint')).toBe('');
    for (const node of screen.getByTestId('chip').querySelectorAll('.truncate')) {
      Object.defineProperty(node, 'scrollWidth', { configurable: true, value: 120 });
      Object.defineProperty(node, 'clientWidth', { configurable: true, value: 80 });
    }
    fireEvent.pointerEnter(wrapper);
    expect(screen.getByTestId('open').parentElement?.getAttribute('data-hint')).toBe('上传的图 1 · 上传 · 参考风格');
  });
});

describe('the text that is cut', () => {
  it('shows the whole text on hover only when it is cut', () => {
    render(<CutHint text="Composition reference"><span data-testid="cell" className="truncate">Composition reference</span></CutHint>);
    const wrapper = screen.getByTestId('hint-wrapper');
    expect(wrapper.getAttribute('data-name')).toBe('Composition reference');
    // The wrapper may be narrower than the text, so it fits a cell that is.
    expect(wrapper.className).toMatch(/\bmin-w-0\b/);
    fireEvent.pointerEnter(wrapper);
    expect(wrapper.getAttribute('data-suppressed')).toBe('true');
    Object.defineProperty(screen.getByTestId('cell'), 'scrollWidth', { configurable: true, value: 140 });
    Object.defineProperty(screen.getByTestId('cell'), 'clientWidth', { configurable: true, value: 90 });
    fireEvent.pointerEnter(wrapper);
    expect(wrapper.getAttribute('data-suppressed')).toBe('false');
  });
});

describe('the strips above the box', () => {
  it('lays the stage picture, the board picture and the attachments out as fixed-size cards side by side, wrapping (#593)', () => {
    render(<Composer {...props}
      chip={{ kind: 'image', thumbnail: '/t.png', label: messages['ui.studio.composer.chip.canvas'], handle: '' }}
      images={['/a.png', '/b.png']} pinnedImage="/board.png" />);
    const row = screen.getByTestId('composer-chips');
    // No text chip, so no grid; the picture cards are one wrapping row.
    expect(screen.queryByTestId('composer-chip-grid')).toBeNull();
    const cards = within(row).getByTestId('composer-chip-cards');
    expect(cards.className).toMatch(/\bflex\b/);
    expect(cards.className).toMatch(/\bflex-wrap\b/);
    expect(cards.className).toMatch(/\bgap-1\.5\b/);
    // Four pictures, four cards of the one fixed width, none stretched over the row.
    const items = [...cards.children] as HTMLElement[];
    expect(items).toHaveLength(4);
    const frames = [screen.getByTestId('composer-object-chip'), ...screen.getAllByTestId('composer-attachment-chip')];
    for (const frame of frames) {
      expect(frame.className).toContain(CHIP_CARD_WIDTH_CLASS);
      expect(frame.className).toMatch(/\bflex-col\b/);
      expect(frame.className).not.toMatch(/\bw-full\b/);
    }
    // The picture and the attachments are children of that one row.
    expect(within(row).getAllByTestId('composer-attachment-chip')).toHaveLength(3);
    expect(screen.queryByTestId('composer-attachment-list')).toBeNull();
    // Order: the object first, then the attachments in the order they were added, the board's picture last.
    const order = [...row.querySelectorAll('[data-testid="composer-object-chip"], [data-testid="composer-attachment-chip"]')].map((node) => node.textContent);
    expect(order[0]).toContain(messages['ui.studio.composer.chip.canvas']);
    expect(order[1]).toContain('上传的图 1');
    expect(order[3]).toContain(messages['ui.studio.composer.attachment.pinned']);
  });

  it('stacks each picture card: the thumbnail first, the kind badge under it, the remove button in the corner (#593)', () => {
    render(<Composer {...props} chip={{ kind: 'model', thumbnail: '/t.png', label: '3D v1', handle: '' }} images={['/a.png']} />);
    for (const frame of [screen.getByTestId('composer-object-chip'), screen.getByTestId('composer-attachment-chip')]) {
      const picture = frame.querySelector(`.${CHIP_CARD_SQUARE_CLASS}`) as HTMLElement;
      const badge = within(frame).getByTestId('kind-badge');
      expect(picture).toBeTruthy();
      expect(Boolean(picture.compareDocumentPosition(badge) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true);
      expect(within(frame).getByTestId('chip-card-remove').className).toMatch(/\babsolute\b.*\btop-0\.5\b|\btop-0\.5\b.*\babsolute\b/);
    }
    expect(within(screen.getByTestId('composer-object-chip')).getByTestId('kind-badge').getAttribute('data-kind')).toBe('model');
    expect(within(screen.getByTestId('composer-attachment-chip')).getByTestId('kind-badge').getAttribute('data-kind')).toBe('image');
    // The names stay for screen readers and the controls keep their names.
    expect(within(screen.getByTestId('composer-attachment-chip')).getByRole('button', { name: messages['ui.chat.input.remove_attachment'].replace('{index}', String(schema.chat.context.image_index_base)) })).toBeTruthy();
    expect(screen.getByTestId('composer-attachment-open').getAttribute('aria-label')).toBe(messages['ui.studio.composer.attachment.preview'].replace('{index}', String(schema.chat.context.image_index_base)));
  });

  it('keeps the text chips (mask, branch, Skill) on the two-column grid above the cards', () => {
    render(<Composer {...props} chip={{ kind: 'image', thumbnail: '/t.png', label: messages['ui.studio.composer.chip.canvas'], handle: '' }}
      branch={{ label: '2D v1', onClear: noop }} />);
    const grid = screen.getByTestId('composer-chip-grid');
    expect(grid.className).toMatch(/\bgrid-cols-2\b/);
    expect(within(grid).getByTestId('composer-branch-chip').className).toMatch(/\bw-full\b/);
    expect(Boolean(grid.compareDocumentPosition(screen.getByTestId('composer-chip-cards')) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true);
  });

  it('shows the picture and the kind badge only: the name 当前：画布 is the hint and the screen-reader text (#546)', () => {
    render(<Composer {...props} chip={{ kind: 'image', thumbnail: '/t.png', label: messages['ui.studio.composer.chip.canvas'], handle: '' }} />);
    const chip = screen.getByTestId('composer-object-chip');
    expect(within(chip).getByTestId('kind-badge').getAttribute('data-kind')).toBe('image');
    // The name is there once, for screen readers only; no second line repeats it.
    const name = within(chip).getByTestId('composer-object-name');
    expect(name.textContent).toBe('当前：画布');
    expect(name.className).toMatch(/\bsr-only\b/);
    expect(chip.textContent?.match(new RegExp(messages['ui.studio.composer.chip.canvas'], 'g'))).toHaveLength(1);
    // Hovering the chip names it.
    expect(chip.parentElement?.getAttribute('data-name')).toBe('当前：画布');
    expect(within(chip).getByRole('button', { name: messages['ui.studio.composer.chip.deselect'] })).toBeTruthy();
  });

  it('keeps the whole kind badge, its icon included, now that no name shares the chip', () => {
    render(<Composer {...props} chip={{ kind: 'image', thumbnail: '/t.png', label: messages['ui.studio.composer.chip.canvas'], handle: '' }} />);
    const badge = within(screen.getByTestId('composer-object-chip')).getByTestId('kind-badge');
    expect(badge.className).not.toContain('[&_svg]:hidden');
    expect(badge.textContent).toContain(messages['ui.studio.kind.badge_2d']);
  });

  it('puts the handle of a version chip in its hint and data, not on a line', () => {
    render(<Composer {...props} chip={{ kind: 'image', thumbnail: '/t.png', label: '2D v3', handle: '@img5' }} />);
    const chip = screen.getByTestId('composer-object-chip');
    expect(chip.getAttribute('data-handle')).toBe('@img5');
    expect(within(chip).getByTestId('composer-object-name').textContent).toBe('当前：@img5');
    expect(chip.textContent).not.toContain('2D v3');
  });

  // #697: no purpose rows; the group under the chips holds only the notices and takes no room without them.
  it('keeps the notices under the chips, with no purpose rows and no empty gap when there are none', () => {
    render(<Composer {...props} images={['/a.png', '/b.png']} pinnedImage="/board.png" />);
    const row = screen.getByTestId('composer-chips');
    const group = screen.getByTestId('composer-attachments');
    expect(Boolean(row.compareDocumentPosition(group) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true);
    expect(within(group).queryByTestId('composer-attachment-chip')).toBeNull();
    expect(within(group).queryByTestId('composer-attachment-purposes')).toBeNull();
    expect(within(group).queryAllByRole('button')).toHaveLength(0);
    expect(group.className).toMatch(/\bempty:hidden\b/);
    expect(group.childElementCount).toBe(0);
  });

  it('shows no chip row at all when there is nothing to show', () => {
    render(<Composer {...props} />);
    expect(screen.queryByTestId('composer-chips')).toBeNull();
    expect(screen.queryByTestId('composer-attachments')).toBeNull();
  });

  // #670: before the Skill catalog is read, the Skill chip reads as loading, never as the Skill id.
  it('names the selected Skill as loading until its catalog is read', () => {
    render(<Composer {...props} skill={{ ...props.skill, selectedId: 'three-view' }} />);
    const card = screen.getByTestId('composer-skill-chip');
    expect(card.textContent).toContain(messages['ui.workspace.skills.loading']);
    expect(card.textContent).not.toContain('three-view');
  });
});
