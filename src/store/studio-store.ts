import type { ChatAttachmentClaim } from '@/lib/chat/attachments';
import { create } from 'zustand';

import { removeCompareSlot, setCompareMode, toggleCompareSlot, type CompareMode, type CompareSlot, type CompareState } from '@/lib/workspace/compare';
import type { ModelPart } from '@/lib/workspace/model-parts';
import { panelFamilyOf, type PanelTabId } from '@/lib/workspace/panel-tabs';
import type { PanelKind } from '@/lib/product-schema';
import { stepResultTarget, type ResultTarget } from '@/lib/workspace/result-target';
import { stepIndex, type LightboxItem, type LightboxState } from '@/lib/workspace/lightbox';

/**
 * 工作台外壳的界面状态：对话栏宽度与收起、舞台左侧工具栏的展开、右侧栏当前标签（2D 一份、3D 一份）、
 * 任务抽屉、全部产出，以及"预填输入框"请求。它们只属于这个浏览器：宽度、收起、工具栏展开写入
 * localStorage（读写都容错，无法存取时仍可用），其余（含右侧栏标签）是这次访问内的瞬态。
 * 业务状态（当前对象、任务、模型选择）在 workspace-store。
 */

export const CHAT_WIDTH_DEFAULT = 360;
export const CHAT_WIDTH_MIN = 320;
export const CHAT_WIDTH_MAX = 520;
/** 收起后的窄条宽度。 */
export const CHAT_RAIL_WIDTH = 56;

/** While the studio watches for versions the tracker does not know of, the chain is read this often. */
export const VERSIONS_WATCH_INTERVAL_MS = 3000;
/** How long a watch lasts: longer than the slowest step a person waits on (a 3D model takes minutes). */
export const VERSIONS_WATCH_DURATION_MS = 5 * 60 * 1000;

/** 2D 舞台头里要画板配合完成的动作：局部重绘（蒙版）、去背景、导出画板、画板合成发到对话。 */
export type ImageCommandKind = 'inpaint' | 'remove-background' | 'export-board' | 'send-board' | 'merge-version';

/** What the composer's next turn carries, published for the canvas notices (derived; never persisted). */
export interface GenerationReferenceContext {
  sessionId: string;
  hasReference: boolean;
  /** The chip of the object on the stage was dismissed: the turn carries neither it nor the artboard. */
  objectSkipped?: boolean;
}

interface StudioPrefs {
  chatWidth: number;
  chatCollapsed: boolean;
  /** The stage's left rail (2D tools, 3D views) shows names beside the icons; one setting for both. Collapsed by default. */
  railExpanded: boolean;
}

export const STUDIO_PREFS_KEY = 'art-ai-studio-prefs';

export function clampChatWidth(width: number): number {
  if (!Number.isFinite(width)) return CHAT_WIDTH_DEFAULT;
  return Math.min(CHAT_WIDTH_MAX, Math.max(CHAT_WIDTH_MIN, Math.round(width)));
}

export function readStudioPrefs(): StudioPrefs {
  const fallback: StudioPrefs = { chatWidth: CHAT_WIDTH_DEFAULT, chatCollapsed: false, railExpanded: false };
  try {
    if (typeof localStorage === 'undefined') return fallback;
    const raw = localStorage.getItem(STUDIO_PREFS_KEY);
    if (!raw) return fallback;
    const parsed: unknown = JSON.parse(raw);
    if (parsed === null || typeof parsed !== 'object') return fallback;
    const value = parsed as Partial<Record<keyof StudioPrefs, unknown>>;
    return {
      chatWidth: typeof value.chatWidth === 'number' ? clampChatWidth(value.chatWidth) : fallback.chatWidth,
      chatCollapsed: value.chatCollapsed === true,
      railExpanded: value.railExpanded === true,
    };
  } catch {
    return fallback;
  }
}

function writeStudioPrefs(prefs: StudioPrefs): void {
  try {
    if (typeof localStorage === 'undefined') return;
    localStorage.setItem(STUDIO_PREFS_KEY, JSON.stringify(prefs));
  } catch {
    /* Private windows and blocked site data: the layout still works for this visit. */
  }
}

