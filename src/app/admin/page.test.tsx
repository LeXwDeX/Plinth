import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import AdminPage from './page';

const state = vi.hoisted(() => ({ overview: undefined as unknown, balance: 100 as number }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: defaultCatalog.application }) }));
vi.mock('swr', () => ({
  default: (key: string) => key.includes('balance')
    ? { data: { currentBalance: state.balance, history: [] }, isLoading: false }
    : { data: state.overview, isLoading: false },
}));

const user = (id: string, extra: Record<string, unknown>) => ({
  userId: id, userName: id, userEmail: '', totalTasks: 1, succeededTasks: 1, failedTasks: 0,
  lastActiveAt: '2026-10-22T06:00:00Z', byType: {}, estimatedCredits: 0, ...extra,
});

// #327: the 预估消耗 card read "≈报价未知" whenever a user had a Provider-native quote.
describe('administration home usage card', () => {
  it('adds every user up per unit instead of saying the price is unknown', () => {
    state.overview = {
      users: [
        user('a', { priceTotals: [{ unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '80' }, { unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '90' }] }),
        user('b', { estimatedCredits: 15 }),
      ],
      total: { estimatedCredits: 0, priceTotals: [
        { unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '95' },
        { unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '90' },
      ] },
    };
    const html = renderToStaticMarkup(<AdminPage />);
    expect(html).toContain('data-testid="admin-usage-total"');
    const card = html.split('data-testid="admin-usage-total"')[1].split('</p>')[0];
    expect(card).toContain('95 历史积分估算');
    expect(card).toContain('90 Tripo credits');
    expect(card).not.toContain('报价未知');
  });

  it('keeps the plain credit total without native quotes', () => {
    state.overview = { users: [user('b', { estimatedCredits: 15 })], total: { estimatedCredits: 15 } };
    const card = renderToStaticMarkup(<AdminPage />).split('data-testid="admin-usage-total"')[1].split('</p>')[0];
    expect(card).toContain('≈15');
  });

  // #62 I18N-06: the overview groups digits like the top bar does, in the UI locale.
  it('formats the balance with the locale digit grouping', () => {
    state.overview = { users: [], total: { estimatedCredits: 0 } };
    state.balance = 10997;
    try {
      expect(renderToStaticMarkup(<AdminPage />)).toContain('10,997');
    } finally {
      state.balance = 100;
    }
  });
});

// #599: the spending share per project group sits between the overview cards and the user table.
describe('administration project group spending share', () => {
  const groupsCard = (html: string) => html.split('data-testid="admin-usage-groups"')[1].split('</table>')[0];

  it('lists each unit with its share, the ungrouped row last and no share for a zero total', () => {
    state.overview = {
      users: [
        user('a', { projectGroupKey: 'studio-a', priceTotals: [{ unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '40' }] }),
        user('b', { projectGroupKey: null, estimatedCredits: 60 }),
      ],
      total: { estimatedCredits: 0, priceTotals: [
        { unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '100' },
        { unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '0' },
      ] },
      groups: [
        { projectGroupKey: 'studio-a', totalTasks: 3, succeededTasks: 2, failedTasks: 1, amounts: [
          { unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '40', shareBasisPoints: 4000 },
          { unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '0', shareBasisPoints: null },
        ] },
        { projectGroupKey: null, totalTasks: 1, succeededTasks: 1, failedTasks: 0, amounts: [
          { unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '60', shareBasisPoints: 6000 },
          { unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '0', shareBasisPoints: null },
        ] },
      ],
    };
    const html = renderToStaticMarkup(<AdminPage />);
    const card = groupsCard(html);
    expect(html.split('data-testid="admin-usage-groups"')[0]).toContain('data-testid="admin-usage-total"');
    expect(html).toContain('项目组支出占比');
    expect(html).toContain('按用户当前所属项目组归集');
    expect(html).toContain('占比按各单位分别计算');
    expect(card).toContain('studio-a');
    expect(card).toContain('40 历史积分估算 (40.00%)');
    expect(card).toContain('0 Tripo credits (—)');
    expect(card).toContain('60 历史积分估算 (60.00%)');
    expect(card.indexOf('studio-a')).toBeLessThan(card.indexOf('未分组'));
    expect(card).not.toContain('NaN');
    // The user table names each user's group too.
    const userTable = html.split('data-testid="admin-usage-groups"')[1].split('</table>')[1];
    expect(userTable).toContain('studio-a');
    expect(userTable).toContain('未分组');
  });

  it('shows the empty state without groups, also for an older server that sends none', () => {
    state.overview = { users: [], total: { estimatedCredits: 0 }, groups: [] };
    const card = renderToStaticMarkup(<AdminPage />).split('data-testid="admin-usage-groups"')[1];
    expect(card.split('用户消费排行')[0]).toContain('暂无使用记录');
    state.overview = { users: [], total: { estimatedCredits: 0 } };
    expect(() => renderToStaticMarkup(<AdminPage />)).not.toThrow();
  });
});
