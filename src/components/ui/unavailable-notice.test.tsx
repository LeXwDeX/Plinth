// @vitest-environment jsdom
import React from 'react';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import { UnavailableNotice } from './unavailable-notice';

afterEach(cleanup);

it('shows the icon and the one short line; the full reason waits in the hint (#93)', () => {
  render(<UnavailableNotice label="无法使用，模型超过 10.48 MB" detail="Meshy UV Unwrap 最多接受 10.48 MB。可以先做重拓扑。" testId="notice" />);
  const notice = screen.getByTestId('notice');
  expect(notice.getAttribute('role')).toBe('status');
  expect(screen.getByTestId('unavailable-icon').getAttribute('aria-hidden')).toBe('true');
  expect(screen.getByTestId('unavailable-label').textContent).toBe('无法使用，模型超过 10.48 MB');
  expect(screen.getByTestId('unavailable-detail').className).toContain('sr-only');
  // Inline is the short line only: the explanation is not visible text, it lives in the hover hint (opened by
  // HintTooltip's own 350ms/focus rules, covered in hint-tooltip.test.tsx) and the accessible description.
  expect(screen.queryByTestId('hint-tooltip')).toBeNull();
  expect(notice.getAttribute('tabindex')).toBe('0');
  expect(notice.parentElement!.tagName).toBe('SPAN');
});

it('without a known reason it is the line alone, with no hint', () => {
  render(<UnavailableNotice label="无法使用，服务当前不可用" />);
  expect(screen.getByTestId('unavailable-notice').getAttribute('tabindex')).toBeNull();
  expect(screen.queryByTestId('unavailable-detail')).toBeNull();
});

it('keeps the screen-reader detail inside the notice so it cannot stretch an outer shell (#210)', () => {
  // sr-only is position:absolute. Without a positioned notice it attaches to the nearest positioned ancestor,
  // escapes the scroll container it sits in and makes the page shell scrollable (the shell then slides away).
  render(<UnavailableNotice label="无法使用，当前未启用" detail="该候选尚待验收，当前未启用。" testId="notice" />);
  expect(screen.getByTestId('notice').className.split(/\s+/)).toContain('relative');
});
