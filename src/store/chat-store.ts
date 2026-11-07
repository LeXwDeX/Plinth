import { create } from 'zustand';
import { useWorkspaceStore } from '@/store/workspace-store';
import { mergeNewestPage, prependOlderPage } from '@/lib/chat/message-pages';
import { latestToolCallCount } from '@/lib/chat/tool-call-count';
import type { NativeQuote, PriceTotal } from '@/lib/pricing';
import type { SessionBudget } from '@/lib/chat/budget';
import type { ProductionBrief } from '@/lib/chat/production-brief';
import type { MultiviewTurnRequest } from '@/lib/chat/browser-client';

// ── 类型 ──────────────────────────────────────────────

export interface ChatImage {
  url: string;
  taskId: string;
}

/** 消息携带的图片或 3D 模型：平台 ID、来源与 Agent 用来指代它的稳定句柄（如 @img3）。 */
export interface ChatAttachment {
  /** 句柄（如 @img3）；会话选用可能没有句柄，此时为空串。 */
  handle: string;
  kind: 'image' | 'model';
  role?: string;
  assetId?: string;
  taskId?: string;
  captureId?: string;
  /** 在消息 images 中的 1 起序号；0/缺省表示该对象没有对应图片。 */
  imageSlot?: number;
  url?: string;
}

/** One tool call the server recorded on an assistant message (#103). */
export interface ChatToolCall {
  name: string;
  /** The call's arguments as the model wrote them; a read-only review tool carries none worth showing. */
  arguments?: string;
  taskId?: string;
  /** The compact result summary a read-only tool stored with the call; shape by tool. */
  summary?: unknown;
}

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  images?: ChatImage[];
  attachments?: ChatAttachment[];
  /** #65: the structured multiview request a user message was sent with; regenerating sends it again. */
  multiview?: MultiviewTurnRequest;
  taskIds?: string[];
  isStreaming?: boolean;
  isPending?: boolean;
  pendingTaskTypes?: string[];
  /** 本条消息里已提交的付费步骤：估算积分与批准来源（服务端按批准方式给出）。 */
  paidSteps?: ChatPaidStep[];
  /** 助手消息里已执行（或正在执行）的工具调用数；#275 的单次回复预算按它计。 */
  toolCallCount?: number;
  /** 助手消息里的工具调用及其紧凑结果（#103）；用户消息没有。 */
  toolCalls?: ChatToolCall[];
  createdAt: number;
}

/** 一个已提交的付费步骤。credits 是估算值；approvedByPolicy 表示由批准方式自动批准。 */
export interface ChatPaidStep {
  taskId: string;
  credits: number;
  approvedByPolicy: boolean;
  nativeQuote?: NativeQuote | null;
}

/** 付费步骤的批准方式；budget 是制作计划加按积分单位的预算（#103、#120）。 */
export type ApprovalMode = 'auto' | 'manual' | 'budget';

/** 会话的 Agent 范围：Agent 只能用 2D 工具、只能用 3D 工具，或由它按要求决定。 */

export interface ChatSession {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  parentSessionId?: string;
  parentMessageId?: string;
  parentTitle?: string;
  parentDeleted?: boolean;
  /** 会话的批准方式；服务端未给出时按手工处理。 */
  approvalMode?: ApprovalMode;
  /** 本会话累计估算积分（估算值，不是结算值）。 */
  estimatedCredits?: number;
  /** Server-grouped totals replace the legacy single total when native units are present. */
  priceTotals?: PriceTotal[];
  /** 会话读取时返回：各域最近一次发送所用的模型（#318），列表项没有。 */
  generationModels?: Record<string, string>;
  /** 会话读取时返回：上一项里哪些域原本用的是已停用的模型（#329 后续），值是那个已停用模型。 */
  generationModelsRetired?: Record<string, string>;
  /** #103: the session's production brief; absent when the server never stored one. */
  brief?: ProductionBrief;
  /** #103: the session's current production plan budget; absent when no plan exists. */
  budget?: SessionBudget;
}

export interface GenState {
  statusText: string;
  progress: number | null;
  /** The status is the model's thinking status: visible text replaces it (#285). */
  thinking?: boolean;
}

/** SSE 流式增量缓冲（独立于 messages，避免双源 ID 竞态） */
export interface StreamingBuffer {
  /** 服务端创建的 assistant 消息 DB ID（从 msg_created 事件获取） */
  assistantDbId: string | null;
  /** 本轮用户消息（本地乐观显示，SSE 结束后被 DB 数据替换） */
  userMessage: { content: string; images?: ChatImage[] } | null;
  previousUserMessageId: string | null;
  /** 累积的 assistant 文本内容 */
  textContent: string;
  /** 收到的图片 */
  images: ChatImage[];
  /** 本轮已提交的付费步骤（任务注册事件带来的估算积分与批准来源） */
  paidSteps: ChatPaidStep[];
  /** 是否正在流式输出 */
  isStreaming: boolean;
  /** 生成状态（非 null 表示正在生成） */
  genState: GenState | null;
}

