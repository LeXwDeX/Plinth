/** How close to the end of the list (in pixels) still counts as "at the bottom". */
export const BOTTOM_THRESHOLD_PIXELS = 48;

export interface ScrollMetrics {
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
}

export function isNearBottom(metrics: ScrollMetrics, threshold: number = BOTTOM_THRESHOLD_PIXELS): boolean {
  return metrics.scrollHeight - metrics.scrollTop - metrics.clientHeight <= threshold;
}

/**
 * Whether new content should scroll the list: while the reader is at the bottom, or when the new
 * content is their own message. A reader who scrolled up is never pulled down.
 */
export function shouldFollow(state: { following: boolean; ownMessage: boolean }): boolean {
  return state.following || state.ownMessage;
}
