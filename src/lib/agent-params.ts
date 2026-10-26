/**
 * AGENTS 模式 harness 端点浏览器客户端（P0 契约）。
 *
 * 后端端点由 internal/transport/httpapi/agent_harness.go 固定注册：
 * - GET/PUT /api/v1/agent/params?scope=<session>  权威请求参数（agent set_params 与用户编辑共写）
 * - PUT     /api/v1/agent/bind                    会话绑定任务（按任务隔离）
 * - GET/PUT /api/v1/agent/consent?scope=<session> 待批准项读取/批准
 *
 * 错误 detail 一律为 error.* 词汇键，由 Product Schema content.messages 映射文案。
 */

export interface AgentHarnessRoutes {
  params: string;
  bind: string;
  consent: string;
}

export type AgentParamValues = Record<string, unknown>;
export interface AgentParameterState { value?: unknown; source: 'default' | 'agent' | 'user'; reason?: string }
export interface AgentParameterProjection { params: AgentParamValues; parameterStates: Record<string, AgentParameterState> }
export function parseAgentParameterProjection(payload: Record<string, unknown>): AgentParameterProjection {
  const params = isRecord(payload.params) ? payload.params : {};
  const states = isRecord(payload.parameter_states) ? payload.parameter_states : {};
  const parameterStates: Record<string, AgentParameterState> = {};
  for (const [name, raw] of Object.entries(states)) {
    if (!isRecord(raw) || !['default', 'agent', 'user'].includes(String(raw.source))) continue;
    parameterStates[name] = {
      value: raw.value,
      source: raw.source as AgentParameterState['source'],
      ...(typeof raw.reason === 'string' ? { reason: raw.reason } : {}),
    };
  }
  return { params, parameterStates };
}

export interface AgentConsentProjection {
  pending: Record<string, unknown> | null;
  approved: boolean;
  taskId: string | null;
  requestId: string | null;
}

export interface AgentHarnessClient {
  getParamsWithState(scope: string, signal?: AbortSignal, offeringId?: string): Promise<AgentParameterProjection>;
  putParamsWithState(scope: string, params: AgentParamValues, signal?: AbortSignal, offeringId?: string): Promise<AgentParameterProjection>;
  getParams(scope: string, signal?: AbortSignal, offeringId?: string): Promise<AgentParamValues>;
  putParams(
    scope: string,
    params: AgentParamValues,
    signal?: AbortSignal,
    offeringId?: string,
  ): Promise<AgentParamValues>;
  getConsent(
    scope: string,
    signal?: AbortSignal,
  ): Promise<Record<string, unknown> | null>;
  putConsent(
    scope: string,
    requestId: string,
    approved: boolean,
    signal?: AbortSignal,
  ): Promise<void>;
  bindTask(
    session: string,
    task: string,
    signal?: AbortSignal,
  ): Promise<void>;
}

export interface AgentHarnessClientDependencies {
  fetcher?: typeof fetch;
  routes: AgentHarnessRoutes;
}

export class AgentHarnessError extends Error {
  readonly status: number;
  readonly detail: string;

