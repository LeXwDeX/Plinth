// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TableSections } from './table-popover';

// #405: the tables are the content of the settings dialog's tabs; there is no trigger to open and no panel to close any
// more (a pick never closes anything), so each case renders the tables themselves.
afterEach(() => cleanup());

describe('unavailable picker rows', () => {
  it('lets keyboard users inspect the reason but not select the row', async () => {
    const pick = vi.fn();
    render(<TableSections sections={[{
      title: 'Skills', head: ['Name'], columns: '1fr', selectedKey: '', onPick: pick,
      rows: [
        { key: 'disabled', cells: [{ text: 'Disabled' }], disabled: true, disabledReason: 'Schema reason', focusableDisabled: true },
        { key: '', cells: [{ text: 'None' }] },
      ],
    }]} />);
    const row = await screen.findByRole('menuitemradio', { name: 'Disabled: Schema reason' });
    expect(row.getAttribute('aria-disabled')).toBe('true');
    row.focus();
    expect(document.activeElement).toBe(row);
    fireEvent.click(row);
    expect(pick).not.toHaveBeenCalled();
    fireEvent.keyDown(row, { key: 'ArrowDown' });
    await waitFor(() => expect(document.activeElement?.textContent).toContain('None'));
  });

  it('keeps two independent row actions out of the row button and leaves the tables where they are (#152)', async () => {
    const pick = vi.fn();
    const edit = vi.fn();
    const remove = vi.fn();
    render(<TableSections testId="picker-panel" sections={[{
      title: 'Skills', head: ['Name'], columns: '1fr', selectedKey: '', onPick: pick,
      rows: [{
        key: 'personal', cells: [{ text: 'Personal' }],
        actions: [
          { label: 'Edit Personal', onClick: edit, testId: 'skill-edit' },
          { label: 'Delete Personal', onClick: remove, testId: 'skill-delete' },
        ],
      }],
    }]} />);
    const editButton = await screen.findByTestId('skill-edit');
    expect(editButton.getAttribute('aria-label')).toBe('Edit Personal');
    expect(screen.getByTestId('skill-delete').getAttribute('aria-label')).toBe('Delete Personal');
    editButton.focus();
    expect(document.activeElement).toBe(editButton);
    fireEvent.click(editButton);
    expect(edit).toHaveBeenCalledTimes(1);
    expect(pick).not.toHaveBeenCalled();
    // The tables are a tab's content, not a popover: running an action does not take them away.
    expect(screen.getByTestId('picker-panel')).toBeTruthy();
    fireEvent.click(screen.getByTestId('skill-delete'));
    expect(remove).toHaveBeenCalledTimes(1);
    expect(pick).not.toHaveBeenCalled();
    expect(screen.getByTestId('skill-delete')).toBeTruthy();
    expect(screen.getByTestId('picker-panel')).toBeTruthy();
  });

  it('keeps a pick in place: the row is picked and the table stays', async () => {
    const pick = vi.fn();
    render(<TableSections testId="picker-panel" sections={[{
      title: 'Skills', head: ['Name'], columns: '1fr', selectedKey: '', onPick: pick,
      rows: [{ key: 'a', cells: [{ text: 'A' }] }, { key: 'b', cells: [{ text: 'B' }] }],
    }]} />);
    fireEvent.click(await screen.findByRole('menuitemradio', { name: 'B' }));
    expect(pick).toHaveBeenCalledWith('b');
    expect(screen.getByTestId('picker-panel')).toBeTruthy();
    expect(screen.getAllByRole('menuitemradio')).toHaveLength(2);
  });
});

describe('the two-column picker (#223)', () => {
  const section = (title: string, keys: string[], selectedKey: string) => ({
    title, head: ['Name'], columns: '1fr', selectedKey, onPick: vi.fn(),
    rows: keys.map((key) => ({ key, cells: [{ text: key }] })),
  });

  it('lays the tables out as columns and moves between them with the left and right arrows', async () => {
    render(<TableSections testId="picker-panel" columns sections={[
      section('Models', ['a', 'b'], 'b'),
      section('Levels', ['x', 'y', 'z'], 'y'),
    ]} />);
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
    render(<TableSections columns sections={[
      section('Models', ['a', 'b'], 'a'),
      section('Levels', ['x', 'y'], ''),
    ]} />);
    const a = await screen.findByRole('menuitemradio', { name: 'a' });
    a.focus();
    fireEvent.keyDown(a, { key: 'ArrowRight' });
    expect(document.activeElement?.textContent).toContain('x');
  });

  it('is plain stacked tables, with no column layout mark, when there is one table or no column mode', async () => {
    const { rerender } = render(<TableSections testId="picker-panel" columns sections={[section('Levels', ['x'], 'x')]} />);
    expect((await screen.findByTestId('picker-panel')).getAttribute('data-layout')).toBeNull();
    rerender(<TableSections testId="picker-panel" sections={[section('2D', ['a'], 'a'), section('3D', ['b'], 'b')]} />);
    expect(screen.getByTestId('picker-panel').getAttribute('data-layout')).toBeNull();
  });
});
