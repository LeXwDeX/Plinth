import type { CanvasProductSchema } from '../product-schema';

export interface CanvasProjectSummary {
  id: string;
  name: string;
  sessionId: string;
  bgStyle: string;
  createdAt: string;
  updatedAt: string;
  nodeCount: number;
}

export interface CanvasNodeRecord {
  id: string;
  projectId: string;
  type: string;
  x: number;
  y: number;
  width: number;
  height: number;
  zIndex: number;
  data: string;
  visible: boolean;
  locked: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface CanvasNodeInput {
  id: string;
  type: string;
  x: number;
  y: number;
  width: number;
  height: number;
  zIndex: number;
  data: string;
  visible: boolean;
  locked: boolean;
}

/** The canvas pixel size and the coordinate contract its nodes use (#246). */
export interface CanvasFrame {
  width: number;
  height: number;
  coordinateVersion: number;
}

export interface CanvasProject {
  id: string;
  userId: string;
  name: string;
  sessionId: string;
  viewport: string;
  bgStyle: string;
  /** Only a create response can confirm a new project (#302). */
  created?: boolean;
  /** 乐观锁版本（B15/C2）：保存时回传，冲突时服务端拒绝。 */
  revision?: number;
  createdAt: string;
  updatedAt: string;
  nodes?: CanvasNodeRecord[];
  /**
   * 已放置结果清单（#239/#262）：null 表示旧画布尚未登记。与节点同一 revision 保存。
   */
  placedResults?: string[] | null;
  /**
   * 画布像素尺寸（#246）：null 表示尚未定尺寸。只在带节点的项目读取中出现。
   */
  frame?: CanvasFrame | null;
  /**
   * 节点几何所用的坐标契约版本：新建项目即为当前版本（尺寸未定时节点也已是像素坐标）；
   * null 表示旧画布，打开时一次性迁移。只在带节点的项目读取中出现。
   */
  coordinateVersion?: number | null;
}

export interface CanvasProjectUpdate {
  name?: string;
  viewport?: string;
  bgStyle?: string;
}

export interface CanvasMergeNode {
  imageUrl: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface CanvasMergeResult {
  assetId: string;
  url: string;
}

export interface CanvasBrowserRoutes {
  projects: string;
  project: string;
  nodes: string;
  merge: string;
}

type Fetcher = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

export interface CanvasBrowserClientConfig {
  schema: CanvasProductSchema;
  routes: CanvasBrowserRoutes;
  fetcher?: Fetcher;
}

export class CanvasBrowserError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'CanvasBrowserError';
    this.status = status;
  }
}

export interface CanvasBrowserClient {
  listProjects(): Promise<CanvasProjectSummary[]>;
  createProject(name?: string, sessionID?: string): Promise<CanvasProject>;
  getProject(projectID: string): Promise<CanvasProject>;
  updateProject(
    projectID: string,
    update: CanvasProjectUpdate,
  ): Promise<CanvasProject>;
  deleteProject(projectID: string): Promise<{ ok: boolean }>;
  replaceNodes(
    projectID: string,
    nodes: CanvasNodeInput[],
    revision?: number,
    placedResults?: string[],
    frame?: CanvasFrame,
  ): Promise<{ ok: boolean; count: number }>;
  mergeImages(nodes: CanvasMergeNode[]): Promise<CanvasMergeResult>;
}

