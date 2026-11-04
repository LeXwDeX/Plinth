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
