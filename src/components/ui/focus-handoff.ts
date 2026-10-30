/**
 * 模态对话框与焦点交接（内部存档 #164）。
 *
 * Base UI 的模态对话框打开时先给页面其余区域加 aria-hidden，再把焦点移进对话框；
 * 关闭时把焦点还给打开前的元素。两个时序问题由这里处理：
 *
 * - 程序化打开（不经 DialogTrigger）时，焦点仍留在即将被隐藏的区域里，
 *   祖先先被标成 aria-hidden，焦点才离开（Chromium 告警“Blocked aria-hidden…”）。
 *   对话框在标记前取下焦点，并自己记住还原目标。
 * - 对话框关闭动画期间，页面其余区域仍是 aria-hidden，此时把焦点放进对话输入框会失败，
 *   随后又被还原到打开前的元素。需要在关闭后聚焦的目标先登记，由对话框关闭时交给它。
 */

const DIALOG_SELECTOR = '[data-slot="dialog-content"]';

let pending: (() => HTMLElement | null) | null = null;

/** 聚焦目标；若有对话框正在显示或关闭，等它关闭时再交给目标。 */
export function focusWhenUnblocked(target: () => HTMLElement | null): void {
  const element = target();
  if (!element) return;
  // 目标在对话框外，而对话框仍在页面上（显示中或关闭动画中）：外面此时被隐藏，等关闭再聚焦。
  if (document.querySelector(DIALOG_SELECTOR) !== null && !element.closest(DIALOG_SELECTOR)) {
    pending = target;
    return;
  }
  pending = null;
  element.focus();
}

/** 对话框关闭时取走登记的目标；只取一次。 */
export function takeFocusHandoff(): HTMLElement | null {
  const target = pending;
  pending = null;
  const element = target?.() ?? null;
  return element && element.isConnected ? element : null;
}

/** 打开对话框前取下页面里的焦点，返回应在关闭时还原的元素。 */
export function releaseOutsideFocus(popup: HTMLElement | null): HTMLElement | null {
  const active = document.activeElement;
  if (!(active instanceof HTMLElement) || active === document.body) return null;
  if (popup?.contains(active)) return null;
  active.blur();
  return active;
}

/** 测试用：清空登记。 */
export function resetFocusHandoff(): void {
  pending = null;
}
