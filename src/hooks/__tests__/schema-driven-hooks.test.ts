import { describe, expect, it } from 'vitest';
import { projectAuthenticationSession } from '../use-auth';
import { projectProviderBalances } from '../use-balance';
import type { AuthenticationAPISchema } from '@/lib/product-schema';

describe('schema-driven hooks', () => {
  it('projects a schema-named authentication session', () => {
    const api = {
      id_field: 'employee_id',
      name_field: 'display_name',
      email_field: 'mail',
      user_field: 'member',
      is_admin_field: 'privileged',
    } as AuthenticationAPISchema;

    expect(projectAuthenticationSession({
      member: {
        employee_id: 'user-7',
        display_name: 'Schema User',
        mail: 'schema@example.test',
      },
      privileged: true,
      user: { id: 'default-fields-must-not-leak' },
    }, api)).toEqual({
      user: {
        id: 'user-7',
        name: 'Schema User',
        email: 'schema@example.test',
      },
      isAdmin: true,
    });
  });

  it('projects schema-named provider balances for every configured account', () => {
    const accounts = projectProviderBalances(
      {
        accounts: [
          { provider: 'meshy', balance: 999 },
          { provider: 'tripo', balance: 'not-a-number' },
        ],
      },
      {
        accounts_field: 'accounts',
        provider_field: 'provider',
        balance_field: 'balance',
        error_field: 'error',
        accounts: [
          {
            account_id: 'meshy',
            label: 'MeshyAI',
            consumption_estimates: [
              { capability: 'text-to-3d', credits: 20 },
            ],
          },
          { account_id: 'tripo', label: 'Tripo' },
        ],
      },
      { 'text-to-3d': 'Text to 3D' },
    );
    expect(accounts).toHaveLength(2);
    expect(accounts[0]).toMatchObject({
      provider: 'meshy',
      label: 'MeshyAI',
      balance: 999,
      estimates: [
        { capability: 'text-to-3d', label: 'Text to 3D', credits: 20, times: 49 },
      ],
    });
    expect(accounts[1].balance).toBeUndefined();
  });

  it('keeps malformed native balances unknown unless the full unit quote is valid', () => {
    const project = (native_balance: unknown) => projectProviderBalances(
      { accounts: [{ provider: 'hyper3d', price_mode: 'native', balance: 999, native_balance }] },
      {
        accounts_field: 'accounts', provider_field: 'provider', balance_field: 'balance', error_field: 'error',
        accounts: [{ account_id: 'hyper3d', label: 'Hyper3D', consumption_estimates: [{ capability: 'text-to-3d', credits: 20 }] }],
      },
      { 'text-to-3d': 'Text to 3D' },
    )[0];

    expect(project({ unit: 'hyper3d.credit', unit_label: 'Hyper3D credits', amount: '0.5' }).nativeBalance)
      .toEqual({ unit: 'hyper3d.credit', unit_label: 'Hyper3D credits', amount: '0.5' });
    for (const malformed of [
      { amount: '0.5' },
      { unit: 'hyper3d.credit', amount: '0.5' },
      { unit: 'hyper3d.credit', unit_label: '', amount: '0.5' },
      { unit: 'bad unit', unit_label: 'Credits', amount: '0.5' },
    ]) {
      expect(project(malformed).nativeBalance).toBeNull();
    }
    expect(project({ amount: '0.5' }).balance).toBeUndefined();
    expect(project({ amount: '0.5' }).estimates).toEqual([]);
  });
});
