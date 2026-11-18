'use client';

import { useCallback } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';

/** One entry of the setting: the asset lists (`list`) or the external MCP (`mcp`). */
export interface StepPolicySetting {
  mode: string;
  cap: number;
}

export interface StepPolicyMode {
  value: string;
  label: string;
}

/**
 * The read and the answer of a write (#330). `version` is 0 until the person
 * saves for the first time; the two entries then show the Schema defaults.
 * `modes` carries the server's labels and `cap_range` the allowed cap, so the
 * control never retypes them.
 */
export interface AccountStepPolicy {
  version: number;
  list: StepPolicySetting;
  mcp: StepPolicySetting;
  modes: StepPolicyMode[];
  cap_range: { minimum: number; maximum: number };
}

/** A save: every member the server requires is merged from the current read. */
export interface StepPolicyUpdate {
  expected_version: number;
  list?: StepPolicySetting;
  mcp?: StepPolicySetting;
}

/**
 * A refused request. `status` is the HTTP status; `message` is the server's own
 * `detail` when it sent one. A 409 is not a failure here: `update` re-reads the
 * setting and reports `conflict` so the panel can show the values now in force.
 */
export class StepPolicyRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
    this.name = 'StepPolicyRequestError';
  }
}

export type StepPolicyUpdateResult =
  | { status: 'saved'; policy: AccountStepPolicy }
  | { status: 'conflict'; policy: AccountStepPolicy | null }
  | { status: 'failed'; error: StepPolicyRequestError };

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function setting(value: unknown): StepPolicySetting | null {
  const entry = record(value);
  if (!entry) return null;
  const mode = typeof entry.mode === 'string' ? entry.mode : '';
  const cap = entry.cap;
  if (!mode || typeof cap !== 'number' || !Number.isInteger(cap)) return null;
  return { mode, cap };
}

/** Projects the server's wire answer; null means it is not the shape the control needs. */
export function projectAccountStepPolicy(value: unknown): AccountStepPolicy | null {
  const policy = record(value);
  if (!policy) return null;
  const version = policy.version;
  const list = setting(policy.list);
  const mcp = setting(policy.mcp);
  const range = record(policy.cap_range);
  const minimum = range?.minimum;
  const maximum = range?.maximum;
  if (typeof version !== 'number' || !list || !mcp) return null;
  if (typeof minimum !== 'number' || typeof maximum !== 'number' || minimum > maximum) return null;
  const modes: StepPolicyMode[] = [];
  if (Array.isArray(policy.modes)) {
    for (const raw of policy.modes) {
      const mode = record(raw);
      if (mode && typeof mode.value === 'string' && typeof mode.label === 'string') {
        modes.push({ value: mode.value, label: mode.label });
      }
    }
  }
  return { version, list, mcp, modes, cap_range: { minimum, maximum } };
}

async function payloadOf(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    return null;
  }
}

function requestError(response: Response, payload: unknown): StepPolicyRequestError {
  const detail = record(payload)?.detail;
  return new StepPolicyRequestError(
    response.status,
    typeof detail === 'string' && detail ? detail : `HTTP ${response.status}`,
  );
}

async function readPolicy(url: string): Promise<AccountStepPolicy> {
  const response = await fetch(url);
  const payload = await payloadOf(response);
  if (!response.ok) throw requestError(response, payload);
  const policy = projectAccountStepPolicy(payload);
  if (!policy) throw new StepPolicyRequestError(response.status, 'invalid step policy response');
  return policy;
}

/**
 * #330: the person's 3D step setting. The control in the top bar reads it once,
 * saves both entries with the version it read, and re-reads after a save or a
 * 409. A server without `application.step_policy` has no route, so no request
 * is made and `update` refuses instead of calling an unknown URL.
 */
export function useAccountStepPolicy() {
  const { schema } = useProductSchema();
  const url = schema.runtime.http.routes.account_step_policy ?? '';
  const { data, error, isLoading, mutate } = useSWR<AccountStepPolicy>(
    url || null,
    readPolicy,
    // A focus revalidation would replace an unsaved draft; the panel saves with
    // the version it read and re-reads on conflict anyway.
    { keepPreviousData: false, shouldRetryOnError: false, revalidateOnFocus: false },
  );

  const refresh = useCallback(async () => {
    if (!url) return;
    await mutate();
  }, [mutate, url]);

  const update = useCallback(async (change: StepPolicyUpdate): Promise<StepPolicyUpdateResult> => {
    if (!url) {
      return { status: 'failed', error: new StepPolicyRequestError(0, 'invalid step policy response') };
    }
    const list = change.list ?? data?.list;
    const mcp = change.mcp ?? data?.mcp;
    if (!list || !mcp) {
      return { status: 'failed', error: new StepPolicyRequestError(0, 'invalid step policy response') };
    }
    let response: Response;
    try {
      response = await fetch(url, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          expected_version: change.expected_version,
          list: { mode: list.mode, cap: list.cap },
          mcp: { mode: mcp.mode, cap: mcp.cap },
        }),
      });
    } catch (cause) {
      const reason = cause instanceof Error ? cause.message : String(cause);
      return { status: 'failed', error: new StepPolicyRequestError(0, reason) };
    }
    const payload = await payloadOf(response);
    // A conflict is a person's change from another tab or device: show it and
    // read the values now in force instead of overwriting them silently.
    if (response.status === 409) {
      const reread = await mutate();
      return { status: 'conflict', policy: reread ?? data ?? null };
    }
    if (!response.ok) {
      return { status: 'failed', error: requestError(response, payload) };
    }
    const saved = projectAccountStepPolicy(payload);
    if (!saved) {
      return { status: 'failed', error: new StepPolicyRequestError(response.status, 'invalid step policy response') };
    }
    const reread = await mutate();
    return { status: 'saved', policy: reread ?? saved };
  }, [data, mutate, url]);

  return {
    policy: data ?? null,
    loading: Boolean(url) && isLoading,
    error: url ? error : undefined,
    update,
    refresh,
  };
}