/**
 * 任务完成提醒：页面在后台时，任务结束会改标签页标题并（经用户同意）弹出
 * 浏览器通知。以下是不依赖界面的判断；文案全部来自 Schema。
 */

export type NoticeOutcome = 'done' | 'failed';

export interface NoticeTask {
  taskId: string;
  terminal: boolean;
  notFound: boolean;
  failure: unknown;
  downloadables: readonly unknown[];
  prompt: string;
  capability: string;
}

export interface NoticeScan {
  /** 本次观察到的仍在进行的任务。 */
  active: Set<string>;
  /** 上次还在进行、这次已结束且有明确结局（成功或失败）的任务。 */
  finished: Array<{ task: NoticeTask; outcome: NoticeOutcome }>;
}

/**
 * 只提醒"这个页面亲眼见过它在进行"的任务：刷新后回灌的历史终态任务、用户自己
 * 取消的任务（没有失败也没有产物）都不提醒。
 */
export function scanTasks(previousActive: ReadonlySet<string>, tasks: readonly NoticeTask[]): NoticeScan {
  const active = new Set<string>();
  const finished: NoticeScan['finished'] = [];
  for (const task of tasks) {
    const running = !task.terminal && !task.notFound;
    if (running) {
      active.add(task.taskId);
      continue;
    }
    if (!previousActive.has(task.taskId) || task.notFound) continue;
    if (task.failure) finished.push({ task, outcome: 'failed' });
    else if (task.downloadables.length > 0) finished.push({ task, outcome: 'done' });
  }
  return { active, finished };
}

export interface PermissionContext {
  supported: boolean;
  permission: NotificationPermission | 'unsupported';
  desktop: boolean;
  pending?: boolean;
}

/** Only an explicit user action may request permission, and only while the browser can still prompt. */
export function canRequestPermission(context: PermissionContext): boolean {
  return context.desktop && context.supported && context.permission === 'default' && context.pending !== true;
}

/** Invoke the browser API synchronously from the caller's user-gesture handler. */
export function requestPermissionFromGesture(
  readContext: () => PermissionContext,
  markPending: () => void,
  request: () => Promise<NotificationPermission>,
): Promise<NotificationPermission> | null {
  if (!canRequestPermission(readContext())) return null;
  markPending();
  return request();
}

export function readNoticePermission(): NotificationPermission | 'unsupported' {
  return typeof Notification === 'undefined' ? 'unsupported' : Notification.permission;
}

/** 只有电脑端且用户已同意时才弹通知。 */
export function mayNotify(context: Pick<PermissionContext, 'supported' | 'permission' | 'desktop'>): boolean {
  return context.desktop && context.supported && context.permission === 'granted';
}

/** 电脑端：宽屏且有精细指针和悬停能力（手机、平板不提醒）。 */
export const DESKTOP_QUERY = '(min-width: 1024px) and (hover: hover) and (pointer: fine)';
