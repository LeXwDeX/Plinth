// @vitest-environment jsdom
import React from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { TablePopover } from './table-popover';

vi.mock('@base-ui/react/popover', () => ({ Popover: {
  Root: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Trigger: (props: React.ButtonHTMLAttributes<HTMLButtonElement>) => <button {...props} />,
  Portal: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  Positioner: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Popup: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
} }));
vi.mock('@/components/ui/hint-tooltip', () => ({ HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</> }));
vi.mock('@/components/ui/overlay', () => ({ anchoredPositionMethod: 'fixed', useAnchoredPositioner: () => '' }));

afterEach(cleanup);

it('exposes independent row actions and keeps row selection and arrow navigation separate', () => {
  const pick = vi.fn();
  const edit = vi.fn();
  const remove = vi.fn();
  render(<TablePopover label="Skills" value="None" sections={[{
    title: 'Skills', head: ['Name'], columns: '1fr', selectedKey: null, onPick: pick,
    rows: [
      { key: 'mine', cells: [{ text: 'Mine' }], disabled: true, actions: [{ label: 'Edit Mine', onClick: edit }], action: { label: 'Delete Mine', onClick: remove } },
      { key: 'first', cells: [{ text: 'First' }] },
      { key: 'second', cells: [{ text: 'Second' }] },
    ],
  }]} />);
  const first = screen.getByRole('menuitemradio', { name: 'First' });
  first.focus();
  fireEvent.keyDown(first, { key: 'ArrowDown' });
  expect(document.activeElement).toBe(screen.getByRole('menuitemradio', { name: 'Second' }));
  fireEvent.click(screen.getByRole('button', { name: 'Edit Mine' }));
  fireEvent.click(screen.getByRole('button', { name: 'Delete Mine' }));
  expect(edit).toHaveBeenCalledOnce();
  expect(remove).toHaveBeenCalledOnce();
  expect(pick).not.toHaveBeenCalled();
});
