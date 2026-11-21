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
 * paused (user, presence, step_cap, failure); `version` is the run's opaque
 * updated_at token. The card still re-reads the list instead of rendering this
 * answer's item tally.
 */
export interface AssetListRunState {
  status: string;
  pause_reason?: string;
  /** The run's opaque RFC3339Nano updated_at token; never the list's version. */
  version: string;
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
  /** #331: the provenance of the item's crop reference; absent on older servers. */
  crop?: AssetListCrop;
  route_id: string;
  route: AssetListRouteStep[];
  planned_step_count: number;
  planned_model3d_step_count: number;
  state: string;
  state_reason?: string;
  /** #328: the item's second axis (awaiting_approval, step_cap); absent otherwise. */
  block?: string;
  version: number;
  session_id?: string;
  project_asset_id?: string;
  round: number;
  /** #330 step 5C: the live 3D step standing of the item's asset; absent before the item has a sub-session. */
  steps?: AssetListItemSteps;
  created_at: string;
  updated_at: string;
}

/**
 * #330 step 5C: the live step standing of one item's asset. `cap` and `extra`
 * appear only in the cap mode; the item's limit is `cap + extra`.
 */
export interface AssetListItemSteps {
  mode: string;
  used: number;
  cap?: number;
  extra?: number;
}

/** The list with its shared brief and items. */
export interface AssetListDetail extends AssetListSummary {
  version: number;
  brief: Record<string, unknown>;
  /** Set on a list that a `replace` decision superseded. */
  superseded_by?: string;
  items: AssetListItem[];
}

/** #329: one stored result file of the acceptance snapshot. */
export interface AssetListBoardArtifact {
  asset_id: string;
  role?: string;
  variant?: string;
  media_type?: string;
  size?: number;
  sha256?: string;
}

/**
 * #329: one acceptance criterion as the server judged it. `status` is pass,
 * fail or unknown; unknown is a fact (no readable GLB, an unusable threshold),
 * never a pass. `actual` is whatever the judge measured for that criterion.
 */
export interface AssetListBoardCheck {
  check: string;
  status: string;
  actual?: unknown;
  budget?: string;
  detail?: string;
}

/** #329: the 3D judgment; an empty asset_id means the results held no GLB. */
export interface AssetListBoardModel3D {
  asset_id?: string;
  checks: AssetListBoardCheck[];
}

/** #329: the 2D delivery judgment of the cover picture. */
export interface AssetListBoardImage2D {
  asset_id?: string;
  width?: number;
  height?: number;
  checks: AssetListBoardCheck[];
}

/** #329: the Agent's persisted self-check conclusion, when one exists. */
export interface AssetListBoardSelfCheck {
  status?: string;
  summary?: string;
  remaining?: string[];
}

/** #329: the step summary of the item's sub-session (#330 wording: steps, not credits). */
export interface AssetListBoardSteps {
  used: number;
  model3d: number;
  cap?: number;
  /** #330 step 5C: the person's extra-step grant for this asset (cap mode only). */
  extra?: number;
  mode?: string;
}

/** #329: the snapshot taken when the item entered review. */
export interface AssetListBoardReview {
  captured_at?: string;
  round?: number;
  task_id?: string;
  cover_asset_id?: string;
  artifacts?: AssetListBoardArtifact[];
  model3d?: AssetListBoardModel3D;
  image2d?: AssetListBoardImage2D;
  self_check?: AssetListBoardSelfCheck;
  steps?: AssetListBoardSteps;
}

/** #329: the server-built completion snapshot stored with a reported-done item. */
export interface AssetListBoardResult {
  task_id?: string;
  assets?: Array<{ id: string; name?: string; role?: string }>;
}

/** #329: one recorded acceptance decision (the latest round shown on the board). */
export interface AssetListBoardDecision {
  id: string;
  round: number;
  verdict: string;
  reason?: string;
  reasons?: string[];
  note?: string;
  reviewer_id?: string;
  review?: Record<string, unknown>;
  created_at: string;
}

/** #329c: where one crop of the item was cut from. */
export interface AssetListBoardSource {
  source_input_asset_id: string;
  source_kind?: string;
  source_width?: number;
  source_height?: number;
}

/** #329c: the cut geometry: the normalized region and its source-pixel rectangle. */
export interface AssetListBoardRegion extends CropRegion {
  pixel_rect?: { x0: number; y0: number; x1: number; y1: number };
}

