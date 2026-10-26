import type { ProductSchema } from '@/lib/product-schema';

export type ProductRoute =
  | { kind: 'workspace' }
  | { kind: 'account' }
  | { kind: 'login' }
  | { kind: 'administration'; key: string }
  | { kind: 'not-found' }
  | { kind: 'forbidden' };

interface RouteAccessInput {
  schema: ProductSchema;
  pathname: string;
  isAdmin: boolean;
  administrableKeys: readonly string[];
}

/**
 * 路由归属投影。`administration.navigation` 声明的全部是管理区目的地，
 * 非管理员一律不得渲染管理外壳（后端管理 API 另有 RBAC 兜底）。
 */
export function projectProductRoute({
  schema,
  pathname,
  isAdmin,
  administrableKeys,
}: RouteAccessInput): ProductRoute {
  const routes = schema.runtime.frontend.client_routes;
  if (pathname === routes.home) {
    return { kind: 'workspace' };
  }
  if (pathname === routes.login) {
    return { kind: 'login' };
  }
  if (pathname === routes.account) {
    return { kind: 'account' };
  }
  const matched = matchAdministrationRoute(
    schema,
    pathname,
    administrableKeys,
  );
  if (!matched) {
    return { kind: 'not-found' };
  }
  return isAdmin ? matched : { kind: 'forbidden' };
}

function matchAdministrationRoute(
  schema: ProductSchema,
  pathname: string,
  administrableKeys: readonly string[],
): Extract<ProductRoute, { kind: 'administration' }> | null {
  const matched = schema.administration.navigation.find((item) => {
    const hit =
      item.match === 'prefix'
        ? pathname === item.href || pathname.startsWith(`${item.href}/`)
        : pathname === item.href;
    return hit && administrableKeys.includes(item.key);
  });
  return matched ? { kind: 'administration', key: matched.key } : null;
}
