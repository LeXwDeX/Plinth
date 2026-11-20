import type { AdministrationProductSchema } from '../product-schema';
import { responseRequestId } from '../problem-reference';

export interface AdministrationAccessKey {
  id: string;
  ownerId: string;
  ownerUsername: string;
  ownerName: string;
  name: string;
  prefix: string;
  scopes: string[];
  createdAt: string;
  lastUsedAt: string | null;
  requestCount: number;
  expiresAt: string | null;
  revokedAt: string | null;
  active: boolean;
}

export interface AdministrationAccessKeysPage {
  data: AdministrationAccessKey[];
  total: number;
  page: number;
  pageSize: number;
  enabled: boolean;
}

export interface AdministrationAccessKeysQuery {
  page?: number;
  pageSize?: number;
  search?: string;
  ownerID?: string;
}

export interface AdministrationAccessKeysBrowserRoutes {
  collection: string;
  item: string;
  switch: string;
}

type Fetcher = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

export interface AdministrationAccessKeysBrowserClientConfig {
  schema: AdministrationProductSchema['access_keys'];
  routes: AdministrationAccessKeysBrowserRoutes;
  fetcher?: Fetcher;
}

export interface AdministrationAccessKeysBrowserClient {
  list(
    query?: AdministrationAccessKeysQuery,
  ): Promise<AdministrationAccessKeysPage>;
  revoke(keyID: string): Promise<void>;
  setEnabled(enabled: boolean): Promise<boolean>;
}

/** An HTTP failure that keeps the server's request ID for the error reference (#240). */
export class AdministrationAccessKeysBrowserError extends Error {
  readonly status: number;
  readonly requestId: string;

  constructor(status: number, message: string, requestId = '') {
    super(message);
    this.name = 'AdministrationAccessKeysBrowserError';
    this.status = status;
    this.requestId = requestId;
  }
}

export function createAdministrationAccessKeysBrowserClient(
  config: AdministrationAccessKeysBrowserClientConfig,
): AdministrationAccessKeysBrowserClient {
  const fetcher = config.fetcher ?? fetch;
  const fields = config.schema.api.fields;
  const queryFields = config.schema.api.query;
  const pathParameter = config.schema.api.path_parameters.key_id;
  const itemToken = `{${pathParameter}}`;

  const itemRoute = (keyID: string): string => {
    if (!config.routes.item.includes(itemToken)) {
      throw new Error(config.schema.messages.internal_error);
    }
    return config.routes.item.replace(itemToken, encodeURIComponent(keyID));
  };

  const request = async (
    target: string,
    method: string,
    body?: Record<string, unknown>,
  ): Promise<Record<string, unknown>> => {
    const response = await fetcher(target, {
      method,
      ...(body === undefined
        ? {}
        : {
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
          }),
    });
    let payload: Record<string, unknown> = {};
    if (response.status !== 204) {
      const text = await response.text().catch(() => '');
      if (text) {
        try {
          payload = asRecord(JSON.parse(text));
        } catch {
          payload = {};
        }
      }
    }
    if (!response.ok) {
      throw new AdministrationAccessKeysBrowserError(
        response.status,
        optionalString(payload[fields.error]) ??
          config.schema.messages.internal_error,
        responseRequestId(response, payload),
      );
    }
    return payload;
  };

  const accessKey = (value: unknown): AdministrationAccessKey => {
    const wire = asRecord(value);
    const scopes = wire[fields.scopes];
    return {
      id: stringValue(wire[fields.id]),
      ownerId: stringValue(wire[fields.owner_id]),
      ownerUsername: stringValue(wire[fields.owner_username]),
      ownerName: stringValue(wire[fields.owner_name]),
      name: stringValue(wire[fields.name]),
      prefix: stringValue(wire[fields.prefix]),
      scopes: Array.isArray(scopes)
        ? scopes.filter((scope): scope is string => typeof scope === 'string')
        : [],
      createdAt: stringValue(wire[fields.created_at]),
      lastUsedAt: nullableString(wire[fields.last_used_at]),
      requestCount: numberValue(wire[fields.request_count]),
      expiresAt: nullableString(wire[fields.expires_at]),
      revokedAt: nullableString(wire[fields.revoked_at]),
      active: wire[fields.active] === true,
    };
  };

  return {
    async list(query = {}) {
      const parameters = new URLSearchParams();
      if (query.page !== undefined) {
        parameters.set(queryFields.page, String(query.page));
      }
      if (query.pageSize !== undefined) {
        parameters.set(queryFields.page_size, String(query.pageSize));
      }
      if (query.search) {
        parameters.set(queryFields.search, query.search);
      }
      if (query.ownerID) {
        parameters.set(queryFields.owner_id, query.ownerID);
      }
      const suffix = parameters.size > 0
        ? `?${parameters.toString()}`
        : '';
      const payload = await request(
        `${config.routes.collection}${suffix}`,
        'GET',
      );
      const rows = payload[fields.data];
      return {
        data: Array.isArray(rows) ? rows.map(accessKey) : [],
        total: numberValue(payload[fields.total]),
        page: numberValue(payload[fields.page]),
        pageSize: numberValue(payload[fields.page_size]),
        enabled: payload[fields.enabled] === true,
      };
    },

    async revoke(keyID) {
      await request(itemRoute(keyID), 'DELETE');
    },

    async setEnabled(enabled) {
      const payload = await request(config.routes.switch, 'PUT', {
        [fields.enabled]: enabled,
      });
      return payload[fields.enabled] === true;
    },
  };
}

function asRecord(value: unknown): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>;
  }
  return {};
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function optionalString(value: unknown): string | null {
  return typeof value === 'string' && value !== '' ? value : null;
}

function nullableString(value: unknown): string | null {
  return optionalString(value);
}

function numberValue(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}