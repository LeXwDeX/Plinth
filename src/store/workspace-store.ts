import { create } from 'zustand';
import { createJSONStorage, persist, type StateStorage } from 'zustand/middleware';

import type { ProductSchema } from '@/lib/product-schema';
import { useCanvasStore } from '@/store/canvas-store';
import { viewableModelAsset } from '@/lib/workspace/task-projection';
import type {
  TrackedAsset,
  TrackedResource,
  TrackedTaskFailure,
  TrackedTaskProjection,
} from '@/lib/workspace/task-projection';

/** 排队发送到对话的附件：URL 加上已知的平台来源（截图记录、来源任务）。 */
export interface QueuedChatAttachment {
  url: string;
  assetId?: string;
  taskId?: string;
  captureId?: string;
}

/** 随下一轮发送的图片来源声明；服务端逐项校验后才会采信。 */
export type AttachmentSource = Omit<QueuedChatAttachment, 'url'>;

/** 生成模型与能力按 2D（图片）和 3D（模型）两个域选择；舞台不再有域标签。 */
export type WorkspaceDomain = 'image' | 'model-3d';

/**
 * 工作区唯一的"当前对象"：舞台、对话输入区的芯片、右侧栏和版本条都读这一个
 * 字段。选用候选、点版本条、打开结果卡片或新结果到达都写它。
 */
export interface CurrentObject {
  taskId: string;
  kind: 'image' | 'model';
  url: string;
  assetId?: string;
  /** 会话内句柄（@img5 / @model2）；由对话消息或版本条补齐，可能暂时未知。 */
  handle?: string;
  captureId?: string;
  imageUrls?: string[];
  /** 产物归属的会话（FEA-02）：画布消费前校验，防止跨会话串内容。 */
  sessionId?: string;
  /**
   * 打开会话时恢复出来的当前对象（#239）：只决定舞台显示什么，不往画布上放置，
   * 所以用户删掉的图刷新后不会复活。
   */
  restored?: boolean;
  /**
   * 会话打开后、舞台空着时因新结果出现而自动选上的当前对象（#262）：画布上已有别的内容时
   * 不改选中、不改视图，正在进行的笔画或拖动不会被打断。
   */
  quiet?: boolean;
}

/** 工作区跟踪的任务生命周期条目（agent 注册 + SSE 实时 + 原生详情轮询）。 */
export interface TrackedTask {
  taskId: string;
  /** 注册该任务的会话（FEA-02）；托盘与画布预览按会话隔离消费。 */
  sessionId?: string;
  capability: string;
  prompt: string;
  /** When the task started, for the elapsed time on its progress card: the server's creation time once known, else when it was first seen. */
  startedAt?: number;
  /** 最近一次已知状态（原生详情优先，SSE 实时事件兜底）。 */
  state: string;
  progress: number;
  progressMaximum: number;
  terminal: boolean;
  failure: TrackedTaskFailure | null;
  /** SSE 实时错误文本（服务端已解析的安全文本或 error.* 词汇键）。 */
  liveError: string;
  assets: TrackedAsset[];
  downloadables: TrackedAsset[];
  resources: TrackedResource[];
  resourcesPending: boolean;
  notFound: boolean;
  updatedAt: number;
  /** Known once the server's task detail has arrived. */
  offeringId?: string;
  modelId?: string;
  /**
   * A format conversion started from this version's Formats tab (#330): the task ID of the version it converts.
   * It is part of that version, never a version of its own.
   */
  formatOf?: string;
}

export interface RegisterTaskInput {
  taskId: string;
  sessionId?: string;
  capability: string;
  prompt?: string;
  progressMaximum: number;
  /** Background plan steps should appear in the tray without replacing the selected source. */
  autoPreview?: boolean;
  /** See TrackedTask.formatOf. */
  formatOf?: string;
}

export interface TaskLivePatch {
  state?: string;
  progress?: number;
  error?: string;
  terminal?: boolean;
}

