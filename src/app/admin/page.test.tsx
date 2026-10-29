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
