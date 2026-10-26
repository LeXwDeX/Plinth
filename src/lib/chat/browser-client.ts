import type { ProductSchema } from '@/lib/product-schema';
import { agentScopeOf } from '@/lib/chat/agent-scope';
import { parseLineage, type LineageGraph } from '@/lib/workspace/lineage';
import { parseThreeViewGroup, parseThreeViews, type ThreeViewAction, type ThreeViewGroup } from '@/lib/workspace/three-view';
import {
  decodeAttachments,
  decodePick,
  encodeAttachmentClaims,
  encodeMentions,
  type ChatAttachmentClaim,
  type ChatMentionClaim,
} from '@/lib/chat/attachments';
import type {
  AgentScope,
  ApprovalMode,
  ChatImage,
  ChatMessage,
  ChatAttachment,
  ChatPaidStep,
  ChatSession,
} from '@/store/chat-store';
import { parseNativeQuote, parsePriceTotals, type NativeQuote } from '@/lib/pricing';
import { responseRequestId, withRequestReference } from '@/lib/problem-reference';

export interface ChatTurnOptions {
	creativeDomain?: 'image' | 'model-3d';
  /** 其他工作区域各自已选的模型；Agent 工具跨域调用时按域取用。 */
  domainModels?: Record<string, string>;
  imageURLs?: string[];
  aspectRatio?: string;
  poseMode?: string;
  model?: string;
  size?: string;
  imageSize?: string;
  parameters?: Record<string, unknown>;
  skill?: {
    id: string;
    name: string;
  };
  referenceImageIndices?: number[];
  /** 对 imageURLs 中图片的声明（截图来源等），服务端逐项校验。 */
  attachments?: ChatAttachmentClaim[];
  /** 本轮发送时工作区正选中的图片或 3D 模型，服务端逐项校验。 */
  selection?: ChatAttachmentClaim[];
  /** Schema `chat.context.canvas_input_source` when `selection` is the artboard snapshot the person sent (#266). */
  inputSource?: string;
  /** `@` references written in the text; the server verifies them, gives the pictures handles and rewrites the tokens. */
  mentions?: ChatMentionClaim[];
  /** #321: the answer to an in-chat choice card; the server checks it against the card. */
  choiceAnswer?: { choiceId: string; optionId?: string; useDefault?: boolean };
}

/** One page of a conversation, oldest first; `nextCursor` reads the page before it ('' when none). */
export interface ChatMessagePage {
  messages: ChatMessage[];
  nextCursor: string;
}

export interface AcceptedChatTurn {
  assistantMessageID: string;
  sessionID: string;
}

export interface ChatSkill {
  id: string;
  name: string;
  displayName: string;
  category: string;
  recommendedSize: string;
  thumbnailPath: string;
  rawImageURL: string;
  prompt: string;
  imageToImageTemplate?: string;
  tags?: string[];
  /** Set on a Schema system Skill: the Agent tool it directs the turn to. */
  tool?: string;
  /** The turn needs a picture (stage selection or attachment) to be sent. */
  requiresImage?: boolean;
  /** Sent as the message text when the composer is empty. */
  defaultMessage?: string;
  /** The selection returns to none once the server accepted the message that used it. */
  oneShot?: boolean;
}

export interface ChatSkillCatalog {
  revision: string;
  skills: ChatSkill[];
}

export type ChatSemanticEvent =
  | { type: 'text'; content: string }
  | { type: 'status'; content: string }
  | { type: 'thinking'; content: string }
  | { type: 'progress'; percent: number }
  | { type: 'images'; taskID: string; urls: string[] }
  | { type: 'messageCreated'; messageID: string }
  | { type: 'title'; content: string }
  | { type: 'error'; content: string }
  | {
      type: 'taskRegistered';
      taskID: string;
      capability: string;
      prompt?: string;
      model?: string;
      sessionID?: string;
      estimatedCredits?: number;
      nativeQuote?: NativeQuote | null;
      approvedByPolicy?: boolean;
    }
  | {
      type: 'taskUpdate';
      taskID: string;
      finalTaskID?: string;
      status?: string;
      progress?: number;
      result?: { imageURLs?: string[] };
      error?: string;
    }
  | { type: 'done' };

/** Preserve native unknown as evidence; never let a missing legacy amount hide it. */
export function paidStepFromTaskRegistered(event: Extract<ChatSemanticEvent, { type: 'taskRegistered' }>): ChatPaidStep | null {
  if (event.nativeQuote === undefined && event.estimatedCredits === undefined) return null;
  return { taskId: event.taskID, credits: event.nativeQuote !== undefined ? 0 : event.estimatedCredits!,
    approvedByPolicy: event.approvedByPolicy === true,
    ...(event.nativeQuote !== undefined ? { nativeQuote: event.nativeQuote } : {}) };
}

/** #322: the session's thinking level, as the server stores it for the chat model in use. */
export interface ChatReasoning {
  /** The level the next turn sends; '' when the model offers none. */
  effort: string;
  /** The model's own level values, shown verbatim; empty when it offers none. */
  levels: string[];
  defaultLevel: string;
}

/** #322: one chat model the administrator offers: its id (the connection) and upstream model name. */
export interface ChatModelOption {
  id: string;
  model: string;
}

/** #322: the session's chat model as the server resolves it. */
export interface ChatModelChoice {
  /** The model the next turn uses. */
  selected: string;
  /** The session's own choice; '' when it follows the administrator's default. */
  chosen: string;
  defaultId: string;
  /** The chosen model is no longer offered, so the default is used. */
  fallback: boolean;
  options: ChatModelOption[];
}

/** #322: one compaction of the session's context. A display record only, never a chat message. */
export interface ChatContextCompaction {
  id: string;
  /** RFC 3339 time of the compaction. */
  createdAt: string;
  trigger: 'manual' | 'auto';
  /** How many rounds the summary replaced. */
  rounds: number;
  throughMessageId: string;
}

