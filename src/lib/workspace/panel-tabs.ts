import type { HintContent } from '@/components/ui/hint-tooltip';
import {
  schemaText,
  type PanelKind, type PanelKindSchema, type PanelTabSchema, type ProductSchema,
} from '@/lib/product-schema';
import type { PlatformInfo } from '@/lib/workspace/key-dispatch';
import { shortcutCaps } from '@/lib/workspace/stage-actions';

/**
 * The right panel's tabs, declared in the Product Schema (`canvas.frontend.panel_tabs`): the width, every tab
 * with its icon, name, hint and shortcut, and for each kind of stage object which tabs it gets, which opens
 * first and which opens instead when the first has nothing to offer. This module only reads that table. What
 * each tab *shows* is code, keyed by id: the right panel binds one body to every id listed here, and a Schema
 * entry whose id is not listed is not drawn (there is nothing for it to show).
 */
export const PANEL_TAB_IDS = ['layers', 'history', 'details', 'process', 'formats', 'parts', 'operations'] as const;

export type PanelTabId = (typeof PANEL_TAB_IDS)[number];

export function isPanelTabId(id: string): id is PanelTabId {
  return (PANEL_TAB_IDS as readonly string[]).includes(id);
}

/** What the panel is showing: a kind of stage object, or nothing (no object and an empty board). */
export type PanelContext = PanelKind | 'none';

/**
 * Which remembered tab a kind uses. A picture and the bare board are both 2D and share one choice; a model is
 * 3D and has its own. The choice lives for this visit only (`studio-store`).
 */
export type PanelFamily = '2d' | '3d';

export function panelFamilyOf(kind: PanelKind): PanelFamily {
  return kind === 'model' ? '3d' : '2d';
}

/**
 * The kind of thing the panel describes: the object on the stage when there is one; otherwise `board` when
 * the session's canvas has something on it (the layers and history are still worth showing), and `none` when
 * it is empty too.
 */
export function panelContextOf(object: { kind: 'image' | 'model' } | null, boardNodeCount: number): PanelContext {
  if (object) return object.kind;
  return boardNodeCount > 0 ? 'board' : 'none';
}

/** The panel's width in pixels (Schema `panel_tabs.width_pixels`). */
export function panelWidth(schema: ProductSchema): number {
  return schema.canvas.frontend.panel_tabs.width_pixels;
}

/** One tab with its texts already looked up. */
export interface PanelTabView {
  id: PanelTabId;
  icon: string;
  label: string;
  /** What the tab is for, one sentence. */
  hint: string;
  /** Name of the `canvas.frontend.keyboard` setting that switches to this tab. */
  shortcut?: string;
}

export function panelTabView(schema: ProductSchema, tab: PanelTabSchema & { id: PanelTabId }): PanelTabView {
  return {
    id: tab.id,
    icon: tab.icon,
    label: schemaText(schema, tab.label_key),
    hint: schemaText(schema, tab.hint_key),
    ...(tab.shortcut ? { shortcut: tab.shortcut } : {}),
  };
}

function kindEntry(schema: ProductSchema, kind: PanelKind): PanelKindSchema | undefined {
  return schema.canvas.frontend.panel_tabs.kinds.find((entry) => entry.kind === kind);
}

/** The tab ids a kind of object offers, in the Schema's order. An id the code has no body for is left out. */
export function panelTabIds(schema: ProductSchema, kind: PanelKind): PanelTabId[] {
  const declared = new Set(schema.canvas.frontend.panel_tabs.tabs.map((tab) => tab.id));
  return (kindEntry(schema, kind)?.tabs ?? []).filter((id): id is PanelTabId => isPanelTabId(id) && declared.has(id));
}

