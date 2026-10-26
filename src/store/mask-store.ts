import { create } from 'zustand';

import { remapStrokesToFrame, strokesTouchFrame } from '@/lib/canvas/inpaint-export';
import type { CurrentObject } from '@/store/workspace-store';

/**
 * 蒙版草稿：整张画布（虚线画板）的蒙版，不属于任何一张图片。笔画点用画板坐标
 * （相对画板左上角，单位与画板一致），所以平移、缩放视图和切换选中的图片都不会让
 * 它移位或消失。每一笔记着自己落笔时的笔刷大小；撤销 / 重做按笔进行。
 * 它在画板之外也要看得见——输入框上方的蒙版芯片、舞台头、退出前的确认——所以放在
 * store 里。同一时刻只有一份草稿，属于它被涂的那个会话。
 */
export interface MaskStroke {
  /** x0, y0, x1, y1 …，画板坐标。 */
  points: number[];
  /** 这一笔的笔刷直径（画布单位），落笔时定下，之后改笔刷不影响它。区域（选框 / 套索）不用它。 */
  size: number;
  /** 擦除：把这一笔涂过的地方还给原图（不再可编辑）；区域加 erase 就是减选。 */
  erase?: boolean;
  /** 封闭区域（矩形选框或套索），points 是多边形顶点，填充而不是描线。 */
  region?: boolean;
  /** 反选：整张画布的可编辑与不可编辑互换，没有 points。 */
  invert?: boolean;
}

/**
 * 蒙版历史里的一步：一笔，或者一次整体替换（选区替换、Ctrl+D、虚线框变化造成的清除；可撤销）。
 * 撤销和重做按步进行，所以清除之后 Ctrl+Z 能把笔画拿回来。快照和重做栈都用当前虚线框的坐标：
 * 虚线框变化时它们和笔画一起平移，撤销 / 重做拿回来的笔画仍落在原来的画面位置。
 */
export type MaskStep =
  | { kind: 'stroke' }
  | { kind: 'frame'; before: MaskStroke[]; after: MaskStroke[] };