export function createCanvasBrowserClient(
  config: CanvasBrowserClientConfig,
): CanvasBrowserClient {
  const fetcher = config.fetcher ?? fetch;
  const fields = config.schema.api.fields;
  const pathParameter = config.schema.api.path_parameters.project_id;

  const itemRoute = (template: string, projectID: string): string => {
    const token = `{${pathParameter}}`;
    if (!template.includes(token)) {
      throw new Error(`Canvas route is missing ${token}`);
    }
    return template.replace(token, encodeURIComponent(projectID));
  };

  const request = async (
    target: string,
    method: string,
    body?: Record<string, unknown>,
  ): Promise<unknown> => {
    const response = await fetcher(target, {
      method,
      ...(body === undefined
        ? {}
        : {
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
          }),
    });
    let payload: unknown;
    try {
      payload = await response.json();
    } catch {
      payload = undefined;
    }
    if (!response.ok) {
      const errorPayload = asRecord(payload);
      const message =
        optionalString(errorPayload[fields.error]) ??
        config.schema.presentation.messages.internal_error;
      throw new CanvasBrowserError(response.status, message);
    }
    return payload;
  };

  return {
    async listProjects() {
      const payload = await request(config.routes.projects, 'GET');
      if (!Array.isArray(payload)) {
        throw invalidPayload(config.schema);
      }
      return payload.map((entry) => {
        const project = asRecord(entry);
        const count = asRecord(project[fields.node_count]);
        return {
          id: requiredString(project[fields.id], config.schema),
          name: requiredString(project[fields.name], config.schema),
          sessionId: optionalString(project[fields.session_id]) ?? '',
          bgStyle: requiredString(project[fields.bg_style], config.schema),
          createdAt: requiredString(
            project[fields.created_at],
            config.schema,
          ),
          updatedAt: requiredString(
            project[fields.updated_at],
            config.schema,
          ),
          nodeCount: requiredNumber(count[fields.nodes], config.schema),
        };
      });
    },

    async createProject(name, sessionID) {
      const body: Record<string, unknown> = {};
      if (name !== undefined) body[fields.name] = name;
      if (sessionID !== undefined) body[fields.session_id] = sessionID;
      const payload = await request(config.routes.projects, 'POST', body);
      return { ...normalizeProject(payload, config.schema), created: asRecord(payload)[fields.created] === true };
    },

    async getProject(projectID) {
      const payload = await request(
        itemRoute(config.routes.project, projectID),
        'GET',
      );
      return normalizeProject(payload, config.schema);
    },

    async updateProject(projectID, update) {
      const body: Record<string, unknown> = {};
      if (update.name !== undefined) {
        body[fields.name] = update.name;
      }
      if (update.viewport !== undefined) {
        body[fields.viewport] = update.viewport;
      }
      if (update.bgStyle !== undefined) {
        body[fields.bg_style] = update.bgStyle;
      }
      const payload = await request(
        itemRoute(config.routes.project, projectID),
        'PATCH',
        body,
      );
      return normalizeProject(payload, config.schema);
    },

    async deleteProject(projectID) {
      const payload = asRecord(await request(
        itemRoute(config.routes.project, projectID),
        'DELETE',
      ));
      return {
        ok: requiredBoolean(payload[fields.ok], config.schema),
      };
    },

    async replaceNodes(projectID, nodes, revision, placedResults, frame) {
      const wireNodes = nodes.map((node) => ({
        [fields.id]: node.id,
        [fields.type]: node.type,
        [fields.x]: node.x,
        [fields.y]: node.y,
        [fields.width]: node.width,
        [fields.height]: node.height,
        [fields.z_index]: node.zIndex,
        [fields.data]: node.data,
        [fields.visible]: node.visible,
        [fields.locked]: node.locked,
      }));
      const body: Record<string, unknown> = { [fields.nodes]: wireNodes };
      if (revision !== undefined && revision > 0) {
        body[fields.revision] = revision;
      }
      if (placedResults !== undefined && fields.placed_results) {
        body[fields.placed_results] = placedResults;
      }
      // The pixel size travels with the nodes it describes, in the same revision.
      if (frame) {
        body[fields.canvas_width] = frame.width;
        body[fields.canvas_height] = frame.height;
        body[fields.coordinate_version] = frame.coordinateVersion;
      }
      const payload = asRecord(await request(
        itemRoute(config.routes.nodes, projectID),
        'PUT',
        body,
      ));
      return {
        ok: requiredBoolean(payload[fields.ok], config.schema),
        count: requiredNumber(payload[fields.count], config.schema),
      };
    },

    async mergeImages(nodes) {
      const payload = asRecord(await request(
        config.routes.merge,
        'POST',
        {
          [fields.nodes]: nodes.map((node) => ({
            [fields.image_url]: node.imageUrl,
            [fields.x]: node.x,
            [fields.y]: node.y,
            [fields.width]: node.width,
            [fields.height]: node.height,
          })),
        },
      ));
      return {
        assetId: requiredString(payload[fields.asset_id], config.schema),
        url: requiredString(payload[fields.url], config.schema),
      };
    },
  };
}

