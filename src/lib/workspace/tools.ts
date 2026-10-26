import type { HintContent } from '@/components/ui/hint-tooltip';
import {
  schemaText,
  type ProductSchema, type ToolModifier, type ToolRailName, type ToolSchema, type ToolSectionName,
} from '@/lib/product-schema';
import { descriptorCaps, type PlatformInfo } from '@/lib/workspace/key-dispatch';

/**
 * The buttons of the stage's two left rails, declared in the Product Schema (`canvas.frontend.tools`): their
 * order, section, icon, name, hint and shortcut. This module only reads that table. What each button *does* is
 * code, keyed by id: the 2D rail adapter and the 3D view bar bind one handler to every id listed here, and a
 * Schema entry whose id is not listed is not drawn (there is nothing for it to do).
 */
export const CANVAS_TOOL_IDS = [
  'select', 'hand', 'marquee', 'lasso', 'mask', 'eyedropper', 'zoom',
  'upload', 'shape', 'text', 'pencil', 'brush', 'eraser', 'crop',
  'snap',
  'undo', 'redo',
] as const;

export const VIEWER_TOOL_IDS = ['texture', 'clay', 'wire'] as const;

export type CanvasToolId = (typeof CANVAS_TOOL_IDS)[number];
export type ViewerToolId = (typeof VIEWER_TOOL_IDS)[number];

/** The ids the code has a handler for, per rail. */
export const TOOL_IDS: Readonly<Record<ToolRailName, readonly string[]>> = {
  canvas: CANVAS_TOOL_IDS,
  viewer: VIEWER_TOOL_IDS,
};

export function isToolId(rail: ToolRailName, id: string): boolean {
  return TOOL_IDS[rail].includes(id);
}

/** One rail button with its texts already looked up. */
export interface ToolView {
  id: string;
  rail: ToolRailName;
  section: ToolSectionName;
  icon: string;
  label: string;
  /** What the tool does, one sentence. */
  hint: string;
  /** Name of the `canvas.frontend.keyboard` setting to print beside the name. */
  shortcut?: string;
  modifiers: readonly ToolModifier[];
}

/** The buttons of one section, in the Schema's order. */
export interface ToolSectionView {
  section: ToolSectionName;
  tools: ToolView[];
}

export function toolView(schema: ProductSchema, tool: ToolSchema): ToolView {
  return {
    id: tool.id,
    rail: tool.rail,
    section: tool.section,
    icon: tool.icon,
    label: schemaText(schema, tool.label_key),
    hint: schemaText(schema, tool.hint_key),
    ...(tool.shortcut ? { shortcut: tool.shortcut } : {}),
    modifiers: tool.modifiers ?? [],
  };
}

/**
 * The buttons of a rail, grouped into sections. Sections come in the order they first appear in the Schema
 * table and keep its button order. An entry the code has no handler for is left out, and so is a section
 * that ends up empty.
 */
export function toolSections(schema: ProductSchema, rail: ToolRailName): ToolSectionView[] {
  const sections: ToolSectionView[] = [];
  for (const tool of schema.canvas.frontend.tools) {
    if (tool.rail !== rail || !isToolId(rail, tool.id)) continue;
    let group = sections.find((candidate) => candidate.section === tool.section);
    if (!group) {
      group = { section: tool.section, tools: [] };
      sections.push(group);
    }
    group.tools.push(toolView(schema, tool));
  }
  return sections;
}

/** The ids a rail draws, in display order. */
export function toolIds(schema: ProductSchema, rail: ToolRailName): string[] {
  return toolSections(schema, rail).flatMap((group) => group.tools.map((tool) => tool.id));
}

/**
 * Key caps of a tool's shortcut: the modifiers, then the key of the keyboard setting ("Space" prints as 空格).
 * Undefined when the tool has no shortcut or the setting does not hold a key.
 */
export function toolShortcutCaps(schema: ProductSchema, view: Pick<ToolView, 'shortcut' | 'modifiers'>, platform?: PlatformInfo): string[] | undefined {
  if (!view.shortcut) return undefined;
  const value = (schema.canvas.frontend.keyboard as unknown as Record<string, unknown>)[view.shortcut];
  if (typeof value !== 'string' || value === '') return undefined;
  return descriptorCaps([...view.modifiers, value].join('+'), (key) => schemaText(schema, key), platform);
}

/**
 * What the hover hint of a rail button says, top to bottom: why it cannot be used right now (when it
 * cannot), the name with its shortcut, and what it does. Rail tools never call a model, so there is no
 * cost line.
 */
export function toolHintContent(schema: ProductSchema, view: ToolView, options: { reason?: string; platform?: PlatformInfo } = {}): HintContent {
  const caps = toolShortcutCaps(schema, view, options.platform);
  return {
    name: view.label,
    ...(caps ? { shortcut: caps } : {}),
    ...(options.reason ? { reason: options.reason } : {}),
    hint: view.hint,
  };
}
