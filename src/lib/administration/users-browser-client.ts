import type {
  AdministrationProductSchema,
  AuthenticationAPISchema,
} from '../product-schema';

export interface AdministrationUser {
  id: string;
  username: string;
  nickname: string;
  name: string;
  email: string;
  isAdmin: boolean;
  isActive: boolean;
  createdAt: string;
  lastLogin: string | null;
  openId: string | null;
}

export interface AdministrationUserCreate {
  username: string;
  password: string;
  name: string;
  email: string;
  isAdmin: boolean;
}

export interface AdministrationUserUpdate {
  name?: string;
  email?: string;
  password?: string;
  isAdmin?: boolean;
  isActive?: boolean;
}

export interface AdministrationUsersBrowserRoutes {
  collection: string;
  item: string;
  session: string;
}

type Fetcher = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

export interface AdministrationUsersBrowserClientConfig {
  schema: AdministrationProductSchema['users'];
  authenticationAPI: AuthenticationAPISchema;
  routes: AdministrationUsersBrowserRoutes;
  fetcher?: Fetcher;
}

export interface AdministrationUsersBrowserClient {
  listUsers(): Promise<AdministrationUser[]>;
  createUser(input: AdministrationUserCreate): Promise<AdministrationUser>;
  updateUser(
    userID: string,
    input: AdministrationUserUpdate,
  ): Promise<AdministrationUser>;
  deleteUser(userID: string): Promise<{ success: boolean }>;
  currentUserID(): Promise<string | null>;
}

export class AdministrationUsersBrowserError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'AdministrationUsersBrowserError';
    this.status = status;
  }
}

export function createAdministrationUsersBrowserClient(
  config: AdministrationUsersBrowserClientConfig,
): AdministrationUsersBrowserClient {
  const fetcher = config.fetcher ?? fetch;
  const fields = config.schema.api.fields;
  const pathParameter = config.schema.api.path_parameters.user_id;

  const itemRoute = (userID: string): string => {
    const token = `{${pathParameter}}`;
    if (!config.routes.item.includes(token)) {
      throw new Error(`Administration user route is missing ${token}`);
    }
    return config.routes.item.replace(token, encodeURIComponent(userID));
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
      const record = asRecord(payload);
      const message =
        optionalString(record[fields.error]) ??
        config.schema.messages.internal_error;
      throw new AdministrationUsersBrowserError(response.status, message);
    }
    return payload;
  };

  const userWire = (payload: unknown): AdministrationUser => {
    const record = asRecord(payload);
    return {
      id: optionalString(record[fields.id]) ?? '',
      username: optionalString(record[fields.username]) ?? '',
      nickname: optionalString(record[fields.nickname]) ?? '',
      name: optionalString(record[fields.name]) ?? '',
      email: optionalString(record[fields.email]) ?? '',
      isAdmin: record[fields.is_admin] === true,
      isActive: record[fields.is_active] === true,
      createdAt: optionalString(record[fields.created_at]) ?? '',
      lastLogin: optionalString(record[fields.last_login]),
      openId: optionalString(record[fields.open_id]),
    };
  };

  return {
    async listUsers() {
      const payload = asRecord(await request(
        config.routes.collection,
        'GET',
      ));
      const users = payload[fields.data];
      return Array.isArray(users) ? users.map(userWire) : [];
    },

    async createUser(input) {
      const payload = asRecord(await request(
        config.routes.collection,
        'POST',
        {
          [fields.username]: input.username,
          [fields.password]: input.password,
          [fields.name]: input.name,
          [fields.email]: input.email,
          [fields.is_admin]: input.isAdmin,
        },
      ));
      return userWire(payload[fields.data]);
    },

    async updateUser(userID, input) {
      const body: Record<string, unknown> = {};
      if (input.name !== undefined) body[fields.name] = input.name;
      if (input.email !== undefined) body[fields.email] = input.email;
      if (input.password !== undefined) {
        body[fields.password] = input.password;
      }
      if (input.isAdmin !== undefined) {
        body[fields.is_admin] = input.isAdmin;
      }
      if (input.isActive !== undefined) {
        body[fields.is_active] = input.isActive;
      }
      const payload = asRecord(await request(
        itemRoute(userID),
        'PATCH',
        body,
      ));
      return userWire(payload[fields.data]);
    },

    async deleteUser(userID) {
      const payload = asRecord(await request(itemRoute(userID), 'DELETE'));
      return { success: payload[fields.success] === true };
    },

    async currentUserID() {
      const payload = asRecord(await request(config.routes.session, 'GET'));
      const user = asRecord(payload[config.authenticationAPI.user_field]);
      return optionalString(user[config.authenticationAPI.id_field]);
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
