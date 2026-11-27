// @vitest-environment jsdom
import React from 'react';
import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import { useStudioStore } from '@/store/studio-store';

import { useNarrowWindowPanel } from './use-narrow-window-panel';

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application }),
}));

function Probe() {
  useNarrowWindowPanel();
  return null;
}

const frames: FrameRequestCallback[] = [];

function resize(width: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: width });
  act(() => {
    window.dispatchEvent(new Event('resize'));
    frames.splice(0).forEach((frame) => frame(0));
  });
}

beforeEach(() => {
  frames.length = 0;
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
    frames.push(callback);
    return frames.length;
  });
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: 1440 });
  useStudioStore.setState({ chatCollapsed: false, panelCollapsed: false });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('a narrow window folds the right panel (#696)', () => {
  it('leaves the panel open while the stage keeps 320 pixels', () => {
    render(<Probe />);
    expect(useStudioStore.getState().panelCollapsed).toBe(false);
  });

  it('folds it when the window gets too narrow, and on opening in a narrow window', () => {
    const { unmount } = render(<Probe />);
    resize(1000);
    expect(useStudioStore.getState().panelCollapsed).toBe(true);
    unmount();
    useStudioStore.setState({ panelCollapsed: false });
    render(<Probe />);
    expect(useStudioStore.getState().panelCollapsed).toBe(true);
  });

  it('keeps a panel the person opens again, and does not reopen it when the window widens', () => {
    render(<Probe />);
    resize(1000);
    act(() => useStudioStore.getState().setPanelCollapsed(false));
    resize(990);
    expect(useStudioStore.getState().panelCollapsed).toBe(false);
    act(() => useStudioStore.getState().setPanelCollapsed(true));
    resize(1600);
    expect(useStudioStore.getState().panelCollapsed).toBe(true);
  });

  it('counts the chat rail, not its 440 floor, while the chat is folded', () => {
    useStudioStore.setState({ chatCollapsed: true });
    Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: 800 });
    render(<Probe />);
    expect(useStudioStore.getState().panelCollapsed).toBe(false);
    // Opening the chat in the same window leaves the stage too little: the panel folds.
    act(() => useStudioStore.setState({ chatCollapsed: false }));
    expect(useStudioStore.getState().panelCollapsed).toBe(true);
  });
});
