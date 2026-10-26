import { describe, expect, it, vi } from 'vitest';
import type {
  AdministrationProductSchema,
  AuthenticationAPISchema,
} from '../product-schema';
import {
  AdministrationUsersBrowserError,
  createAdministrationUsersBrowserClient,
} from './users-browser-client';

const schema: AdministrationProductSchema['users'] = {
  api: {
    fields: {
      data: 'payload',
      success: 'done',
      error: 'problem',
      id: 'accountId',
      username: 'login',
      password: 'credential',
      nickname: 'alias',
      name: 'displayName',
      email: 'mailbox',
      is_admin: 'privileged',
      is_active: 'enabled',
      created_at: 'registered',
      last_login: 'seen',
      open_id: 'externalId',
    },
    path_parameters: { user_id: 'account' },
  },
  limits: {
    username_minimum_characters: 3,
    password_minimum_characters: 9,
  },
  messages: {
    user_not_found: 'configured missing',
    self_deactivate: 'configured self deactivate',
    self_delete: 'configured self delete',
    duplicate_username: 'configured duplicate',
    invalid_username: 'configured username',
    invalid_password: 'configured password',
    invalid_request: 'configured request',
    internal_error: 'configured internal',
  },
  presentation: {
    date_locale: 'configured-locale',
    loading_rows: 2,
    date_time_options: { month: 'short', day: 'numeric' },
    empty_placeholder: 'configured-empty',
    text: {},
  },
};

const authenticationAPI: AuthenticationAPISchema = {
  id_field: 'subjectId',
  username_field: 'username',
  password_field: 'password',
  name_field: 'name',
  email_field: 'email',
  error_field: 'authProblem',
  user_field: 'subject',
  is_admin_field: 'isAdmin',
};

const routes = {
  collection: '/configured/operators',
  item: '/configured/operators/{account}',
  session: '/configured/session',
};

describe('Administration Users Browser Client', () => {
  it('normalizes Schema fields and resolves the current session identity', async () => {
    const fetcher = vi.fn(async (target: RequestInfo | URL) => {
      if (target === routes.session) {
        return new Response(JSON.stringify({
          subject: { subjectId: 'current-account' },
        }), { status: 200 });
      }
      return new Response(JSON.stringify({
        payload: [{
          accountId: 'account-1',
          login: 'alice',
          alias: 'Alice Alias',
          displayName: 'Alice',
          mailbox: 'alice@example.test',
          privileged: true,
          enabled: false,
          registered: '2026-07-26T00:00:00Z',
          seen: null,
          externalId: null,
        }],
      }), { status: 200 });
    });
    const client = createAdministrationUsersBrowserClient({
      schema,
      authenticationAPI,
      routes,
      fetcher,
    });

    await expect(client.listUsers()).resolves.toEqual([{
      id: 'account-1',
      username: 'alice',
      nickname: 'Alice Alias',
      name: 'Alice',
      email: 'alice@example.test',
      isAdmin: true,
      isActive: false,
      createdAt: '2026-07-26T00:00:00Z',
      lastLogin: null,
      openId: null,
    }]);
    await expect(client.currentUserID()).resolves.toBe('current-account');
  });

  it('encodes all mutations through Schema routes and wire fields', async () => {
    const responseUser = {
      accountId: 'account/a b',
      login: 'alice',
      alias: 'Alice',
      displayName: 'Alice',
      mailbox: '',
      privileged: false,
      enabled: true,
      registered: '2026-07-26T00:00:00Z',
      seen: null,
      externalId: null,
    };
    const responses = [
      new Response(JSON.stringify({ payload: responseUser }), { status: 201 }),
      new Response(JSON.stringify({ payload: responseUser }), { status: 200 }),
      new Response(JSON.stringify({ done: true }), { status: 200 }),
    ];
    const fetcher = vi.fn(async () => responses.shift()!);
    const client = createAdministrationUsersBrowserClient({
      schema,
      authenticationAPI,
      routes,
      fetcher,
    });

    await client.createUser({
      username: 'alice',
      password: 'credential',
      name: 'Alice',
      email: '',
      isAdmin: false,
    });
    await client.updateUser('account/a b', {
      name: 'Updated',
      email: 'updated@example.test',
      password: 'new-credential',
      isAdmin: true,
      isActive: false,
    });
    await expect(client.deleteUser('account/a b')).resolves.toEqual({
      success: true,
    });

    expect(fetcher.mock.calls).toEqual([
      [
        routes.collection,
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({
            login: 'alice',
            credential: 'credential',
            displayName: 'Alice',
            mailbox: '',
            privileged: false,
          }),
        }),
      ],
      [
        '/configured/operators/account%2Fa%20b',
        expect.objectContaining({
          method: 'PATCH',
          body: JSON.stringify({
            displayName: 'Updated',
            mailbox: 'updated@example.test',
            credential: 'new-credential',
            privileged: true,
            enabled: false,
          }),
        }),
      ],
      [
        '/configured/operators/account%2Fa%20b',
        expect.objectContaining({ method: 'DELETE' }),
      ],
    ]);
  });

  it('uses the Schema error field and preserves HTTP status', async () => {
    const client = createAdministrationUsersBrowserClient({
      schema,
      authenticationAPI,
      routes,
      fetcher: vi.fn(async () => new Response(JSON.stringify({
        problem: 'configured failure',
      }), { status: 409 })),
    });

    await expect(client.listUsers()).rejects.toEqual(
      new AdministrationUsersBrowserError(409, 'configured failure'),
    );
  });
});
