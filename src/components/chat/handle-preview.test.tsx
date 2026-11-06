// @vitest-environment jsdom
import { act, cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { HandleTag } from '@/components/chat/handle-tag';
import { previewPlacement, previewBesideList } from '@/components/chat/handle-preview';

const schema = defaultCatalog.application;
const refer = vi.hoisted(() => vi.fn());
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => (url.startsWith('/api/') ? `${url}?preview=1` : '') }),
}));
vi.mock('@/store/composer-reference-store', () => ({ useComposerReferenceStore: (selector: (state: object) => unknown) => selector({ refer }) }));

const reference = { handle: '@img25', url: '/api/v1/tasks/t1/assets/a1', taskId: 't1', assetId: 'a1' };
const delay = schema.chat.frontend.handle_preview_delay_milliseconds;

beforeEach(() => { vi.useFakeTimers(); refer.mockClear(); });
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe('the large preview of a picture handle in a message (#87)', () => {
  it('waits the Schema delay (150ms), shows the preview copy and lets clicks through', () => {
    expect(delay).toBe(150);
    const view = render(<HandleTag reference={reference} preview testId="chat-text-handle" />);
    fireEvent.pointerEnter(view.getByTestId('handle-preview-anchor'), { pointerType: 'mouse' });
    act(() => { vi.advanceTimersByTime(delay - 1); });
    expect(document.querySelector('[data-testid="handle-preview"]')).toBeNull();
    act(() => { vi.advanceTimersByTime(1); });
    const preview = document.querySelector<HTMLElement>('[data-testid="handle-preview"]');
    expect(preview).not.toBeNull();
    expect(preview!.className).toContain('pointer-events-none');
    expect(preview!.parentElement).toBe(document.body);
    expect(preview!.querySelector('img')!.getAttribute('src')).toBe('/api/v1/tasks/t1/assets/a1?preview=1');
    expect(preview!.style.maxWidth).toBe(`${schema.chat.frontend.handle_preview_maximum_pixels}px`);
    // The handle is still the button that attaches the picture.
    fireEvent.click(view.getByTestId('chat-text-handle'));
    expect(refer).toHaveBeenCalledWith(reference);
  });

  it('closes when the pointer leaves, and never opens for a pointer that left before the delay', () => {
    const view = render(<HandleTag reference={reference} preview />);
    const anchor = view.getByTestId('handle-preview-anchor');
    fireEvent.pointerEnter(anchor, { pointerType: 'mouse' });
    act(() => { vi.advanceTimersByTime(delay); });
    expect(document.querySelector('[data-testid="handle-preview"]')).not.toBeNull();
    fireEvent.pointerLeave(anchor);
    expect(document.querySelector('[data-testid="handle-preview"]')).toBeNull();
    fireEvent.pointerEnter(anchor, { pointerType: 'mouse' });
    fireEvent.pointerLeave(anchor);
    act(() => { vi.advanceTimersByTime(delay * 2); });
    expect(document.querySelector('[data-testid="handle-preview"]')).toBeNull();
  });

  it('closes when the page scrolls', () => {
    const view = render(<HandleTag reference={reference} preview />);
    fireEvent.pointerEnter(view.getByTestId('handle-preview-anchor'), { pointerType: 'mouse' });
    act(() => { vi.advanceTimersByTime(delay); });
    act(() => { window.dispatchEvent(new Event('scroll')); });
    expect(document.querySelector('[data-testid="handle-preview"]')).toBeNull();
  });

  it('has no preview for a model handle', () => {
    const view = render(<HandleTag reference={{ handle: '@model2', kind: 'model', url: '', taskId: 'm' }} preview />);
    expect(view.queryByTestId('handle-preview-anchor')).toBeNull();
  });
});

describe('previewPlacement', () => {
  const window = { width: 1440, height: 900 };
  it('goes to the right of the handle, vertically centred on it', () => {
    expect(previewPlacement({ left: 100, right: 150, top: 400, bottom: 420 }, 360, window)).toEqual({ left: 162, top: 230 });
  });
  it('goes to the left when the right has no room, and stays inside the window', () => {
    expect(previewPlacement({ left: 1300, right: 1350, top: 10, bottom: 30 }, 360, window)).toEqual({ left: 928, top: 12 });
    expect(previewPlacement({ left: 100, right: 150, top: 880, bottom: 895 }, 360, window).top).toBe(900 - 360 - 12);
  });
});

describe('preview beside a mention list (#157)', () => {
  const list = { left: 300, right: 700, top: 100, bottom: 400 };
  const row = { left: 300, right: 700, top: 200, bottom: 240 };
  it('uses the list boundary and shrinks instead of covering it', () => {
    const result = previewBesideList(row, list, 360, { width: 850, height: 500 })!;
    expect(result.left + result.size).toBeLessThanOrEqual(list.left);
    expect(result.size).toBeLessThan(360);
    expect(result.top).toBeGreaterThanOrEqual(0);
    expect(result.top + result.size).toBeLessThanOrEqual(500);
  });
  it('hides when neither side has usable space', () => {
    expect(previewBesideList(row, { ...list, left: 10, right: 840 }, 360, { width: 850, height: 500 })).toBeNull();
  });
});