interface StudioState extends StudioPrefs {
  /**
   * The right panel's tab the person last chose, one for 2D (a picture and the bare board share it) and one
   * for 3D, for this visit only. Null until chosen: the Schema's default for the kind applies. A remembered
   * tab the current kind does not offer is ignored (`activePanelTab`).
   */
  panelTab2d: PanelTabId | null;
  panelTab3d: PanelTabId | null;
  taskDrawerOpen: boolean;
  allOutputsOpen: boolean;
  /** The shared 任务详情 dialog (ResultDetail): what it is open on; null when closed. */
  resultDetail: ResultTarget | null;
  /** Derived composer applicability only; parameter values remain in the authoritative Offering store. */
  generationReferenceContext: GenerationReferenceContext | null;
  setGenerationReferenceContext: (context: GenerationReferenceContext | null) => void;
  /** The keyboard-shortcut panel ("?"). */
  shortcutsOpen: boolean;
  /** Asks the 3D stage to put its camera back (Ctrl+0 and the 复位视角 button share this). */
  viewResetRequest: { seq: number } | null;
  /** 起步卡片 / 重新发起：把文字放进输入框（不发送）；seq 让相同文字也能再次触发。 */
  composerPrefill: { text: string; seq: number } | null;
  /** 舞台上的动作（如"转 3D"）请 Agent 直接做一件事；由对话栏作为一轮发出。 */
  turnRequest: { text: string; seq: number; selection?: ChatAttachmentClaim[] } | null;
  /** 请 3D 舞台把某个模型当前的视角截图到 2D（对话里的结果卡也能发起）。 */
  captureRequest: { taskId: string; seq: number } | null;
  /** The parts of the model on the 3D stage, reported by its viewer; `key` names the object they belong to (`taskId:assetId`). */
  stageParts: { key: string; parts: ModelPart[] } | null;
  /** The part lit on the stage, chosen in the 部件 tab or by a click in the 3D view. */
  selectedPart: { key: string; id: string } | null;
  /** Asks the 3D stage to write one part as a GLB download. */
  partExportRequest: { key: string; id: string; seq: number } | null;
  /** 右侧栏「加工」标签预选哪个加工能力（舞台头的加工按钮）；面板读到后清掉。 */
  processPreset: string | null;
  /** 请 2D 舞台对当前图做一件事；画板在加载好后执行。 */
  imageCommand: { kind: ImageCommandKind; seq: number } | null;
  /** 「从这里继续」：请对话栏把当前对象放回输入区的引用，并把焦点给输入框。 */
  continueRequest: { seq: number } | null;
  /** Bumped when something that is a version appears without a task finishing (a screenshot): the chain is read again. */
  versionsRevision: number;
  /** Until this time (ms since epoch) the version chain is polled, for work started where the tracker cannot see it. */
  versionsWatchUntil: number;
  /** 全窗口大图（缩放、平移、逐个切换）；null 为关闭。 */
  lightbox: LightboxState | null;
  /** 比较：2 联、4 联或滑块；null 为没有在比较。 */
  compare: CompareState | null;
  /** Why the last version offered to the comparison was not taken; cleared by the next one that is. */
  compareNotice: 'unavailable' | null;
  setChatWidth: (width: number) => void;
  setChatCollapsed: (collapsed: boolean) => void;
  /** Expands or collapses the stage's left rail (2D tools and 3D views share it); remembered by this browser. */
  setRailExpanded: (expanded: boolean) => void;
  /** Chooses a right-panel tab for a kind of object; 2D kinds and 3D each keep their own choice. */
  setPanelTab: (kind: PanelKind, tab: PanelTabId) => void;
  setTaskDrawerOpen: (open: boolean) => void;
  setAllOutputsOpen: (open: boolean) => void;
  openResultDetail: (target: ResultTarget) => void;
  closeResultDetail: () => void;
  /** Moves the open 任务详情 to the previous / next result of its group (no-op without a group or at the ends). */
  stepResultDetail: (delta: number) => void;
  setShortcutsOpen: (open: boolean) => void;
  requestViewReset: () => void;
  prefillComposer: (text: string) => void;
  clearComposerPrefill: () => void;
  /** `selection` names the picture the turn is about (the rasterised board), instead of what the stage shows. */
  requestTurn: (text: string, selection?: ChatAttachmentClaim[]) => void;
  clearTurnRequest: () => void;
  requestCapture: (taskId: string) => void;
  clearCaptureRequest: () => void;
  setStageParts: (stageParts: { key: string; parts: ModelPart[] } | null) => void;
  setSelectedPart: (selectedPart: { key: string; id: string } | null) => void;
  requestPartExport: (key: string, id: string) => void;
  clearPartExportRequest: () => void;
  showProcess: (capability: string | null) => void;
  clearProcessPreset: () => void;
  requestImageCommand: (kind: ImageCommandKind) => void;
  clearImageCommand: () => void;
  requestContinue: () => void;
  clearContinueRequest: () => void;
  bumpVersions: () => void;
  watchVersions: (durationMs?: number) => void;
  openLightbox: (items: LightboxItem[], index?: number) => void;
  closeLightbox: () => void;
  stepLightbox: (delta: number) => void;
  setLightboxIndex: (index: number) => void;
  setCompareNotice: (notice: 'unavailable' | null) => void;
  startCompare: (initial?: CompareState) => void;
  endCompare: () => void;
  setCompareMode: (mode: CompareMode) => void;
  toggleCompareSlot: (slot: CompareSlot) => void;
  removeCompareSlot: (key: string) => void;
}

