import { create } from 'zustand';
import { useWorkspaceStore } from '@/store/workspace-store';
import { mergeNewestPage, prependOlderPage } from '@/lib/chat/message-pages';
import type { NativeQuote, PriceTotal } from '@/lib/pricing';

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

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  images?: ChatImage[];
  attachments?: ChatAttachment[];
  taskIds?: string[];
  isStreaming?: boolean;
  isPending?: boolean;
  pendingTaskTypes?: string[];
  /** 本条消息里已提交的付费步骤：估算积分与批准来源（服务端按批准方式给出）。 */
  paidSteps?: ChatPaidStep[];
  createdAt: number;
}

/** 一个已提交的付费步骤。credits 是估算值；approvedByPolicy 表示由批准方式自动批准。 */
export interface ChatPaidStep {
  taskId: string;
  credits: number;
  approvedByPolicy: boolean;
  nativeQuote?: NativeQuote | null;
}

export type ApprovalMode = 'auto' | 'manual';

/** 会话的 Agent 范围：Agent 只能用 2D 工具、只能用 3D 工具，或由它按要求决定。 */
export type AgentScope = '2d' | '3d' | 'auto';

export interface ChatSession {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  /** 会话的批准方式；服务端未给出时按手工处理。 */
  approvalMode?: ApprovalMode;
  /** 会话的 Agent 范围；服务端未给出时按 Schema 默认（自动模式）。 */
  agentScope?: AgentScope;
  /** 本会话累计估算积分（估算值，不是结算值）。 */
  estimatedCredits?: number;
  /** Server-grouped totals replace the legacy single total when native units are present. */
  priceTotals?: PriceTotal[];
  /** 会话读取时返回：各域最近一次发送所用的模型（#318），列表项没有。 */
  generationModels?: Record<string, string>;
  /** 会话读取时返回：上一项里哪些域原本用的是已停用的模型（#329 后续），值是那个已停用模型。 */
  generationModelsRetired?: Record<string, string>;
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

  // 会话操作
  setSessions: (sessions: ChatSession[]) => void;
  setActiveSession: (id: string | null) => void;
  addSession: (session: ChatSession) => void;
  removeSession: (id: string) => void;
  updateSessionTitle: (id: string, title: string) => void;
  /** 只更新批准方式与累计积分，不改动其它会话字段。 */
  updateSessionApproval: (id: string, approval: { approvalMode?: ApprovalMode; estimatedCredits?: number; priceTotals?: PriceTotal[] }) => void;
  /** 只更新 Agent 范围，不改动其它会话字段。 */
  updateSessionAgentScope: (id: string, agentScope: AgentScope) => void;
  setSessionsLoaded: (v: boolean) => void;

  // 消息操作（按会话隔离，仅用于 DB 数据）
  setMessages: (sessionId: string, msgs: ChatMessage[]) => void;
  /** The newest page read again: replaces the end of the loaded messages, keeping earlier pages already read. */
  applyNewestMessagePage: (sessionId: string, page: { messages: ChatMessage[]; nextCursor: string }) => void;
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
  removeSession: (id) =>
    set((s) => {
      const { [id]: _m, ...restMessages } = s.messagesBySession;
      const { [id]: _c, ...restCursors } = s.olderCursorBySession;
      const { [id]: _b, ...restBuffers } = s.streamingBuffers;
      if (s.activeSessionId === id) useWorkspaceStore.getState().clearSessionContext();
      return {
        sessions: s.sessions.filter((ss) => ss.id !== id),
        activeSessionId: s.activeSessionId === id ? null : s.activeSessionId,
        activeSessionRevision: s.activeSessionRevision + (s.activeSessionId === id ? 1 : 0),
        sessionListRevision: s.sessionListRevision + 1,
        messagesBySession: restMessages,
        olderCursorBySession: restCursors,
        streamingBuffers: restBuffers,
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
  updateSessionAgentScope: (id, agentScope) =>
    set((s) => ({
      sessions: s.sessions.map((ss) => (ss.id === id ? { ...ss, agentScope } : ss)),
    })),
  setSessionsLoaded: (v) => set({ sessionsLoaded: v }),

  // ── 消息（仅从 DB 加载） ──
  setMessages: (sessionId, msgs) =>
    set((s) => ({
      messagesBySession: { ...s.messagesBySession, [sessionId]: msgs },
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
      return { messagesBySession: rest, olderCursorBySession: restCursors };
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
}));
