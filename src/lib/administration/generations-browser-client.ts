import type { AdministrationProductSchema } from '@/lib/product-schema';
import { parseNativeQuote, type NativeQuote } from '@/lib/pricing';
import { parseInputDerivations, type InputDerivation } from '@/lib/workspace/input-derivation';

export interface AdministrationGenerationRecord {
  id: string;
  userId: string;
  userName: string;
  userEmail: string;
  taskType: string;
  taskId: string;
  estimatedCredits: number;
  nativeQuote?: NativeQuote | null;
  taskStatus: string;
  params: string;
  resultUrls: string;
  thumbnailUrl: string | null;
  createdAt: string;
  finishedAt: string | null;
}

export interface AdministrationGenerationQuery {
  page?: number;
  pageSize?: number;
  userId?: string;
  taskType?: string;
  status?: string;
  from?: string;
  to?: string;
}

export interface AdministrationGenerationPage {
  records: AdministrationGenerationRecord[];
  total: number;
  page: number;
  pageSize: number;
}

export interface AdministrationGenerationLedger
  extends Omit<AdministrationGenerationRecord, 'params' | 'resultUrls'> {
  params: Record<string, unknown>;
  resultUrls: string[];
}

export interface AdministrationGenerationCache {
  taskId: string;
  taskType: string;
  status: string;
  providerId: string;
  data: Record<string, unknown>;
  cachedAt: string;
  updatedAt: string;
}

export interface AdministrationGenerationDetail {
  ledger: AdministrationGenerationLedger;
  taskCache: AdministrationGenerationCache | null;
  /** Native tasks only: reference images sent as a re-encoded copy (#276). */
  inputDerivations: InputDerivation[];
}

type Fetcher = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

export interface AdministrationGenerationsBrowserRoutes {
  collection: string;
  item: string;
  download: string;
}

export interface AdministrationGenerationsBrowserClientConfig {
  schema: AdministrationProductSchema['generations'];
  routes: AdministrationGenerationsBrowserRoutes;
  fetcher?: Fetcher;
}

export interface AdministrationGenerationsBrowserClient {
  listGenerations(
    query: AdministrationGenerationQuery,
  ): Promise<AdministrationGenerationPage>;
  getGeneration(taskID: string): Promise<AdministrationGenerationDetail>;
  generationDownloadURL(taskID: string): string;
}

export class AdministrationGenerationsBrowserError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = 'AdministrationGenerationsBrowserError';
  }
}