/** #322: the session's context usage as the upstream reported it; null while unknown, never estimated. */
export interface ChatContextUsage {
  tokens: number | null;
  window: number | null;
  /** Oldest first. */
  compactions: ChatContextCompaction[];
}

/** 会话的批准方式与累计估算积分（估算值，不是结算值）。 */
export interface ChatApproval {
  approvalMode: ApprovalMode;
  estimatedCredits?: number;
  priceTotals?: import('@/lib/pricing').PriceTotal[];
}

export interface ChatSubscriptionOptions {
  fromID?: number;
  onEvent(event: ChatSemanticEvent): void;
  onDone?(): void;
  onError?(error: Error): void;
}

export interface EventSourceAdapter {
  onmessage: ((event: MessageEvent<string>) => void) | null;
  onerror: ((event?: Event) => void) | null;
  close(): void;
}

export interface ChatBrowserClient {
  generationCapability(referenceImageCount: number): string;
  enhancePrompt(
    prompt: string,
    capability: string,
    model?: string,
    signal?: AbortSignal,
  ): Promise<string>;
  enhancePromptResult(
    prompt: string,
    capability: string,
    model?: string,
    signal?: AbortSignal,
  ): Promise<PromptEnhanceResult>;
  listSkills(signal?: AbortSignal): Promise<ChatSkillCatalog>;
  createSession(title?: string): Promise<ChatSession>;
  renameSession(sessionID: string, title: string): Promise<ChatSession>;
  deleteSession(sessionID: string): Promise<boolean>;
  getSession(sessionID: string, signal?: AbortSignal): Promise<ChatSession>;
  listSessions(signal?: AbortSignal): Promise<ChatSession[]>;
  listSessionPage(query: { search: string; cursor?: string; limit: number }, signal?: AbortSignal): Promise<{ sessions: ChatSession[]; nextCursor: string; total: number }>;
  deleteMessage(
    sessionID: string,
    messageID: string,
  ): Promise<boolean>;
  /**
   * One page of the conversation, oldest first: the newest `limit` messages, or those just before
   * `before` (the `nextCursor` of an earlier page). An empty `nextCursor` means nothing is older.
   */
  listMessagePage(
    sessionID: string,
    query: { before?: string; limit: number },
    signal?: AbortSignal,
  ): Promise<ChatMessagePage>;
  startTurn(
    sessionID: string,
    content: string,
    options: ChatTurnOptions,
    signal?: AbortSignal,
  ): Promise<AcceptedChatTurn>;
  // cancelTurn 请求服务端停止当前轮次（中断 LLM 与工具执行并尽力
  // 取消已绑定的生成任务），返回是否有轮次被中断。
  cancelTurn(sessionID: string, signal?: AbortSignal): Promise<boolean>;
  getApproval(sessionID: string, signal?: AbortSignal): Promise<ChatApproval>;
  setApprovalMode(sessionID: string, mode: ApprovalMode): Promise<ChatApproval>;
  /** 会话的 Agent 范围（2D 模式 / 3D 模式 / 自动模式）。服务端按它限制 Agent 能用的工具。 */
  getAgentScope(sessionID: string, signal?: AbortSignal): Promise<AgentScope>;
  setAgentScope(sessionID: string, scope: AgentScope): Promise<AgentScope>;
  /** #322: the session's thinking level and the levels the chat model offers. */
  getReasoning(sessionID: string, signal?: AbortSignal): Promise<ChatReasoning>;
  setReasoning(sessionID: string, level: string): Promise<ChatReasoning>;
  /** #322: the session's context usage and compaction records. */
  getContext(sessionID: string, signal?: AbortSignal): Promise<ChatContextUsage>;
  /** #322: the session's chat model and the models the administrator offers. */
  getChatModel(sessionID: string, signal?: AbortSignal): Promise<ChatModelChoice>;
  /** #322: pick the session's chat model ('' follows the default); later turns use it. */
  setChatModel(sessionID: string, model: string): Promise<ChatModelChoice>;
  /**
   * #322: compact the session's context now. A refusal throws a ChatBrowserError with the server's
   * text (empty when it gave none) and the HTTP status.
   */
  compactContext(sessionID: string): Promise<ChatContextUsage>;
  /** 会话设定摘要：Agent 每轮都能看到，用户可查看和修改。 */
  getNotes(sessionID: string, signal?: AbortSignal): Promise<string>;
  setNotes(sessionID: string, notes: string): Promise<string>;
  /** 会话选用的候选图（后续步骤默认以它为准）；没有则为 null。 */
  getPick(sessionID: string, signal?: AbortSignal): Promise<ChatAttachment | null>;
  setPick(sessionID: string, claim: ChatAttachmentClaim): Promise<ChatAttachment | null>;
  clearPick(sessionID: string): Promise<void>;
  /** 会话版本链（2D/3D 版本及其来源）；只读，来源无法确认时保持未知。 */
  getLineage(sessionID: string, signal?: AbortSignal): Promise<LineageGraph>;
  getLineagePage(sessionID: string, cursor?: string, signal?: AbortSignal): Promise<LineageGraph>;
  /** 本会话的三视图分组（每组三个视图的状态）；只读投影。 */
  listThreeViews(sessionID: string, signal?: AbortSignal): Promise<ThreeViewGroup[]>;
  /** 开始或重试某一组里的某一个视图；返回这一组现在的样子。 */
  advanceThreeView(
    sessionID: string,
    runID: string,
    viewKey: string,
    action: ThreeViewAction,
    attempt: number,
  ): Promise<ThreeViewGroup>;
  subscribe(
    sessionID: string,
    options: ChatSubscriptionOptions,
  ): () => void;
}

export interface PromptEnhanceResult {
  prompt: string;
  negativePrompt: string;
}

export interface ChatBrowserClientDependencies {
  fetcher?: typeof fetch;
  eventSourceFactory?: (url: string) => EventSourceAdapter;
}

