'use client';

/**
 * 页面在后台时任务结束：标签页标题带上完成数，并（经用户同意）弹出浏览器通知。
 * 只在电脑端提醒；权限由 TaskDrawer 中的明确用户操作请求。
 * 判断见 lib/workspace/completion-notice，文案全部来自 Schema。
 */

import { useEffect } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import {
  DESKTOP_QUERY,
  mayNotify,
  readNoticePermission,
  scanTasks,
  type NoticeOutcome,
} from '@/lib/workspace/completion-notice';
import { useWorkspaceStore } from '@/store/workspace-store';

export function useTaskCompletionNotice(): void {
  const { schema } = useProductSchema();
  useEffect(() => {
    const text = (key: string) => schemaText(schema, key);
    const capabilityLabels = new Map(schema.capabilities.map((capability) => [capability.id, capability.label_cn || capability.label]));
    const desktopQuery = window.matchMedia(DESKTOP_QUERY);
    let previousActive = new Set<string>();
    let unseen = 0;
    let baseTitle = document.title;
    const open: Notification[] = [];

    const restore = () => {
      if (unseen > 0) document.title = baseTitle;
      unseen = 0;
      for (const notification of open.splice(0)) notification.close();
    };
    const onVisibility = () => {
      if (document.visibilityState === 'visible') restore();
    };
    const label = (outcome: NoticeOutcome) => text(outcome === 'done' ? 'ui.workspace.notice.done' : 'ui.workspace.notice.failed');

    const unsubscribe = useWorkspaceStore.subscribe((state) => {
      const scan = scanTasks(previousActive, state.trackedTasks);
      previousActive = scan.active;
      const desktop = desktopQuery.matches;
      if (scan.finished.length === 0 || document.visibilityState === 'visible') return;
      if (unseen === 0) baseTitle = document.title;
      unseen += scan.finished.length;
      document.title = schemaCountMessage(schema, 'ui.workspace.notice.tab_title', unseen, { count: unseen, title: baseTitle });
      const permission = readNoticePermission();
      if (!mayNotify({ desktop, supported: permission !== 'unsupported', permission })) return;
      for (const { task, outcome } of scan.finished) {
        try {
          const notification = new Notification(label(outcome), {
            body: task.prompt !== '' ? task.prompt : formatSchemaText(text('ui.workspace.notice.body_fallback'), { capability: capabilityLabels.get(task.capability) ?? task.capability }),
            tag: `task-${task.taskId}`,
          });
          notification.onclick = () => { window.focus(); notification.close(); };
          open.push(notification);
        } catch { /* Some platforms refuse page-created notifications; the title still tells. */ }
      }
    });
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      unsubscribe();
      document.removeEventListener('visibilitychange', onVisibility);
      restore();
    };
  }, [schema]);
}
