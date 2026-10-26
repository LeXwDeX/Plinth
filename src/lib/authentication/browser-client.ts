import type { ProductSchema } from '@/lib/product-schema';

type Fetcher = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

export interface AuthenticationBrowserClientDependencies {
  fetcher?: Fetcher;
}

export interface AuthenticationBrowserClient {
  login(username: string, password: string): Promise<void>;
  logout(): Promise<void>;
}

export class AuthenticationBrowserError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = 'AuthenticationBrowserError';
    this.status = status;
  }
}

// 只接受同源相对路径。登录页把 URL 查询参数赋给 location 前必须
// 经过这里，否则 //evil.com、javascript: 等值会构成开放重定向/XSS。
export function safeRedirectPath(
  requested: string | null,
  fallback: string,
): string {
  const value = (requested ?? '').trim();
  if (value === '') return fallback;
  if (/[\u0000-\u001f\u007f]/.test(value)) return fallback;
  if (!value.startsWith('/') || value.startsWith('//')) return fallback;
  if (value.includes('\\')) return fallback;
  return value;
}

export function createAuthenticationBrowserClient(
  schema: ProductSchema,
  dependencies: AuthenticationBrowserClientDependencies = {},
): AuthenticationBrowserClient {
  const fetcher = dependencies.fetcher ?? fetch;
  return {
    async login(username, password) {
      const api = schema.authentication.api;
      const response = await fetcher(
        schema.runtime.http.legacy_routes.auth_login,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            [api.username_field]: username,
            [api.password_field]: password,
          }),
        },
      );
      if (!response.ok) {
        const payload = await responseObject(response);
        const configuredMessage = payload[api.error_field];
        throw new AuthenticationBrowserError(
          typeof configuredMessage === 'string'
            ? configuredMessage
            : response.status === 401
              ? schema.authentication.messages.invalid_credentials
              : schema.authentication.messages.login_failed,
          response.status,
        );
      }
    },
    async logout() {
      const api = schema.authentication.api;
      const response = await fetcher(
        schema.runtime.http.legacy_routes.auth_session,
        { method: 'DELETE' },
      );
      if (!response.ok) {
        const payload = await responseObject(response);
        const configuredMessage = payload[api.error_field];
        throw new AuthenticationBrowserError(
          typeof configuredMessage === 'string'
            ? configuredMessage
            : schema.authentication.messages.internal_error,
          response.status,
        );
      }
    },
  };
}

async function responseObject(
  response: Response,
): Promise<Record<string, unknown>> {
  try {
    const payload: unknown = await response.json();
    if (payload !== null && typeof payload === 'object' &&
        !Array.isArray(payload)) {
      return payload as Record<string, unknown>;
    }
  } catch {
    // 非 JSON 错误响应使用 Product Schema fallback。
  }
  return {};
}