// ── Store ─────────────────────────────────────────────

interface ChatStore {
  // 会话管理
  sessions: ChatSession[];
  activeSessionId: string | null;
  activeSessionRevision: number;
  /**
   * Bumped whenever a session is added, removed, renamed (by hand or by the automatic title) or a turn ends (which
   * moves the session up the list). The open session menu reads its list again when this changes; the menu also
   * reads it again every time it opens.
   */
  sessionListRevision: number;
  sessionsLoaded: boolean;

  // 每个会话独立的消息缓存（仅从 DB 加载）
  messagesBySession: Record<string, ChatMessage[]>;
  /** Cursor of the messages before those loaded, per session; '' when the start is loaded. */
  olderCursorBySession: Record<string, string>;

  // SSE 流式缓冲（按会话隔离，独立于 messages）
  streamingBuffers: Record<string, StreamingBuffer>;

  /**
   * 每个会话最新一条助手回复已执行的工具调用数（#275）。回复进行中随每轮工具调用上涨，回复结束后
   * 保持最终值；没有记录时由 messagesBySession 里最新的助手消息推导。
   */
  toolCallCounts: Record<string, number>;

  // 会话操作
  setSessions: (sessions: ChatSession[]) => void;
  setActiveSession: (id: string | null) => void;
  addSession: (session: ChatSession) => void;
  rememberSession: (session: ChatSession) => void;
  removeSession: (id: string) => void;
  updateSessionTitle: (id: string, title: string) => void;
  /** 只更新批准方式与累计积分，不改动其它会话字段。 */
  updateSessionApproval: (id: string, approval: { approvalMode?: ApprovalMode; estimatedCredits?: number; priceTotals?: PriceTotal[] }) => void;
  /** #103: replace the session's production brief without touching other session fields. */
  updateSessionBrief: (id: string, brief: ProductionBrief) => void;
  /** #103: replace (or clear) the session's production plan budget. */
  updateSessionBudget: (id: string, budget: SessionBudget | null) => void;
  setSessionsLoaded: (v: boolean) => void;

  // 消息操作（按会话隔离，仅用于 DB 数据）
  setMessages: (sessionId: string, msgs: ChatMessage[]) => void;
  /** The newest page read again: replaces the end of the loaded messages, keeping earlier pages already read. */
  applyNewestMessagePage: (sessionId: string, page: { messages: ChatMessage[]; nextCursor: string }) => void;
  /**
   * Ends a streaming reply by putting the stored newest page in place of the live buffer in one update
   * (#177): clearing the buffer first would drop the whole turn for a frame and make the list jump.
   */
  settleStreaming: (sessionId: string, page: { messages: ChatMessage[]; nextCursor: string }) => void;
  /** An earlier page the person asked for: goes in front of the loaded messages. */
  applyOlderMessagePage: (sessionId: string, page: { messages: ChatMessage[]; nextCursor: string }) => void;
  deleteMessage: (sessionId: string, id: string) => void;
  clearSessionMessages: (sessionId: string) => void;

  // 流式缓冲操作
  initStreaming: (sessionId: string, userMsg: { content: string; images?: ChatImage[] }) => void;
  setAssistantDbId: (sessionId: string, dbId: string) => void;
  appendStreamText: (sessionId: string, text: string) => void;
  addStreamImages: (sessionId: string, images: ChatImage[]) => void;
  addStreamPaidStep: (sessionId: string, step: ChatPaidStep) => void;
  updateStreamGenState: (sessionId: string, statusText: string, progress: number | null, thinking?: boolean) => void;
  finishStreaming: (sessionId: string) => void;
  /** 回复进行中读到的工具调用数：只升不降（乱序返回的旧读数不能把计数拉回去）。 */
  observeToolCallCount: (sessionId: string, count: number) => void;
}


/** The counts after a message read: a running reply keeps its live count, otherwise the newest assistant message decides. */
function countsAfterRead(state: ChatStore, sessionId: string, messages: readonly ChatMessage[]): Record<string, number> {
  if (state.streamingBuffers[sessionId]) return state.toolCallCounts;
  const count = latestToolCallCount(messages);
  if (count === undefined || state.toolCallCounts[sessionId] === count) return state.toolCallCounts;
  return { ...state.toolCallCounts, [sessionId]: count };
}

