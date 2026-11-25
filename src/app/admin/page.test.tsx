import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import AdminPage from './page';

const native = (unit: string, unitLabel: string, amount: string) => ({ unit, unit_label: unitLabel, amount });
const state = vi.hoisted(() => ({
  overview: undefined as unknown,
  balance: 100 as number,
  accounts: [] as unknown[],
  keys: [] as unknown[],
}));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: defaultCatalog.application }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'admin' } }) }));
vi.mock('swr', () => ({
  default: (key: unknown) => {
    state.keys.push(key);
    // #623: the per-provider balances come from the provider balance route (an array key).
    if (Array.isArray(key)) return { data: { accounts: state.accounts }, isLoading: false };
    return String(key).includes('balance')
      ? { data: { currentBalance: state.balance, history: [] }, isLoading: false }
      : { data: state.overview, isLoading: false };
  },
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

  // #62 I18N-06: the overview groups digits like the top bar does.
  it('formats the balance with the locale digit grouping', () => {
    state.overview = { users: [], total: { estimatedCredits: 0 } };
    state.accounts = [{ provider: 'meshy', price_mode: 'native', native_balance: native('meshy.credit', 'Meshy credits', '10997') }];
    try {
      expect(balanceCard(renderToStaticMarkup(<AdminPage />))).toContain('10,997 Meshy credits');
    } finally {
      state.accounts = [];
    }
  });
});

const balanceCard = (html: string) => html.split('data-testid="admin-balance"')[1].split('</ul>')[0];

// #623: the card showed one unitless number, the Meshy account's (4,097), and no Tripo or Hyper3D.
describe('administration home balance card', () => {
  it('lists every provider account with its own unit', () => {
    state.overview = { users: [], total: { estimatedCredits: 0 } };
    state.accounts = [
      { provider: 'meshy', price_mode: 'native', native_balance: native('meshy.credit', 'Meshy credits', '4097') },
      { provider: 'tripo', price_mode: 'native', native_balance: native('tripo.credit', 'Tripo credits', '25040') },
      { provider: 'hyper3d', price_mode: 'native', native_balance: native('hyper3d.credit', 'Hyper3D credits', '100') },
    ];
    try {
      const card = balanceCard(renderToStaticMarkup(<AdminPage />));
      expect(card).toContain('MeshyAI');
      expect(card).toContain('4,097 Meshy credits');
      expect(card).toContain('Tripo');
      expect(card).toContain('25,040 Tripo credits');
      expect(card).toContain('Hyper3D');
      expect(card).toContain('100 Hyper3D credits');
    } finally {
      state.accounts = [];
    }
  });

  it('keeps an account it could not read, with a placeholder instead of dropping it', () => {
    state.overview = { users: [], total: { estimatedCredits: 0 } };
    state.accounts = [
      { provider: 'meshy', price_mode: 'native', native_balance: native('meshy.credit', 'Meshy credits', '4097') },
      { provider: 'hyper3d', price_mode: 'native', native_balance: null },
    ];
    try {
      const card = balanceCard(renderToStaticMarkup(<AdminPage />));
      expect(card).toContain('4,097 Meshy credits');
      expect(card.split('Tripo')[1]).toContain('精确余额未知');
      expect(card.split('Hyper3D')[1]).toContain('精确余额未知');
    } finally {
      state.accounts = [];
    }
  });

  // The balance summary route is the only writer of the 7-day balance snapshots;
  // the page still asks it so the snapshots keep being recorded.
  it('still asks the balance summary route', () => {
    state.overview = { users: [], total: { estimatedCredits: 0 } };
    state.keys = [];
    renderToStaticMarkup(<AdminPage />);
    expect(state.keys).toContain(defaultCatalog.application.runtime.http.legacy_routes.admin_balance_summary);
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
