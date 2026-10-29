import { useEffect, type ReactNode } from 'react';
import { usePathname, useRouter } from '@/lib/browser-navigation';

import { useProductSchema } from '@/components/product-schema-provider';
import { useAuth, type SessionFailure } from '@/hooks/use-auth';
import type { ProductSchema } from '@/lib/product-schema';

type BrowserAccess =
  | { state: 'render' }
  | { state: 'loading' }
  | { state: 'session-failed'; failure: Exclude<SessionFailure, null> }
  | { state: 'redirect'; href: string };

interface BrowserAccessInput {
  schema: ProductSchema;
  requestedPath: string;
  isLoading: boolean;
  hasUser: boolean;
  /** The session request failed with something other than 401: the login is unknown, not gone. */
  sessionFailure?: SessionFailure;
}

export function projectBrowserAccess({
  schema,
  requestedPath,
  isLoading,
  hasUser,
  sessionFailure = null,
}: BrowserAccessInput): BrowserAccess {
  const pathname = requestedPath.split('?', 1)[0];
  const isPublic = schema.authentication.public_paths.some((path) =>
    pathname.startsWith(path),
  );
  if (isPublic) {
    return { state: 'render' };
  }
  if (isLoading) {
    return { state: 'loading' };
  }
  if (hasUser) {
    return { state: 'render' };
  }
  if (sessionFailure) {
    return { state: 'session-failed', failure: sessionFailure };
  }

  const query = new URLSearchParams({
    [schema.authentication.login.redirect_query]: requestedPath,
  });
  return {
    state: 'redirect',
    href: `${schema.runtime.frontend.client_routes.login}?${query}`,
  };
}

export function AuthenticationGate({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const { schema, text } = useProductSchema();
  const { user, isLoading, sessionFailure } = useAuth();
  const requestedPath = `${pathname}${window.location.search}`;
  const access = projectBrowserAccess({
    schema,
    requestedPath,
    isLoading,
    hasUser: user !== null,
    sessionFailure,
  });

  useEffect(() => {
    if (access.state === 'redirect') {
      router.replace(access.href);
    }
  }, [access.state, access.state === 'redirect' ? access.href : '', router]);

  if (access.state === 'session-failed') {
    // The session request is retried with backoff; the login is not touched.
    return (
      <div role="alert" data-testid="session-failed" className="flex h-screen items-center justify-center px-4 text-center text-muted-foreground">
        {text(access.failure === 'rate_limited' ? 'ui.connectivity.session_rate_limited' : 'ui.connectivity.session_unavailable')}
      </div>
    );
  }
  if (access.state !== 'render') {
    return (
      <div className="flex h-screen items-center justify-center text-muted-foreground">
        {schema.authentication.login.loading_label}
      </div>
    );
  }
  return <>{children}</>;
}