export interface MaskFrame {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface MaskDraft {
  sessionId: string;
  strokes: MaskStroke[];
  /** 被撤销、可重做的笔画（最近撤销的在末尾）；画新的一笔会清空它。 */
  redo: MaskStroke[];
  /** 正在画的这一笔。 */
  current: MaskStroke | null;
  /** 这些笔画所属的虚线框（世界坐标）；虚线框变了就按它重映射。 */
  frame?: MaskFrame;
  /** 线性历史；缺省时每一笔就是一步。 */
  steps?: MaskStep[];
  redoSteps?: MaskStep[];
}

/** 等待用户回答的问题：退出蒙版会丢掉已经涂好的笔画。 */
export type MaskQuestion = { kind: 'exit' };

type DraftUpdate = MaskDraft | null | ((current: MaskDraft | null) => MaskDraft | null);

/** 虚线框变化后蒙版的去向：没变、笔画跟着移动、或者无法映射被清除。 */
export type MaskRemapOutcome = 'none' | 'moved' | 'cleared';

/** How a new region meets the selection that is already there (Photoshop: none / Shift / Alt). */
export type SelectionMode = 'replace' | 'add' | 'subtract';

interface MaskStore {
  draft: MaskDraft | null;
  /** The quick mask paints (false) or erases (true); X toggles it. */
  erasing: boolean;
  setErasing: (erasing: boolean) => void;
  toggleErasing: () => void;
  /** Bumped by "局部重绘" in the stage header when a selection already exists: the board then submits it. */
  submitRequest: number;
  requestSubmit: () => void;
  /** A marquee / lasso region joins the selection; `replace` drops what was there (one undoable step). */
  commitRegion: (sessionId: string, region: MaskStroke, mode: SelectionMode, frame?: MaskFrame) => void;
  /** Ctrl+Shift+I. */
  invertSelection: (sessionId: string, frame?: MaskFrame) => void;
  /** Ctrl+D: drops the whole selection as one undoable step; false when there was none. */
  clearSelection: (sessionId: string) => boolean;
  /** 其他会话停放的蒙版草稿；切换会话时互不丢失。 */
  parked: Record<string, MaskDraft>;
  question: MaskQuestion | null;
  setDraft: (update: DraftUpdate) => void;
  clearDraft: () => void;
  /** 把 `sessionId` 的草稿换成当前草稿，原来的当前草稿停放起来。 */
  activate: (sessionId: string) => void;
  ask: (question: MaskQuestion) => void;
  dismissQuestion: () => void;
  /** 落笔：开始新的一笔并清空重做栈。`frame` 是此刻的虚线框。 */
  beginStroke: (sessionId: string, point: { x: number; y: number }, size: number, frame?: MaskFrame, erase?: boolean) => void;
  extendStroke: (sessionId: string, point: { x: number; y: number }) => void;
  /** 抬笔：正在画的这一笔成为一笔已完成的笔画。 */
  endStroke: (sessionId: string) => void;
  /** 撤销最后一步；没有可撤销的步骤时返回 false（调用方什么也不做，不落到画布历史）。 */
  undoStroke: (sessionId: string) => boolean;
  redoStroke: (sessionId: string) => boolean;
  /**
   * 虚线框变了：笔画（连同撤销 / 重做历史）跟着画面移动，画面上的位置不变；没有一笔还碰得到虚线框时
   * 清除，清除是可撤销的一步。单纯的移动不记入蒙版历史：虚线框本身的变化由画布历史撤销，撤回后蒙版
   * 再跟着移回去。
   */
  remapFrame: (sessionId: string, frame: MaskFrame) => MaskRemapOutcome;
}

function stepsOf(draft: MaskDraft): MaskStep[] {
  return draft.steps ?? draft.strokes.map((): MaskStep => ({ kind: 'stroke' }));
}

function sameFrame(left: MaskFrame, right: MaskFrame): boolean {
  return left.x === right.x && left.y === right.y && left.width === right.width && left.height === right.height;
}

/** The whole draft (strokes, the stroke being drawn and every history snapshot) carried from one frame to another. */
function draftInFrame(draft: MaskDraft, from: MaskFrame, to: MaskFrame): MaskDraft {
  const move = (strokes: MaskStroke[]) => remapStrokesToFrame(strokes, from, to);
  const moveStep = (step: MaskStep): MaskStep => (step.kind === 'frame' ? { kind: 'frame', before: move(step.before), after: move(step.after) } : step);
  return {
    ...draft,
    frame: to,
    strokes: move(draft.strokes),
    redo: move(draft.redo),
    current: draft.current ? move([draft.current])[0] : null,
    ...(draft.steps ? { steps: draft.steps.map(moveStep) } : {}),
    ...(draft.redoSteps ? { redoSteps: draft.redoSteps.map(moveStep) } : {}),
  };
}

export const useMaskStore = create<MaskStore>()((set, get) => {
  /** The draft of `sessionId`, bringing a parked one forward when another session's draft was in view. */
  const draftOf = (state: Pick<MaskStore, 'draft' | 'parked'>, sessionId: string) => {
    if (state.draft && state.draft.sessionId === sessionId) return { draft: state.draft, parked: state.parked };
    const parked = { ...state.parked };
    if (state.draft) parked[state.draft.sessionId] = state.draft;
    const next = parked[sessionId] ?? null;
    delete parked[sessionId];
    return { draft: next, parked };
  };
  return {
    draft: null,
    parked: {},
    question: null,
    erasing: false,
    setErasing: (erasing) => set({ erasing }),
    toggleErasing: () => set((state) => ({ erasing: !state.erasing })),
    submitRequest: 0,
    requestSubmit: () => set((state) => ({ submitRequest: state.submitRequest + 1 })),
    commitRegion: (sessionId, region, mode, frame) => set((state) => {
      const { draft: existing, parked } = draftOf(state, sessionId);
      const base: MaskDraft = existing ?? { sessionId, strokes: [], redo: [], current: null };
      const stroke: MaskStroke = mode === 'subtract' ? { ...region, erase: true } : { ...region, erase: undefined };
      if (mode === 'subtract' && base.strokes.length === 0) return state;
      if (mode === 'replace') {
        const after = [stroke];
        return {
          parked,
          draft: { ...base, frame: frame ?? base.frame, strokes: after, steps: [...stepsOf(base), { kind: 'frame', before: base.strokes, after }], redo: [], redoSteps: [], current: null },
        };
      }
      return {
        parked,
        draft: { ...base, frame: frame ?? base.frame, strokes: [...base.strokes, stroke], steps: [...stepsOf(base), { kind: 'stroke' }], redo: [], redoSteps: [], current: null },
      };
    }),
    invertSelection: (sessionId, frame) => set((state) => {
      const { draft: existing, parked } = draftOf(state, sessionId);
      const base: MaskDraft = existing ?? { sessionId, strokes: [], redo: [], current: null };
      const invert: MaskStroke = { points: [], size: 0, invert: true };
      return {
        parked,
        draft: { ...base, frame: frame ?? base.frame, strokes: [...base.strokes, invert], steps: [...stepsOf(base), { kind: 'stroke' }], redo: [], redoSteps: [], current: null },
      };
    }),
    clearSelection: (sessionId) => {
      const draft = get().draft;
      if (!draft || draft.sessionId !== sessionId || draft.strokes.length === 0) return false;
      set({
        draft: { ...draft, strokes: [], steps: [...stepsOf(draft), { kind: 'frame', before: draft.strokes, after: [] }], redo: [], redoSteps: [], current: null },
      });
      return true;
    },
    setDraft: (update) => set((state) => ({ draft: typeof update === 'function' ? update(state.draft) : update })),
    clearDraft: () => set({ draft: null, question: null }),
    activate: (sessionId) => set((state) => {
      if (state.draft?.sessionId === sessionId) return state;
      const moved = draftOf(state, sessionId);
      return { draft: moved.draft, parked: moved.parked, question: null };
    }),
    ask: (question) => set({ question }),
    dismissQuestion: () => set({ question: null }),
    beginStroke: (sessionId, point, size, frame, erase = false) => set((state) => {
      const { draft: existing, parked } = draftOf(state, sessionId);
      const base: MaskDraft = existing ?? { sessionId, strokes: [], redo: [], current: null };
      return {
        parked,
        draft: {
          ...base,
          frame: frame ?? base.frame,
          steps: stepsOf(base),
          redo: [],
          redoSteps: [],
          current: { points: [point.x, point.y], size, ...(erase ? { erase: true } : {}) },
        },
      };
    }),
    extendStroke: (sessionId, point) => set((state) => {
      const draft = state.draft;
      if (!draft || draft.sessionId !== sessionId || !draft.current) return state;
      return { draft: { ...draft, current: { ...draft.current, points: [...draft.current.points, point.x, point.y] } } };
    }),
    endStroke: (sessionId) => set((state) => {
      const draft = state.draft;
      if (!draft || draft.sessionId !== sessionId || !draft.current) return state;
      return { draft: { ...draft, strokes: [...draft.strokes, draft.current], steps: [...stepsOf(draft), { kind: 'stroke' }], current: null } };
    }),
    undoStroke: (sessionId) => {
      const draft = get().draft;
      if (!draft || draft.sessionId !== sessionId) return false;
      const steps = stepsOf(draft);
      const step = steps[steps.length - 1];
      if (!step) return false;
      const redoSteps = [...(draft.redoSteps ?? []), step];
      if (step.kind === 'stroke') {
        const stroke = draft.strokes[draft.strokes.length - 1];
        set({ draft: { ...draft, strokes: draft.strokes.slice(0, -1), redo: [...draft.redo, stroke], steps: steps.slice(0, -1), redoSteps, current: null } });
      } else {
        set({ draft: { ...draft, strokes: step.before, steps: steps.slice(0, -1), redoSteps, current: null } });
      }
      return true;
    },
    redoStroke: (sessionId) => {
      const draft = get().draft;
      if (!draft || draft.sessionId !== sessionId) return false;
      const redoSteps = draft.redoSteps ?? draft.redo.map((): MaskStep => ({ kind: 'stroke' }));
      const step = redoSteps[redoSteps.length - 1];
      if (!step) return false;
      const steps = [...stepsOf(draft), step];
      if (step.kind === 'stroke') {
        const stroke = draft.redo[draft.redo.length - 1];
        set({ draft: { ...draft, strokes: [...draft.strokes, stroke], redo: draft.redo.slice(0, -1), steps, redoSteps: redoSteps.slice(0, -1) } });
      } else {
        set({ draft: { ...draft, strokes: step.after, steps, redoSteps: redoSteps.slice(0, -1) } });
      }
      return true;
    },
    remapFrame: (sessionId, frame) => {
      const draft = get().draft;
      if (!draft || draft.sessionId !== sessionId) return 'none';
      if (!draft.frame) {
        // A draft from before frames were recorded has nothing to map from; adopt the frame as it is.
        set({ draft: { ...draft, frame } });
        return 'none';
      }
      if (sameFrame(draft.frame, frame)) return 'none';
      // Everything the mask can show again (its strokes, the undo snapshots, the redo stack) moves with the
      // picture, so an undo or redo after the frame changed lands where the user saw it, not off by the shift.
      const moved = draftInFrame(draft, draft.frame, frame);
      if (draft.strokes.length === 0) {
        set({ draft: moved });
        return 'none';
      }
      if (strokesTouchFrame(moved.strokes, frame)) {
        set({ draft: moved });
        return 'moved';
      }
      set({
        draft: {
          ...moved,
          strokes: [],
          steps: [...stepsOf(moved), { kind: 'frame', before: moved.strokes, after: [] }],
          redo: [],
          redoSteps: [],
          current: null,
        },
      });
      return 'cleared';
    },
  };
});

/** 一个对象的身份：会话、任务、图和资产。 */
export function objectKey(object: Pick<CurrentObject, 'sessionId' | 'taskId' | 'url' | 'assetId'>): string {
  return [object.sessionId ?? '', object.taskId, object.url, object.assetId ?? ''].join('|');
}

/** Whether the current draft is still the exact object this submission built its mask from. */
export function sameMaskDraft(current: MaskDraft | null, submitted: MaskDraft | null): boolean {
  return Boolean(current && submitted && current === submitted);
}

/** 已经涂下的笔数（不含正在画的那一笔）；不属于这个会话的草稿不算。 */
export function maskStrokeCount(draft: MaskDraft | null, sessionId: string | null | undefined): number {
  if (!draft || !sessionId || draft.sessionId !== sessionId) return 0;
  return draft.strokes.length;
}

export function sameObject(left: Pick<CurrentObject, 'taskId' | 'url' | 'assetId'> | null, right: Pick<CurrentObject, 'taskId' | 'url' | 'assetId'> | null): boolean {
  if (!left || !right) return left === right;
  return left.taskId === right.taskId && left.url === right.url && (left.assetId ?? '') === (right.assetId ?? '');
}

