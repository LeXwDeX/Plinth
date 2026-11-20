// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { Composer, type ComposerProps } from '@/components/studio/composer';
import { CHIP_WIDTH_CAP_CLASS, ChipSquare, ContextChip, chipLines } from '@/components/studio/composer-chips';
import { useChatStore } from '@/store/chat-store';

// #405 (the scope note): the strips that show what a turn is about (the picture on the stage or board, the pictures
// attached, the mask, the branch, the Skill) take their width from their content, never more than half the row, sit
// side by side and wrap, say a repeated word once, and show their whole text on hover when it is cut.
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
  chip: null, chipSkipped: false, onToggleChip: noop, images: [], onRemoveImage: noop, pinnedImage: null, onUnpin: noop,
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
    <ContextChip testId="chip" accent square={<ChipSquare>x</ChipSquare>} title="当前：画布" subtitle="画布" remove={{ label: '本轮不带这个对象', onClick: noop }} {...extra} />,
  );

  it('is never wider than half of its row, whichever element is the outermost one', () => {
    expect(CHIP_WIDTH_CAP_CLASS).toBe('max-w-[calc(50%-3px)]');
    // Hint wrapper outermost: the wrapper carries the cap and the frame only fills it (a second cap would halve it again).
    chip();
    const frame = screen.getByTestId('chip');
    const wrapper = frame.parentElement as HTMLElement;
    expect(wrapper.getAttribute('data-testid')).toBe('hint-wrapper');
    expect(wrapper.className).toContain(CHIP_WIDTH_CAP_CLASS);
    expect(frame.className).not.toContain(CHIP_WIDTH_CAP_CLASS);
    expect(frame.className).toContain('max-w-full');
    cleanup();
    // A chip whose own button carries the hint (an attachment): the frame is outermost and carries the cap.
    chip({ open: { label: '预览附件 1', onClick: noop } });
    expect(screen.getByTestId('chip').className).toContain(CHIP_WIDTH_CAP_CLASS);
    expect(screen.getByTestId('chip').parentElement?.getAttribute('data-testid')).not.toBe('hint-wrapper');
  });

  it('merges a repeated second line into one and keeps the picture, the badge slot and the remove button', () => {
    chip({ title: <span className="flex min-w-0 items-center gap-1.5"><i data-testid="badge">2D</i><span className="truncate">当前：画布</span></span>, label: '当前：画布' });
    const frame = screen.getByTestId('chip');
    // Only the title line: the repeated word is not written again.
    expect(frame.querySelectorAll('.truncate.font-medium')).toHaveLength(1);
    expect(frame.querySelectorAll('.truncate.text-\\[11px\\]')).toHaveLength(0);
    expect(frame.textContent).toBe('x2D当前：画布');
    expect(within(frame).getByTestId('badge')).toBeTruthy();
    expect(within(frame).getByRole('button', { name: '本轮不带这个对象' })).toBeTruthy();
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

describe('the strips above the box', () => {
  it('lays the stage picture, the board picture and the attachments out in one row that wraps', () => {
    render(<Composer {...props}
      chip={{ kind: 'image', thumbnail: '/t.png', label: messages['ui.studio.composer.chip.canvas'], handle: '' }}
      images={['/a.png', '/b.png']} pinnedImage="/board.png" />);
    const row = screen.getByTestId('composer-chips');
    expect(row.className).toMatch(/\bflex-wrap\b/);
    expect(row.className).toMatch(/\bflex\b/);
    // The picture on the stage, the pinned board picture and both uploads are children of that one row.
    expect(within(row).getByTestId('composer-object-chip')).toBeTruthy();
    expect(within(row).getAllByTestId('composer-attachment-chip')).toHaveLength(3);
    // No second row of chips of its own.
    expect(screen.queryByTestId('composer-attachment-list')).toBeNull();
    // Order: the object first, then the attachments in the order they were added, the board's picture last.
    const order = [...row.querySelectorAll('[data-testid="composer-object-chip"], [data-testid="composer-attachment-chip"]')].map((node) => node.textContent);
    expect(order[0]).toContain(messages['ui.studio.composer.chip.canvas']);
    expect(order[1]).toContain('上传的图 1');
    expect(order[3]).toContain(messages['ui.studio.composer.attachment.pinned']);
  });

  it('says 当前：画布 once on the canvas chip and keeps the kind badge', () => {
    render(<Composer {...props} chip={{ kind: 'image', thumbnail: '/t.png', label: messages['ui.studio.composer.chip.canvas'], handle: '' }} />);
    const chip = screen.getByTestId('composer-object-chip');
    expect(chip.textContent?.match(new RegExp(messages['ui.studio.composer.chip.canvas'], 'g'))).toHaveLength(1);
    expect(within(chip).getByTestId('kind-badge').getAttribute('data-kind')).toBe('image');
    expect(chip.textContent).toContain('当前：画布');
  });

  it('lets the kind badge give up its icon, not its "2D", while the column is narrow, so the name fits in half a row', () => {
    render(<Composer {...props} chip={{ kind: 'image', thumbnail: '/t.png', label: messages['ui.studio.composer.chip.canvas'], handle: '' }} />);
    const badge = within(screen.getByTestId('composer-object-chip')).getByTestId('kind-badge');
    // Under 24rem of composer width the icon is hidden (a container query on the composer); the text stays.
    expect(badge.className).toContain('@max-[24rem]:[&_svg]:hidden');
    expect(badge.textContent).toContain(messages['ui.studio.kind.badge_2d']);
    expect(screen.getByTestId('studio-composer').className).toMatch(/(^|\s)@container(\s|$)/);
  });

  it('keeps the two lines of a version chip, whose second line is its version label', () => {
    render(<Composer {...props} chip={{ kind: 'image', thumbnail: '/t.png', label: '2D v3', handle: '@img5' }} />);
    const chip = screen.getByTestId('composer-object-chip');
    expect(chip.textContent).toContain('当前：@img5');
    expect(chip.textContent).toContain('2D v3');
  });

  it('keeps the purpose controls under the chips, with the notices after them', () => {
    render(<Composer {...props} images={['/a.png']} onAttachmentPurpose={noop} />);
    const row = screen.getByTestId('composer-chips');
    const purposes = screen.getByTestId('composer-attachments');
    expect(Boolean(row.compareDocumentPosition(purposes) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true);
    expect(within(purposes).getByTestId('composer-attachment-purposes')).toBeTruthy();
    expect(within(purposes).queryByTestId('composer-attachment-chip')).toBeNull();
  });

  it('shows no chip row at all when there is nothing to show', () => {
    render(<Composer {...props} />);
    expect(screen.queryByTestId('composer-chips')).toBeNull();
    expect(screen.queryByTestId('composer-attachments')).toBeNull();
  });
});
