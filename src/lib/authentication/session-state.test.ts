import { afterEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createAuthenticationBrowserClient } from './browser-client';
import { clearAccountClientStateWithoutSecureContext } from './session-state';

class MemoryStorage {
  private values = new Map<string, string>();
  get length() { return this.values.size; }
  key(index: number) { return [...this.values.keys()][index] ?? null; }
  getItem(key: string) { return this.values.get(key) ?? null; }
  setItem(key: string, value: string) { this.values.set(key, value); }
  removeItem(key: string) { this.values.delete(key); }
  clear() { this.values.clear(); }
}

function stubBrowser(isSecureContext: boolean) {
  const sessionStorage = new MemoryStorage();
  const localStorage = new MemoryStorage();
  sessionStorage.setItem('pending-task:s1', '{}');
  localStorage.setItem('artai.composer.draft.s1', '{"text":"draft"}');
  localStorage.setItem('studio.last-object.s1', '{}');
  localStorage.setItem('art-ai-preferences', '{"language":"en"}');
  vi.stubGlobal('window', { isSecureContext, sessionStorage, localStorage });
  return { sessionStorage, localStorage };
}

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

afterEach(() => vi.unstubAllGlobals());

describe('clearAccountClientStateWithoutSecureContext (#134)', () => {
  it('clears per-account state outside a secure context and keeps UI preferences', () => {
    const { sessionStorage, localStorage } = stubBrowser(false);
    clearAccountClientStateWithoutSecureContext();
    expect(sessionStorage.length).toBe(0);
    expect(localStorage.getItem('artai.composer.draft.s1')).toBeNull();
    expect(localStorage.getItem('studio.last-object.s1')).toBeNull();
    expect(localStorage.getItem('art-ai-preferences')).not.toBeNull();
  });

  it('leaves state alone in a secure context, where Clear-Site-Data applies', () => {
    const { sessionStorage, localStorage } = stubBrowser(true);
    clearAccountClientStateWithoutSecureContext();
    expect(sessionStorage.length).toBe(1);
    expect(localStorage.length).toBe(3);
  });

  it('tolerates unavailable storage', () => {
    const blocked = { get sessionStorage(): never { throw new Error('blocked'); }, get localStorage(): never { throw new Error('blocked'); }, isSecureContext: false };
    vi.stubGlobal('window', blocked);
    expect(() => clearAccountClientStateWithoutSecureContext()).not.toThrow();
  });

  it('runs after a successful logout and login but not after a failed logout', async () => {
    const schema = schemaFixture();
    const { sessionStorage } = stubBrowser(false);
    const failing = createAuthenticationBrowserClient(schema, { fetcher: async () => new Response('', { status: 500 }) });
    await expect(failing.logout()).rejects.toBeDefined();
    expect(sessionStorage.length).toBe(1);

    await createAuthenticationBrowserClient(schema, { fetcher: async () => new Response(null, { status: 200 }) }).logout();
    expect(sessionStorage.length).toBe(0);

    sessionStorage.setItem('pending-task:s2', '{}');
    await createAuthenticationBrowserClient(schema, { fetcher: async () => Response.json({}) }).login('alice', 'pw');
    expect(sessionStorage.length).toBe(0);
  });
});
