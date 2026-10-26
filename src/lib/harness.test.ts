import { describe, expect, it, vi } from 'vitest';
import catalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from './product-schema';
import { createHarnessClient, HarnessBrowserError, type HarnessSelection, type HarnessView } from './harness';

const schema = structuredClone(catalog.application) as unknown as ProductSchema;
const selection: HarnessSelection = {
  session_id: 's1', revision: 3, digest: 'server-digest', operation_id: 'image.generate', offering_id: 'o1',
  offering_revision: 'r1', enabled_operations: ['image.generate'], sources: [], fixed_parameters: {},
  fillable_parameters: ['prompt'], allow_prerequisites: true, updated_at: '2026-10-15T00:00:00Z',
};
const view: HarnessView = {
  selection, status: 'ready', reason: '', missing_parameters: [], can_propose: true,
  active_operation: null, estimated_credits: 0, turn: null,
};
function response(body: unknown, status = 200) { return { ok: status < 400, status, json: async () => body } as Response; }

describe('session Harness browser client', () => {
  it('uses the Schema route, encoded session path, same-origin credentials and exact CAS request', async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = [];
    const fetcher = vi.fn(async (url, init) => { calls.push({ url: String(url), init }); return response(view); }) as typeof fetch;
    const client = createHarnessClient(schema, fetcher);
    await client.get('session/one');
    await client.put('session/one', 3, selection);
    const route = schema.runtime.http.routes.chat_session_harness!;
    expect(calls[0].url).toBe(route.replace('{sessionID}', 'session%2Fone'));
    expect(calls[0].init).toMatchObject({ method: 'GET', credentials: 'same-origin' });
    expect(calls[1].url).toBe(calls[0].url);
    expect(calls[1].init).toMatchObject({ method: 'PUT', credentials: 'same-origin' });
    expect(JSON.parse(String(calls[1].init?.body))).toEqual({ expected_revision: 3, selection });
  });

  it('preserves typed 409 details and never retries a compare-and-swap conflict', async () => {
    const fetcher = vi.fn(async () => response({ detail: 'error.harness_conflict' }, 409));
    const client = createHarnessClient(schema, fetcher as typeof fetch);
    await expect(client.put('s1', 3, selection)).rejects.toMatchObject({
      name: 'HarnessBrowserError', status: 409, detail: 'error.harness_conflict',
    });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it('refuses missing or cross-origin configured routes without making a request', async () => {
    const fetcher = vi.fn();
    const missing = structuredClone(schema); delete missing.runtime.http.routes.chat_session_harness;
    await expect(createHarnessClient(missing, fetcher).get('s1')).rejects.toBeInstanceOf(HarnessBrowserError);
    const external = structuredClone(schema); external.runtime.http.routes.chat_session_harness = 'https://external.invalid/{sessionID}';
    await expect(createHarnessClient(external, fetcher).get('s1')).rejects.toBeInstanceOf(HarnessBrowserError);
    expect(fetcher).not.toHaveBeenCalled();
  });
});
