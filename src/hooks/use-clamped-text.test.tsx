// @vitest-environment jsdom
import React from 'react';
import { act, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { isClamped, useClampedText } from './use-clamped-text';

describe('a clamped text (#546)', () => {
  it('is cut when its content is taller or wider than its box', () => {
    expect(isClamped({ scrollHeight: 48, clientHeight: 32, scrollWidth: 100, clientWidth: 100 })).toBe(true);
    expect(isClamped({ scrollHeight: 32, clientHeight: 32, scrollWidth: 140, clientWidth: 100 })).toBe(true);
    expect(isClamped({ scrollHeight: 32, clientHeight: 32, scrollWidth: 100, clientWidth: 100 })).toBe(false);
    // A sub-pixel rounding difference is not a cut.
    expect(isClamped({ scrollHeight: 33, clientHeight: 32, scrollWidth: 100, clientWidth: 100 })).toBe(false);
    expect(isClamped(null)).toBe(false);
  });

  it('reads the sizes only when measured', () => {
    let api: ReturnType<typeof useClampedText<HTMLSpanElement>> | null = null;
    function Probe() {
      api = useClampedText<HTMLSpanElement>();
      return <span ref={api.ref} data-testid="text" data-clamped={String(api.clamped)}>long text</span>;
    }
    render(<Probe />);
    const node = screen.getByTestId('text');
    Object.defineProperty(node, 'scrollHeight', { configurable: true, value: 60 });
    Object.defineProperty(node, 'clientHeight', { configurable: true, value: 32 });
    expect(node.getAttribute('data-clamped')).toBe('false');
    act(() => api!.measure());
    expect(node.getAttribute('data-clamped')).toBe('true');
  });
});
