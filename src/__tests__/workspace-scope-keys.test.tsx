import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it, vi } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import { useBalance } from '@/hooks/use-balance';
import { useSessionSkillRuns } from '@/hooks/use-session-skill-runs';

let userId: string | null = 'user-a';
const keys: unknown[] = [];

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application }),
}));
vi.mock('@/hooks/use-auth', () => ({
  useAuth: () => ({ user: userId ? { id: userId } : null }),
}));
vi.mock('swr', () => ({
  default: (key: unknown) => {
    keys.push(key);
    return { data: undefined, error: undefined, isLoading: Boolean(key), mutate: () => {} };
  },
}));

function Probe({ sessionId }: { sessionId: string }) {
  useBalance();
  useSessionSkillRuns(sessionId);
  return null;
}

it('isolates balance data by user and SkillRun data by user and session', () => {
  try {
    renderToStaticMarkup(<Probe sessionId="session-a" />);
    expect(keys[0]).toEqual([defaultCatalog.application.runtime.http.legacy_routes.provider_balance, 'user-a']);
    expect(keys[1]).toEqual([
      `${defaultCatalog.application.runtime.http.routes.skill_runs}?session_id=session-a&limit=${defaultCatalog.application.runtime.frontend.session_menu_page_size}`,
      'user-a', 'session-a',
    ]);

    userId = 'user-b';
    renderToStaticMarkup(<Probe sessionId="session-b" />);
    expect(keys[2]).toEqual([defaultCatalog.application.runtime.http.legacy_routes.provider_balance, 'user-b']);
    expect(keys[3]).toEqual([
      `${defaultCatalog.application.runtime.http.routes.skill_runs}?session_id=session-b&limit=${defaultCatalog.application.runtime.frontend.session_menu_page_size}`,
      'user-b', 'session-b',
    ]);

    userId = null;
    renderToStaticMarkup(<Probe sessionId="session-b" />);
    expect(keys.slice(4)).toEqual([null, null]);
  } finally {
    userId = 'user-a';
    keys.length = 0;
  }
});
