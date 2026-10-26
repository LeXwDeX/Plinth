import { create } from 'zustand';

import { isTaskFilter, type TaskFilter } from '@/lib/workspace/task-filter';

/**
 * 用户偏好设置 Store — localStorage 持久化
 *
 * 当前偏好：
 * - taskFilter: 任务中心的筛选（全部 / 进行中 / 失败），下次打开时沿用。
 *   取代了早先没有任何地方读取的 hideFailures。
 */

const STORAGE_KEY = 'art-ai-preferences';

interface Preferences {
  taskFilter: TaskFilter;
}

interface PreferenceStore extends Preferences {
  setTaskFilter: (value: TaskFilter) => void;
}

function loadFromStorage(): Partial<Preferences> {
  if (typeof window === 'undefined') return {};
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as { taskFilter?: unknown };
    return isTaskFilter(parsed.taskFilter) ? { taskFilter: parsed.taskFilter } : {};
  } catch {
    return {};
  }
}

function saveToStorage(prefs: Preferences) {
  if (typeof window === 'undefined') return;
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
  } catch {
    // localStorage 不可用时静默忽略
  }
}

export const usePreferences = create<PreferenceStore>((set) => {
  const saved = loadFromStorage();
  return {
    taskFilter: saved.taskFilter ?? 'all',

    setTaskFilter: (value: TaskFilter) => {
      set({ taskFilter: value });
      saveToStorage({ taskFilter: value });
    },
  };
});
