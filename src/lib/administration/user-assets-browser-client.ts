import type { AdministrationProductSchema } from '@/lib/product-schema';
import { parseNativeQuote, type NativeQuote } from '@/lib/pricing';

export interface AdministrationUserAssetsUser {
  id: string;
  username: string;
  nickname: string;
  email: string;
  name: string;
  isAdmin: boolean;
  isActive: boolean;
  createdAt: string;
  lastLogin: string | null;
}

export interface AdministrationUserAssetUpload {
  id: string;
  assetType: string;
  originalFilename: string;
  storedFilename: string;
  mimeType: string;
  fileSize: number;
  storagePath: string;
  createdAt: string;
}

export interface AdministrationUserAssetGeneration {
  id: string;
  taskType: string;
  taskId: string;
  taskStatus: string;
  resultUrls: string;
  params: string;
  estimatedCredits: number;
  nativeQuote?: NativeQuote | null;
  thumbnailUrl: string | null;
  createdAt: string;
  finishedAt: string | null;
}

export interface AdministrationUserAssetDownload {
  id: string;
  action: string;
  resourceType: string;
  resourceId: string;
  detail: string;
  createdAt: string;
}

export interface AdministrationUserAssetsPage<T> {
  data: T[];
  total: number;
  page: number;
  pageSize: number;
}

export interface AdministrationUserAssetsOverview {
  user: AdministrationUserAssetsUser;
  summary: {
    totalUploads: number;
    totalGenerations: number;
    totalDownloads: number;
    totalStorageBytes: number;
  };
  uploads: AdministrationUserAssetsPage<AdministrationUserAssetUpload>;
  generations: AdministrationUserAssetsPage<
    AdministrationUserAssetGeneration
  >;
  downloads: AdministrationUserAssetsPage<
    AdministrationUserAssetDownload
  >;
}

export interface AdministrationUserAssetsQuery {
  pageSize?: number;
  uploadsPage?: number;
  generationsPage?: number;
  downloadsPage?: number;
  from?: string;
  to?: string;
}

type Fetcher = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

export interface AdministrationUserAssetsBrowserRoutes {
  overview: string;
  asset: string;
}

export interface AdministrationUserAssetsBrowserClientConfig {
  schema: AdministrationProductSchema['user_assets'];
  routes: AdministrationUserAssetsBrowserRoutes;
  fetcher?: Fetcher;
}

export interface AdministrationUserAssetsBrowserClient {
  getUserAssets(
    userID: string,
    query: AdministrationUserAssetsQuery,
  ): Promise<AdministrationUserAssetsOverview>;
  assetURL(assetID: string): string;
}

export class AdministrationUserAssetsBrowserError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = 'AdministrationUserAssetsBrowserError';
  }
}

