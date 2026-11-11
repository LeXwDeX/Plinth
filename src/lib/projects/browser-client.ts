import type { ProductSchema } from '@/lib/product-schema';

/**
 * #240: browser client for the owner-scoped project routes. Every route and
 * limit comes from the public Schema; agent templates and server copy stay out
 * of this projection. Unknown or foreign projects answer 404 and are surfaced
 * as the server's own text.
 */

export type ProjectAssetKind = string;

export interface ProjectSummary {
  id: string;
  name: string;
  createdAt: string;
  updatedAt: string;
}

/** One stored reference: exactly one of an input asset or a finished Task asset. */
export interface ProjectReference {
  task_id?: string;
  asset_id?: string;
  input_asset_id?: string;
}

export interface ProjectAsset {
  id: string;
  projectId: string;
  kind: ProjectAssetKind;
  name: string;
  description: string;
  images: ProjectReference[];
  models: ProjectReference[];
  createdAt: string;
  updatedAt: string;
}

export interface ProjectStyleGuide {
  projectId: string;
  enabled: boolean;
  text: string;
  palette: string[];
  references: ProjectReference[];
  updatedAt: string;
}

export interface ProjectConventions {
  projectId: string;
  brief: Record<string, unknown>;
  texts: Record<string, string>;
  updatedAt: string;
}

export interface PromoteProjectAssetInput {
  source: ProjectReference;
  kind: ProjectAssetKind;
  name: string;
  description?: string;
}

export class ProjectBrowserError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = 'ProjectBrowserError';
    this.status = status;
  }
}

export interface ProjectBrowserDependencies {
  fetcher?: typeof fetch;
}

interface ProjectClientContract {
  listProjects(signal?: AbortSignal): Promise<ProjectSummary[]>;
  createProject(name: string): Promise<ProjectSummary>;
  renameProject(projectID: string, name: string): Promise<ProjectSummary>;
  deleteProject(projectID: string): Promise<void>;
  getStyleGuide(projectID: string, signal?: AbortSignal): Promise<ProjectStyleGuide>;
  setStyleGuide(projectID: string, guide: Omit<ProjectStyleGuide, 'projectId' | 'updatedAt'>): Promise<ProjectStyleGuide>;
  getConventions(projectID: string, signal?: AbortSignal): Promise<ProjectConventions>;
  setConventions(projectID: string, conventions: Pick<ProjectConventions, 'brief' | 'texts'>): Promise<ProjectConventions>;
  listAssets(projectID: string, signal?: AbortSignal): Promise<ProjectAsset[]>;
  createAsset(projectID: string, input: { kind: ProjectAssetKind; name: string; description?: string; images?: ProjectReference[]; models?: ProjectReference[] }): Promise<ProjectAsset>;
  updateAsset(projectID: string, assetID: string, input: { kind?: string; name?: string; description?: string }): Promise<ProjectAsset>;
  deleteAsset(projectID: string, assetID: string): Promise<void>;
  promoteAsset(projectID: string, input: PromoteProjectAssetInput): Promise<ProjectAsset>;
}

export type ProjectBrowserClient = ProjectClientContract;

