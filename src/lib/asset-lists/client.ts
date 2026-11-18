import type { ProductSchema } from '@/lib/product-schema';

/**
 * #327: browser client for the owner-scoped production list routes. Summaries
 * and the detail stay in the server's wire spelling: these are the list card's
 * and project panel's contract, not a second model. Every route template comes
 * from the public Schema; a Catalog without the section has no routes and the
 * client refuses to build a URL.
 */

export type AssetListDecision = 'confirm' | 'reject' | 'replace';

/** #328: the four run controls the person sends from the list card. */
export type AssetListRunAction = 'start' | 'pause' | 'resume' | 'stop';

/**
 * #328: the run's item tally as the server counts it. Every state of
 * `asset_list.item_states` has a field; the browser never derives its own.
 */
export interface AssetListRunCounts {
  todo: number;
  working: number;
  review: number;
  accepted: number;
  returned: number;
  failed: number;
  skipped: number;
}

/**
 * #328: what a run control answers. `status` is the server's wire spelling
 * (idle, running, paused, stopped, completed); `pause_reason` is empty unless
 * paused (user, presence, step_cap, failure). The control response carries no
 * item tally, so the card re-reads the list instead of rendering this object.
 */
export interface AssetListRunState {
  status: string;
  pause_reason?: string;
}

/** #328: the run summary on a project or detail list. */
export interface AssetListRun extends AssetListRunState {
  item_counts: AssetListRunCounts;
}

/** One stored reference: exactly one of an input asset or a finished Task asset. */
export interface AssetListReference {
  task_id?: string;
  asset_id?: string;
  input_asset_id?: string;
}

/** One planned capability with the availability snapshot taken at proposal time. */
export interface AssetListRouteStep {
  capability: string;
  offering_id?: string;
  /** False means the step cannot run now; `reason` says why. */
  available: boolean;
  reason?: string;
}

/** One row of the project panel or the list card. */
export interface AssetListSummary {
  id: string;
  project_id: string;
  source_session_id?: string;
  origin_message_id?: string;
  title: string;
  status: string;
  /** Derived at read time: an unexpired draft whose TTL passed reads as expired. */
  effective_status: string;
  item_count: number;
  planned_step_count: number;
  planned_model3d_step_count: number;
  /** Absent until the list is decided. */
  decided_at?: string;
  decision_reason?: string;
  expires_at: string;
  created_at: string;
  updated_at: string;
  /** #328: null or absent until the list has a run; the browser shows no run line then. */
  run?: AssetListRun | null;
}

/** One asset of a list. */
export interface AssetListItem {
  id: string;
  kind: string;
  name: string;
  description?: string;
  references: AssetListReference[];
  route_id: string;
  route: AssetListRouteStep[];
  planned_step_count: number;
  planned_model3d_step_count: number;
  state: string;
  state_reason?: string;
  version: number;
  session_id?: string;
  project_asset_id?: string;
  round: number;
  created_at: string;
  updated_at: string;
}

/** The list with its shared brief and items. */
export interface AssetListDetail extends AssetListSummary {
  version: number;
  brief: Record<string, unknown>;
  /** Set on a list that a `replace` decision superseded. */
  superseded_by?: string;
  items: AssetListItem[];
}

/**
 * A refused list request. The message is the server's `detail` when it sent
 * one; `code` names the refusal (`asset_list_active_conflict`,
 * `asset_list_expired`, ...). A 409 that names the list blocking the decision
 * also carries `activeListId` and, on a confirm conflict, `activeListTitle`.
 */
export class AssetListError extends Error {
  readonly status: number;
  readonly code: string;
  readonly detail: string;
  readonly activeListId?: string;
  readonly activeListTitle?: string;

  constructor(
    message: string,
    status: number,
    options: { code?: string; detail?: string; activeListId?: string; activeListTitle?: string } = {},
  ) {
    super(message);
    this.name = 'AssetListError';
    this.status = status;
    this.code = options.code ?? '';
    this.detail = options.detail ?? message;
    this.activeListId = options.activeListId;
    this.activeListTitle = options.activeListTitle;
  }
}

export interface AssetListClientDependencies {
  fetcher?: typeof fetch;
}

export type AssetListPresenceState = 'present' | 'leave';

export interface AssetListClient {
  listProjectLists(projectID: string): Promise<AssetListSummary[]>;
  getList(projectID: string, listID: string): Promise<AssetListDetail>;
  decide(projectID: string, listID: string, decision: AssetListDecision, reason?: string): Promise<AssetListDetail>;
  listSessionLists(sessionID: string): Promise<AssetListSummary[]>;
  /** #328: start, pause, resume or stop the list's run; resolves to the new run. */
  controlRun(
    projectID: string,
    listID: string,
    action: AssetListRunAction,
    expectedVersion: number,
    reason?: string,
  ): Promise<AssetListRunState>;
  /** #328: skip one item of a list; resolves to the item as the server stored it. */
  skipItem(
    projectID: string,
    listID: string,
    itemID: string,
    expectedVersion: number,
    reason?: string,
  ): Promise<AssetListItem>;
  /** #328: this browser tab is (or is no longer) watching the project. */
  presence(projectID: string, tabID: string, state: AssetListPresenceState): Promise<void>;
}

