import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';
import { fetchPurposeParameters, followsPurpose, purposeParametersRoute, readPurposeParameters } from '@/lib/workspace/purpose-parameters';

// #306: the 加工 panel asks the server what the session purpose gives the parameters that follow it (#292), so an
// untouched one can say "按会话用途：2K" before the step is confirmed. The route is the Schema's; nothing here
// knows a purpose, a platform or a size.

const schema = defaultCatalog.application as unknown as ProductSchema;
const offering = (id: string) => ({ ...defaultCatalog.offerings.find((entry) => entry.id === id)!, available: true }) as unknown as OfferingWire;

describe('the purpose parameters route', () => {
  it('names the session and the Offering in the Schema route', () => {
    expect(purposeParametersRoute(schema, 's/1', 'tripo/processing/rigging-v3'))
      .toBe('/api/v1/chat/sessions/s%2F1/purpose-parameters?offering_id=tripo%2Fprocessing%2Frigging-v3');
  });

  it('follows a route the Schema words differently', () => {
    const renamed = structuredClone(schema);
    renamed.runtime.http.routes.chat_session_purpose_parameters = '/configured/{sessionID}/purpose';
    expect(purposeParametersRoute(renamed, 's1', 'a/b')).toBe('/configured/s1/purpose?offering_id=a%2Fb');
  });

  it('is absent when the Schema has no route (an older server)', () => {
    const bare = structuredClone(schema);
    delete bare.runtime.http.routes.chat_session_purpose_parameters;
    expect(purposeParametersRoute(bare, 's1', 'a')).toBeNull();
  });
});

describe('reading an answer', () => {
  it('takes the parameters object of the answer', () => {
    expect(readPurposeParameters({ session_id: 's1', offering_id: 'a', parameters: { export_texture_size: '2k' } }))
      .toEqual({ export_texture_size: '2k' });
    expect(readPurposeParameters({ parameters: {} })).toEqual({});
  });

  it('reads any other shape as nothing to show', () => {
    for (const payload of [null, undefined, 'x', 4, [], {}, { parameters: null }, { parameters: 'x' }, { parameters: ['2k'] }]) {
      expect(readPurposeParameters(payload)).toEqual({});
    }
  });
});

describe('which Offerings follow the purpose', () => {
  it('are the ones that declare a parameter with presentation.follows_purpose, and no other', () => {
    expect(followsPurpose(offering('tripo/processing/rigging-v3'))).toBe(true);
    expect(followsPurpose(offering('tripo/geometry/decimate-triangle'))).toBe(true);
    expect(followsPurpose(offering('meshy/processing/remesh'))).toBe(false);
    const followers = defaultCatalog.offerings.filter((entry) => followsPurpose(entry as unknown as OfferingWire));
    // The ten Tripo exports of #292, whatever else the Catalog adds later.
    expect(followers.length).toBeGreaterThanOrEqual(10);
    expect(followers.every((entry) => entry.id.startsWith('tripo/'))).toBe(true);
  });

  it('is not an Offering without parameters', () => {
    expect(followsPurpose({ id: 'x' } as unknown as OfferingWire)).toBe(false);
  });
});

describe('asking the server for several Offerings', () => {
  const answer = (parameters: Record<string, unknown>) => ({ ok: true, json: async () => ({ parameters }) }) as unknown as Response;

  it('asks once per Offering and keys the answers by Offering', async () => {
    const fetcher = vi.fn(async (url: RequestInfo | URL) => String(url).includes('rigging')
      ? answer({ export_texture_size: '2k' }) : answer({ export_texture_size: '4k' }));
    const values = await fetchPurposeParameters(schema, 's1', ['tripo/processing/rigging-v3', 'tripo/geometry/decimate-triangle'], fetcher as unknown as typeof fetch);
    expect(values).toEqual({
      'tripo/processing/rigging-v3': { export_texture_size: '2k' },
      'tripo/geometry/decimate-triangle': { export_texture_size: '4k' },
    });
    expect(fetcher.mock.calls.map(([url]) => String(url))).toEqual([
      '/api/v1/chat/sessions/s1/purpose-parameters?offering_id=tripo%2Fprocessing%2Frigging-v3',
      '/api/v1/chat/sessions/s1/purpose-parameters?offering_id=tripo%2Fgeometry%2Fdecimate-triangle',
    ]);
  });

  it('leaves out an Offering whose answer is an error, is not JSON or never came, and keeps the others', async () => {
    const fetcher = vi.fn(async (url: RequestInfo | URL) => {
      const id = decodeURIComponent(String(url).split('offering_id=')[1]);
      if (id === 'a') return { ok: false, status: 404, json: async () => ({ error: 'x' }) } as unknown as Response;
      if (id === 'b') return { ok: true, json: async () => { throw new Error('not json'); } } as unknown as Response;
      if (id === 'c') throw new Error('offline');
      return answer({ size: '2k' });
    });
    expect(await fetchPurposeParameters(schema, 's1', ['a', 'b', 'c', 'd'], fetcher as unknown as typeof fetch)).toEqual({ d: { size: '2k' } });
  });

  it('asks nothing without a route', async () => {
    const bare = structuredClone(schema);
    delete bare.runtime.http.routes.chat_session_purpose_parameters;
    const fetcher = vi.fn();
    expect(await fetchPurposeParameters(bare, 's1', ['a'], fetcher as unknown as typeof fetch)).toEqual({});
    expect(fetcher).not.toHaveBeenCalled();
  });
});
