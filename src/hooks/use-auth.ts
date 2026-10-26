'use client';

import useSWR from 'swr';
import { useProductSchema } from '@/components/product-schema-provider';
import type { AuthenticationAPISchema } from '@/lib/product-schema';

const fetcher = (url: string) =>
  fetch(url).then((r) => {
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
    return r.json();
  });

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
  );
  const session = projectAuthenticationSession(data, api);
  return {
    ...session,
    isLoading,
    error,
    refresh: mutate,
  };
}
