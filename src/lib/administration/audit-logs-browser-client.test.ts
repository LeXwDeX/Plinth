import { describe, expect, it, vi } from 'vitest';
import type { AdministrationProductSchema } from '../product-schema';
import {
  AdministrationAuditLogsBrowserError,
  createAdministrationAuditLogsBrowserClient,
} from './audit-logs-browser-client';

const schema: AdministrationProductSchema['audit_logs'] = {
  api: {
    fields: {
      data: 'records',
      error: 'problem',
      total: 'matches',
      page: 'current',
      page_size: 'limit',
      id: 'event_id',
      action: 'operation',
      user_id: 'actor_id',
      user_name: 'actor_name',
      resource_type: 'subject_type',
      resource_id: 'subject_id',
      detail: 'payload',
      ip_address: 'client_ip',
      user_agent: 'client_agent',
      created_at: 'occurred_at',
    },
    query: {
      page: 'cursor',
      page_size: 'take',
      user_id: 'actor',
      action: 'operation',
      from: 'after',
      to: 'before',
    },
  },
  limits: {
    default_page: 1,
    default_page_size: 50,
    maximum_page_size: 100,
  },
  messages: { internal_error: 'configured failure' },
  presentation: {
    default_recent_days: 3,
    loading_rows: 5,
    user_id_preview_characters: 8,
    resource_id_preview_characters: 12,
    preview_suffix: '...',
    empty_placeholder: '-',
    date_locale: 'en-US',
    date_time_options: { year: 'numeric' },
    action_badge_classes: { login: 'login' },
    action_prefix_badge_classes: { admin_: 'admin' },
    default_badge_class: 'default',
    text: {},
  },
};

describe('administration audit logs browser client', () => {
  it('hides configured query and response wire fields', async () => {
    const fetcher = vi.fn(async (
      _input: RequestInfo | URL,
      _init?: RequestInit,
    ) => new Response(JSON.stringify({
      records: [{
        event_id: 'audit-1',
        operation: 'login',
        actor_id: 'user-1',
        actor_name: 'Alice',
        subject_type: 'session',
        subject_id: 'session-1',
        payload: '{"ok":true}',
        client_ip: '192.0.2.1',
        client_agent: 'browser-test',
        occurred_at: '2026-07-26T12:00:00Z',
      }],
      matches: 7,
      current: 2,
      limit: 5,
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }));
    const client = createAdministrationAuditLogsBrowserClient({
      schema,
      route: '/custom/audits',
      fetcher,
    });
    const from = new Date('2026-07-20T00:00:00Z');
    const to = new Date('2026-07-21T23:59:59.999Z');

    const page = await client.listAuditLogs({
      page: 2,
      pageSize: 5,
      userID: 'user-1',
      action: 'login',
      from,
      to,
    });

    expect(fetcher).toHaveBeenCalledTimes(1);
    const [target, init] = fetcher.mock.calls[0];
    expect(String(target)).toBe(
      '/custom/audits?cursor=2&take=5&actor=user-1&operation=login' +
      '&after=2026-07-20T00%3A00%3A00.000Z' +
      '&before=2026-07-21T23%3A59%3A59.999Z',
    );
    expect(init).toEqual({ method: 'GET' });
    expect(page).toEqual({
      data: [{
        id: 'audit-1',
        action: 'login',
        userId: 'user-1',
        userName: 'Alice',
        resourceType: 'session',
        resourceId: 'session-1',
        detail: '{"ok":true}',
        ipAddress: '192.0.2.1',
        userAgent: 'browser-test',
        createdAt: '2026-07-26T12:00:00Z',
      }],
      total: 7,
      page: 2,
      pageSize: 5,
    });
  });

  it('uses configured error field and message fallback', async () => {
    const configured = createAdministrationAuditLogsBrowserClient({
      schema,
      route: '/custom/audits',
      fetcher: async () => new Response(
        JSON.stringify({ problem: 'custom forbidden' }),
        { status: 403 },
      ),
    });
    await expect(configured.listAuditLogs({
      page: 1,
      pageSize: 50,
    })).rejects.toEqual(
      new AdministrationAuditLogsBrowserError(403, 'custom forbidden'),
    );

    const fallback = createAdministrationAuditLogsBrowserClient({
      schema,
      route: '/custom/audits',
      fetcher: async () => new Response('not-json', { status: 500 }),
    });
    await expect(fallback.listAuditLogs({
      page: 1,
      pageSize: 50,
    })).rejects.toEqual(
      new AdministrationAuditLogsBrowserError(500, 'configured failure'),
    );
  });
});