/**
 * #329: one item of the acceptance board: the list item plus its produced
 * artifacts, its acceptance snapshot, the latest decision and the crop
 * provenance (#331). `result` and `review` are `{}` before the item has them;
 * `latest_review` is null until the person decides.
 */
export interface AssetListBoardItem extends AssetListItem {
  result?: AssetListBoardResult | null;
  review?: AssetListBoardReview | null;
  latest_review?: AssetListBoardDecision | null;
  reference_asset_id?: string;
  source?: AssetListBoardSource;
  region?: AssetListBoardRegion;
  representative_asset_id?: string;
}

/** #329: the read-only acceptance board: the list summary plus every item in position order. */
export interface AssetListBoard {
  list: AssetListSummary & { version: number };
  items: AssetListBoardItem[];
}

/** #329d: the two board decisions; a return names one item, an accept may name many. */
export type AssetListReviewDecision = 'accept' | 'return';

/**
 * #329d: one board submission. `expected_version` is the list version the
 * board read; a stale one is a 409 and nothing is written. A return needs at
 * least one `reason_ids` entry; an accept may carry a note but never reasons.
 */
export interface AssetListReviewInput {
  decision: AssetListReviewDecision;
  item_ids: string[];
  reason_ids?: string[];
  note?: string;
  expected_version: number;
}

/** #329d: one item's outcome of a batch; `error` is the server's per-item refusal. */
export interface AssetListItemReview {
  item_id: string;
  verdict?: string;
  project_asset_id?: string;
  error?: string;
}

/** #329d: the refreshed list plus one outcome per submitted item. */
export interface AssetListReviewResult {
  list: AssetListDetail;
  items: AssetListItemReview[];
}

/** #329d: a downloaded archive and the filename the server suggested. */
export interface AssetListArchiveDownload {
  blob: Blob;
  filename: string;
}

