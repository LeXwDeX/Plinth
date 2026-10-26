import type { AdministrationProductSchema } from '../product-schema';

export interface AdministrationAuditLog {
  id: string;
  action: string;
  userId: string;
  userName: string;
  resourceType: string;
  resourceId: string;
  detail: string;
  ipAddress: string;
  userAgent: string;
  createdAt: string;
}

export interface AdministrationAuditLogQuery {
  page: number;
  pageSize: number;
  userID?: string;
  action?: string;
  from?: Date;
  to?: Date;
}

export interface AdministrationAuditLogPage {
  data: AdministrationAuditLog[];
  total: number;
  page: number;
  pageSize: number;
}

type Fetcher = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

export interface AdministrationAuditLogsBrowserClientConfig {
  schema: AdministrationProductSchema['audit_logs'];
  route: string;
  fetcher?: Fetcher;
}

export interface AdministrationAuditLogsBrowserClient {
  listAuditLogs(
    query: AdministrationAuditLogQuery,
  ): Promise<AdministrationAuditLogPage>;
}

export class AdministrationAuditLogsBrowserError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'AdministrationAuditLogsBrowserError';
    this.status = status;
  }
}

export function createAdministrationAuditLogsBrowserClient(
  config: AdministrationAuditLogsBrowserClientConfig,
): AdministrationAuditLogsBrowserClient {
  const fetcher = config.fetcher ?? fetch;
  const fields = config.schema.api.fields;
  const queryFields = config.schema.api.query;

  const auditLogWire = (payload: unknown): AdministrationAuditLog => {
    const record = asRecord(payload);
    return {
      id: optionalString(record[fields.id]) ?? '',
      action: optionalString(record[fields.action]) ?? '',
      userId: optionalString(record[fields.user_id]) ?? '',
      userName: optionalString(record[fields.user_name]) ?? '',
      resourceType: optionalString(record[fields.resource_type]) ?? '',
      resourceId: optionalString(record[fields.resource_id]) ?? '',
      detail: optionalString(record[fields.detail]) ?? '',
      ipAddress: optionalString(record[fields.ip_address]) ?? '',
      userAgent: optionalString(record[fields.user_agent]) ?? '',
      createdAt: optionalString(record[fields.created_at]) ?? '',
    };
  };

  return {
    async listAuditLogs(query) {
      const parameters = new URLSearchParams();
      parameters.set(queryFields.page, String(query.page));
      parameters.set(queryFields.page_size, String(query.pageSize));
      if (query.userID) parameters.set(queryFields.user_id, query.userID);
      if (query.action) parameters.set(queryFields.action, query.action);
      if (query.from) {
        parameters.set(queryFields.from, query.from.toISOString());
      }
      if (query.to) {
        parameters.set(queryFields.to, query.to.toISOString());
      }
      const target = `${config.route}?${parameters.toString()}`;
      const response = await fetcher(target, { method: 'GET' });
      let payload: unknown;
      try {
        payload = await response.json();
      } catch {
        payload = undefined;
      }
      const record = asRecord(payload);
      if (!response.ok) {
        const message =
          optionalString(record[fields.error]) ??
          config.schema.messages.internal_error;
        throw new AdministrationAuditLogsBrowserError(
          response.status,
          message,
        );
      }
      const data = record[fields.data];
      return {
        data: Array.isArray(data) ? data.map(auditLogWire) : [],
        total: numberValue(record[fields.total]),
        page: numberValue(record[fields.page]),
        pageSize: numberValue(record[fields.page_size]),
      };
    },
  };
}

function asRecord(value: unknown): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>;
  }
  return {};
}

function optionalString(value: unknown): string | null {
  return typeof value === 'string' ? value : null;
}

function numberValue(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}