export function createAdministrationUserAssetsBrowserClient(
  config: AdministrationUserAssetsBrowserClientConfig,
): AdministrationUserAssetsBrowserClient {
  const fetcher = config.fetcher ?? fetch;
  const schema = config.schema;
  const envelope = schema.api.envelope_fields;
  const pageFields = schema.api.page_fields;
  const queryFields = schema.api.query;
  const overviewToken = `{${schema.api.path_parameters.user_id}}`;
  const assetToken = singleRouteToken(
    config.routes.asset,
    schema.messages.internal_error,
  );

  const routeValue = (
    route: string,
    token: string,
    value: string,
  ): string => {
    if (!route.includes(token)) {
      throw new Error(schema.messages.internal_error);
    }
    return route.replaceAll(token, encodeURIComponent(value.trim()));
  };

  const request = async (target: string): Promise<Record<string, unknown>> => {
    const response = await fetcher(target);
    const payload = asRecord(await response.json().catch(() => ({})));
    if (!response.ok) {
      throw new AdministrationUserAssetsBrowserError(
        response.status,
        stringValue(payload[envelope.error]) ||
          schema.messages.internal_error,
      );
    }
    return payload;
  };

  const page = <T>(
    value: unknown,
    decoder: (item: unknown) => T,
  ): AdministrationUserAssetsPage<T> => {
    const wire = asRecord(value);
    const rows = wire[pageFields.data];
    return {
      data: Array.isArray(rows) ? rows.map(decoder) : [],
      total: numberValue(wire[pageFields.total]),
      page: numberValue(wire[pageFields.page]),
      pageSize: numberValue(wire[pageFields.page_size]),
    };
  };

  const user = (value: unknown): AdministrationUserAssetsUser => {
    const wire = asRecord(value);
    const fields = schema.api.user_fields;
    return {
      id: stringValue(wire[fields.id]),
      username: stringValue(wire[fields.username]),
      nickname: stringValue(wire[fields.nickname]),
      email: stringValue(wire[fields.email]),
      name: stringValue(wire[fields.name]),
      isAdmin: wire[fields.is_admin] === true,
      isActive: wire[fields.is_active] === true,
      createdAt: stringValue(wire[fields.created_at]),
      lastLogin: nullableString(wire[fields.last_login]),
    };
  };

  const summary = (value: unknown) => {
    const wire = asRecord(value);
    const fields = schema.api.summary_fields;
    return {
      totalUploads: numberValue(wire[fields.total_uploads]),
      totalGenerations: numberValue(wire[fields.total_generations]),
      totalDownloads: numberValue(wire[fields.total_downloads]),
      totalStorageBytes: numberValue(wire[fields.total_storage_bytes]),
    };
  };

  const upload = (value: unknown): AdministrationUserAssetUpload => {
    const wire = asRecord(value);
    const fields = schema.api.upload_fields;
    return {
      id: stringValue(wire[fields.id]),
      assetType: stringValue(wire[fields.asset_type]),
      originalFilename: stringValue(wire[fields.original_filename]),
      storedFilename: stringValue(wire[fields.stored_filename]),
      mimeType: stringValue(wire[fields.mime_type]),
      fileSize: numberValue(wire[fields.file_size]),
      storagePath: stringValue(wire[fields.storage_path]),
      createdAt: stringValue(wire[fields.created_at]),
    };
  };

  const generation = (
    value: unknown,
  ): AdministrationUserAssetGeneration => {
    const wire = asRecord(value);
    const fields = schema.api.generation_fields;
    const nativeQuote = Object.hasOwn(wire, 'native_quote') ? parseNativeQuote(wire.native_quote) : undefined;
    return {
      id: stringValue(wire[fields.id]),
      taskType: stringValue(wire[fields.task_type]),
      taskId: stringValue(wire[fields.task_id]),
      taskStatus: stringValue(wire[fields.task_status]),
      resultUrls: jsonText(wire[fields.result_urls], []),
      params: jsonText(wire[fields.params], {}),
      ...(nativeQuote === undefined ? { estimatedCredits: numberValue(wire[fields.estimated_credits]) } : { estimatedCredits: 0, nativeQuote }),
      thumbnailUrl: nullableString(wire[fields.thumbnail_url]),
      createdAt: stringValue(wire[fields.created_at]),
      finishedAt: nullableString(wire[fields.finished_at]),
    };
  };

  const download = (
    value: unknown,
  ): AdministrationUserAssetDownload => {
    const wire = asRecord(value);
    const fields = schema.api.download_fields;
    return {
      id: stringValue(wire[fields.id]),
      action: stringValue(wire[fields.action]),
      resourceType: stringValue(wire[fields.resource_type]),
      resourceId: stringValue(wire[fields.resource_id]),
      detail: jsonText(wire[fields.detail], {}),
      createdAt: stringValue(wire[fields.created_at]),
    };
  };

  return {
    async getUserAssets(userID, query) {
      const parameters = new URLSearchParams();
      setNumber(parameters, queryFields.page_size, query.pageSize);
      setNumber(parameters, queryFields.uploads_page, query.uploadsPage);
      setNumber(
        parameters,
        queryFields.generations_page,
        query.generationsPage,
      );
      setNumber(parameters, queryFields.downloads_page, query.downloadsPage);
      setString(parameters, queryFields.from, query.from);
      setString(parameters, queryFields.to, query.to);
      const base = routeValue(
        config.routes.overview,
        overviewToken,
        userID,
      );
      const suffix = parameters.size > 0
        ? `?${parameters.toString()}`
        : '';
      const payload = await request(`${base}${suffix}`);
      return {
        user: user(payload[envelope.user]),
        summary: summary(payload[envelope.summary]),
        uploads: page(payload[envelope.uploads], upload),
        generations: page(payload[envelope.generations], generation),
        downloads: page(payload[envelope.downloads], download),
      };
    },
    assetURL(assetID) {
      return routeValue(config.routes.asset, assetToken, assetID);
    },
  };
}

function singleRouteToken(route: string, message: string): string {
  const tokens = route.match(/\{[^{}]+\}/g) ?? [];
  if (tokens.length !== 1) throw new Error(message);
  return tokens[0]!;
}

function asRecord(value: unknown): Record<string, unknown> {
  if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
    return value as Record<string, unknown>;
  }
  return {};
}

function stringValue(value: unknown): string {
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

function setNumber(
  parameters: URLSearchParams,
  key: string,
  value: number | undefined,
) {
  if (value !== undefined) parameters.set(key, String(value));
}

function setString(
  parameters: URLSearchParams,
  key: string,
  value: string | undefined,
) {
  if (value) parameters.set(key, value);
}
