// @vitest-environment jsdom
import React, { useState } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
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
vi.mock('@/components/studio/model-picker', () => ({ ModelPicker: () => null, GenerationModelNotice: () => null }));
vi.mock('@/components/studio/skill-picker', () => ({ SkillPicker: () => null }));
vi.mock('@/components/studio/chat-model-picker', () => ({ ChatModelPicker: () => null, ChatModelNotice: () => null }));
vi.mock('@/components/studio/params-popover', () => ({ ParamsPopover: () => null }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => url }) }));

const noop = () => undefined;
const send = vi.fn();
const props: ComposerProps = {
  value: '', onChange: noop, onSend: send, onStop: noop, streaming: false, locked: false, canSend: true,
  chip: null, chipSkipped: false, onToggleChip: noop, images: [], onRemoveImage: noop, pinnedImage: null, onUnpin: noop,
  acceptedImages: undefined, onFiles: noop, onPasteImages: noop, editing: false, onCancelEdit: noop, sendCredits: null, mentions: [],
  params: {} as ComposerProps['params'],
  skill: { sessionId: 's1', catalog: null, effective: null, selectedId: null, unavailable: false, loading: false, failed: false, disabled: false, onSelect: noop, onCreated: noop },
};
function Editor(overrides: Partial<ComposerProps>) {
  const [value, setValue] = useState('');
  return <Composer {...props} {...overrides} value={value} onChange={setValue} />;
}
beforeEach(() => { vi.stubGlobal('React', React); send.mockClear(); });
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); });