export const useStudioStore = create<StudioState>()((set, get) => {
  const persist = (patch: Partial<StudioPrefs>) => {
    const { chatWidth, chatCollapsed, railExpanded } = get();
    writeStudioPrefs({ chatWidth, chatCollapsed, railExpanded, ...patch });
  };
  return {
    ...readStudioPrefs(),
    panelTab2d: null,
    panelTab3d: null,
    taskDrawerOpen: false,
    allOutputsOpen: false,
    resultDetail: null,
    generationReferenceContext: null,
    shortcutsOpen: false,
    viewResetRequest: null,
    composerPrefill: null,
    turnRequest: null,
    captureRequest: null,
    imageCommand: null,
    continueRequest: null,
    processPreset: null,
    stageParts: null,
    selectedPart: null,
    partExportRequest: null,
    versionsRevision: 0,
    versionsWatchUntil: 0,
    lightbox: null,
    compare: null,
    compareNotice: null,
    setChatWidth: (width) => {
      const chatWidth = clampChatWidth(width);
      set({ chatWidth });
      persist({ chatWidth });
    },
    setChatCollapsed: (chatCollapsed) => {
      set({ chatCollapsed });
      persist({ chatCollapsed });
    },
    setRailExpanded: (railExpanded) => {
      set({ railExpanded });
      persist({ railExpanded });
    },
    setPanelTab: (kind, tab) => set(panelFamilyOf(kind) === '3d' ? { panelTab3d: tab } : { panelTab2d: tab }),
    setTaskDrawerOpen: (taskDrawerOpen) => set({ taskDrawerOpen }),
    setAllOutputsOpen: (allOutputsOpen) => set({ allOutputsOpen }),
    setGenerationReferenceContext: (generationReferenceContext) => set({ generationReferenceContext }),
    openResultDetail: (resultDetail) => set({ resultDetail }),
    closeResultDetail: () => set({ resultDetail: null }),
    stepResultDetail: (delta) => set((state) => {
      const next = stepResultTarget(state.resultDetail, delta);
      return next ? { resultDetail: next } : {};
    }),
    setShortcutsOpen: (shortcutsOpen) => set({ shortcutsOpen }),
    requestViewReset: () => set((state) => ({ viewResetRequest: { seq: (state.viewResetRequest?.seq ?? 0) + 1 } })),
    prefillComposer: (text) => set((state) => ({ composerPrefill: { text, seq: (state.composerPrefill?.seq ?? 0) + 1 } })),
    clearComposerPrefill: () => set({ composerPrefill: null }),
    requestTurn: (text, selection) => set((state) => ({ turnRequest: { text, seq: (state.turnRequest?.seq ?? 0) + 1, ...(selection ? { selection } : {}) } })),
    clearTurnRequest: () => set({ turnRequest: null }),
    requestCapture: (taskId) => set((state) => ({ captureRequest: { taskId, seq: (state.captureRequest?.seq ?? 0) + 1 } })),
    clearCaptureRequest: () => set({ captureRequest: null }),
    setStageParts: (stageParts) => set({ stageParts }),
    setSelectedPart: (selectedPart) => set({ selectedPart }),
    requestPartExport: (key, id) => set((state) => ({ partExportRequest: { key, id, seq: (state.partExportRequest?.seq ?? 0) + 1 } })),
    clearPartExportRequest: () => set({ partExportRequest: null }),
    showProcess: (capability) => set({ panelTab3d: 'process', processPreset: capability }),
    clearProcessPreset: () => set({ processPreset: null }),
    requestImageCommand: (kind) => set((state) => ({ imageCommand: { kind, seq: (state.imageCommand?.seq ?? 0) + 1 } })),
    clearImageCommand: () => set({ imageCommand: null }),
    requestContinue: () => set((state) => ({ continueRequest: { seq: (state.continueRequest?.seq ?? 0) + 1 } })),
    clearContinueRequest: () => set({ continueRequest: null }),
    bumpVersions: () => set((state) => ({ versionsRevision: state.versionsRevision + 1 })),
    watchVersions: (durationMs = VERSIONS_WATCH_DURATION_MS) => set((state) => ({
      versionsWatchUntil: Math.max(state.versionsWatchUntil, Date.now() + durationMs),
      versionsRevision: state.versionsRevision + 1,
    })),
    openLightbox: (items, index = 0) => set(items.length === 0 ? {} : { lightbox: { items, index: Math.min(Math.max(index, 0), items.length - 1) } }),
    closeLightbox: () => set({ lightbox: null }),
    stepLightbox: (delta) => set((state) => state.lightbox
      ? { lightbox: { ...state.lightbox, index: stepIndex(state.lightbox.index, delta, state.lightbox.items.length) } }
      : {}),
    setLightboxIndex: (index) => set((state) => state.lightbox
      ? { lightbox: { ...state.lightbox, index: Math.min(Math.max(index, 0), state.lightbox.items.length - 1) } }
      : {}),
    setCompareNotice: (compareNotice) => set({ compareNotice }),
    startCompare: (initial) => set({ compare: initial ?? { mode: 'two', slots: [] }, compareNotice: null }),
    endCompare: () => set({ compare: null, compareNotice: null }),
    setCompareMode: (mode) => set((state) => state.compare ? { compare: setCompareMode(state.compare, mode) } : {}),
    toggleCompareSlot: (slot) => set((state) => state.compare ? { compare: toggleCompareSlot(state.compare, slot) } : {}),
    removeCompareSlot: (key) => set((state) => state.compare ? { compare: removeCompareSlot(state.compare, key) } : {}),
  };
});

