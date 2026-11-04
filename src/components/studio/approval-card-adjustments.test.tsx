// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ApprovalCard } from '@/components/studio/approval-card';
import type { ApprovalCardModel } from '@/lib/chat/approval-card';
import type { ParameterAdjustment } from '@/lib/chat/pending-approvals';

const messages = defaultCatalog.application.content.messages as Record<string, string>;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));
vi.mock('swr', () => ({ default: () => ({ data: undefined, isLoading: false }) }));
vi.mock('@/hooks/use-balance', () => ({ useBalance: () => ({ accounts: [] }) }));
vi.mock('@/hooks/use-handle-references', () => ({ useHandleReferences: () => new Map() }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

const profileAdjustment = (name: string, label: string, recommended: unknown, value: unknown): ParameterAdjustment => ({
  name, label, recommendedValue: recommended, value,
  reason: '道具与场景件的面数预算更低', advice: '建议保留低面数', source: 'profile:game_prop', profile: 'game_prop',
});
const agentAdjustment: ParameterAdjustment = {
  name: 'texture_resolution', label: '贴图分辨率', recommendedValue: '2k', value: '4k',
  reason: 'Agent 的理由', advice: '建议改成 2k', source: 'agent',
};
const card = (parameterAdjustments: ParameterAdjustment[]): ApprovalCardModel => ({
  requestId: 'r1', source: 'detached', status: 'pending', capability: 'text-to-3d', model: 'tripo-v3.1-full',
  provider: 'tripo', providerId: 'tripo', credits: 20, extra: null, prompt: '', inputs: [], sourceTaskId: '',
  expiresAt: '2099-01-01T00:00:00Z', parameterAdjustments,
});
const adjustments = [
  profileAdjustment('face_limit', '面数上限', 100000, 8000),
  profileAdjustment('target_polycount', '目标面数', 100000, 8000),
  agentAdjustment,
];
const renderCard = (items: ParameterAdjustment[] = adjustments) =>
  render(<ApprovalCard model={card(items)} busy={false} now={Date.parse('2026-10-09T00:00:00Z')} onDecide={() => {}} onRerequest={() => {}} />);

beforeEach(() => undefined);
afterEach(cleanup);

describe('the adjustments of one approval step (#103)', () => {
  it('merges the adjustments of one usage profile into one line with one shared reason', () => {
    const view = renderCard();
    const summaries = view.getAllByTestId('studio-approval-profile-summary');
    expect(summaries).toHaveLength(1);
    expect(summaries[0].textContent).toContain(
      messages['ui.studio.approval.card.parameter_summary'].replace('{profile}', '游戏/实时·道具与场景件').replace('{count}', '2'),
    );
    // The reason belongs to the group: it appears once, under the merged line.
    expect(view.getAllByText('道具与场景件的面数预算更低')).toHaveLength(1);
    // Each Agent adjustment keeps its own line, with its own reason.
    const agentLine = view.getByTestId('studio-approval-agent-line');
    expect(agentLine.textContent).toContain(
      messages['ui.studio.approval.card.parameter_agent_line']
        .replace('{name}', '贴图分辨率').replace('{recommended}', '2k').replace('{actual}', '4k'),
    );
    expect(view.getAllByText('Agent 的理由')).toHaveLength(1);
    // The parameter table stays folded until asked for.
    expect(view.queryByTestId('studio-approval-parameter-table')).toBeNull();
    const toggle = view.getByTestId('studio-approval-parameters-toggle');
    expect(toggle.textContent).toBe('展开 3 项参数');
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
  });

  it('opens a compact table of name, recommended → actual and source', () => {
    const view = renderCard();
    fireEvent.click(view.getByTestId('studio-approval-parameters-toggle'));
    const toggle = view.getByTestId('studio-approval-parameters-toggle');
    expect(toggle.textContent).toBe(messages['ui.studio.approval.card.parameters_hide']);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    const table = view.getByTestId('studio-approval-parameter-table');
    expect(table.textContent).toContain(messages['ui.studio.params.label']);
    expect(table.textContent).toContain(messages['ui.studio.approval.card.recommended_value']);
    expect(table.textContent).toContain(messages['ui.studio.approval.card.actual_value']);
    expect(table.textContent).toContain(messages['ui.studio.approval.card.parameter_source']);
    const rows = [...table.querySelectorAll('tbody tr[data-parameter]')];
    expect(rows.map((row) => row.getAttribute('data-parameter'))).toEqual(['face_limit', 'target_polycount', 'texture_resolution']);
    for (const row of rows.slice(0, 2)) {
      expect(row.textContent).toContain('100000 → 8000');
      expect(row.textContent).toContain('用途「游戏/实时·道具与场景件」');
    }
    expect(rows[2].textContent).toContain('2k → 4k');
    expect(rows[2].textContent).toContain(messages['ui.studio.approval.card.parameter_source_agent']);
    expect(rows[2].getAttribute('data-source')).toBe('agent');
    // Closing the table restores the summary-only card.
    fireEvent.click(toggle);
    expect(view.queryByTestId('studio-approval-parameter-table')).toBeNull();
  });

  it('opens a long reason on click instead of growing the card', () => {
    const view = renderCard();
    const profileReason = view.getByTestId('studio-approval-profile-reason');
    expect(profileReason.getAttribute('aria-expanded')).toBe('false');
    expect(profileReason.className).toContain('line-clamp-1');
    fireEvent.click(profileReason);
    expect(profileReason.getAttribute('aria-expanded')).toBe('true');
    expect(profileReason.className).not.toContain('line-clamp-1');
    fireEvent.click(profileReason);
    expect(profileReason.className).toContain('line-clamp-1');

    const agentReason = view.getByTestId('studio-approval-agent-line-reason');
    const button = agentReason;
    expect(button.textContent).toBe('Agent 的理由');
    expect(button.className).toContain('line-clamp-1');
    fireEvent.click(button);
    expect(button.textContent).toContain('Agent 的理由 建议改成 2k');
    expect(button.className).not.toContain('line-clamp-1');
  });

  it('keeps the whole card inside the Schema height and scrolls its inside', () => {
    const view = renderCard();
    const article = view.getByTestId('studio-approval-card');
    expect(article.getAttribute('style')).toContain(
      `max-height: ${defaultCatalog.application.chat.frontend.approval_card_max_height_pixels}px`,
    );
    expect(article.getAttribute('style')).toContain('overflow-y: auto');
  });

  it('shows only the Agent line for an Agent-only adjustment and nothing without adjustments', () => {
    const agentOnly = renderCard([agentAdjustment]);
    expect(agentOnly.queryAllByTestId('studio-approval-profile-summary')).toHaveLength(0);
    expect(agentOnly.getAllByTestId('studio-approval-agent-line')).toHaveLength(1);
    expect(agentOnly.getByTestId('studio-approval-agent-line').textContent).toContain('贴图分辨率：2k → 4k');
    fireEvent.click(agentOnly.getByTestId('studio-approval-parameters-toggle'));
    expect(
      agentOnly.queryAllByTestId('studio-approval-profile-reason'),
    ).toHaveLength(0);
    cleanup();

    const none = renderCard([]);
    expect(none.queryByTestId('studio-approval-parameter-adjustments')).toBeNull();
  });
});