function normalizeProject(
  payload: unknown,
  schema: CanvasProductSchema,
): CanvasProject {
  const fields = schema.api.fields;
  const project = asRecord(payload);
  const normalized: CanvasProject = {
    id: requiredString(project[fields.id], schema),
    userId: requiredString(project[fields.user_id], schema),
    name: requiredString(project[fields.name], schema),
    sessionId: optionalString(project[fields.session_id]) ?? '',
    viewport: requiredString(project[fields.viewport], schema),
    bgStyle: requiredString(project[fields.bg_style], schema),
    revision: optionalNumber(project[fields.revision]),
    createdAt: requiredString(project[fields.created_at], schema),
    updatedAt: requiredString(project[fields.updated_at], schema),
  };
  if (fields.placed_results) {
    const placed = project[fields.placed_results];
    normalized.placedResults = Array.isArray(placed) && placed.every((entry) => typeof entry === 'string')
      ? placed as string[]
      : null;
  }
  const nodes = project[fields.nodes];
  if (Array.isArray(nodes)) {
    normalized.nodes = nodes.map((entry) => normalizeNode(entry, schema));
    normalized.frame = normalizeFrame(project, schema);
    const version = project[fields.coordinate_version];
    normalized.coordinateVersion = typeof version === 'number' && Number.isInteger(version) && version > 0 ? version : null;
  }
  return normalized;
}

/** All three fields as whole positive numbers, or null (a canvas not yet migrated to pixels). */
function normalizeFrame(project: Record<string, unknown>, schema: CanvasProductSchema): CanvasFrame | null {
  const fields = schema.api.fields;
  const width = project[fields.canvas_width];
  const height = project[fields.canvas_height];
  const coordinateVersion = project[fields.coordinate_version];
  const whole = (value: unknown): value is number => typeof value === 'number' && Number.isInteger(value) && value > 0;
  return whole(width) && whole(height) && whole(coordinateVersion) ? { width, height, coordinateVersion } : null;
}

function normalizeNode(
  payload: unknown,
  schema: CanvasProductSchema,
): CanvasNodeRecord {
  const fields = schema.api.fields;
  const node = asRecord(payload);
  return {
    id: requiredString(node[fields.id], schema),
    projectId: requiredString(node[fields.project_id], schema),
    type: requiredString(node[fields.type], schema),
    x: requiredNumber(node[fields.x], schema),
    y: requiredNumber(node[fields.y], schema),
    width: requiredNumber(node[fields.width], schema),
    height: requiredNumber(node[fields.height], schema),
    zIndex: requiredNumber(node[fields.z_index], schema),
    data: requiredString(node[fields.data], schema),
    visible: requiredBoolean(node[fields.visible], schema),
    locked: requiredBoolean(node[fields.locked], schema),
    createdAt: requiredString(node[fields.created_at], schema),
    updatedAt: requiredString(node[fields.updated_at], schema),
  };
}

function asRecord(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return {};
  }
  return value as Record<string, unknown>;
}

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined;
}

function optionalNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

function requiredString(
  value: unknown,
  schema: CanvasProductSchema,
): string {
  const text = optionalString(value);
  if (text === undefined) {
    throw invalidPayload(schema);
  }
  return text;
}

function requiredNumber(
  value: unknown,
  schema: CanvasProductSchema,
): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw invalidPayload(schema);
  }
  return value;
}

function requiredBoolean(
  value: unknown,
  schema: CanvasProductSchema,
): boolean {
  if (typeof value !== 'boolean') {
    throw invalidPayload(schema);
  }
  return value;
}

function invalidPayload(schema: CanvasProductSchema): CanvasBrowserError {
  return new CanvasBrowserError(
    500,
    schema.presentation.messages.internal_error,
  );
}
