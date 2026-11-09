// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { TablePopover } from './table-popover';

describe('unavailable picker rows', () => {
  it('lets keyboard users inspect the reason but not select the row', async () => {
    const pick = vi.fn();
    render(<TablePopover label="Skills" value="none" sections={[{
      title: 'Skills', head: ['Name'], columns: '1fr', selectedKey: '', onPick: pick,
      rows: [
        { key: 'disabled', cells: [{ text: 'Disabled' }], disabled: true, disabledReason: 'Schema reason', focusableDisabled: true },
        { key: '', cells: [{ text: 'None' }] },
      ],
    }]} />);
    fireEvent.click(screen.getByRole('button', { name: 'Skills: none' }));
    const row = await screen.findByRole('menuitemradio', { name: 'Disabled: Schema reason' });
    expect(row.getAttribute('aria-disabled')).toBe('true');
    row.focus();
    expect(document.activeElement).toBe(row);
    fireEvent.click(row);
    expect(pick).not.toHaveBeenCalled();
    fireEvent.keyDown(row, { key: 'ArrowDown' });
    await waitFor(() => expect(document.activeElement?.textContent).toContain('None'));
  });

  it('keeps two independent row actions out of the row button and closes only when asked (#152)', async () => {
    const pick = vi.fn();
    const edit = vi.fn();
    const remove = vi.fn();
    render(<TablePopover label="Skills" value="none" testId="picker" sections={[{
      title: 'Skills', head: ['Name'], columns: '1fr', selectedKey: '', onPick: pick,
      rows: [{
        key: 'personal', cells: [{ text: 'Personal' }],
        actions: [
          { label: 'Edit Personal', onClick: edit, testId: 'skill-edit', closeOnClick: true },
          { label: 'Delete Personal', onClick: remove, testId: 'skill-delete' },
        ],
      }],
    }]} />);
    fireEvent.click(screen.getByRole('button', { name: 'Skills: none' }));
    const editButton = await screen.findByTestId('skill-edit');
    expect(editButton.getAttribute('aria-label')).toBe('Edit Personal');
    expect(screen.getByTestId('skill-delete').getAttribute('aria-label')).toBe('Delete Personal');
    editButton.focus();
    expect(document.activeElement).toBe(editButton);
    fireEvent.click(editButton);
    expect(edit).toHaveBeenCalledTimes(1);
    expect(pick).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByTestId('picker-panel')).toBeNull());

    fireEvent.click(screen.getByRole('button', { name: 'Skills: none' }));
    fireEvent.click(await screen.findByTestId('skill-delete'));
    expect(remove).toHaveBeenCalledTimes(1);
    expect(pick).not.toHaveBeenCalled();
    expect(screen.getByTestId('skill-delete')).toBeTruthy();
  });
});

describe('the two-column picker (#223)', () => {
  const section = (title: string, keys: string[], selectedKey: string) => ({
    title, head: ['Name'], columns: '1fr', selectedKey, onPick: vi.fn(), closeOnPick: false,
    rows: keys.map((key) => ({ key, cells: [{ text: key }] })),
  });

  it('lays the tables out as columns and moves between them with the left and right arrows', async () => {
    render(<TablePopover label="Model" value="b · y" testId="picker" columns sections={[
      section('Models', ['a', 'b'], 'b'),
      section('Levels', ['x', 'y', 'z'], 'y'),
    ]} />);
    fireEvent.click(screen.getByRole('button', { name: 'Model: b · y' }));
    const panel = await screen.findByTestId('picker-panel');
    expect(panel.getAttribute('data-layout')).toBe('columns');
    expect(screen.getByRole('menu', { name: 'Models' })).toBeTruthy();
    expect(screen.getByRole('menu', { name: 'Levels' })).toBeTruthy();
    const a = screen.getByRole('menuitemradio', { name: 'a' });
    a.focus();
    // To the other column: its current choice takes focus.
    fireEvent.keyDown(a, { key: 'ArrowRight' });
    expect(document.activeElement?.textContent).toContain('y');
    fireEvent.keyDown(document.activeElement as Element, { key: 'ArrowDown' });
    expect(document.activeElement?.textContent).toContain('z');
    fireEvent.keyDown(document.activeElement as Element, { key: 'ArrowLeft' });
    expect(document.activeElement?.textContent).toContain('b');
    // No column further left: focus stays.
    fireEvent.keyDown(document.activeElement as Element, { key: 'ArrowLeft' });
    expect(document.activeElement?.textContent).toContain('b');
  });

  it('focuses the first row of a column without a current choice', async () => {
    render(<TablePopover label="Model" value="a" columns sections={[
      section('Models', ['a', 'b'], 'a'),
      section('Levels', ['x', 'y'], ''),
    ]} />);
    fireEvent.click(screen.getByRole('button', { name: 'Model: a' }));
    const a = await screen.findByRole('menuitemradio', { name: 'a' });
    a.focus();
    fireEvent.keyDown(a, { key: 'ArrowRight' });
    expect(document.activeElement?.textContent).toContain('x');
  });
});
