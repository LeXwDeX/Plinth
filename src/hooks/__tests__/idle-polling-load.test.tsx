// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, render } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { createAgentHarnessClient } from '@/lib/agent-params';
import { swrPollingConfig } from '@/lib/polling';
import type { ProductSchema } from '@/lib/product-schema';
import { useChatStore } from '@/store/chat-store';
import { useConnectivityStore } from '@/store/connectivity-store';
import { useWorkspaceStore } from '@/store/workspace-store';
import { useBalance } from '../use-balance';
import { useSessionAgentConsent } from '../use-session-agent-consent';
import { useSessionAgentParams } from '../use-session-agent-params';
import { useSessionPendingApprovals } from '../use-session-pending-approvals';
import { useSessionSkillRuns } from '../use-session-skill-runs';

const base = defaultCatalog.application as unknown as ProductSchema;
const current = vi.hoisted(() => ({ schema: undefined as unknown as ProductSchema }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: current.schema }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner1' } }) }));
vi.mock('sonner', () => ({ toast: { error: vi.fn() } }));

const HOUR = 3_600_000;
let visibility: DocumentVisibilityState = 'visible';

beforeEach(() => {
  visibility = 'visible';
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility);
  useConnectivityStore.setState({ online: true, sessionExpired: false, reconnectTick: 0 });
  useChatStore.setState({ streamingBuffers: {} });
  useWorkspaceStore.setState({ trackedTasks: [] });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

/** The server answers every read with the same thing: an idle session. */
function idleServer() {
  const counts: Record<string, number> = {};
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const path = new URL(String(input), 'http://app.test').pathname;
    const name = path.endsWith('/agent/consent') ? 'consent'
      : path.endsWith('/agent/params') ? 'params'
        : path.endsWith('/skill-runs') ? 'skill-runs'
          : path.endsWith('/pending-approvals') ? 'pending-approvals'
            : path.endsWith('/meshy/balance') ? 'balance' : path;
    counts[name] = (counts[name] ?? 0) + 1;
    const body = name === 'consent' ? { consent: {} }
      : name === 'params' ? { params: {}, parameter_states: {} }
        : name === 'skill-runs' ? { runs: [] }
          : name === 'pending-approvals' ? { items: [] }
            : name === 'balance' ? { accounts: [] } : {};
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
  }));
  return counts;
}

/** What one open studio tab keeps reading: two panels of agent parameters, consent, SkillRuns, approvals, balance. */
function Tab({ schema }: { schema: ProductSchema }) {
  const sessionId = 'session_1';
  const harness = React.useMemo(() => createAgentHarnessClient({
    routes: {
      params: schema.runtime.http.routes.agent_params,
      bind: schema.runtime.http.routes.agent_bind,
      consent: schema.runtime.http.routes.agent_consent,
    },
  }), [schema]);
  useSessionAgentConsent(sessionId, harness, schema.chat.frontend.fallback_poll_milliseconds);
  useSessionAgentParams(sessionId, [], 'image/offering-a');
  useSessionAgentParams(sessionId, [], 'model/offering-b');
  useSessionSkillRuns(sessionId);
  useSessionPendingApprovals(sessionId);
  useBalance();
  return null;
}

/** Moves the clock in steps of five seconds, each in its own `act`: React caps the updates one `act` scope may batch. */
async function advance(milliseconds: number) {
  const step = 5000;
  for (let elapsed = 0; elapsed < milliseconds; elapsed += step) {
    await act(async () => { await vi.advanceTimersByTimeAsync(Math.min(step, milliseconds - elapsed)); });
  }
}

// Each test simulates hours of timers; they are slow when the machine is busy (the default 5 s is not enough).
const SIMULATION_TIMEOUT = 120_000;

/** One simulated hour of a studio tab; the page's SWR configuration is the one web/src/main.tsx installs. */
async function simulateHour(schema: ProductSchema, options: { hideAfter?: number } = {}) {
  current.schema = schema;
  vi.useFakeTimers();
  const counts = idleServer();
  render(
    <SWRConfig value={{ ...swrPollingConfig, provider: () => new Map() }}>
      <Tab schema={schema} />
    </SWRConfig>,
  );
  if (options.hideAfter !== undefined) {
    await advance(options.hideAfter);
    visibility = 'hidden';
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
    await advance(HOUR - options.hideAfter);
  } else {
    await advance(HOUR);
  }
  return counts;
}

