'use client';

/**
 * 工作区任务生命周期轮询器。
 *
 * Agent 注册任务（chat SSE taskRegistered）后，本 hook 以 Schema 声明的
 * 间隔（runtime.frontend.task_polling_interval_milliseconds）轮询原生
 * 任务详情（runtime.http.routes.task_item），把 state/progress/failure/
 * assets[]/resources_pending 投影进 workspace store；终态或 404 后停止。
 * 轮询失败保持静默重试（下一个 tick 再试），404 视为终态。
 *
 * 页面加载/刷新后内存任务集合清空，本 hook 还会从服务端任务列表
 * （runtime.http.routes.tasks，按非终态过滤）回灌仍在运行的任务，
 * 让托盘与轮询在刷新后自动恢复，不依赖丢失的 SSE 现场。
 */

import { useEffect } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { hydrateRunningTasks, hydrateSessionTasks } from '@/lib/workspace/task-hydration';
import { projectNativeTaskDetail } from '@/lib/workspace/task-projection';
import { startSerialPoller } from '@/lib/workspace/serial-poller';
import { useWorkspaceStore } from '@/store/workspace-store';
import { useChatStore } from '@/store/chat-store';

export function useTaskTracker(): void {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  // 稳定依赖：仅当“活跃任务集合”变化时重建轮询循环。
  // 终态但 resources_pending=true 的任务仍需轮询（成功后附加产物落盘）。
  const isActive = (task: {
    terminal: boolean;
    notFound: boolean;
    resourcesPending: boolean;
  }) => !task.notFound && (!task.terminal || task.resourcesPending);
  const activeKey = useWorkspaceStore((state) =>
    state.trackedTasks
      .filter(isActive)
      .map((task) => task.taskId)
      .sort()
      .join(','),
  );

  // 刷新回灌：把服务端仍在运行的任务重新注册进跟踪集合（registerTask 幂等去重）。
  useEffect(() => {
    const controller = new AbortController();
    hydrateRunningTasks({
      tasksRoute: schema.runtime.http.routes.tasks,
      taskStates: schema.task_states,
      progressMaximum: schema.runtime.frontend.percentage_multiplier,
      signal: controller.signal,
      register: (input) => {
        if (!controller.signal.aborted) useWorkspaceStore.getState().registerTask(input);
      },
    }).catch(() => {
      // 服务端任务列表不可用时保持静默：对话内 SSE 恢复仍是主路径。
    });
    return () => {
      controller.abort();
    };
  }, [schema]);

  // The tray is a view of server tasks. Restore recent versions when the user
  // opens a session from history, including tasks completed in another tab.
  useEffect(() => {
    if (!activeSessionId) return;
    const controller = new AbortController();
    hydrateSessionTasks({
      tasksRoute: schema.runtime.http.routes.tasks,
      sessionId: activeSessionId,
      limit: schema.runtime.frontend.session_menu_page_size,
      taskStates: schema.task_states,
      progressMaximum: schema.runtime.frontend.percentage_multiplier,
      signal: controller.signal,
      consume: (input, projection) => {
        if (controller.signal.aborted || useChatStore.getState().activeSessionId !== activeSessionId) return;
        const store = useWorkspaceStore.getState();
        if (store.trackedTasks.some((task) => task.taskId === input.taskId)) return;
        store.registerTask({ ...input, autoPreview: false });
        useWorkspaceStore.getState().applyTaskDetail(input.taskId, projection, activeSessionId);
      },
    }).catch(() => {
      // History remains available if the task list is temporarily unavailable.
    });
    return () => controller.abort();
  }, [activeSessionId, schema]);

  useEffect(() => {
    if (activeKey === '') return;
    const progressMaximum = schema.runtime.frontend.percentage_multiplier;

    return startSerialPoller(async (signal) => {
      const store = useWorkspaceStore.getState();
      const active = store.trackedTasks.filter(isActive);
      await Promise.all(
        active.map(async (task) => {
          const route = schema.runtime.http.routes.task_item.replace(
            '{taskID}',
            encodeURIComponent(task.taskId),
          );
          try {
            const response = await fetch(route, { signal });
            if (signal.aborted) return;
            if (response.status === 404) {
              useWorkspaceStore.getState().markTaskNotFound(task.taskId);
              return;
            }
            if (!response.ok) return;
            const raw: unknown = await response.json();
            if (signal.aborted) return;
            const projection = projectNativeTaskDetail(
              raw,
              schema.task_states,
              progressMaximum,
            );
            if (projection && !signal.aborted) {
              useWorkspaceStore.getState().applyTaskDetail(
                task.taskId,
                projection,
                useChatStore.getState().activeSessionId,
              );
            }
          } catch {
            // 瞬时网络错误：静默等待下一个轮询 tick。
          }
        }),
      );
    }, schema.runtime.frontend.task_polling_interval_milliseconds);
  }, [activeKey, schema]);
}