interface WorkspaceState {
  modelByDomain: Record<WorkspaceDomain, string | null>;
  currentObject: CurrentObject | null;
  /** 用户显式钉住的画布参考图 URL；仅随会话切换或手动取消而清除。 */
  canvasReference: string | null;
  /** 合并结果（`canvas:merged:…`）对应的合并前对象：撤销合并时当前对象回到它。 */
  mergeBases: Record<string, CurrentObject>;
  rememberMergeBase: (mergedObjectId: string, base: CurrentObject) => void;
  /** 发送到对话动作排队的附件，由对话列的输入区消费并清空。 */
  pendingChatAttachments: string[];
  /** 已排队附件的来源声明，按 URL 索引；发送后由对话面板清理。 */
  attachmentSources: Record<string, AttachmentSource>;
  pendingPreviewTaskId: string | null;
  /**
   * 新结果已完成，但用户正在别的对象上使用画笔 / 蒙版，所以没有抢走舞台。
   * 舞台上显示"新版本已完成，查看"，点击后才切换。
   */
  pendingResult: CurrentObject | null;
  /** 2D 舞台上有绘制类工具（画笔、橡皮、蒙版、裁剪）处于激活状态。 */
  paintToolActive: boolean;
  trackedTasks: TrackedTask[];
  setPaintToolActive: (active: boolean) => void;
  openPendingResult: () => void;
  dismissPendingResult: () => void;
  setModel: (domain: WorkspaceDomain, modelId: string | null) => void;
  /**
   * #329: the domain's saved or restored model is retired, so the successor is used and the workspace says
   * so until the person picks a model. Not persisted.
   */
  retiredModelNotice: Partial<Record<WorkspaceDomain, { from: string; to: string }>>;
  switchRetiredModel: (domain: WorkspaceDomain, successor: string, notice: { from: string; to: string }) => void;
  /** Opens an object on the stage; the mask belongs to the canvas and stays. */
  setCurrentObject: (selection: CurrentObject | null) => void;
  /** The same, without the mask-tool handling of setCurrentObject. */
  forceCurrentObject: (selection: CurrentObject | null) => void;
  setCanvasReference: (url: string | null) => void;
  queueChatAttachments: (items: ReadonlyArray<string | QueuedChatAttachment>) => void;
  consumeChatAttachments: () => string[];
  forgetAttachmentSources: (urls: readonly string[]) => void;
  clearSessionContext: () => void;
  registerTask: (input: RegisterTaskInput) => void;
  applyTaskDetail: (taskId: string, projection: TrackedTaskProjection, activeSessionId: string | null) => void;
  applyTaskLive: (taskId: string, patch: TaskLivePatch) => void;
  markTaskNotFound: (taskId: string) => void;
  dismissTask: (taskId: string) => void;
  clearTrackedTasks: () => void;
}

function emptyTask(
  input: RegisterTaskInput,
): TrackedTask {
  return {
    taskId: input.taskId,
    sessionId: input.sessionId,
    capability: input.capability,
    prompt: input.prompt ?? '',
    startedAt: Date.now(),
    state: '',
    progress: 0,
    progressMaximum: input.progressMaximum,
    terminal: false,
    failure: null,
    liveError: '',
    assets: [],
    downloadables: [],
    resources: [],
    resourcesPending: false,
    notFound: false,
    updatedAt: Date.now(),
    ...(input.formatOf ? { formatOf: input.formatOf } : {}),
  };
}

function patchTask(
  tasks: TrackedTask[],
  taskId: string,
  patch: (task: TrackedTask) => TrackedTask,
): TrackedTask[] {
  let touched = false;
  const next = tasks.map((task) => {
    if (task.taskId !== taskId) return task;
    touched = true;
    return patch(task);
  });
  return touched ? next : tasks;
}

/**
 * 刷新后保留用户偏好：每个域所选模型。任务托盘、画布
 * 选区等运行状态是服务端事实或会话内瞬态，一律不持久化（ hydration 由
 * use-task-tracker 回灌负责）。
 */
export interface WorkspacePrefs {
  modelByDomain: Record<WorkspaceDomain, string | null>;
}

export function partializeWorkspacePrefs(state: WorkspaceState): WorkspacePrefs {
  return { modelByDomain: state.modelByDomain };
}

/** Node/测试环境无 localStorage：退化为内存存储，持久化退化为空操作。 */
const memoryStorage: StateStorage = (() => {
  const map = new Map<string, string>();
  return {
    getItem: (name) => map.get(name) ?? null,
    setItem: (name, value) => void map.set(name, value),
    removeItem: (name) => void map.delete(name),
  };
})();

