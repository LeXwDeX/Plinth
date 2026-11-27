// @vitest-environment jsdom
import React from 'react';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { TaskCenterButton, TaskDrawer } from '@/components/studio/task-center';
import { schemaCountMessage, type ProductSchema } from '@/lib/product-schema';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore, type TrackedTask } from '@/store/workspace-store';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;
const text = (key: string) => messages[key] ?? '';

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema, text }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => url, proxyAssetUrl: (url: string) => url }),
}));
const skillRuns = vi.hoisted(() => ({ value: { runs: [] as unknown[], error: null as unknown, isLoading: false, reload: () => {} } }));
vi.mock('@/hooks/use-session-skill-runs', () => ({ useSessionSkillRuns: () => skillRuns.value }));

function task(taskId: string, overrides: Partial<TrackedTask> = {}): TrackedTask {
  return {
    taskId, sessionId: 's1', capability: 'text-to-image', prompt: '', state: 'RUNNING', progress: 40, progressMaximum: 100,
    terminal: false, failure: null, liveError: '', assets: [], downloadables: [], resources: [], resourcesPending: false,
    notFound: false, updatedAt: 0, ...overrides,
  } as TrackedTask;
}

beforeEach(() => {
  useChatStore.setState({ activeSessionId: 's1' });
  useStudioStore.setState({ taskDrawerOpen: true });
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  useWorkspaceStore.setState({ trackedTasks: [] });
  skillRuns.value = { runs: [], error: null, isLoading: false, reload: () => {} };
});

describe('task center labels (#670)', () => {
  it('counts the running tasks in the label, like the badge, with their unit', () => {
    useWorkspaceStore.setState({ trackedTasks: [
      task('t1'),
      task('t2', { state: 'SUCCEEDED', terminal: true, progress: 100 }),
      task('t3', { state: 'SUCCEEDED', terminal: true, progress: 100 }),
    ] });
    render(<TaskCenterButton />);
    const button = screen.getByTestId('workspace-task-center');
    expect(screen.getByTestId('workspace-active-count').textContent).toBe('1');
    const label = button.getAttribute('aria-label') ?? '';
    expect(label).toContain(schemaCountMessage(schema, 'ui.workspace.tasks.running_count', 1, { count: 1 }));
    expect(label).not.toContain('（3');
  });

  it('says only "open" when nothing runs', () => {
    useWorkspaceStore.setState({ trackedTasks: [task('t2', { state: 'SUCCEEDED', terminal: true, progress: 100 })] });
    render(<TaskCenterButton />);
    expect(screen.getByTestId('workspace-task-center').getAttribute('aria-label')).toBe(text('ui.workspace.tasks.open'));
  });

  it('says loading, not the drawer title, while the Skill steps are read', () => {
    skillRuns.value = { ...skillRuns.value, isLoading: true };
    render(<TaskDrawer />);
    const status = screen.getAllByRole('status').find((node) => node.querySelector('svg.animate-spin'));
    expect(status?.textContent).toBe(text('ui.loading'));
  });
});