function object(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function array(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function references(value: unknown): ProjectReference[] {
  return array(value).map((item) => {
    const source = object(item);
    return {
      ...(text(source.task_id) ? { task_id: text(source.task_id) } : {}),
      ...(text(source.asset_id) ? { asset_id: text(source.asset_id) } : {}),
      ...(text(source.input_asset_id) ? { input_asset_id: text(source.input_asset_id) } : {}),
    };
  });
}

function projectSummary(value: unknown): ProjectSummary {
  const source = object(value);
  return {
    id: text(source.id),
    name: text(source.name),
    createdAt: text(source.created_at),
    updatedAt: text(source.updated_at),
  };
}

function projectAsset(value: unknown): ProjectAsset {
  const source = object(value);
  return {
    id: text(source.id),
    projectId: text(source.project_id),
    kind: text(source.kind),
    name: text(source.name),
    description: text(source.description),
    images: references(source.images),
    models: references(source.models),
    createdAt: text(source.created_at),
    updatedAt: text(source.updated_at),
  };
}

function styleGuide(value: unknown): ProjectStyleGuide {
  const source = object(value);
  return {
    projectId: text(source.project_id),
    enabled: source.enabled === true,
    text: text(source.text),
    palette: array(source.palette).filter((item): item is string => typeof item === 'string'),
    references: references(source.references),
    updatedAt: text(source.updated_at),
  };
}

function conventions(value: unknown): ProjectConventions {
  const source = object(value);
  return {
    projectId: text(source.project_id),
    brief: object(source.brief),
    texts: Object.fromEntries(Object.entries(object(source.texts))
      .filter((entry): entry is [string, string] => typeof entry[1] === 'string')),
    updatedAt: text(source.updated_at),
  };
}

function route(template: string, parameters: Record<string, string>): string {
  return template.replace(/\{([^{}]+)\}/g, (placeholder, name: string) => {
    const value = parameters[name];
    return value === undefined ? placeholder : encodeURIComponent(value);
  });
}

export function createProjectBrowserClient(
  schema: ProductSchema,
  dependencies: ProjectBrowserDependencies = {},
): ProjectBrowserClient {
  const fetcher = dependencies.fetcher ?? fetch;
  const routes = schema.runtime.http.routes;
  const required = (template: string | undefined): string => {
    if (!template) throw new ProjectBrowserError(schema.chat.presentation.messages.internal_error, 0);
    return template;
  };
  const projectRoute = () => required(routes.project);
  const itemRoute = (projectID: string) => route(required(routes.project_item), { projectID });
  const assetsRoute = (projectID: string) => route(required(routes.project_assets), { projectID });
  const assetRoute = (projectID: string, assetID: string) =>
    route(required(routes.project_asset_item), { projectID, assetID });
  const styleRoute = (projectID: string) => route(required(routes.project_style_guide), { projectID });
  const conventionsRoute = (projectID: string) => route(required(routes.project_conventions), { projectID });
  const promoteRoute = (projectID: string) => route(required(routes.project_promote), { projectID });

  const failure = async (response: Response, payload: unknown): Promise<never> => {
    const message = text(object(payload).error) || text(object(payload).detail);
    throw new ProjectBrowserError(message || schema.chat.presentation.messages.internal_error, response.status);
  };
  const value = async (response: Response): Promise<unknown> => {
    if (response.status === 204) return null;
    try {
      return await response.json();
    } catch {
      return null;
    }
  };
  return {
    async listProjects(signal) {
      const response = await fetcher(projectRoute(), { signal });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
      return array(object(payload).projects).map(projectSummary);
    },
    async createProject(name) {
      const response = await fetcher(projectRoute(), {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
      });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
      return projectSummary(payload);
    },
    async renameProject(projectID, name) {
      const response = await fetcher(itemRoute(projectID), {
        method: 'PATCH', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
      });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
      return projectSummary(payload);
    },
    async deleteProject(projectID) {
      const response = await fetcher(itemRoute(projectID), { method: 'DELETE' });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
    },
    async getStyleGuide(projectID, signal) {
      const response = await fetcher(styleRoute(projectID), { signal });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
      return styleGuide(payload);
    },
    async setStyleGuide(projectID, guide) {
      const response = await fetcher(styleRoute(projectID), {
        method: 'PUT', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          enabled: guide.enabled, text: guide.text,
          palette: guide.palette, references: guide.references,
        }),
      });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
      return styleGuide(payload);
    },
    async getConventions(projectID, signal) {
      const response = await fetcher(conventionsRoute(projectID), { signal });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
      return conventions(payload);
    },
    async setConventions(projectID, next) {
      const response = await fetcher(conventionsRoute(projectID), {
        method: 'PUT', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ brief: next.brief, texts: next.texts }),
      });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
      return conventions(payload);
    },
    async listAssets(projectID, signal) {
      const response = await fetcher(assetsRoute(projectID), { signal });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
      return array(object(payload).assets).map(projectAsset);
    },
    async createAsset(projectID, input) {
      const response = await fetcher(assetsRoute(projectID), {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
      return projectAsset(payload);
    },
    async updateAsset(projectID, assetID, input) {
      const response = await fetcher(assetRoute(projectID, assetID), {
        method: 'PATCH', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
      return projectAsset(payload);
    },
    async deleteAsset(projectID, assetID) {
      const response = await fetcher(assetRoute(projectID, assetID), { method: 'DELETE' });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
    },
    async promoteAsset(projectID, input) {
      const response = await fetcher(promoteRoute(projectID), {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      });
      const payload = await value(response);
      if (!response.ok) return failure(response, payload);
      return projectAsset(payload);
    },
  };
}