/** The tabs a kind of object offers, in the Schema's order, with their texts. */
export function panelTabViews(schema: ProductSchema, kind: PanelKind): PanelTabView[] {
  const views: PanelTabView[] = [];
  for (const id of panelTabIds(schema, kind)) {
    const tab = schema.canvas.frontend.panel_tabs.tabs.find((candidate) => candidate.id === id);
    if (tab) views.push(panelTabView(schema, { ...tab, id }));
  }
  return views;
}

/** What the panel cannot know from the Schema: which tabs have nothing to offer right now. */
export interface PanelAvailability {
  /** Tabs with nothing to offer (today only 加工 can be empty: no processing step has an Offering that can run). */
  unavailable: readonly PanelTabId[];
}

/** Everything is available. */
export const ALL_PANEL_TABS_AVAILABLE: PanelAvailability = { unavailable: [] };

/**
 * The tab a kind opens on when nothing was chosen for it: its Schema default, or the Schema's fallback while
 * the default tab has nothing to offer (R3: no empty form). Null when the kind has no tab the code can draw.
 */
export function defaultPanelTab(schema: ProductSchema, kind: PanelKind, availability: PanelAvailability): PanelTabId | null {
  const ids = panelTabIds(schema, kind);
  const entry = kindEntry(schema, kind);
  if (!entry || ids.length === 0) return null;
  const preferred = isPanelTabId(entry.default) && ids.includes(entry.default) ? entry.default : ids[0];
  const fallback = entry.default_when_unavailable;
  if (availability.unavailable.includes(preferred) && fallback && isPanelTabId(fallback) && ids.includes(fallback)) return fallback;
  return preferred;
}

/**
 * The tab to show: the one remembered for this kind's family when this kind offers it, otherwise the kind's
 * default. A remembered tab another kind of the same family does not offer (2D remembered 详情, now the
 * board) falls back to the default instead of showing nothing.
 */
export function activePanelTab(
  schema: ProductSchema,
  kind: PanelKind,
  remembered: PanelTabId | null,
  availability: PanelAvailability,
): PanelTabId | null {
  if (remembered && panelTabIds(schema, kind).includes(remembered)) return remembered;
  return defaultPanelTab(schema, kind, availability);
}

/** What the hover hint of a tab says: its name with the shortcut key (when it has one), then what it is for. */
export function panelTabHintContent(schema: ProductSchema, view: PanelTabView, platform?: PlatformInfo): HintContent {
  const caps = shortcutCaps(schema, view.shortcut, platform);
  return {
    name: view.label,
    ...(caps ? { shortcut: caps } : {}),
    hint: view.hint,
  };
}

/**
 * The tabs a keyboard shortcut switches to, from the Schema: each tab that names a keyboard setting, with the
 * setting's value ("f7", "alt+f9") for the key matcher. A setting that holds no key is skipped.
 */
export function panelShortcutTabs(schema: ProductSchema): Array<{ id: PanelTabId; descriptor: string }> {
  const keyboard = schema.canvas.frontend.keyboard as unknown as Record<string, unknown>;
  const entries: Array<{ id: PanelTabId; descriptor: string }> = [];
  for (const tab of schema.canvas.frontend.panel_tabs.tabs) {
    if (!tab.shortcut || !isPanelTabId(tab.id)) continue;
    const value = keyboard[tab.shortcut];
    if (typeof value === 'string' && value !== '') entries.push({ id: tab.id, descriptor: value });
  }
  return entries;
}

/** The tab a roving-focus key moves to: Left / Right step (wrapping), Home / End jump. Null for any other key. */
export function steppedPanelTab(ids: readonly PanelTabId[], current: PanelTabId, key: string): PanelTabId | null {
  if (ids.length === 0) return null;
  const index = Math.max(0, ids.indexOf(current));
  switch (key) {
    case 'ArrowRight': return ids[(index + 1) % ids.length];
    case 'ArrowLeft': return ids[(index - 1 + ids.length) % ids.length];
    case 'Home': return ids[0];
    case 'End': return ids[ids.length - 1];
    default: return null;
  }
}