export function createAdministrationGenerationsBrowserClient(
  config: AdministrationGenerationsBrowserClientConfig,
): AdministrationGenerationsBrowserClient {
  const fetcher = config.fetcher ?? fetch;
  const fields = config.schema.api.fields;
  const queryFields = config.schema.api.query;
  const pathToken = `{${config.schema.api.path_parameters.task_id}}`;

  const itemRoute = (route: string, taskID: string): string => {
    if (!route.includes(pathToken)) {
      throw new Error(config.schema.messages.internal_error);
    }
    return route.replaceAll(pathToken, encodeURIComponent(taskID.trim()));
  };

  const request = async (
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Record<string, unknown>> => {
    const response = await fetcher(input, init);
    const payload = asRecord(await response.json().catch(() => ({})));
    if (!response.ok) {
      throw new AdministrationGenerationsBrowserError(
        response.status,
        optionalString(payload[fields.error]) ||
          config.schema.messages.internal_error,
      );
    }
    return payload;
  };

  const generationRecord = (
    value: unknown,
  ): AdministrationGenerationRecord => {
    const wire = asRecord(value);
    const nativeQuote = Object.hasOwn(wire, 'native_quote') ? parseNativeQuote(wire.native_quote) : undefined;
    return {
      id: optionalString(wire[fields.id]),
      userId: optionalString(wire[fields.user_id]),
      userName: optionalString(wire[fields.user_name]),
      userEmail: optionalString(wire[fields.user_email]),
      taskType: optionalString(wire[fields.task_type]),
      taskId: optionalString(wire[fields.task_id]),
      ...(nativeQuote === undefined ? { estimatedCredits: numberValue(wire[fields.estimated_credits]) } : { estimatedCredits: 0, nativeQuote }),
      taskStatus: optionalString(wire[fields.task_status]),
      params: jsonText(wire[fields.params], {}),
      resultUrls: jsonText(wire[fields.result_urls], []),
      thumbnailUrl: nullableString(wire[fields.thumbnail_url]),
      createdAt: optionalString(wire[fields.created_at]),
      finishedAt: nullableString(wire[fields.finished_at]),
    };
  };

  const generationLedger = (
    value: unknown,
  ): AdministrationGenerationLedger => {
    const record = generationRecord(value);
    return {
      ...record,
      params: objectValue(record.params),
      resultUrls: stringArrayValue(record.resultUrls),
    };
  };

  const generationCache = (
    value: unknown,
  ): AdministrationGenerationCache | null => {
    if (value === null || value === undefined) return null;
    const wire = asRecord(value);
    return {
      taskId: optionalString(wire[fields.task_id]),
      taskType: optionalString(wire[fields.task_type]),
      status: optionalString(wire[fields.status]),
      providerId: optionalString(wire[fields.provider_id]),
      data: objectValue(wire[fields.cache_data]),
      cachedAt: optionalString(wire[fields.cached_at]),
      updatedAt: optionalString(wire[fields.updated_at]),
    };
  };

  return {
    async listGenerations(query) {
      const parameters = new URLSearchParams();
      setNumber(parameters, queryFields.page, query.page);
      setNumber(parameters, queryFields.page_size, query.pageSize);
      setString(parameters, queryFields.user_id, query.userId);
      setString(parameters, queryFields.task_type, query.taskType);
      setString(parameters, queryFields.status, query.status);
      setString(parameters, queryFields.from, query.from);
      setString(parameters, queryFields.to, query.to);
      const suffix = parameters.size > 0
        ? `?${parameters.toString()}`
        : '';
      const payload = await request(`${config.routes.collection}${suffix}`);
      const wireRecords = payload[fields.data];
      const records = Array.isArray(wireRecords)
        ? wireRecords.map(generationRecord)
        : [];
      return {
        records,
        total: numberValue(payload[fields.total]),
        page: numberValue(payload[fields.page]),
        pageSize: numberValue(payload[fields.page_size]),
      };
    },
    async getGeneration(taskID) {
      const payload = await request(
        itemRoute(config.routes.item, taskID),
        undefined,
      );
      return {
        ledger: generationLedger(payload[fields.ledger]),
        taskCache: generationCache(payload[fields.task_cache]),
        inputDerivations: fields.input_derivations ? parseInputDerivations(payload[fields.input_derivations]) : [],
      };
    },
    generationDownloadURL(taskID) {
      return itemRoute(config.routes.download, taskID);
    },
  };
}

function asRecord(value: unknown): Record<string, unknown> {
  if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
    return value as Record<string, unknown>;
  }
  return {};
}

function optionalString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function nullableString(value: unknown): string | null {
  return typeof value === 'string' && value !== '' ? value : null;
}

function numberValue(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function jsonText(value: unknown, fallback: unknown): string {
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value ?? fallback);
  } catch {
    return JSON.stringify(fallback);
  }
}

function objectValue(value: unknown): Record<string, unknown> {
  if (typeof value === 'string') {
    try {
      return asRecord(JSON.parse(value));
    } catch {
      return {};
    }
  }
  return asRecord(value);
}

function stringArrayValue(value: unknown): string[] {
  if (typeof value === 'string') {
    try {
      return stringArrayValue(JSON.parse(value));
    } catch {
      return [];
    }
  }
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === 'string');
}

function setString(
  parameters: URLSearchParams,
  name: string,
  value: string | undefined,
): void {
  if (value) parameters.set(name, value);
}

function setNumber(
  parameters: URLSearchParams,
  name: string,
  value: number | undefined,
): void {
  if (value !== undefined) parameters.set(name, String(value));
}
