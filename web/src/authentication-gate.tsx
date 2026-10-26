import { useEffect, type ReactNode } from 'react';
import { usePathname, useRouter } from '@/lib/browser-navigation';

import { useProductSchema } from '@/components/product-schema-provider';
import { useAuth } from '@/hooks/use-auth';
import type { ProductSchema } from '@/lib/product-schema';

type BrowserAccess =
  | { state: 'render' }
  | { state: 'loading' }
  | { state: 'redirect'; href: string };

interface BrowserAccessInput {
  schema: ProductSchema;
  requestedPath: string;
  isLoading: boolean;
  hasUser: boolean;
}

export function projectBrowserAccess({
  schema,
  requestedPath,
  isLoading,
  hasUser,
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
  const { schema } = useProductSchema();
  const { user, isLoading } = useAuth();
  const requestedPath = `${pathname}${window.location.search}`;
  const access = projectBrowserAccess({
    schema,
    requestedPath,
    isLoading,
    hasUser: user !== null,
  });

  useEffect(() => {
    if (access.state === 'redirect') {
      router.replace(access.href);
    }
  }, [access.state, access.state === 'redirect' ? access.href : '', router]);

  if (access.state !== 'render') {
    return (
      <div className="flex h-screen items-center justify-center text-muted-foreground">
        {schema.authentication.login.loading_label}
      </div>
    );
  }
  return <>{children}</>;
}