function object(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function array(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function route(template: string, parameters: Record<string, string>): string {
  return template.replace(/\{([^{}]+)\}/g, (placeholder, name: string) => {
    const value = parameters[name];
    return value === undefined ? placeholder : encodeURIComponent(value);
  });
}

/**
 * #328: the presence URL for `navigator.sendBeacon`. Empty when the Catalog has
 * no route; the heartbeat then falls back to the client's fetch.
 */
export function assetListPresenceURL(schema: ProductSchema, projectID: string): string {
  const template = schema.runtime.http.routes.project_presence;
  return template ? route(template, { projectID }) : '';
}

export function createAssetListClient(
  schema: ProductSchema,
  dependencies: AssetListClientDependencies = {},
): AssetListClient {
  const fetcher = dependencies.fetcher ?? fetch;
  const routes = schema.runtime.http.routes;
  const fallback = schema.chat.presentation.messages.internal_error;
  const required = (template: string | undefined): string => {
    if (!template) throw new AssetListError(fallback, 0);
    return template;
  };
  const projectListsRoute = () => required(routes.project_asset_lists);
  const listRoute = (projectID: string, listID: string) =>
    route(required(routes.project_asset_list), { projectID, listID });
  const decisionRoute = (projectID: string, listID: string) =>
    route(required(routes.project_asset_list_decision), { projectID, listID });
  const runRoute = (projectID: string, listID: string) =>
    route(required(routes.project_asset_list_run), { projectID, listID });
  const itemRoute = (projectID: string, listID: string, itemID: string) =>
    route(required(routes.project_asset_list_item), { projectID, listID, itemID });
  const sessionListsRoute = (sessionID: string) =>
    route(required(routes.chat_session_asset_lists), { sessionID });
  const presenceRoute = (projectID: string) =>
    route(required(routes.project_presence), { projectID });

  // A non-2xx answer is the server's own refusal: carry its code, detail and
  // the 409 list fields. A body that is not JSON still keeps the status.
  const failure = (response: Response, payload: unknown): never => {
    const body = object(payload);
    const detail = text(body.detail);
    throw new AssetListError(detail || fallback, response.status, {
      code: text(body.code),
      detail,
      activeListId: text(body.active_list_id) || undefined,
      activeListTitle: text(body.active_list_title) || undefined,
    });
  };
  const payloadOf = async (response: Response): Promise<unknown> => {
    try {
      return await response.json();
    } catch {
      if (response.ok) throw new AssetListError(fallback, response.status);
      return null;
    }
  };
  const summaries = (payload: unknown): AssetListSummary[] => array(object(payload).data) as AssetListSummary[];
  const detailOf = (payload: unknown): AssetListDetail => object(payload) as unknown as AssetListDetail;
  const runOf = (payload: unknown): AssetListRunState => object(payload) as unknown as AssetListRunState;
  const itemOf = (payload: unknown): AssetListItem => object(payload) as unknown as AssetListItem;

  return {
    async listProjectLists(projectID) {
      const response = await fetcher(route(projectListsRoute(), { projectID }));
      const payload = await payloadOf(response);
      if (!response.ok) failure(response, payload);
      return summaries(payload);
    },
    async getList(projectID, listID) {
      const response = await fetcher(listRoute(projectID, listID));
      const payload = await payloadOf(response);
      if (!response.ok) failure(response, payload);
      return detailOf(payload);
    },
    async decide(projectID, listID, decision, reason) {
      const body = reason ? { decision, reason } : { decision };
      const response = await fetcher(decisionRoute(projectID, listID), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      const payload = await payloadOf(response);
      if (!response.ok) failure(response, payload);
      return detailOf(payload);
    },
    async controlRun(projectID, listID, action, expectedVersion, reason) {
      const body = reason ? { action, expected_version: expectedVersion, reason } : { action, expected_version: expectedVersion };
      const response = await fetcher(runRoute(projectID, listID), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      const payload = await payloadOf(response);
      if (!response.ok) failure(response, payload);
      return runOf(payload);
    },
    async skipItem(projectID, listID, itemID, expectedVersion, reason) {
      const body = reason
        ? { action: 'skip', expected_version: expectedVersion, reason }
        : { action: 'skip', expected_version: expectedVersion };
      const response = await fetcher(itemRoute(projectID, listID, itemID), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      const payload = await payloadOf(response);
      if (!response.ok) failure(response, payload);
      return itemOf(payload);
    },
    async listSessionLists(sessionID) {
      const response = await fetcher(sessionListsRoute(sessionID));
      const payload = await payloadOf(response);
      if (!response.ok) failure(response, payload);
      return summaries(payload);
    },
    async presence(projectID, tabID, state) {
      // 204: no body to read. A leave may be the last request of a page that
      // is going away, so it asks the browser to finish it after the page.
      const response = await fetcher(presenceRoute(projectID), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tab_id: tabID, state }),
        ...(state === 'leave' ? { keepalive: true } : {}),
      });
      if (!response.ok) failure(response, await payloadOf(response));
    },
  };
}

/** The client a component builds from the Schema it already holds (see `useProjects`). */
export function assetListClient(schema: ProductSchema): AssetListClient {
  return createAssetListClient(schema);
}