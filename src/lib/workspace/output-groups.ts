/**
 * 全部产出的整理：按日期分组，以及 Shift 连选。只做排列和集合运算，不碰界面。
 */

export interface DayGroup<T> {
  /** Local calendar day, "2026-10-08"; the key of the group. */
  day: string;
  kind: 'today' | 'yesterday' | 'date';
  year: number;
  month: number;
  dayOfMonth: number;
  items: T[];
}

function dayKey(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

/**
 * Newest day first; inside a day the order given is kept. Something without a time (work that
 * has just started and is not on the server's chain yet) belongs to today.
 */
export function groupByDay<T>(items: readonly T[], createdAt: (item: T) => string, now: Date = new Date()): DayGroup<T>[] {
  const today = dayKey(now);
  const yesterdayDate = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
  const yesterday = dayKey(yesterdayDate);
  const groups = new Map<string, DayGroup<T>>();
  for (const item of items) {
    const stamp = createdAt(item);
    const parsed = stamp ? new Date(stamp) : now;
    const date = Number.isFinite(parsed.getTime()) ? parsed : now;
    const day = dayKey(date);
    let group = groups.get(day);
    if (!group) {
      group = {
        day,
        kind: day === today ? 'today' : day === yesterday ? 'yesterday' : 'date',
        year: date.getFullYear(), month: date.getMonth() + 1, dayOfMonth: date.getDate(),
        items: [],
      };
      groups.set(day, group);
    }
    group.items.push(item);
  }
  return [...groups.values()].sort((left, right) => right.day.localeCompare(left.day));
}

/**
 * A click on a tile's checkbox. A plain click flips that tile. With Shift, and an earlier click
 * to start from, every tile from there to this one (in the order shown) joins the selection.
 * `order` lists the tiles that can be chosen, as they appear on the page.
 */
export function selectionAfterClick(
  order: readonly string[],
  selected: ReadonlySet<string>,
  anchor: string | null,
  target: string,
  shift: boolean,
): Set<string> {
  const next = new Set(selected);
  const from = anchor === null ? -1 : order.indexOf(anchor);
  const to = order.indexOf(target);
  if (shift && from >= 0 && to >= 0) {
    const [low, high] = from < to ? [from, to] : [to, from];
    for (const key of order.slice(low, high + 1)) next.add(key);
    return next;
  }
  if (next.has(target)) next.delete(target); else next.add(target);
  return next;
}

/** Select exactly one tile's worth of change: flip a single key (a click without Shift). */
export function toggleSelection(selected: ReadonlySet<string>, key: string): Set<string> {
  const next = new Set(selected);
  if (next.has(key)) next.delete(key); else next.add(key);
  return next;
}

/** The empty selection (Esc, or the bulk bar's cancel). */
export function clearSelection(): Set<string> {
  return new Set();
}

/** How many of the chosen keys are still among the tiles that can be chosen. */
export function selectedCount(order: readonly string[], selected: ReadonlySet<string>): number {
  return order.filter((key) => selected.has(key)).length;
}

/**
 * Esc clears the selection only when there is one and nothing above the panel owns the key:
 * the large view, an open popup or dialog, or a text field.
 */
export function escapeClearsSelection(count: number, blockers: { lightbox: boolean; popupOpen: boolean; editable: boolean }): boolean {
  return count > 0 && !blockers.lightbox && !blockers.popupOpen && !blockers.editable;
}