export class ChatBrowserError extends Error {
  readonly status: number;
  /** The server's request ID, shown after the message as the error reference (#240). */
  readonly requestId: string;

  constructor(message: string, status: number, requestId = '') {
    super(message);
    this.name = 'ChatBrowserError';
    this.status = status;
    this.requestId = requestId;
  }
}

function route(
  template: string,
  parameters: Record<string, string>,
): string {
  return template.replace(/\{([^{}]+)\}/g, (placeholder, name: string) => {
    const value = parameters[name];
    return value === undefined ? placeholder : encodeURIComponent(value);
  });
}

function approvalError(schema: ProductSchema, payload: unknown, status: number): ChatBrowserError {
  const message = record(payload)[schema.chat.api.response_fields.error];
  return new ChatBrowserError(
    typeof message === 'string' ? message : schema.chat.presentation.messages.internal_error,
    status,
  );
}

function agentScopeRoute(schema: ProductSchema, sessionID: string): string {
  const template = schema.runtime.http.routes.chat_session_agent_scope;
  if (!template) throw new ChatBrowserError(schema.chat.presentation.messages.internal_error, 0);
  return route(template, { sessionID });
}

function sessionRoute(schema: ProductSchema, template: string | undefined, sessionID: string): string {
  if (!template) throw new ChatBrowserError(schema.chat.presentation.messages.internal_error, 0);
  return route(template, { sessionID });
}

/** A refusal with the server's own text; empty when it gave none, so the caller words it. */
function serverError(schema: ProductSchema, response: Response, payload: unknown): ChatBrowserError {
  const message = record(payload)[schema.chat.api.response_fields.error];
  return new ChatBrowserError(typeof message === 'string' ? message : '', response.status, responseRequestId(response, payload));
}

function stringList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string' && item !== '') : [];
}

function reasoningOf(schema: ProductSchema, payload: unknown): ChatReasoning {
  const source = record(payload);
  const fields = schema.chat.api.response_fields;
  const text = (name: string | undefined) => {
    const value = name ? source[name] : undefined;
    return typeof value === 'string' ? value : '';
  };
  return {
    effort: text(fields.reasoning_effort),
    levels: stringList(fields.reasoning_levels ? source[fields.reasoning_levels] : undefined),
    defaultLevel: text(fields.reasoning_default),
  };
}

function chatModelOf(schema: ProductSchema, payload: unknown): ChatModelChoice {
  const source = record(payload);
  const fields = schema.chat.api.response_fields;
  const text = (item: Record<string, unknown>, name: string | undefined) => {
    const value = name ? item[name] : undefined;
    return typeof value === 'string' ? value : '';
  };
  const list = fields.chat_models ? source[fields.chat_models] : undefined;
  const options = (Array.isArray(list) ? list : []).map(record)
    .map((item): ChatModelOption => ({ id: text(item, fields.id), model: text(item, fields.chat_model_name) }))
    .filter((option) => option.model !== '');
  return {
    selected: text(source, fields.chat_model),
    chosen: text(source, fields.chat_model_chosen),
    defaultId: text(source, fields.chat_model_default),
    fallback: Boolean(fields.chat_model_fallback && source[fields.chat_model_fallback] === true),
    options,
  };
}

function tokenCount(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
}

function contextOf(schema: ProductSchema, payload: unknown): ChatContextUsage {
  const source = record(payload);
  const fields = schema.chat.api.response_fields;
  const member = (item: Record<string, unknown>, name: string | undefined) => (name ? item[name] : undefined);
  const compactions = (Array.isArray(member(source, fields.compactions)) ? member(source, fields.compactions) as unknown[] : [])
    .map(record)
    .map((item): ChatContextCompaction => {
      const rounds = member(item, fields.compacted_rounds);
      return {
        id: String(item[fields.id] ?? ''),
        createdAt: String(item[fields.created_at] ?? ''),
        trigger: member(item, fields.compaction_trigger) === 'auto' ? 'auto' : 'manual',
        rounds: typeof rounds === 'number' && Number.isFinite(rounds) ? rounds : 0,
        throughMessageId: String(member(item, fields.through_message_id) ?? ''),
      };
    });
  return {
    tokens: tokenCount(member(source, fields.context_tokens)),
    window: tokenCount(member(source, fields.context_window)),
    compactions,
  };
}

function notesRoute(schema: ProductSchema, sessionID: string): string {
  const template = schema.runtime.http.routes.chat_session_notes;
  if (!template) throw new ChatBrowserError(schema.chat.presentation.messages.internal_error, 0);
  return route(template, { sessionID });
}

function notesOf(schema: ProductSchema, payload: unknown): string {
  const value = record(payload)[schema.chat.api.response_fields.notes];
  return typeof value === 'string' ? value : '';
}

function pickRoute(schema: ProductSchema, sessionID: string): string {
  const template = schema.runtime.http.routes.chat_session_pick;
  if (!template) throw new ChatBrowserError(schema.chat.presentation.messages.internal_error, 0);
  return route(template, { sessionID });
}

function lineageRoute(schema: ProductSchema, sessionID: string): string {
  const template = schema.runtime.http.routes.chat_session_lineage;
  if (!template) throw new ChatBrowserError(schema.chat.presentation.messages.internal_error, 0);
  return route(template, { sessionID });
}

function threeViewsRoute(schema: ProductSchema, sessionID: string): string {
  const template = schema.runtime.http.routes.chat_session_three_views;
  if (!template) throw new ChatBrowserError(schema.chat.presentation.messages.internal_error, 0);
  return route(template, { sessionID });
}

function threeViewItemRoute(schema: ProductSchema, sessionID: string, runID: string, viewKey: string): string {
  const template = schema.runtime.http.routes.chat_session_three_view_item;
  if (!template) throw new ChatBrowserError(schema.chat.presentation.messages.internal_error, 0);
  return route(template, { sessionID, runID, viewKey });
}