describe('composer expanded editor (#156)', () => {
  it('grows and shrinks with content, bounded by the schema', () => {
    vi.spyOn(HTMLTextAreaElement.prototype, 'scrollHeight', 'get').mockImplementation(function (this: HTMLTextAreaElement) { return this.value.length * 20; });
    schema.chat.frontend.textarea_maximum_height_pixels = 100;
    const view = render(<Editor />);
    const input = screen.getByRole('textbox') as HTMLTextAreaElement;
    expect(input.className).toContain('min-h-11');
    fireEvent.change(input, { target: { value: 'abcdefghi' } });
    expect(input.style.height).toBe('100px');
    expect(input.style.overflowY).toBe('auto');
    schema.chat.frontend.textarea_maximum_height_pixels = 160;
    view.rerender(<Editor />);
    expect(input.style.height).toBe('160px');
    fireEvent.change(input, { target: { value: 'abc' } });
    expect(input.style.height).toBe('60px');
    fireEvent.change(input, { target: { value: '' } });
    expect(input.style.height).toBe('0px');
  });
  it('uses the same rich paste path in the expanded editor', async () => {
    const onPasteImages = vi.fn();
    render(<Editor onPasteImages={onPasteImages} />);
    fireEvent.click(screen.getByRole('button', { name: '展开编辑' }));
    const dialog = await screen.findByRole('dialog');
    const input = dialog.querySelector('textarea')!;
    await waitFor(() => expect(document.activeElement).toBe(input));
    fireEvent.paste(input, { clipboardData: { files: [], getData: (type: string) => type === 'text/plain' ? 'pasted' : '<img src="/image.png">' } });
    expect(input.value).toBe('pasted');
    expect(onPasteImages).toHaveBeenCalledWith([new URL('/image.png', document.baseURI).href]);
  });
  it('keeps the draft and restores focus on Escape; respects Enter and composition', async () => {
    render(<Editor />);
    const input = screen.getByRole('textbox') as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: 'draft' } });
    fireEvent.click(screen.getByRole('button', { name: '展开编辑' }));
    const dialog = await screen.findByRole('dialog');
    const expanded = dialog.querySelector('textarea')!;
    await waitFor(() => expect(document.activeElement).toBe(expanded));
    fireEvent.change(expanded, { target: { value: 'draft changed' } });
    fireEvent.keyDown(expanded, { key: 'Enter', shiftKey: true });
    fireEvent.keyDown(expanded, { key: 'Enter', isComposing: true });
    expect(send).not.toHaveBeenCalled();
    fireEvent.keyDown(expanded, { key: 'Enter' });
    expect(send).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('dialog')).toBeTruthy();
    fireEvent.keyDown(expanded, { key: 'Escape' });
    await act(async () => {});
    expect(input.value).toBe('draft changed');
    await waitFor(() => expect(document.activeElement).toBe(input));
  });
  it('uses mentions in the expanded editor and dismisses the list before the dialog', async () => {
    render(<Editor mentions={[{ handle: '@img1', kind: 'image', source: 'history', thumbnail: '/api/a', url: '/api/a' }]} />);
    fireEvent.click(screen.getByRole('button', { name: '展开编辑' }));
    const dialog = await screen.findByRole('dialog');
    const input = dialog.querySelector('textarea')!;
    await waitFor(() => expect(document.activeElement).toBe(input));
    fireEvent.change(input, { target: { value: '@', selectionStart: 1 } });
    expect(screen.getByRole('listbox')).toBeTruthy();
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(screen.getByRole('dialog')).toBeTruthy();
    fireEvent.change(input, { target: { value: '@i', selectionStart: 2 } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(input.value).toBe('@img1 ');
    expect(send).not.toHaveBeenCalled();
  });
  it('anchors the expanded mention list outside the editor and flips it within the viewport', async () => {
    vi.spyOn(HTMLUListElement.prototype, 'scrollHeight', 'get').mockReturnValue(180);
    render(<Editor mentions={[{ handle: '@img1', kind: 'image', thumbnail: '/api/a', url: '/api/a' }]} />);
    fireEvent.click(screen.getByRole('button', { name: '展开编辑' }));
    const dialog = await screen.findByRole('dialog');
    const input = dialog.querySelector('textarea')!;
    await waitFor(() => expect(document.activeElement).toBe(input));
    let bounds = { top: 320, bottom: window.innerHeight - 88 };
    let dialogBounds = { top: 250, bottom: window.innerHeight - 68 };
    vi.spyOn(input, 'getBoundingClientRect').mockImplementation(() => ({ ...bounds, left: 100, right: 500, width: 400, height: bounds.bottom - bounds.top, x: 100, y: bounds.top, toJSON: () => ({}) }));
    vi.spyOn(dialog, 'getBoundingClientRect').mockImplementation(() => ({ ...dialogBounds, left: 80, right: 520, width: 440, height: dialogBounds.bottom - dialogBounds.top, x: 80, y: dialogBounds.top, toJSON: () => ({}) }));
    fireEvent.change(input, { target: { value: '@', selectionStart: 1 } });
    const list = screen.getByRole('listbox');
    expect(list.style.bottom).toBe('calc(100% + 76px)');
    expect(list.style.maxHeight).toBe('min(13rem, 236px)');
    bounds = { top: 120, bottom: window.innerHeight - 288 };
    dialogBounds = { top: 50, bottom: window.innerHeight - 268 };
    act(() => { window.dispatchEvent(new Event('resize')); });
    expect(list.style.top).toBe('calc(100% + 26px)');
    expect(list.style.maxHeight).toBe('min(13rem, 254px)');
    bounds = { top: 100, bottom: window.innerHeight - 75 };
    dialogBounds = { top: 70, bottom: window.innerHeight - 55 };
    act(() => { window.dispatchEvent(new Event('resize')); });
    expect(list.style.bottom).toBe('calc(100% + 36px)');
    expect(list.style.maxHeight).toBe('min(13rem, 56px)');
  });
  it('keeps the ordinary composer list above its input', () => {
    render(<Editor mentions={[{ handle: '@img1', kind: 'image', thumbnail: '/api/a', url: '/api/a' }]} />);
    const input = screen.getByRole('textbox');
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: '@', selectionStart: 1 } });
    expect(screen.getByRole('listbox').classList.contains('bottom-full')).toBe(true);
  });
  it('positions an already open mention list after opening the editor', async () => {
    render(<Editor mentions={[{ handle: '@img1', kind: 'image', thumbnail: '/api/a', url: '/api/a' }]} />);
    const input = screen.getByRole('textbox');
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: '@', selectionStart: 1 } });
    fireEvent.click(screen.getByRole('button', { name: '展开编辑' }));
    const dialog = await screen.findByRole('dialog');
    await waitFor(() => expect(document.activeElement).toBe(dialog.querySelector('textarea')));
    await waitFor(() => expect(screen.getByRole('listbox').style.maxHeight).not.toBe(''));
  });
});

