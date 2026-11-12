// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render } from '@testing-library/react';

import { StageActionStrip } from './stage-action-strip';

afterEach(cleanup);

function layout(row: HTMLElement, { scrollLeft, clientWidth, scrollWidth }: { scrollLeft: number; clientWidth: number; scrollWidth: number }) {
  Object.defineProperty(row, 'clientWidth', { configurable: true, value: clientWidth });
  Object.defineProperty(row, 'scrollWidth', { configurable: true, value: scrollWidth });
  row.scrollLeft = scrollLeft;
}

describe('the stage action strip (#274)', () => {
  it('shows an arrow only at an end with hidden actions and pages the row', () => {
    const view = render(
      <StageActionStrip previousLabel="prev" nextLabel="next"><button type="button">a</button><button type="button">b</button></StageActionStrip>,
    );
    const row = view.getByTestId('stage-actions');
    const scrollBy = vi.fn();
    row.scrollBy = scrollBy as unknown as typeof row.scrollBy;

    layout(row, { scrollLeft: 0, clientWidth: 200, scrollWidth: 200 });
    act(() => { fireEvent.scroll(row); });
    expect(view.queryByTestId('stage-actions-previous')).toBeNull();
    expect(view.queryByTestId('stage-actions-next')).toBeNull();

    layout(row, { scrollLeft: 0, clientWidth: 200, scrollWidth: 500 });
    act(() => { fireEvent.scroll(row); });
    expect(view.queryByTestId('stage-actions-previous')).toBeNull();
    fireEvent.click(view.getByTestId('stage-actions-next'));
    expect(scrollBy).toHaveBeenCalledWith({ left: 140, behavior: 'smooth' });

    layout(row, { scrollLeft: 300, clientWidth: 200, scrollWidth: 500 });
    act(() => { fireEvent.scroll(row); });
    expect(view.queryByTestId('stage-actions-next')).toBeNull();
    fireEvent.click(view.getByTestId('stage-actions-previous'));
    expect(scrollBy).toHaveBeenLastCalledWith({ left: -140, behavior: 'smooth' });
    expect(view.getByTestId('stage-actions-previous').getAttribute('aria-label')).toBe('prev');
  });
});