const total = (counts: Record<string, number>) => Object.values(counts).reduce((sum, value) => sum + value, 0);

/** The Schema as it was before #385: a factor of one never slows a read down, so every read keeps its own interval. */
function beforeSchema(): ProductSchema {
  const copy = structuredClone(base);
  copy.runtime.frontend.idle_polling = { backoff_factor: 1, max_interval_milliseconds: copy.runtime.frontend.idle_polling.max_interval_milliseconds };
  return copy;
}

describe('requests per hour of an idle studio tab', () => {
  it('drops by an order of magnitude with idle backoff, and a hidden tab sends nothing at all', async () => {
    const before = await simulateHour(beforeSchema());
    cleanup();
    vi.useRealTimers();
    const after = await simulateHour(structuredClone(base));
    cleanup();
    vi.useRealTimers();
    const hidden = await simulateHour(structuredClone(base), { hideAfter: 60_000 });

    // The numbers of this change (also in the pull request): requests of one idle visible tab in one hour.
    console.info(`[#385] idle tab, one hour: before ${JSON.stringify(before)} = ${total(before)}; after ${JSON.stringify(after)} = ${total(after)}; hidden after 1 min ${JSON.stringify(hidden)} = ${total(hidden)}`);

    // Before: every read at its own fixed interval, as the field logs show (params every ~2 s with two panels,
    // consent and SkillRuns every 3 s).
    expect(before['consent']).toBeGreaterThan(1000);
    expect(before['skill-runs']).toBeGreaterThan(1000);
    expect(before['params']).toBeGreaterThan(1500);
    expect(total(before)).toBeGreaterThan(4000);

    // After: at least ten times fewer requests for each of the three hot reads (the ones that were 79% of all requests in
    // the field logs) and in total. The approvals (20 s) and the balance (60 s) were never dense; they only may not grow.
    for (const name of ['consent', 'skill-runs', 'params']) {
      expect(after[name], name).toBeLessThan(before[name] / 10);
    }
    expect(after['pending-approvals']).toBeLessThanOrEqual(before['pending-approvals']);
    expect(after['balance']).toBeLessThanOrEqual(before['balance']);
    expect(total(after) * 10).toBeLessThan(total(before));
    // A hidden tab: the reads of its first minute, and not one more for the rest of the hour.
    expect(total(hidden)).toBeLessThan(total(after) / 2);
    expect(total(hidden)).toBeLessThan(40);
  }, SIMULATION_TIMEOUT);

  it('an idle tab keeps polling at its own interval while a turn runs in its session', async () => {
    useChatStore.getState().initStreaming('session_1', { content: '' });
    const counts = await simulateHour(structuredClone(base));
    // A turn streams for the whole hour: the Agent may ask for a decision or set a parameter at any moment.
    expect(counts['consent']).toBeGreaterThan(1000);
    expect(counts['skill-runs']).toBeGreaterThan(1000);
  }, SIMULATION_TIMEOUT);

  it('a tab whose login ended stops reading at the first 401 and never reads again', async () => {
    current.schema = structuredClone(base);
    vi.useFakeTimers();
    const counts: Record<string, number> = {};
    let ended = false;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = new URL(String(input), 'http://app.test').pathname;
      counts[path] = (counts[path] ?? 0) + 1;
      if (ended) {
        // What the page's own fetch wrapper sees (src/lib/connectivity-watch.ts).
        useConnectivityStore.getState().markSessionExpired();
        return new Response('{}', { status: 401 });
      }
      return new Response(JSON.stringify({ consent: {}, params: {}, runs: [], items: [], accounts: [] }), { status: 200 });
    }));
    render(
      <SWRConfig value={{ ...swrPollingConfig, provider: () => new Map() }}>
        <Tab schema={current.schema} />
      </SWRConfig>,
    );
    await advance(30_000);
    ended = true;
    await advance(5 * 60_000);
    const atExpiry = total(counts);
    expect(useConnectivityStore.getState().sessionExpired).toBe(true);
    // The stretch of the field logs: a tab left open for 20 hours after its login ended. Nothing is left to render.
    await vi.advanceTimersByTimeAsync(20 * HOUR);
    expect(total(counts)).toBe(atExpiry);
  }, SIMULATION_TIMEOUT);
});