  constructor(detail: string, status: number) {
    super(detail);
    this.name = 'AgentHarnessError';
    this.status = status;
    this.detail = detail;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function scopedUrl(route: string, scope: string, offeringId?: string): string {
  return `${route}?scope=${encodeURIComponent(scope)}${offeringId ? `&offering_id=${encodeURIComponent(offeringId)}` : ''}`;
}

async function unwrap(
  response: Response,
): Promise<Record<string, unknown>> {
  const payload: unknown = await response.json().catch(() => null);
  if (!response.ok) {
    const detail = isRecord(payload) && typeof payload.detail === 'string'
      ? payload.detail
      : 'error.agent_request_failed';
    throw new AgentHarnessError(detail, response.status);
  }
  return isRecord(payload) ? payload : {};
}

function paramsEnvelope(payload: Record<string, unknown>): AgentParamValues {
  return isRecord(payload.params) ? payload.params : {};
}

export function createAgentHarnessClient(
  dependencies: AgentHarnessClientDependencies,
): AgentHarnessClient {
  const fetcher = dependencies.fetcher ?? fetch;
  const { routes } = dependencies;

  return {
    async getParamsWithState(scope, signal, offeringId) {
      const response = await fetcher(scopedUrl(routes.params, scope, offeringId), { method: 'GET', signal });
      return parseAgentParameterProjection(await unwrap(response));
    },
    async putParamsWithState(scope, params, signal, offeringId) {
      const response = await fetcher(scopedUrl(routes.params, scope, offeringId), { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ params }), signal });
      return parseAgentParameterProjection(await unwrap(response));
    },
    async getParams(scope, signal, offeringId) {
      const response = await fetcher(scopedUrl(routes.params, scope, offeringId), {
        method: 'GET',
        signal,
      });
      return paramsEnvelope(await unwrap(response));
    },
    async putParams(scope, params, signal, offeringId) {
      const response = await fetcher(scopedUrl(routes.params, scope, offeringId), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ params }),
        signal,
      });
      return paramsEnvelope(await unwrap(response));
    },
    async getConsent(scope, signal) {
      const response = await fetcher(scopedUrl(routes.consent, scope), {
        method: 'GET',
        signal,
      });
      const payload = await unwrap(response);
      return isRecord(payload.consent) ? payload.consent : null;
    },
    async putConsent(scope, requestId, approved, signal) {
      const response = await fetcher(scopedUrl(routes.consent, scope), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ approved, request_id: requestId }),
        signal,
      });
      await unwrap(response);
    },
    async bindTask(session, task, signal) {
      const response = await fetcher(routes.bind, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session, task }),
        signal,
      });
      await unwrap(response);
    },
  };
}

/**
 * 用户本地编辑叠加到服务端权威参数之上；undefined 表示清空该键
 * （清空后由服务端默认值 / schema 默认值兜底）。
 */
export function mergeAgentParams(
  server: AgentParamValues,
  local: AgentParamValues,
): AgentParamValues {
  const merged: AgentParamValues = { ...server };
  for (const [key, value] of Object.entries(local)) {
    if (value === undefined || value === null) {
      delete merged[key];
    } else {
      merged[key] = value;
    }
  }
  return merged;
}

/** 把 consent GET 的自由形态状态投影为 UI 可直接消费的三字段。 */
export function projectConsentState(
  state: Record<string, unknown> | null,
): AgentConsentProjection {
  if (!isRecord(state)) {
    return { pending: null, approved: false, taskId: null, requestId: null };
  }
  return {
    pending: isRecord(state.consent_pending) ? state.consent_pending : null,
    approved: state.consent_approved === true,
    taskId: typeof state.task_id === 'string' ? state.task_id : null,
    requestId: typeof state.consent_request_id === 'string' ? state.consent_request_id : null,
  };
}

/** 同意决定值（后端 agenttools.ConsentDecision* 词汇）。 */
export type ConsentDecision = 'pending' | 'approved' | 'denied' | 'none';

/**
 * 读取后端写入的同意决定标记（consent_decision）。缺省（旧状态或从未
 * 请求）时回退到 consent_approved：已批准视为 approved，否则 pending。
 */
export function consentDecision(
  state: Record<string, unknown> | null,
): ConsentDecision {
  if (!isRecord(state) || !isRecord(state.consent_pending)) return 'none';
  const decision = state.consent_decision;
  if (decision === 'approved' || decision === 'denied') return decision;
  return state.consent_approved === true ? 'approved' : 'pending';
}

/**
 * 是否仍有待用户批准的同意请求：pending 存在且尚未做出 approved/denied
 * 决定。批准后 consent_pending 仍在 store 中，但决定标记翻转为 approved，
 * 因此对话框据此隐藏（避免批准后仍常驻）。
 */
export function consentAwaitingDecision(
  state: Record<string, unknown> | null,
): boolean {
  return consentDecision(state) === 'pending';
}
