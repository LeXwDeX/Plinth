// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { useSessionSkillSelection } from './use-session-skill-selection';
import { writeSessionSkill } from '@/lib/session-skill-storage';

describe('useSessionSkillSelection', () => {
  const data = new Map<string, string>();
  const storage = {
    getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => { data.set(key, value); },
    removeItem: (key: string) => { data.delete(key); },
  };
  beforeEach(() => {
    data.clear();
    Object.defineProperty(window, 'localStorage', { configurable: true, value: storage });
  });
  const skills = [{ id: 'skill-a' }, { id: 'one-shot' }];

  it('restores a valid selection per owner and session and persists changes', async () => {
    writeSessionSkill(storage, 'a', 'one', 'skill-a');
    const { result, rerender, unmount } = renderHook(
      ({ owner, session }) => useSessionSkillSelection(owner, session, skills),
      { initialProps: { owner: 'a', session: 'one' } },
    );
    await waitFor(() => expect(result.current.selection.one).toBe('skill-a'));
    rerender({ owner: 'a', session: 'two' });
    await waitFor(() => expect(result.current.selection.two).toBeNull());
    act(() => result.current.select('two', 'one-shot'));
    rerender({ owner: 'b', session: 'two' });
    await waitFor(() => expect(result.current.selection.two).toBeNull());
    rerender({ owner: 'a', session: 'two' });
    await waitFor(() => expect(result.current.selection.two).toBe('one-shot'));
    act(() => result.current.select('two', null));
    unmount();
    const again = renderHook(() => useSessionSkillSelection('a', 'two', skills));
    await waitFor(() => expect(again.result.current.selection.two).toBeNull());
  });

  it('waits for data, keeps an unavailable selection, and clears a deleted one', async () => {
    writeSessionSkill(storage, 'a', 'one', 'skill-a');
    const { result, rerender } = renderHook(
      ({ catalog }) => useSessionSkillSelection('a', 'one', catalog),
      { initialProps: { catalog: null as typeof skills | null } },
    );
    expect(result.current.selection.one).toBe('skill-a');
    // A model without this Skill hides it from the effective configuration; the stored choice stays.
    rerender({ catalog: skills });
    await waitFor(() => expect(result.current.selection.one).toBe('skill-a'));
    expect(data.size).toBe(1);
    writeSessionSkill(storage, 'a', 'two', 'deleted');
    const other = renderHook(() => useSessionSkillSelection('a', 'two', skills));
    await waitFor(() => expect(other.result.current.selection.two).toBeNull());
  });
});
