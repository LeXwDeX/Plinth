import type { ProductSchema } from '@/lib/product-schema';
import type { NativeQuote } from '@/lib/pricing';

export type HarnessSourceKind = 'task_asset' | 'input_asset';
export type HarnessTaskSourceMode = 'agent' | 'fixed';
export interface HarnessSource { kind?: HarnessSourceKind; selection_mode?: HarnessTaskSourceMode; parameter: string; task_id?: string; asset_id?: string }
export interface HarnessSourceView { kind: HarnessSourceKind; asset_id: string; original_filename: string; media_type: string; size: number; profile_id?: string; state: 'ready' | 'unavailable'; reason?: string }

export interface HarnessSelection {
  session_id: string;
  revision: number;
  digest: string;
  operation_id?: string;
  offering_id?: string;
  offering_revision?: string;
  enabled_operations: string[];
  sources: HarnessSource[];
  fixed_parameters: Record<string, unknown>;
  fillable_parameters: string[];
  allow_prerequisites: boolean;
  updated_at: string;
}

export interface HarnessPublicOperation {
  id: string;
  label: string;
  description: string;
  group: 'common' | 'model';
  capability: string;
  model_ids?: string[];
  prerequisites?: string[][];
  unavailable_reason?: string;
  uploaded_source_unavailable_reason?: string;
}

export interface PublicHarnessSchema {
  task_source_modes?: Array<{ id: HarnessTaskSourceMode; label: string; description: string }>;
  default_task_source_mode?: HarnessTaskSourceMode;
  source_clarification_message?: string;
  source_kinds?: Array<{ id: HarnessSourceKind; label: string; description: string }>;
  groups: Array<{ id: string; label: string; empty_message: string }>;
  operations: HarnessPublicOperation[];
  default_enabled_operations: string[];
  default_allow_prerequisites: boolean;
}

export interface HarnessView {
  source_views?: HarnessSourceView[];
  selection: HarnessSelection;
  status: 'free' | 'ready' | 'needs_input' | 'unavailable';
  reason: string;
  missing_parameters: string[];
  can_propose: boolean;
  active_operation?: HarnessPublicOperation | null;
  price_mode?: 'native';
  native_quote?: NativeQuote;
  estimated_credits?: number;
  turn?: { assistant_message_id: string; selection: HarnessSelection } | null;
}

export type HarnessSelectionInput = Omit<HarnessSelection, 'digest' | 'updated_at'> &
  Partial<Pick<HarnessSelection, 'digest' | 'updated_at'>>;

export class HarnessBrowserError extends Error {
  readonly status: number;
  readonly detail: string;

  constructor(detail: string, status: number) {
    super(detail);
    this.name = 'HarnessBrowserError';
    this.status = status;
    this.detail = detail;
  }
}

type Fetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export interface HarnessBrowserClient {
  get(sessionId: string, signal?: AbortSignal): Promise<HarnessView>;
  put(sessionId: string, expectedRevision: number, selection: HarnessSelectionInput, signal?: AbortSignal): Promise<HarnessView>;
}

function routeFor(schema: ProductSchema, sessionId: string): string {
  const template = schema.runtime.http.routes.chat_session_harness;
  if (!template) throw new HarnessBrowserError('Harness route is unavailable', 0);
  const route = template.replace(/\{([^{}]+)\}/g, (placeholder, name: string) =>
    name === 'sessionID' ? encodeURIComponent(sessionId) : placeholder);
  try {
    const parsed = new URL(route, 'http://localhost');
    if (parsed.origin !== 'http://localhost' || !route.startsWith('/')) {
      throw new HarnessBrowserError('Harness route must be same-origin', 0);
    }
    if (route.includes('{') || route.includes('}')) {
      throw new HarnessBrowserError('Harness route has an unresolved path parameter', 0);
    }
  } catch (error) {
    if (error instanceof HarnessBrowserError) throw error;
    throw new HarnessBrowserError('Harness route is invalid', 0);
  }
  return route;
}

async function payload(response: Response): Promise<unknown> {
  const value: unknown = await response.json().catch(() => null);
  if (!response.ok) {
    const detail = value && typeof value === 'object' && !Array.isArray(value) &&
      typeof (value as Record<string, unknown>).detail === 'string'
      ? (value as Record<string, string>).detail
      : 'Harness request failed';
    throw new HarnessBrowserError(detail, response.status);
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new HarnessBrowserError('Harness response is invalid', response.status);
  }
  return value;
}

/** Same-origin session Harness API. Authentication remains the browser's normal cookie/API middleware. */
export function createHarnessClient(schema: ProductSchema, fetcher: Fetcher = fetch): HarnessBrowserClient {
  return {
    async get(sessionId, signal) {
      const response = await fetcher(routeFor(schema, sessionId), {
        method: 'GET', credentials: 'same-origin', signal,
      });
      return await payload(response) as HarnessView;
    },
    async put(sessionId, expectedRevision, selection, signal) {
      const response = await fetcher(routeFor(schema, sessionId), {
        method: 'PUT', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ expected_revision: expectedRevision, selection }), signal,
      });
      return await payload(response) as HarnessView;
    },
  };
}
