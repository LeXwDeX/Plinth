// @vitest-environment jsdom

import { cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { resetProjectStore } from '@/store/project-store';
import { useProjects } from './use-projects';

const schema = defaultCatalog.application as unknown as ProductSchema;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));

function jsonResponse(payload: unknown): Response {
  return { ok: true, status: 200, json: async () => payload } as Response;
}

/**
 * A fetch that honours AbortSignal: when the effect aborted its own request,
 * the load never finished and loading stayed true. The automatic load must now
 * settle to loaded=true / loading=false.
 */
function signalAwareFetch(delayMs: number) {
  const abortedAtResolution: boolean[] = [];
  const fetcher = vi.fn((_input: RequestInfo | URL, init?: RequestInit) =>
    new Promise<Response>((resolve, reject) => {
      const signal = init?.signal ?? undefined;
      if (signal?.aborted) {
        reject(new DOMException('Aborted', 'AbortError'));
        return;
      }
      const timer = setTimeout(() => {
        abortedAtResolution.push(signal?.aborted ?? false);
        resolve(jsonResponse({
          projects: [{ id: 'prj_1', name: 'Star Harbor', created_at: 'c', updated_at: 'u' }],
        }));
      }, delayMs);
      signal?.addEventListener('abort', () => {
        clearTimeout(timer);
        reject(new DOMException('Aborted', 'AbortError'));
      }, { once: true });
    }));
  return { fetcher, abortedAtResolution };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  resetProjectStore();
});
beforeEach(resetProjectStore);

describe('useProjects', () => {
  it('finishes the automatic load instead of aborting it when loading changes', async () => {
    const { fetcher, abortedAtResolution } = signalAwareFetch(10);
    vi.stubGlobal('fetch', fetcher);
    const { result } = renderHook(() => useProjects(null));

    await waitFor(() => expect(result.current.loaded).toBe(true));
    expect(result.current.loading).toBe(false);
    expect(result.current.failed).toBe(false);
    expect(result.current.projects.map((project) => project.id)).toEqual(['prj_1']);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(abortedAtResolution).toEqual([false]);
  });
});