/** The three-view routes answer failures as { code, detail } with the Schema's message in detail. */
function problemError(schema: ProductSchema, payload: unknown, status: number): ChatBrowserError {
  const detail = record(payload).detail;
  return new ChatBrowserError(
    withRequestReference(typeof detail === 'string' && detail !== '' ? detail : schema.chat.presentation.messages.internal_error, payload),
    status,
  );
}

function pickOf(schema: ProductSchema, payload: unknown): ChatAttachment | null {
  return decodePick(schema, record(payload)[schema.chat.api.response_fields.pick]);
}

function assignIfDefined(
  target: Record<string, unknown>,
  field: string,
  value: unknown,
): void {
  if (value !== undefined) target[field] = value;
}

async function responseObject(response: Response): Promise<Record<string, unknown>> {
  const value = await response.json().catch(() => ({}));
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}

async function responseValue(response: Response): Promise<unknown> {
  return response.json().catch(() => null);
}

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}

function array(value: unknown): unknown[] {
  if (Array.isArray(value)) return value;
  if (typeof value !== 'string') return [];
  try {
    const parsed = JSON.parse(value) as unknown;
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function normalizeMessage(
  schema: ProductSchema,
  value: unknown,
): ChatMessage {
  const source = record(value);
  const fields = schema.chat.api.response_fields;
  const content = String(source[fields.content] ?? '');
  const pending =
    source[fields.is_pending] === true ||
    content === schema.chat.state.pending_content;
  const imageURLs = array(source[fields.image_urls])
    .filter((item): item is string => typeof item === 'string');
  const toolCalls = array(source[fields.tool_calls]).map(record);
  const taskIds = toolCalls
    .map((call) => optionalString(call, fields.task_id))
    .filter((taskID): taskID is string => Boolean(taskID));
  // A message with exactly one task names it for every picture that has no attachment of its own.
  const soleTaskID = new Set(taskIds).size === 1 ? taskIds[0] : '';
  const paidSteps = toolCalls.flatMap((call): ChatPaidStep[] => {
    const callTaskID = optionalString(call, fields.task_id);
    const credits = optionalNumber(call, fields.estimated_credits);
    const hasNativeQuote = Object.hasOwn(call, 'native_quote');
    const usesNativePricing = call.price_mode === 'native' || hasNativeQuote;
    const nativeQuote = parseNativeQuote(call.native_quote);
    if (!callTaskID || (credits === undefined && !usesNativePricing)) return [];
    return [{
      taskId: callTaskID,
      credits: credits ?? 0,
      approvedByPolicy: call[fields.approval_source] === approvalSourcePolicy,
      ...(usesNativePricing ? { nativeQuote } : {}),
    }];
  });
  const attachments = decodeAttachments(schema, source[fields.attachments]);
  // One message can hold the pictures of several tasks, so a picture's task is the one its attachment
  // names; a picture with none belongs to the message's only task, or to no known task.
  const images: ChatImage[] = imageURLs.map((url, index) => ({
    url,
    taskId: attachments.find((item) => item.imageSlot === index + 1 && item.taskId)?.taskId ?? soleTaskID,
  }));
  const toolCapabilities = new Map(
    schema.chat.tools.map((tool) => [
      tool.name,
      tool.pending_capability,
    ]),
  );
  const pendingTaskTypes = pending
    ? toolCalls
      .map((item) => item[fields.tool_name])
      .filter((name): name is string => typeof name === 'string')
      .map((name) => toolCapabilities.get(name))
      .filter((capability): capability is string => Boolean(capability))
    : undefined;
  const wireRole = String(source[fields.role] ?? '');
  const created = source[fields.created_at];
  const createdAt = typeof created === 'number'
    ? created
    : Date.parse(String(created ?? ''));
  return {
    id: String(source[fields.id] ?? ''),
    role: wireRole === schema.chat.roles.user ? 'user' : 'assistant',
    content: pending ? '' : content,
    ...(images.length > 0 ? { images } : {}),
    ...(attachments.length > 0 ? { attachments } : {}),
    ...(taskIds.length > 0 ? { taskIds } : {}),
    ...(paidSteps.length > 0 ? { paidSteps } : {}),
    isPending: pending,
    ...(pendingTaskTypes && pendingTaskTypes.length > 0
      ? { pendingTaskTypes }
      : {}),
    createdAt: Number.isFinite(createdAt) ? createdAt : 0,
  };
}

// 服务端 approval_source 的取值：policy 表示由批准方式自动批准，不是用户点的批准。
const approvalSourcePolicy = 'policy';

function approvalModeOf(schema: ProductSchema, value: unknown): ApprovalMode {
  // 与服务端一致：Schema 没有声明的方式一律按手工处理。
  return value === 'auto' && schema.chat.approval_policy.modes.includes('auto')
    ? 'auto'
    : 'manual';
}

function normalizeApproval(schema: ProductSchema, source: Record<string, unknown>): ChatApproval {
  const fields = schema.chat.api.response_fields;
  const priceTotals = Object.hasOwn(source, 'price_totals') ? parsePriceTotals(source.price_totals) : undefined;
  return {
    approvalMode: approvalModeOf(schema, source[fields.approval_mode]),
    ...(priceTotals === undefined ? { estimatedCredits: optionalNumber(source, fields.estimated_credits) ?? 0 } : { priceTotals: priceTotals ?? [] }),
  };
}

function normalizeSession(
  schema: ProductSchema,
  value: unknown,
): ChatSession {
  const source = record(value);
  const fields = schema.chat.api.response_fields;
  // 旧响应没有这个成员：此时不写，界面按 Schema 默认显示。
  const agentScope = source[fields.agent_scope] === undefined ? {} : { agentScope: agentScopeOf(schema, source[fields.agent_scope]) };
  const generationModels = sessionModels(fields.generation_models ? source[fields.generation_models] : undefined);
  const generationModelsRetired = sessionModels(fields.generation_models_retired ? source[fields.generation_models_retired] : undefined);
  return {
    ...agentScope,
    ...(generationModels ? { generationModels } : {}),
    ...(generationModels && generationModelsRetired ? { generationModelsRetired } : {}),
    id: String(source[fields.id] ?? ''),
    title: String(source[fields.title] ?? ''),
    createdAt: String(source[fields.created_at] ?? ''),
    updatedAt: String(source[fields.updated_at] ?? ''),
    ...normalizeApproval(schema, source),
  };
}

/** #318: only plain string entries of the per-domain model map are kept. */
function sessionModels(value: unknown): Record<string, string> | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const models = Object.fromEntries(
    Object.entries(value).filter((entry): entry is [string, string] => typeof entry[1] === 'string' && entry[1] !== ''),
  );
  return Object.keys(models).length > 0 ? models : undefined;
}

function optionalString(
  source: Record<string, unknown>,
  field: string,
): string | undefined {
  const value = source[field];
  return typeof value === 'string' && value !== '' ? value : undefined;
}

function optionalNumber(
  source: Record<string, unknown>,
  field: string,
): number | undefined {
  const value = source[field];
  return typeof value === 'number' && Number.isFinite(value)
    ? value
    : undefined;
}

function decodeEvent(
  schema: ProductSchema,
  value: unknown,
): ChatSemanticEvent | null {
  const source = record(value);
  const fields = schema.chat.stream.event_fields;
  const types = schema.chat.stream.event_types;
  const wireType = source[fields.type];
  // #285: the thinking event carries only the Schema status text.
  if (types.thinking && wireType === types.thinking) {
    return {
      type: 'thinking',
      content: optionalString(source, fields.content) ??
        schema.chat.presentation.status.thinking ?? '',
    };
  }
  switch (wireType) {
    case types.text:
      return {
        type: 'text',
        content: optionalString(source, fields.content) ?? '',
      };
    case types.status:
      return {
        type: 'status',
        content: optionalString(source, fields.content) ?? '',
      };
    case types.progress:
      return {
        type: 'progress',
        percent: optionalNumber(source, fields.percent) ?? 0,
      };
    case types.images:
      return {
        type: 'images',
        taskID: optionalString(source, fields.task_id) ?? '',
        urls: array(source[fields.urls])
          .filter((url): url is string => typeof url === 'string'),
      };
    case types.message_created:
      return {
        type: 'messageCreated',
        messageID: optionalString(source, fields.message_id) ?? '',
      };
    case types.title:
      return {
        type: 'title',
        content: optionalString(source, fields.content) ?? '',
      };
    case types.error:
      return {
        type: 'error',
        content: optionalString(source, fields.content) ??
          optionalString(source, fields.error) ??
          schema.chat.presentation.messages.unknown_error,
      };
    case types.task_registered:
      return {
        type: 'taskRegistered',
        taskID: optionalString(source, fields.task_id) ?? '',
        capability: optionalString(source, fields.capability) ?? '',
        prompt: optionalString(source, fields.prompt),
        model: optionalString(source, fields.ai_model),
        sessionID: optionalString(source, fields.session_id),
        ...(Object.hasOwn(source, 'native_quote') || source.price_mode === 'native'
          ? { nativeQuote: parseNativeQuote(source.native_quote) }
          : { estimatedCredits: optionalNumber(source, fields.estimated_credits) }),
        approvedByPolicy: source[fields.approval_source] === approvalSourcePolicy,
      };
    case types.task_update: {
      const rawResult = record(source[fields.result]);
      const imageURLs = array(rawResult[fields.image_urls])
        .filter((url): url is string => typeof url === 'string');
      return {
        type: 'taskUpdate',
        taskID: optionalString(source, fields.task_id) ?? '',
        finalTaskID: optionalString(source, fields.final_task_id),
        status: optionalString(source, fields.status),
        progress: optionalNumber(source, fields.progress),
        ...(imageURLs.length > 0
          ? { result: { imageURLs } }
          : {}),
        error: optionalString(source, fields.error),
      };
    }
    case types.done:
      return { type: 'done' };
    default:
      return null;
  }
}

export function createChatBrowserClient(
  schema: ProductSchema,
  dependencies: ChatBrowserClientDependencies = {},
): ChatBrowserClient {
  const fetcher = dependencies.fetcher ?? fetch;
  const eventSourceFactory = dependencies.eventSourceFactory ??
    ((url: string) => new EventSource(url));
  const fields = schema.chat.api.fields;
  const responseFields = schema.chat.api.response_fields;
  const requestPromptEnhancement = async (
    prompt: string,
    capability: string,
    model?: string,
    signal?: AbortSignal,
  ): Promise<PromptEnhanceResult & { primary: string }> => {
    const contract = schema.chat.api.prompt_enhance;
    const body: Record<string, unknown> = {
      [contract.prompt_field]: prompt,
      [contract.capability_field]: capability,
    };
    assignIfDefined(body, contract.model_field, model);
    const response = await fetcher(
      schema.runtime.http.legacy_routes.prompt_enhance,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        signal,
      },
    );
    const payload = await responseObject(response);
    if (!response.ok) {
      const message = payload[contract.error_field];
      throw new ChatBrowserError(
        typeof message === 'string'
          ? message
          : schema.chat.presentation.messages.internal_error,
        response.status,
      );
    }
    const primary = String(payload[contract.result_field] ?? '');
    return {
      primary,
      prompt: String(
        payload[contract.prompt_result_field] ?? primary,
      ),
      negativePrompt: String(
        payload[contract.negative_prompt_result_field] ?? '',
      ),
    };
  };

  return {
    generationCapability(referenceImageCount) {
      const tool = schema.chat.tools[0];
      return referenceImageCount > 0
        ? tool.capability
        : tool.fallback_capability;
    },
    async enhancePrompt(prompt, capability, model, signal) {
      return (
        await requestPromptEnhancement(
          prompt,
          capability,
          model,
          signal,
        )
      ).primary;
    },
    async enhancePromptResult(prompt, capability, model, signal) {
      const result = await requestPromptEnhancement(
        prompt,
        capability,
        model,
        signal,
      );
      return {
        prompt: result.prompt,
        negativePrompt: result.negativePrompt,
      };
    },
    async listSkills(signal) {
      const contract = schema.chat.skills.fields;
      const response = await fetcher(
        schema.runtime.http.routes.chat_skills,
        { signal },
      );
      const payload = await responseObject(response);
      if (!response.ok) {
        const message = payload[contract.error];
        throw new ChatBrowserError(
          typeof message === 'string'
            ? message
            : schema.chat.presentation.messages.internal_error,
          response.status,
        );
      }
      const skills = array(payload[contract.skills]).map((value) => {
        const source = record(value);
        const name = String(source[contract.name] ?? '');
        const nameZH = optionalString(source, contract.name_zh);
        const imageToImageTemplate = optionalString(
          source,
          contract.image_to_image_template,
        );
        const tool = optionalString(source, contract.tool);
        const defaultMessage = optionalString(source, contract.default_message);
        const tags = array(source[contract.tags]).filter(
          (tag): tag is string => typeof tag === 'string',
        );
        return {
          id: String(source[contract.id] ?? ''),
          name,
          displayName: nameZH ?? name,
          category: String(source[contract.category] ?? ''),
          recommendedSize: String(
            source[contract.recommended_size] ?? '',
          ),
          thumbnailPath: String(source[contract.thumbnail_path] ?? ''),
          rawImageURL: String(source[contract.raw_image_url] ?? ''),
          prompt: String(source[contract.prompt] ?? ''),
          ...(imageToImageTemplate
            ? { imageToImageTemplate }
            : {}),
          ...(tags.length > 0 ? { tags } : {}),
          ...(tool
            ? {
                tool,
                requiresImage: source[contract.requires_image] === true,
                oneShot: source[contract.one_shot] === true,
                ...(defaultMessage ? { defaultMessage } : {}),
              }
            : {}),
        };
      });
      return {
        revision: String(payload[contract.revision] ?? ''),
        skills,
      };
    },
    // An empty title lets the server apply the Schema default and keep the
    // session waiting for its automatic title, whatever the UI locale.
    async createSession(title = '') {
      const response = await fetcher(
        schema.runtime.http.legacy_routes.chat_sessions,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ [fields.title]: title }),
        },
      );
      const payload = await responseValue(response);
      if (!response.ok) {
        const message = record(payload)[responseFields.error];
        throw new ChatBrowserError(
          typeof message === 'string'
            ? message
            : schema.chat.presentation.messages.internal_error,
          response.status,
        );
      }
      return normalizeSession(schema, payload);
    },
    async renameSession(sessionID, title) {
      const response = await fetcher(
        route(schema.runtime.http.legacy_routes.chat_session, { sessionID }),
        {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ [fields.title]: title }),
        },
      );
      const payload = await responseValue(response);
      if (!response.ok) {
        const message = record(payload)[responseFields.error];
        throw new ChatBrowserError(
          typeof message === 'string' ? message : schema.chat.presentation.messages.internal_error,
          response.status,
        );
      }
      return normalizeSession(schema, payload);
    },
    async deleteSession(sessionID) {
      const response = await fetcher(
        route(schema.runtime.http.legacy_routes.chat_session, { sessionID }),
        { method: 'DELETE' },
      );
      if (response.status === 404) return false;
      if (!response.ok) {
        const payload = await responseObject(response);
        const message = payload[responseFields.error];
        throw new ChatBrowserError(
          typeof message === 'string'
            ? message
            : schema.chat.presentation.messages.internal_error,
          response.status,
        );
      }
      return true;
    },
    async listSessions(signal) {
      const response = await fetcher(
        schema.runtime.http.legacy_routes.chat_sessions,
        { signal },
      );
      const payload = await responseValue(response);
      if (!response.ok) {
        const message = record(payload)[responseFields.error];
        throw new ChatBrowserError(
          typeof message === 'string'
            ? message
            : schema.chat.presentation.messages.internal_error,
          response.status,
        );
      }
      return array(payload).map((session) => normalizeSession(schema, session));
    },
    async getSession(sessionID, signal) {
      const response = await fetcher(
        route(schema.runtime.http.legacy_routes.chat_session, { sessionID }),
        { signal },
      );
      const payload = record(await responseValue(response));
      if (!response.ok) {
        const message = payload[responseFields.error];
        throw new ChatBrowserError(
          typeof message === 'string' ? message : schema.chat.presentation.messages.internal_error,
          response.status,
        );
      }
      return normalizeSession(schema, payload);
    },
    async listSessionPage(query, signal) {
      const url = new URL(schema.runtime.http.legacy_routes.chat_sessions, window.location.origin);
      url.searchParams.set('search', query.search);
      url.searchParams.set('limit', String(query.limit));
      if (query.cursor) url.searchParams.set('cursor', query.cursor);
      const response = await fetcher(url.pathname + url.search, { signal });
      const payload = record(await responseValue(response));
      if (!response.ok) {
        const message = payload[responseFields.error];
        throw new ChatBrowserError(
          typeof message === 'string' ? message : schema.chat.presentation.messages.internal_error,
          response.status,
        );
      }
      // `total` is an owner-visible fact: a missing or malformed value fails this read instead of
      // showing a made-up zero.
      const total = payload.total;
      if (typeof total !== 'number' || !Number.isSafeInteger(total) || total < 0) {
        throw new ChatBrowserError(schema.chat.presentation.messages.internal_error, response.status);
      }
      return {
        sessions: array(payload.sessions).map((session) => normalizeSession(schema, session)),
        nextCursor: typeof payload.next_cursor === 'string' ? payload.next_cursor : '',
        total,
      };
    },
    async deleteMessage(sessionID, messageID) {
      const response = await fetcher(
        route(schema.runtime.http.legacy_routes.chat_message, {
          sessionID,
          messageID,
        }),
        { method: 'DELETE' },
      );
      if (response.status === 404) return false;
      if (!response.ok) {
        const payload = await responseObject(response);
        const message = payload[responseFields.error];
        throw new ChatBrowserError(
          typeof message === 'string'
            ? message
            : schema.chat.presentation.messages.internal_error,
          response.status,
        );
      }
      return true;
    },
    async listMessagePage(sessionID, query, signal) {
      const params = new URLSearchParams({ limit: String(query.limit) });
      if (query.before) params.set('before', query.before);
      const response = await fetcher(
        `${route(schema.runtime.http.legacy_routes.chat_messages, { sessionID })}?${params}`,
        { signal },
      );
      const payload = record(await responseValue(response));
      if (!response.ok) {
        const message = payload[responseFields.error];
        throw new ChatBrowserError(
          typeof message === 'string' ? message : schema.chat.presentation.messages.internal_error,
          response.status,
        );
      }
      return {
        messages: array(payload.messages).map((message) => normalizeMessage(schema, message)),
        nextCursor: typeof payload.next_cursor === 'string' ? payload.next_cursor : '',
      };
    },
    async startTurn(sessionID, content, options, signal) {
      const body: Record<string, unknown> = {
        [fields.content]: content,
      };
      assignIfDefined(body, fields.image_urls, options.imageURLs);
      assignIfDefined(body, fields.aspect_ratio, options.aspectRatio);
      assignIfDefined(body, fields.pose_mode, options.poseMode);
      assignIfDefined(body, fields.ai_model, options.model);
      assignIfDefined(body, fields.creative_domain, options.creativeDomain);
      if (options.domainModels && Object.keys(options.domainModels).length > 0) {
        body[fields.domain_models] = options.domainModels;
      }
      assignIfDefined(body, fields.size, options.size);
      assignIfDefined(body, fields.image_size, options.imageSize);
      assignIfDefined(body, fields.parameters, options.parameters);
      assignIfDefined(
        body,
        fields.reference_image_indices,
        options.referenceImageIndices,
      );
      if (options.attachments && options.attachments.length > 0) {
        body[fields.attachments] = encodeAttachmentClaims(schema, options.attachments);
      }
      if (options.selection && options.selection.length > 0) {
        body[fields.selection] = encodeAttachmentClaims(schema, options.selection);
      }
      if (options.mentions && options.mentions.length > 0) {
        body[fields.mentions] = encodeMentions(schema, options.mentions);
      }
      assignIfDefined(body, fields.input_source, options.inputSource);
      if (options.choiceAnswer && fields.choice_answer) {
        body[fields.choice_answer] = options.choiceAnswer.useDefault
          ? { choice_id: options.choiceAnswer.choiceId, use_default: true }
          : { choice_id: options.choiceAnswer.choiceId, option_id: options.choiceAnswer.optionId ?? '' };
      }
      if (options.skill !== undefined) {
        body[fields.skill] = {
          [fields.skill_id]: options.skill.id,
          [fields.skill_name]: options.skill.name,
        };
      }
      const response = await fetcher(
        route(schema.runtime.http.legacy_routes.chat_messages, { sessionID }),
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
          signal,
        },
      );
      const payload = await responseObject(response);
      if (!response.ok) {
        const message = payload[responseFields.error];
        throw new ChatBrowserError(
          typeof message === 'string'
            ? message
            : schema.chat.presentation.messages.internal_error,
          response.status,
          responseRequestId(response, payload),
        );
      }
      return {
        assistantMessageID: String(
          payload[responseFields.message_id] ?? '',
        ),
        sessionID: String(payload[responseFields.session_id] ?? ''),
      };
    },
    async cancelTurn(sessionID, signal) {
      const response = await fetcher(
        route(schema.runtime.http.legacy_routes.chat_turn_cancel, {
          sessionID,
        }),
        { method: 'POST', signal },
      );
      if (!response.ok) return false;
      const payload = await responseObject(response);
      return payload[fields.turn_canceled] === true;
    },
    async getApproval(sessionID, signal) {
      const response = await fetcher(
        route(schema.runtime.http.routes.chat_session_approval, { sessionID }),
        { signal },
      );
      const payload = await responseValue(response);
      if (!response.ok) throw approvalError(schema, payload, response.status);
      return normalizeApproval(schema, record(payload));
    },
    async setApprovalMode(sessionID, mode) {
      const response = await fetcher(
        route(schema.runtime.http.routes.chat_session_approval, { sessionID }),
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ [fields.approval_mode]: mode }),
        },
      );
      const payload = await responseValue(response);
      if (!response.ok) throw approvalError(schema, payload, response.status);
      return normalizeApproval(schema, record(payload));
    },
    async getAgentScope(sessionID, signal) {
      const response = await fetcher(agentScopeRoute(schema, sessionID), { signal });
      const payload = await responseValue(response);
      if (!response.ok) throw approvalError(schema, payload, response.status);
      return agentScopeOf(schema, record(payload)[schema.chat.api.response_fields.agent_scope]);
    },
    async setAgentScope(sessionID, scope) {
      const response = await fetcher(agentScopeRoute(schema, sessionID), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ [fields.agent_scope]: scope }),
      });
      const payload = await responseValue(response);
      if (!response.ok) throw approvalError(schema, payload, response.status);
      return agentScopeOf(schema, record(payload)[schema.chat.api.response_fields.agent_scope]);
    },
    async getReasoning(sessionID, signal) {
      const response = await fetcher(sessionRoute(schema, schema.runtime.http.routes.chat_session_reasoning, sessionID), { signal });
      const payload = await responseValue(response);
      if (!response.ok) throw serverError(schema, response, payload);
      return reasoningOf(schema, payload);
    },
    async setReasoning(sessionID, level) {
      const member = fields.reasoning_effort;
      if (!member) throw new ChatBrowserError(schema.chat.presentation.messages.internal_error, 0);
      const response = await fetcher(sessionRoute(schema, schema.runtime.http.routes.chat_session_reasoning, sessionID), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ [member]: level }),
      });
      const payload = await responseValue(response);
      if (!response.ok) throw serverError(schema, response, payload);
      return reasoningOf(schema, payload);
    },
    async getContext(sessionID, signal) {
      const response = await fetcher(sessionRoute(schema, schema.runtime.http.routes.chat_session_context, sessionID), { signal });
      const payload = await responseValue(response);
      if (!response.ok) throw serverError(schema, response, payload);
      return contextOf(schema, payload);
    },
    async getChatModel(sessionID, signal) {
      const response = await fetcher(sessionRoute(schema, schema.runtime.http.routes.chat_session_model, sessionID), { signal });
      const payload = await responseValue(response);
      if (!response.ok) throw serverError(schema, response, payload);
      return chatModelOf(schema, payload);
    },
    async setChatModel(sessionID, model) {
      const member = fields.chat_model;
      if (!member) throw new ChatBrowserError(schema.chat.presentation.messages.internal_error, 0);
      const response = await fetcher(sessionRoute(schema, schema.runtime.http.routes.chat_session_model, sessionID), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ [member]: model }),
      });
      const payload = await responseValue(response);
      if (!response.ok) throw serverError(schema, response, payload);
      return chatModelOf(schema, payload);
    },
    async compactContext(sessionID) {
      const response = await fetcher(
        sessionRoute(schema, schema.runtime.http.routes.chat_session_context_compaction, sessionID),
        { method: 'POST' },
      );
      const payload = await responseValue(response);
      if (!response.ok) throw serverError(schema, response, payload);
      return contextOf(schema, payload);
    },
    async getNotes(sessionID, signal) {
      const response = await fetcher(notesRoute(schema, sessionID), { signal });
      const payload = await responseValue(response);
      if (!response.ok) throw approvalError(schema, payload, response.status);
      return notesOf(schema, payload);
    },
    async setNotes(sessionID, notes) {
      const response = await fetcher(notesRoute(schema, sessionID), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ [fields.notes]: notes }),
      });
      const payload = await responseValue(response);
      if (!response.ok) throw approvalError(schema, payload, response.status);
      return notesOf(schema, payload);
    },
    async getPick(sessionID, signal) {
      const response = await fetcher(pickRoute(schema, sessionID), { signal });
      const payload = await responseValue(response);
      if (!response.ok) throw approvalError(schema, payload, response.status);
      return pickOf(schema, payload);
    },
    async setPick(sessionID, claim) {
      const response = await fetcher(pickRoute(schema, sessionID), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(encodeAttachmentClaims(schema, [claim])[0]),
      });
      const payload = await responseValue(response);
      if (!response.ok) throw approvalError(schema, payload, response.status);
      return pickOf(schema, payload);
    },
    async getLineage(sessionID, signal) {
      const response = await fetcher(lineageRoute(schema, sessionID), { signal });
      const payload = await responseValue(response);
      if (!response.ok) throw approvalError(schema, payload, response.status);
      return parseLineage(payload);
    },
    async getLineagePage(sessionID, cursor = '', signal) {
      const query = new URLSearchParams({ cursor });
      const response = await fetcher(`${lineageRoute(schema, sessionID)}?${query}`, { signal });
      const payload = await responseValue(response);
      if (!response.ok) throw approvalError(schema, payload, response.status);
      return parseLineage(payload);
    },
    async listThreeViews(sessionID, signal) {
      const response = await fetcher(threeViewsRoute(schema, sessionID), { signal });
      const payload = await responseValue(response);
      if (!response.ok) throw problemError(schema, payload, response.status);
      return parseThreeViews(payload);
    },
    async advanceThreeView(sessionID, runID, viewKey, action, attempt) {
      const response = await fetcher(threeViewItemRoute(schema, sessionID, runID, viewKey), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action, attempt }),
      });
      const payload = await responseValue(response);
      if (!response.ok) throw problemError(schema, payload, response.status);
      const group = parseThreeViewGroup(record(payload).group);
      if (!group) throw new ChatBrowserError(schema.chat.presentation.messages.internal_error, response.status);
      return group;
    },
    async clearPick(sessionID) {
      const response = await fetcher(pickRoute(schema, sessionID), { method: 'DELETE' });
      if (!response.ok) throw approvalError(schema, await responseValue(response), response.status);
    },
    subscribe(sessionID, options) {
      const fromID = options.fromID ?? 0;
      const baseURL = route(
        schema.runtime.http.legacy_routes.chat_stream,
        { sessionID },
      );
      const query = new URLSearchParams({
        [fields.from_id]: String(fromID),
      });
      const source = eventSourceFactory(`${baseURL}?${query.toString()}`);
      let finished = false;
      const connectionTimeout = setTimeout(() => {
        if (finished) return;
        finished = true;
        source.close();
        options.onError?.(
          new Error(schema.chat.presentation.messages.sse_interrupted),
        );
      }, schema.chat.frontend.connection_timeout_milliseconds);
      const finish = (): void => {
        if (finished) return;
        finished = true;
        clearTimeout(connectionTimeout);
        source.close();
        options.onDone?.();
      };
      source.onmessage = (message: MessageEvent<string>) => {
        clearTimeout(connectionTimeout);
        let parsed: unknown;
        try {
          parsed = JSON.parse(message.data) as unknown;
        } catch {
          return;
        }
        const event = decodeEvent(schema, parsed);
        if (event === null) return;
        options.onEvent(event);
        if (event.type === 'done') finish();
      };
      source.onerror = () => {
        if (finished) return;
        finished = true;
        clearTimeout(connectionTimeout);
        source.close();
        options.onError?.(
          new Error(schema.chat.presentation.messages.sse_interrupted),
        );
      };
      return () => {
        if (finished) return;
        finished = true;
        clearTimeout(connectionTimeout);
        source.close();
      };
    },
  };
}
