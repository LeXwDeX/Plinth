// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { BudgetBadge } from '@/components/studio/budget-badge';
import type { SessionBudget } from '@/lib/chat/budget';
import { formatSchemaText, schemaCountMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import type { ChatSession } from '@/store/chat-store';
import { useChatStore } from '@/store/chat-store';
import { useWorkspaceStore, type TrackedTask } from '@/store/workspace-store';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;
const text = (key: string) => schemaText(schema, key);

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock('swr', () => ({
  default: () => ({ data: { 'text-to-3d': [{ id: 'tripo-v3', model_name: 'Tripo v3' }] } }),
}));

const plan = {
  title: '木椅',
  steps: [{ capability: 'text-to-3d', offeringId: 'tripo-v3', purpose: '主体', estimatedCredits: 80 }],
  correctionCapabilities: [],
};
const budget: SessionBudget = {
  id: 'budget-1', state: 'active', limitCredits: 300, spentCredits: 120, reservedCredits: 0, remainingCredits: 180,
  plan, createdAt: '2026-10-01T00:00:00Z', decidedAt: '2026-10-01T00:01:00Z',
};
const session = (withBudget?: SessionBudget): ChatSession => ({
  id: 's1', title: '会话', createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z',
  ...(withBudget ? { budget: withBudget } : {}),
});
const task = (taskId: string, sessionId: string, state: string, startedAt = Date.parse('2026-10-01T00:02:00Z')): TrackedTask => ({
  taskId, sessionId, capability: 'text-to-3d', prompt: '', state, progress: 0, progressMaximum: 100, terminal: false,
  failure: null, liveError: '', assets: [], downloadables: [], resources: [], resourcesPending: false, notFound: false, updatedAt: 0, startedAt,
});

beforeEach(() => {
  useChatStore.setState({ activeSessionId: null, sessions: [] });
  useWorkspaceStore.setState({ trackedTasks: [] });
});
afterEach(cleanup);

describe('the session budget badge (#103)', () => {
  it('exists only for a session with a plan that was not declined', () => {
    const blank = render(<BudgetBadge />);
    expect(blank.container.innerHTML).toBe('');
    cleanup();

    useChatStore.setState({ activeSessionId: 's1', sessions: [session()] });
    expect(render(<BudgetBadge />).container.innerHTML).toBe('');
    cleanup();

    useChatStore.setState({ activeSessionId: 's1', sessions: [session({ ...budget, state: 'denied' })] });
    expect(render(<BudgetBadge />).container.innerHTML).toBe('');
  });

  it('reads spent / limit on the badge and opens the plan summary with the live Task progress', async () => {
    useChatStore.setState({ activeSessionId: 's1', sessions: [session(budget)] });
    useWorkspaceStore.setState({ trackedTasks: [
      task('t1', 's1', 'SUCCEEDED'),
      // A task of another session must not count toward this plan.
      task('other', 's2', 'RUNNING'),
      // A task from before the plan was approved must not fill its first step.
      task('historical', 's1', 'SUCCEEDED', Date.parse('2026-10-01T00:00:30Z')),
    ] });
    const view = render(<BudgetBadge />);
    const badge = view.getByTestId('studio-budget-badge');
    expect(badge.textContent).toBe(schemaCountMessage(schema, 'ui.studio.budget.badge', 120, { spent: 120, limit: 300 }));
    expect(badge.getAttribute('aria-label')).toBe(text('ui.studio.budget.title'));

    fireEvent.click(badge);
    const card = await view.findByTestId('studio-plan-card');
    expect(view.getByText(text('ui.studio.budget.title'))).toBeTruthy();
    expect(card.getAttribute('data-state')).toBe('active');
    expect(card.textContent).toContain('木椅');
    expect(view.getByTestId('studio-plan-budget').textContent).toBe(
      schemaCountMessage(schema, 'ui.studio.plan.progress', 120, { spent: 120, limit: 300 }),
    );
    // One of the plan's single step has a successful task in this session; the other session's task is ignored.
    expect(view.getByTestId('studio-plan-progress').textContent).toContain(
      formatSchemaText(text('ui.studio.plan.steps_done'), { done: 1, total: 1 }),
    );
    expect(view.getByTestId('studio-plan-state').textContent).toBe(text('ui.studio.plan.state.active'));
  });

  it('words a plan that waits for approval without a spent amount', async () => {
    useChatStore.setState({ activeSessionId: 's1', sessions: [session({ ...budget, state: 'pending' })] });
    const view = render(<BudgetBadge />);
    fireEvent.click(view.getByTestId('studio-budget-badge'));
    const card = await view.findByTestId('studio-plan-card');
    expect(card.getAttribute('data-state')).toBe('pending');
    expect(view.getByTestId('studio-plan-state').textContent).toBe(text('ui.studio.plan.state.pending'));
  });
});
