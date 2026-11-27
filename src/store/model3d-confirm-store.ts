import { create } from 'zustand';

/**
 * #678: 转 3D pressed without a 3D model picked asks once: 「使用 {模型} 转 3D？」. 确认 runs this one conversion with
 * that model (`run`); it writes no session lock and no stored preference. 换一个… opens the composer settings on the
 * models tab instead. The confirmation is not an approval card: the session's approval mode still applies.
 */
export interface Model3DConfirmRequest {
  modelId: string;
  modelName: string;
  /** The cost line under the question; empty when no estimate is known. */
  cost: string;
  run: (modelId: string) => void;
}

interface Model3DConfirmState {
  request: Model3DConfirmRequest | null;
  ask: (request: Model3DConfirmRequest) => void;
  close: () => void;
}

export const useModel3DConfirmStore = create<Model3DConfirmState>((set) => ({
  request: null,
  ask: (request) => set({ request }),
  close: () => set({ request: null }),
}));
