'use client';

import useSWR from 'swr';
import { useProductSchema } from '@/components/product-schema-provider';
import type { AuthenticationAPISchema } from '@/lib/product-schema';

/** The session request failed; `retryAfterSeconds` is the server's Retry-After, when it sent one. */
export class SessionRequestError extends Error {
  constructor(readonly status: number, readonly retryAfterSeconds: number | null) {
    super(`HTTP ${status}`);
  }
}

const MAX_RETRY_MILLISECONDS = 120_000;

function parseRetryAfter(value: string | null): number | null {
  if (!value) return null;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) return seconds;
  const at = Date.parse(value);
  return Number.isNaN(at) ? null : Math.max(0, (at - Date.now()) / 1000);
}

const fetcher = (url: string) =>
  fetch(url).then((r) => {
    if (!r.ok) throw new SessionRequestError(r.status, parseRetryAfter(r.headers.get('Retry-After')));
    return r.json();
  });

/** Wait before asking again: exponential backoff, never shorter than the server's Retry-After. */
export function sessionRetryDelayMs(error: unknown, retryCount: number): number {
  const backoff = Math.min(30_000, 1_000 * 2 ** Math.min(retryCount, 5));
  const asked = error instanceof SessionRequestError && error.retryAfterSeconds !== null ? error.retryAfterSeconds * 1000 : 0;
  return Math.min(MAX_RETRY_MILLISECONDS, Math.max(backoff, asked));
}

export type SessionFailure = 'rate_limited' | 'unavailable' | null;

/**
 * Why the session could not be read. A 401 means "not signed in" and is not a failure; every other error
 * (429, 5xx, network) says nothing about the login and must not be taken for a signed-out visitor.
 */
export function projectSessionFailure(error: unknown, hasAnswer: boolean): SessionFailure {
  if (!error || hasAnswer) return null;
  if (error instanceof SessionRequestError && error.status === 401) return null;
  return error instanceof SessionRequestError && error.status === 429 ? 'rate_limited' : 'unavailable';
}

export interface AuthenticationSessionProjection {
  user: { id: string; name: string; email: string } | null;
  isAdmin: boolean;
}

function objectRecord(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

export function projectAuthenticationSession(
  response: unknown,
  api: AuthenticationAPISchema,
): AuthenticationSessionProjection {
  const envelope = objectRecord(response);
  const wireUser = objectRecord(envelope?.[api.user_field]);
  const id = wireUser?.[api.id_field];
  const name = wireUser?.[api.name_field];
  const email = wireUser?.[api.email_field];
  const user = (
    typeof id === 'string' &&
    typeof name === 'string' &&
    typeof email === 'string'
  ) ? { id, name, email } : null;
  return {
    user,
    isAdmin: envelope?.[api.is_admin_field] === true,
  };
}

export function useAuth() {
  const { schema } = useProductSchema();
  const api = schema.authentication.api;
  const { data, error, isLoading, mutate } = useSWR<unknown>(
    schema.runtime.http.legacy_routes.auth_session,
    fetcher,
    {
      onErrorRetry: (err, _key, _config, revalidate, { retryCount }) => {
        if (err instanceof SessionRequestError && err.status === 401) return;
        setTimeout(() => { void revalidate({ retryCount: retryCount + 1 }); }, sessionRetryDelayMs(err, retryCount));
      },
    },
  );
  const session = projectAuthenticationSession(data, api);
  return {
    ...session,
    isLoading,
    error,
    sessionFailure: projectSessionFailure(error, data !== undefined),
    refresh: mutate,
  };
}
