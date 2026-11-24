// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import { TaskDrawer } from '@/components/studio/task-center';
import { usePreferences } from '@/store/preference-store';
import type { TrackedTask } from '@/store/workspace-store';

// #546: the drawer lists the newest task first, ten at a time, and pages what the filter kept.
const messages = (defaultCatalog.application as unknown as { content: { messages: Record<string, string> } }).content.messages;
const english = (defaultCatalog.application as unknown as { localization: { bundles: { en: { messages: Record<string, string> } } } }).localization.bundles.en.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));
const studioState = { taskDrawerOpen: true, setTaskDrawerOpen: () => {} };
vi.mock('@/store/studio-store', () => ({
  useStudioStore: (selector: (state: typeof studioState) => unknown) => selector(studioState),
}));
const storeState = { trackedTasks: [] as TrackedTask[], setCurrentObject: () => {}, dismissTask: () => {}, applyTaskDetail: () => {} };
vi.mock('@/store/workspace-store', () => ({
  useWorkspaceStore: (selector: (state: typeof storeState) => unknown) => selector(storeState),
}));
const chatState = { activeSessionId: 'session-a', streamingBuffers: {}, messagesBySession: {} };
vi.mock('@/store/chat-store', () => ({
  useChatStore: (selector: (state: typeof chatState) => unknown) => selector(chatState),
}));
vi.mock('@/hooks/use-session-skill-runs', () => ({
  useSessionSkillRuns: () => ({ runs: [], error: undefined, isLoading: false, reload: () => undefined }),
}));

const task = (index: number, state = 'RUNNING'): TrackedTask => ({
  taskId: `t${index}`, sessionId: 'session-a', capability: 'text-to-image', prompt: `task ${index}`, startedAt: 1_000 + index,
  state, progress: 40, progressMaximum: 100, terminal: state === 'SUCCEEDED' || state === 'FAILED',
  failure: state === 'FAILED' ? { failureClass: 'provider', detail: '' } as unknown as TrackedTask['failure'] : null,
  liveError: '', assets: [], downloadables: [], resources: [], resourcesPending: false, notFound: false, updatedAt: 0,
});
const rows = () => screen.getAllByTestId('workspace-task-card').map((row) => row.textContent ?? '');
const pageSize = (defaultCatalog.application as unknown as { runtime: { frontend: { task_list_page_size: number } } }).runtime.frontend.task_list_page_size;

beforeEach(() => {
  // jsdom has no media queries; the drawer reads one for its desktop notice.
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }));
  act(() => usePreferences.getState().setTaskFilter('all'));
});
afterEach(cleanup);

describe('the task drawer pages (#546)', () => {
  it('declares its page size and the "show more" line in the Schema, in both languages', () => {
    expect(pageSize).toBe(10);
    expect(messages['ui.workspace.tasks.show_more']).toBe('—— 点击查看更多 ——');
    expect(english['ui.workspace.tasks.show_more']).toBeTruthy();
  });

  it('lists the newest task first, whatever order the tasks were tracked in', () => {
    storeState.trackedTasks = [task(1), task(3), task(2)];
    render(<TaskDrawer />);
    expect(rows().map((text) => text.match(/task \d+/)?.[0])).toEqual(['task 3', 'task 2', 'task 1']);
  });

  it('keeps a row in place when its task finishes', () => {
    storeState.trackedTasks = [task(1), task(2), task(3)];
    const view = render(<TaskDrawer />);
    const before = rows().map((text) => text.match(/task \d+/)?.[0]);
    storeState.trackedTasks = [task(1), task(2, 'SUCCEEDED'), task(3)];
    view.rerender(<TaskDrawer />);
    expect(rows().map((text) => text.match(/task \d+/)?.[0])).toEqual(before);
  });

  it('shows ten rows and a "show more" line past ten, and ten more with each click', () => {
    storeState.trackedTasks = Array.from({ length: 23 }, (_, index) => task(index + 1));
    render(<TaskDrawer />);
    expect(rows()).toHaveLength(10);
    expect(rows()[0]).toContain('task 23');
    const more = screen.getByTestId('workspace-task-show-more');
    expect(more.textContent).toBe(messages['ui.workspace.tasks.show_more']);
    fireEvent.click(more);
    expect(rows()).toHaveLength(20);
    fireEvent.click(screen.getByTestId('workspace-task-show-more'));
    expect(rows()).toHaveLength(23);
    expect(screen.queryByTestId('workspace-task-show-more')).toBeNull();
  });

  it('pages the filtered list, and starts again at the first page when the filter changes', () => {
    storeState.trackedTasks = [
      ...Array.from({ length: 15 }, (_, index) => task(index + 1, 'FAILED')),
      ...Array.from({ length: 12 }, (_, index) => task(index + 100)),
    ];
    render(<TaskDrawer />);
    fireEvent.click(screen.getByTestId('workspace-task-show-more'));
    expect(rows()).toHaveLength(20);
    fireEvent.click(screen.getByTestId('workspace-task-filter-failed'));
    expect(rows()).toHaveLength(10);
    expect(rows().every((text) => /task \d{1,2}$|task \d{1,2}\D/.test(text))).toBe(true);
    fireEvent.click(screen.getByTestId('workspace-task-show-more'));
    expect(rows()).toHaveLength(15);
    fireEvent.click(screen.getByTestId('workspace-task-filter-active'));
    expect(rows()).toHaveLength(10);
  });

  it('shows no "show more" line while the list fits on one page', () => {
    storeState.trackedTasks = Array.from({ length: 10 }, (_, index) => task(index + 1));
    render(<TaskDrawer />);
    expect(rows()).toHaveLength(10);
    expect(screen.queryByTestId('workspace-task-show-more')).toBeNull();
  });
});
