import { expect, it } from 'vitest';
import {
  partializeWorkspacePrefs,
  useWorkspaceStore,
} from '@/store/workspace-store';

/**
 * 刷新后保留用户偏好：只持久化每个域所选模型；任务托盘、舞台上的当前对象
 * 等运行状态是服务端事实或会话内瞬态，不进偏好存储。
 * Node 测试环境无 localStorage → persist 退化内存存储（不崩即通过）。
 */

it('partializes only the per-domain model preferences', () => {
  const store = useWorkspaceStore.getState();
  store.setModel('model-3d', 'meshy-7');
  store.registerTask({
    taskId: 'task_pref_probe',
    capability: 'text-to-image',
    progressMaximum: 100,
  });

  const prefs = partializeWorkspacePrefs(useWorkspaceStore.getState());
  expect(prefs).toEqual({
    modelByDomain: { image: null, 'model-3d': 'meshy-7' },
  });
  expect('trackedTasks' in prefs).toBe(false);
  expect('currentObject' in prefs).toBe(false);

  // 还原共享 store，避免污染其他用例。
  store.setModel('model-3d', null);
  store.clearTrackedTasks();
});