export const useChatStore = create<ChatStore>((set) => ({
  sessions: [],
  activeSessionId: null,
  activeSessionRevision: 0,
  sessionListRevision: 0,
  sessionsLoaded: false,
  messagesBySession: {},
  olderCursorBySession: {},
  streamingBuffers: {},
  toolCallCounts: {},

  // ── 会话 ──
  setSessions: (sessions) => set({ sessions, sessionsLoaded: true }),
  setActiveSession: (id) => set((state) => {
    if (state.activeSessionId === id) return state;
    useWorkspaceStore.getState().clearSessionContext();
    return { activeSessionId: id, activeSessionRevision: state.activeSessionRevision + 1 };
  }),
  addSession: (session) =>
    set((s) => {
      if (s.activeSessionId !== session.id) useWorkspaceStore.getState().clearSessionContext();
      return {
        sessions: [session, ...s.sessions], activeSessionId: session.id,
        activeSessionRevision: s.activeSessionRevision + (s.activeSessionId === session.id ? 0 : 1),
        sessionListRevision: s.sessionListRevision + 1,
      };
    }),
  rememberSession: (session) => set((state) => ({
    sessions: [session, ...state.sessions.filter((item) => item.id !== session.id)],
    sessionListRevision: state.sessionListRevision + 1,
  })),
  removeSession: (id) =>
    set((s) => {
      const { [id]: _m, ...restMessages } = s.messagesBySession;
      const { [id]: _c, ...restCursors } = s.olderCursorBySession;
      const { [id]: _b, ...restBuffers } = s.streamingBuffers;
      const { [id]: _t, ...restCounts } = s.toolCallCounts;
      if (s.activeSessionId === id) useWorkspaceStore.getState().clearSessionContext();
      return {
        sessions: s.sessions.filter((ss) => ss.id !== id).map((ss) => ss.parentSessionId === id ? { ...ss, parentDeleted: true } : ss),
        activeSessionId: s.activeSessionId === id ? null : s.activeSessionId,
        activeSessionRevision: s.activeSessionRevision + (s.activeSessionId === id ? 1 : 0),
        sessionListRevision: s.sessionListRevision + 1,
        messagesBySession: restMessages,
        olderCursorBySession: restCursors,
        streamingBuffers: restBuffers,
        toolCallCounts: restCounts,
      };
    }),
  updateSessionTitle: (id, title) =>
    set((s) => ({
      sessions: s.sessions.map((ss) => (ss.id === id ? { ...ss, title } : ss)),
      sessionListRevision: s.sessionListRevision + (s.sessions.some((ss) => ss.id === id && ss.title === title) ? 0 : 1),
    })),
  updateSessionApproval: (id, approval) =>
    set((s) => ({
      sessions: s.sessions.map((ss) => (ss.id === id ? {
        ...ss,
        ...(approval.approvalMode !== undefined ? { approvalMode: approval.approvalMode } : {}),
        ...(approval.estimatedCredits !== undefined ? { estimatedCredits: approval.estimatedCredits } : {}),
        ...(approval.priceTotals !== undefined ? { priceTotals: approval.priceTotals, estimatedCredits: undefined } : {}),
      } : ss)),
    })),
  updateSessionBrief: (id, brief) =>
    set((s) => ({
      sessions: s.sessions.map((ss) => (ss.id === id ? { ...ss, brief } : ss)),
    })),
  updateSessionBudget: (id, budget) =>
    set((s) => ({
      sessions: s.sessions.map((ss) => {
        if (ss.id !== id) return ss;
        if (budget) return { ...ss, budget };
        const { budget: _cleared, ...rest } = ss;
        return rest;
      }),
    })),
  setSessionsLoaded: (v) => set({ sessionsLoaded: v }),

  // ── 消息（仅从 DB 加载） ──
  setMessages: (sessionId, msgs) =>
    set((s) => ({
      messagesBySession: { ...s.messagesBySession, [sessionId]: msgs },
      toolCallCounts: countsAfterRead(s, sessionId, msgs),
    })),
  applyNewestMessagePage: (sessionId, page) =>
    set((s) => {
      const loaded = sessionId in s.messagesBySession
        ? { messages: s.messagesBySession[sessionId], olderCursor: s.olderCursorBySession[sessionId] ?? '' }
        : undefined;
      const next = mergeNewestPage(loaded, page);
      return {
        messagesBySession: { ...s.messagesBySession, [sessionId]: next.messages },
        olderCursorBySession: { ...s.olderCursorBySession, [sessionId]: next.olderCursor },
        toolCallCounts: countsAfterRead(s, sessionId, next.messages),
      };
    }),
  settleStreaming: (sessionId, page) =>
    set((s) => {
      const loaded = sessionId in s.messagesBySession
        ? { messages: s.messagesBySession[sessionId], olderCursor: s.olderCursorBySession[sessionId] ?? '' }
        : undefined;
      const next = mergeNewestPage(loaded, page);
      const { [sessionId]: _b, ...restBuffers } = s.streamingBuffers;
      return {
        messagesBySession: { ...s.messagesBySession, [sessionId]: next.messages },
        olderCursorBySession: { ...s.olderCursorBySession, [sessionId]: next.olderCursor },
        streamingBuffers: restBuffers,
        toolCallCounts: countsAfterRead({ ...s, streamingBuffers: restBuffers }, sessionId, next.messages),
        sessionListRevision: s.sessionListRevision + 1,
      };
    }),
  applyOlderMessagePage: (sessionId, page) =>
    set((s) => {
      const next = prependOlderPage(
        { messages: s.messagesBySession[sessionId] ?? [], olderCursor: s.olderCursorBySession[sessionId] ?? '' },
        page,
      );
      return {
        messagesBySession: { ...s.messagesBySession, [sessionId]: next.messages },
        olderCursorBySession: { ...s.olderCursorBySession, [sessionId]: next.olderCursor },
      };
    }),
  deleteMessage: (sessionId, id) =>
    set((s) => ({
      messagesBySession: {
        ...s.messagesBySession,
        [sessionId]: (s.messagesBySession[sessionId] || []).filter((m) => m.id !== id),
      },
    })),
  clearSessionMessages: (sessionId) =>
    set((s) => {
      const { [sessionId]: _, ...rest } = s.messagesBySession;
      const { [sessionId]: _cursor, ...restCursors } = s.olderCursorBySession;
      const { [sessionId]: _count, ...restCounts } = s.toolCallCounts;
      return { messagesBySession: rest, olderCursorBySession: restCursors, toolCallCounts: restCounts };
    }),

  // ── 流式缓冲操作 ──
  initStreaming: (sessionId, userMsg) =>
    set((s) => ({
      streamingBuffers: {
        ...s.streamingBuffers,
        [sessionId]: {
          assistantDbId: null,
          userMessage: userMsg,
          previousUserMessageId: [...(s.messagesBySession[sessionId] ?? [])].reverse().find((message) => message.role === 'user')?.id ?? null,
          textContent: '',
          images: [],
          paidSteps: [],
          isStreaming: true,
          genState: { statusText: '', progress: null },
        },
      },
      // Every new reply starts counting tool calls from 0 (#275).
      toolCallCounts: { ...s.toolCallCounts, [sessionId]: 0 },
    })),

  setAssistantDbId: (sessionId, dbId) =>
    set((s) => {
      const buf = s.streamingBuffers[sessionId];
      if (!buf) return s;
      return {
        streamingBuffers: {
          ...s.streamingBuffers,
          [sessionId]: { ...buf, assistantDbId: dbId },
        },
      };
    }),

  appendStreamText: (sessionId, text) =>
    set((s) => {
      const buf = s.streamingBuffers[sessionId];
      if (!buf) return s;
      return {
        streamingBuffers: {
          ...s.streamingBuffers,
          [sessionId]: { ...buf, textContent: buf.textContent + text },
        },
      };
    }),

  addStreamImages: (sessionId, images) =>
    set((s) => {
      const buf = s.streamingBuffers[sessionId];
      if (!buf) return s;
      return {
        streamingBuffers: {
          ...s.streamingBuffers,
          [sessionId]: { ...buf, images: [...buf.images, ...images] },
        },
      };
    }),

  addStreamPaidStep: (sessionId, step) =>
    set((s) => {
      const buf = s.streamingBuffers[sessionId];
      if (!buf || buf.paidSteps.some((existing) => existing.taskId === step.taskId)) return s;
      return {
        streamingBuffers: {
          ...s.streamingBuffers,
          [sessionId]: { ...buf, paidSteps: [...buf.paidSteps, step] },
        },
      };
    }),

  updateStreamGenState: (sessionId, statusText, progress, thinking = false) =>
    set((s) => {
      const buf = s.streamingBuffers[sessionId];
      if (!buf) return s;
      return {
        streamingBuffers: {
          ...s.streamingBuffers,
          [sessionId]: { ...buf, genState: thinking ? { statusText, progress, thinking } : { statusText, progress } },
        },
      };
    }),

  finishStreaming: (sessionId) =>
    set((s) => {
      const { [sessionId]: _, ...rest } = s.streamingBuffers;
      // The turn's messages moved the session's updated time, which orders the session list.
      return { streamingBuffers: rest, sessionListRevision: s.sessionListRevision + 1 };
    }),

  observeToolCallCount: (sessionId, count) =>
    set((s) => {
      if (!s.streamingBuffers[sessionId] || count <= (s.toolCallCounts[sessionId] ?? 0)) return s;
      return { toolCallCounts: { ...s.toolCallCounts, [sessionId]: count } };
    }),
}));