describe('mention picture previews (#157)', () => {
  it('previews image sources but not board or model entries, and closes on pointer leave', () => {
    vi.useFakeTimers();
    render(<Editor mentions={[
      ...(['history', 'upload', 'version', 'canvas'] as const).map((source) => ({ handle: `@${source}`, kind: 'image' as const, source, thumbnail: `/api/${source}`, url: `/api/${source}` })),
      { handle: '@board', kind: 'image', board: true, thumbnail: '' },
      { handle: '@model', kind: 'model', thumbnail: '/api/model', url: '/api/model' },
    ]} />);
    const input = screen.getByRole('textbox');
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: '@', selectionStart: 1 } });
    for (const [index, option] of screen.getAllByRole('option').entries()) {
      fireEvent.mouseEnter(option);
      act(() => { vi.advanceTimersByTime(schema.chat.frontend.handle_preview_delay_milliseconds); });
      if (index < 4) expect(screen.getByTestId('handle-preview').querySelector('img')?.getAttribute('src')).toBe(`/api/${['history', 'upload', 'version', 'canvas'][index]}`);
      else expect(screen.queryByTestId('handle-preview')).toBeNull();
      fireEvent.mouseLeave(option);
      expect(screen.queryByTestId('handle-preview')).toBeNull();
    }
  });
  it('shows one delayed preview for pointer or keyboard and closes it when picked', () => {
    vi.useFakeTimers();
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
      const list = this.getAttribute('data-testid') === 'composer-mention-list';
      return { left: list ? 500 : 510, right: list ? 800 : 540, top: 300, bottom: 340, width: 300, height: 40, x: 500, y: 300, toJSON: () => ({}) };
    });
    render(<Editor mentions={[
      { handle: '@img1', kind: 'image', source: 'history', thumbnail: '/api/a', url: '/api/a' },
      { handle: '@img2', kind: 'image', source: 'canvas', thumbnail: '/api/b', url: '/api/b' },
      { handle: '@board', kind: 'image', board: true, thumbnail: '' },
    ]} />);
    const input = screen.getByRole('textbox');
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: '@', selectionStart: 1 } });
    const options = screen.getAllByRole('option');
    fireEvent.mouseEnter(options[0]);
    act(() => { vi.advanceTimersByTime(schema.chat.frontend.handle_preview_delay_milliseconds - 1); });
    expect(screen.queryByTestId('handle-preview')).toBeNull();
    act(() => { vi.advanceTimersByTime(1); });
    const firstPreview = screen.getByTestId('handle-preview');
    expect(firstPreview.querySelector('img')?.getAttribute('src')).toBe('/api/a');
    expect(firstPreview.style.visibility).toBe('hidden');
    fireEvent.load(firstPreview.querySelector('img')!);
    expect(firstPreview.style.visibility).toBe('visible');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(screen.queryByTestId('handle-preview')).toBeNull();
    act(() => { vi.advanceTimersByTime(schema.chat.frontend.handle_preview_delay_milliseconds); });
    expect(screen.getByTestId('handle-preview').querySelector('img')?.getAttribute('src')).toBe('/api/b');
    fireEvent.error(screen.getByTestId('handle-preview').querySelector('img')!);
    expect(screen.queryByTestId('handle-preview')).toBeNull();
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    act(() => { vi.advanceTimersByTime(schema.chat.frontend.handle_preview_delay_milliseconds); });
    expect(screen.queryByTestId('handle-preview')).toBeNull();
    fireEvent.keyDown(input, { key: 'ArrowUp' });
    act(() => { vi.advanceTimersByTime(schema.chat.frontend.handle_preview_delay_milliseconds); });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(screen.queryByTestId('handle-preview')).toBeNull();
  });
});