export const useWorkspaceStore = create<WorkspaceState>()(
  persist(
    (set, get) => ({
  modelByDomain: { image: null, 'model-3d': null },
  currentObject: null,
  canvasReference: null,
  mergeBases: {},
  rememberMergeBase: (mergedObjectId, base) => set((state) => ({ mergeBases: { ...state.mergeBases, [mergedObjectId]: base } })),
  pendingChatAttachments: [],
  attachmentSources: {},
  pendingPreviewTaskId: null,
  pendingResult: null,
  paintToolActive: false,
  trackedTasks: [],
  setPaintToolActive: (paintToolActive) => set({ paintToolActive }),
  openPendingResult: () => {
    const pending = get().pendingResult;
    if (pending) get().setCurrentObject(pending);
  },
  dismissPendingResult: () => set({ pendingResult: null }),
  setModel: (domain, modelId) =>
    set((state) => {
      const { [domain]: _cleared, ...retiredModelNotice } = state.retiredModelNotice;
      return { modelByDomain: { ...state.modelByDomain, [domain]: modelId }, retiredModelNotice };
    }),
  retiredModelNotice: {},
  switchRetiredModel: (domain, successor, notice) =>
    set((state) => ({
      modelByDomain: { ...state.modelByDomain, [domain]: successor },
      retiredModelNotice: { ...state.retiredModelNotice, [domain]: notice },
    })),
  setCurrentObject: (currentObject) => {
    // The mask belongs to the canvas, not to the picture on the stage: changing the stage object keeps it.
    // A model on the stage has no 2D canvas to paint on, so the mask tool is put down (the mask stays).
    if (currentObject && currentObject.kind !== 'image' && useCanvasStore.getState().activeTool === 'mask') {
      useCanvasStore.getState().setActiveTool('select');
    }
    set({ currentObject, pendingPreviewTaskId: null, pendingResult: null });
  },
  forceCurrentObject: (currentObject) => set({ currentObject, pendingPreviewTaskId: null, pendingResult: null }),
  setCanvasReference: (canvasReference) => set({ canvasReference }),
  clearSessionContext: () => {
    // The mask tool is armed for one session's picture: it does not follow to the next session.
    if (useCanvasStore.getState().activeTool === 'mask') useCanvasStore.getState().setActiveTool('select');
    set({
      currentObject: null,
      canvasReference: null,
      mergeBases: {},
      pendingChatAttachments: [],
      attachmentSources: {},
      pendingPreviewTaskId: null,
      pendingResult: null,
      paintToolActive: false,
    });
  },
  queueChatAttachments: (items) =>
    set((state) => {
      const sources = { ...state.attachmentSources };
      const urls = items.map((item) => {
        if (typeof item === 'string') return item;
        const { url, ...source } = item;
        if (source.assetId || source.taskId || source.captureId) sources[url] = source;
        return url;
      });
      return {
        pendingChatAttachments: [...state.pendingChatAttachments, ...urls],
        attachmentSources: sources,
      };
    }),
  forgetAttachmentSources: (urls) =>
    set((state) => {
      if (!urls.some((url) => url in state.attachmentSources)) return state;
      const sources = { ...state.attachmentSources };
      for (const url of urls) delete sources[url];
      return { attachmentSources: sources };
    }),
  consumeChatAttachments: () => {
    const attachments = get().pendingChatAttachments;
    if (attachments.length > 0) set({ pendingChatAttachments: [] });
    return attachments;
  },
  registerTask: (input) =>
    set((state) => {
      if (state.trackedTasks.some((task) => task.taskId === input.taskId)) {
        return state;
      }
      return {
        trackedTasks: [...state.trackedTasks, emptyTask(input)],
        pendingPreviewTaskId: input.autoPreview === false
          ? state.pendingPreviewTaskId
          : input.taskId,
      };
    }),
  applyTaskDetail: (taskId, projection, activeSessionId) =>
    set((state) => {
      // FEA-02：产物预览只写进任务归属会话的画布；切会话后旧轮次的
      // 产物不再串入新会话的画布选区。
      const ownerSessionId = state.trackedTasks.find(
        (task) => task.taskId === taskId,
      )?.sessionId;
      const sessionMatches = ownerSessionId != null && ownerSessionId === activeSessionId;
      const preview = state.pendingPreviewTaskId === taskId && sessionMatches
        ? viewableModelAsset(projection.downloadables)
          // A model task whose formats the viewer cannot load shows nothing rather than its texture pictures.
          ?? (projection.downloadables.some((asset) => asset.kind === 'model')
            ? undefined
            : projection.downloadables.find((asset) => asset.kind === 'image'))
        : undefined;
      const result: CurrentObject | null = preview
        ? {
            taskId, kind: preview.kind as 'image' | 'model', url: preview.url, assetId: preview.id,
            sessionId: ownerSessionId,
            ...(preview.kind === 'image'
              ? { imageUrls: projection.downloadables.filter((asset) => asset.kind === 'image').map((asset) => asset.url) }
              : {}),
          }
        : null;
      // A finished result takes the stage unless the user is painting on another
      // object; then it waits behind a pill instead of pulling the canvas away.
      const holdBack = result != null && state.paintToolActive
        && state.currentObject != null && state.currentObject.taskId !== taskId;
      return {
        trackedTasks: patchTask(state.trackedTasks, taskId, (task) => ({
        ...task,
        state: projection.state || task.state,
        progress: projection.progress,
        terminal: projection.terminal,
        failure: projection.failure,
        assets: projection.assets,
        downloadables: projection.downloadables,
        resources: projection.resources,
        resourcesPending: projection.resourcesPending,
        startedAt: projection.createdAt ?? task.startedAt,
        offeringId: projection.offeringId || task.offeringId,
        modelId: projection.modelId || task.modelId,
        updatedAt: Date.now(),
        })),
        currentObject: result && !holdBack ? result : state.currentObject,
        pendingResult: holdBack ? result : state.pendingResult,
        pendingPreviewTaskId: preview ? null : state.pendingPreviewTaskId,
      };
    }),
  applyTaskLive: (taskId, patch) =>
    set((state) => ({
      trackedTasks: patchTask(state.trackedTasks, taskId, (task) => ({
        ...task,
        state: patch.state ?? task.state,
        progress: patch.progress ?? task.progress,
        liveError: patch.error ?? task.liveError,
        terminal: patch.terminal ?? task.terminal,
        updatedAt: Date.now(),
      })),
    })),
  markTaskNotFound: (taskId) =>
    set((state) => ({
      trackedTasks: patchTask(state.trackedTasks, taskId, (task) => ({
        ...task,
        notFound: true,
        terminal: true,
        updatedAt: Date.now(),
      })),
    })),
  dismissTask: (taskId) =>
    set((state) => ({
      trackedTasks: state.trackedTasks.filter(
        (task) => task.taskId !== taskId,
      ),
    })),
  clearTrackedTasks: () => set({
    trackedTasks: [],
    currentObject: null,
    pendingResult: null,
    pendingPreviewTaskId: null,
  }),
    }),
    {
      name: 'art-ai-workspace-prefs',
      version: 2,
      // v1 also kept the retired 2D/3D tab; only the per-domain models remain.
      migrate: (persisted) => {
        const models = (persisted as { modelByDomain?: Partial<Record<WorkspaceDomain, string | null>> } | null)?.modelByDomain;
        return { modelByDomain: { image: models?.image ?? null, 'model-3d': models?.['model-3d'] ?? null } };
      },
      storage: createJSONStorage<WorkspacePrefs>(() =>
        typeof localStorage !== 'undefined' ? localStorage : memoryStorage,
      ),
      partialize: partializeWorkspacePrefs,
    },
  ),
);

/** 按域返回 Product Schema 声明的能力（按 order 排序）。 */
export function domainCapabilities(
  schema: ProductSchema,
  domain: WorkspaceDomain,
): Array<{ id: string; label_cn: string }> {
  return schema.capabilities
    .filter((capability) => capability.domain === domain && capability.workspace_model_selector !== false)
    .sort((a, b) => a.order - b.order)
    .map((capability) => ({ id: capability.id, label_cn: capability.label_cn }));
}