/** #331: a normalized region relative to its source picture, right and bottom edges exclusive. */
export interface CropRegion {
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * #331: an edit instruction. The server cuts `region` out of the owner's
 * `source_input_asset_id` picture, stores it as an input asset and makes it the
 * item's crop reference, replacing the item's previous crop reference.
 */
export interface CropInstruction {
  source_input_asset_id: string;
  region: CropRegion;
}

/**
 * #331: the crop provenance the list detail exposes for one item's crop
 * reference. Geometry only: the source picture and the normalized region.
 * Absent when the item has no crop reference or the server predates #331.
 */
export interface AssetListCrop extends CropInstruction {
  source_kind?: string;
  source_width?: number;
  source_height?: number;
  pixel_rect?: { x0: number; y0: number; x1: number; y1: number };
}

/**
 * #331: one item of a whole-list edit. `references` are explicit project
 * references (an input asset or a Task output); the edit route resolves no
 * @handles. `crop` is edit-only: the proposal tool refuses it and names its
 * crops through `reference_input_asset_ids` instead.
 */
export interface AssetListItemInput {
  kind: string;
  name: string;
  description?: string;
  route_id?: string;
  references?: AssetListReference[];
  crop?: CropInstruction;
}

/**
 * #331: the whole-list replacement body of `PUT /asset-lists/{listID}`. The
 * server replaces the list's items with exactly this set. `expected_version`
 * is the numeric list version the browser read; a stale version is a 409.
 * `brief` is replaced as well: omitting it stores an empty brief, so an editor
 * echoes the detail's brief.
 */
export interface AssetListEditInput {
  title: string;
  expected_version: number;
  items: AssetListItemInput[];
  brief?: Record<string, unknown>;
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
  /** #329: the read-only acceptance board of one list, every item from one aggregate read. */
  getBoard(projectID: string, listID: string): Promise<AssetListBoard>;
  /**
   * #329d: accept one or many reviewed items, or return exactly one with its
   * declared reasons. The list version gates the whole batch.
   */
  review(projectID: string, listID: string, input: AssetListReviewInput): Promise<AssetListReviewResult>;
  /**
   * #329d: the batch archive of the accepted items. `itemIDs` narrows it to a
   * subset after the server refused a whole-list download as too large.
   */
  downloadArchive(projectID: string, listID: string, itemIDs?: readonly string[]): Promise<AssetListArchiveDownload>;
  decide(projectID: string, listID: string, decision: AssetListDecision, reason?: string): Promise<AssetListDetail>;
  listSessionLists(sessionID: string): Promise<AssetListSummary[]>;
  /**
   * #328: start, pause, resume or stop the list's run; resolves to the new run.
   * `expectedVersion` is the run summary's opaque `version` string, not the
   * list's numeric version.
   */
  controlRun(
    projectID: string,
    listID: string,
    action: AssetListRunAction,
    expectedVersion: string,
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
  /**
   * #330 step 5C: set one item asset's whole extra-step grant. The value is an
   * absolute grant (not an increment), so a retried request cannot add twice;
   * `expectedVersion` is the item version the browser read.
   */
  addItemSteps(
    projectID: string,
    listID: string,
    itemID: string,
    extraSteps: number,
    expectedVersion: number,
  ): Promise<AssetListItem>;
  /** #328: this browser tab is (or is no longer) watching the project. */
  presence(projectID: string, tabID: string, state: AssetListPresenceState): Promise<void>;
  /**
   * #331: replace a draft list's title, brief and items as a whole. Resolves to
   * the stored detail; a stale `expected_version` is a 409 `AssetListError`.
   */
  edit(projectID: string, listID: string, input: AssetListEditInput): Promise<AssetListDetail>;
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

/** The server's `Content-Disposition` name, preferring RFC 5987 when present. */
export function archiveFilename(header: string | null): string {
  if (!header) return '';
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(header);
  if (encoded) {
    try {
      return decodeURIComponent(encoded[1]);
    } catch {
      return encoded[1];
    }
  }
  const plain = /filename="?([^";]+)"?/i.exec(header);
  return plain ? plain[1] : '';
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
  const boardRoute = (projectID: string, listID: string) =>
    route(required(routes.project_asset_list_board), { projectID, listID });
  const reviewRoute = (projectID: string, listID: string) =>
    route(required(routes.project_asset_list_item_review), { projectID, listID });
  const archiveRoute = (projectID: string, listID: string, itemIDs: readonly string[]) => {
    const base = route(required(routes.project_asset_list_archive), { projectID, listID });
    const query = itemIDs
      .filter((id) => id !== '')
      .map((id) => `item_ids=${encodeURIComponent(id)}`)
      .join('&');
    return query ? `${base}?${query}` : base;
  };
  const decisionRoute = (projectID: string, listID: string) =>
    route(required(routes.project_asset_list_decision), { projectID, listID });
  const runRoute = (projectID: string, listID: string) =>
    route(required(routes.project_asset_list_run), { projectID, listID });
  const itemRoute = (projectID: string, listID: string, itemID: string) =>
    route(required(routes.project_asset_list_item), { projectID, listID, itemID });
  const itemStepsRoute = (projectID: string, listID: string, itemID: string) =>
    route(required(routes.project_asset_list_item_steps), { projectID, listID, itemID });
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
  const boardOf = (payload: unknown): AssetListBoard => object(payload) as unknown as AssetListBoard;
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
    async getBoard(projectID, listID) {
      const response = await fetcher(boardRoute(projectID, listID));
      const payload = await payloadOf(response);
      if (!response.ok) failure(response, payload);
      return boardOf(payload);
    },
    async review(projectID, listID, input) {
      const body: Record<string, unknown> = {
        decision: input.decision,
        item_ids: input.item_ids,
      };
      if (input.reason_ids && input.reason_ids.length > 0) body.reason_ids = input.reason_ids;
      if (input.note) body.note = input.note;
      body.expected_version = input.expected_version;
      const response = await fetcher(reviewRoute(projectID, listID), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      const payload = await payloadOf(response);
      if (!response.ok) failure(response, payload);
      return object(payload) as unknown as AssetListReviewResult;
    },
    async downloadArchive(projectID, listID, itemIDs) {
      const response = await fetcher(archiveRoute(projectID, listID, itemIDs ?? []));
      if (!response.ok) failure(response, await payloadOf(response));
      const blob = await response.blob();
      return {
        blob,
        filename: archiveFilename(response.headers.get('content-disposition')) || `asset-list-${listID}.zip`,
      };
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
    async addItemSteps(projectID, listID, itemID, extraSteps, expectedVersion) {
      const response = await fetcher(itemStepsRoute(projectID, listID, itemID), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ extra_steps: extraSteps, expected_version: expectedVersion }),
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
    async edit(projectID, listID, input) {
      const response = await fetcher(listRoute(projectID, listID), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      });
      const payload = await payloadOf(response);
      if (!response.ok) failure(response, payload);
      return detailOf(payload);
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