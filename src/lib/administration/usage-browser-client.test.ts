import { describe, expect, it, vi } from 'vitest';
import type { AdministrationProductSchema } from '@/lib/product-schema';
import {
  AdministrationUsageBrowserError,
  createAdministrationUsageBrowserClient,
} from './usage-browser-client';

const schema = {
  api: {
    balance_fields: {
      current_balance: 'available',
      history: 'snapshots',
      balance: 'remaining',
      captured_at: 'observed',
    },
    fields: {
      data: 'records',
      error: 'issue',
      user_id: 'owner',
      user_name: 'display',
      user_email: 'mail',
      total_tasks: 'tasks',
      succeeded_tasks: 'done',
      failed_tasks: 'broken',
      estimated_credits: 'credits',
      last_active_at: 'lastSeen',
      by_type: 'kinds',
      total: 'overall',
      project_group_key: 'team',
      by_group: 'teams',
      amounts: 'spend',
      unit: 'currency',
      unit_label: 'currencyName',
      amount: 'value',
      share_basis_points: 'bp',
    },
    query: {
      days: 'window',
      start_date: 'begin',
      end_date: 'finish',
    },
  },
  messages: {
    internal_error: 'configured internal error',
  },
} as unknown as AdministrationProductSchema['usage'];

describe('administration usage browser client', () => {
  it('从 Schema 生成查询并隐藏用量线协议', async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({
      records: [{
        owner: 'user-1',
        display: 'Alice',
        mail: 'alice@example.test',
        tasks: 3,
        done: 2,
        broken: 1,
        credits: 9,
        lastSeen: '2026-07-26T12:00:00Z',
        kinds: { image: 9 },
      }],
    })));
    const client = createAdministrationUsageBrowserClient({
      schema,
      routes: {
        usage: '/configured/admin/usage',
        balance: '/configured/admin/balance',
      },
      fetcher,
    });

    expect(client.usageURL({ days: 30 })).toBe(
      '/configured/admin/usage?window=30',
    );
    const records = await client.listUsage({
      startDate: '2026-07-01',
      endDate: '2026-07-26',
    });

    expect(fetcher).toHaveBeenCalledWith(
      '/configured/admin/usage?begin=2026-07-01&finish=2026-07-26',
      undefined,
    );
    expect(records).toEqual([{
      userId: 'user-1',
      userName: 'Alice',
      userEmail: 'alice@example.test',
      projectGroupKey: null,
      totalTasks: 3,
      succeededTasks: 2,
      failedTasks: 1,
      estimatedCredits: 9,
      lastActiveAt: '2026-07-26T12:00:00Z',
      byType: { image: 9 },
    }]);
  });

  it('reads the total of all users, per unit once a native quote is among them (#327)', async () => {
    const respond = (body: unknown) => createAdministrationUsageBrowserClient({
      schema,
      routes: { usage: '/u', balance: '/b' },
      fetcher: vi.fn(async () => new Response(JSON.stringify(body))),
    }).usageOverview({ days: 7 });
    await expect(respond({ records: [], overall: { credits: 95 } })).resolves.toEqual({ users: [], total: { estimatedCredits: 95 }, groups: [] });
    const totals = [
      { unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '95' },
      { unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '195' },
    ];
    await expect(respond({ records: [], overall: { price_totals: totals } })).resolves.toEqual({
      users: [], total: { estimatedCredits: 0, priceTotals: totals }, groups: [],
    });
    // An older server without the total: none is shown rather than a wrong sum.
    await expect(respond({ records: [] })).resolves.toEqual({ users: [], total: null, groups: [] });
  });

  it('从 Schema 隐藏余额线协议并保留 HTTP 状态', async () => {
    const successFetcher = vi.fn(async () => new Response(JSON.stringify({
      available: 88,
      snapshots: [{ remaining: 90, observed: '2026-07-25T12:00:00Z' }],
    })));
    const client = createAdministrationUsageBrowserClient({
      schema,
      routes: {
        usage: '/configured/admin/usage',
        balance: '/configured/admin/balance',
      },
      fetcher: successFetcher,
    });

    await expect(client.getBalance()).resolves.toEqual({
      currentBalance: 88,
      history: [{
        balance: 90,
        capturedAt: '2026-07-25T12:00:00Z',
      }],
    });
    expect(successFetcher).toHaveBeenCalledWith(
      '/configured/admin/balance',
      undefined,
    );

    const failureClient = createAdministrationUsageBrowserClient({
      schema,
      routes: {
        usage: '/configured/admin/usage',
        balance: '/configured/admin/balance',
      },
      fetcher: vi.fn(async () => new Response(
        JSON.stringify({ issue: 'configured forbidden' }),
        { status: 403 },
      )),
    });
    await expect(failureClient.listUsage({ days: 7 })).rejects.toEqual(
      new AdministrationUsageBrowserError(403, 'configured forbidden'),
    );
  });

  // #599: spending per project group, read through the Schema field names.
  it('reads project group rows with per-unit amounts and shares', async () => {
    const client = createAdministrationUsageBrowserClient({
      schema,
      routes: { usage: '/u', balance: '/b' },
      fetcher: vi.fn(async () => new Response(JSON.stringify({
        records: [{ owner: 'user-1', team: 'studio-a' }, { owner: 'user-2', team: null }],
        overall: { credits: 0 },
        teams: [
          { team: 'studio-a', tasks: 2, done: 1, broken: 1, spend: [
            { currency: 'legacy.credits', currencyName: 'legacy.credits', value: '40', bp: 4000 },
            { currency: 'tripo.credit', currencyName: 'Tripo credits', value: '0.5', bp: 10000 },
          ] },
          { team: null, tasks: 1, done: 1, broken: 0, spend: [
            { currency: 'legacy.credits', currencyName: 'legacy.credits', value: '0', bp: null },
          ] },
          { team: 'broken-amounts', tasks: 1, done: 0, broken: 0, spend: [
            { currency: 'tripo.credit', currencyName: 'Tripo credits', value: 'NaN', bp: 1.5 },
          ] },
        ],
      }))),
    });
    const overview = await client.usageOverview({ days: 7 });
    expect(overview.users.map((user) => user.projectGroupKey)).toEqual(['studio-a', null]);
    expect(overview.groups).toEqual([
      { projectGroupKey: 'studio-a', totalTasks: 2, succeededTasks: 1, failedTasks: 1, amounts: [
        { unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '40', shareBasisPoints: 4000 },
        { unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '0.5', shareBasisPoints: 10000 },
      ] },
      { projectGroupKey: null, totalTasks: 1, succeededTasks: 1, failedTasks: 0, amounts: [
        { unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '0', shareBasisPoints: null },
      ] },
      { projectGroupKey: 'broken-amounts', totalTasks: 1, succeededTasks: 0, failedTasks: 0, amounts: [] },
    ]);
  });

  it('explicitly present but invalid native totals do not fall back to legacy credits', async () => {
    const client = createAdministrationUsageBrowserClient({
      schema,
      routes: { usage: '/configured/admin/usage', balance: '/configured/admin/balance' },
      fetcher: async () => new Response(JSON.stringify({ records: [{ owner: 'user-1', credits: 99, price_totals: null }] })),
    });
    const [record] = await client.listUsage({ days: 30 });
    expect(record.estimatedCredits).toBe(0);
    expect(record.priceTotals).toEqual([]);
  });
});
