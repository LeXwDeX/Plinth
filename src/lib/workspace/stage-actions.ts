import type { HintContent } from '@/components/ui/hint-tooltip';
import {
  formatSchemaText, schemaText,
  type CapabilitySchema, type ProductSchema, type StageActionCostHint, type StageActionGroup, type StageActionKind, type StageActionSchema,
} from '@/lib/product-schema';
import { descriptorCaps, type PlatformInfo } from '@/lib/workspace/key-dispatch';

/**
 * The stage toolbar's buttons, declared in the Product Schema (`canvas.frontend.stage_actions`): their
 * order, group, icon, texts, the possible-cost mark and what the hover hint says about cost. This module
 * only reads that table. What each button *does* is code, keyed by id: the header binds one handler to every
 * id listed here, and a Schema entry whose id is not listed is not drawn (there is nothing for it to do).
 */
export const STAGE_ACTION_IDS = [
  'merge',
  'continue_from',
  'inpaint',
  'remove_background',
  'to_3d',
  'retexture_with_image',
  'capture_2d',
  'send_chat',
  'export_board',
  'compare',
  'enlarge',
] as const;

export type StageActionId = (typeof STAGE_ACTION_IDS)[number];

export function isStageActionId(id: string): id is StageActionId {
  return (STAGE_ACTION_IDS as readonly string[]).includes(id);
}

/** Stable test ids (acceptance scripts and wiring tests rely on them). */
export const STAGE_ACTION_TEST_IDS: Readonly<Record<StageActionId, string>> = {
  merge: 'stage-merge',
  continue_from: 'stage-continue',
  inpaint: 'stage-inpaint',
  remove_background: 'stage-remove-background',
  to_3d: 'stage-to-3d',
  retexture_with_image: 'stage-retexture-image',
  capture_2d: 'stage-capture',
  send_chat: 'stage-send-chat',
  export_board: 'stage-export-board',
  compare: 'stage-compare',
  enlarge: 'stage-enlarge',
};

/** The toolbar groups after the identity group, in display order. */
export const STAGE_ACTION_GROUPS: readonly StageActionGroup[] = ['produce', 'output', 'view'];

/** What is on the stage: a picture, a 3D model, or (with content but no version yet) the bare board. */
export function stageKindOf(object: { kind: 'image' | 'model' } | null): StageActionKind {
  return object === null ? 'board' : object.kind;
}

/** One toolbar button with its texts already looked up. */
export interface ActionView {
  id: string;
  testId: string;
  icon: string;
  label: string;
  hint: string;
  /** The "acts on" sentence, already worded ("作用于：…"). */
  target: string;
  /** The cost line for the hover hint. */
  costText: string;
  /** Legacy name: the possible-cost mark, not evidence that a selected Offering calls a model. */
  callsModel: boolean;
  group: StageActionGroup;
  /** Name of the `canvas.frontend.keyboard` setting to print beside the name. */
  shortcut?: string;
}

/** A button for an id the code has a handler for. */
export type StageActionView = ActionView & { id: StageActionId };

const COST_KEY_PREFIX = 'ui.studio.hint.cost.';

/** The Schema message that words a cost hint. */
export function costMessageKey(hint: StageActionCostHint): string {
  return `${COST_KEY_PREFIX}${hint}`;
}

function targetText(schema: ProductSchema, key: string): string {
  return formatSchemaText(schemaText(schema, 'ui.studio.hint.target'), { target: schemaText(schema, key) });
}

export function actionView(schema: ProductSchema, action: StageActionSchema & { id: StageActionId }): StageActionView {
  return {
    id: action.id,
    testId: STAGE_ACTION_TEST_IDS[action.id],
    icon: action.icon,
    label: schemaText(schema, action.label_key),
    hint: schemaText(schema, action.hint_key),
    target: targetText(schema, action.target_key),
    costText: schemaText(schema, costMessageKey(action.cost_hint)),
    callsModel: action.calls_model,
    group: action.group,
    ...(action.shortcut ? { shortcut: action.shortcut } : {}),
  };
}

/**
 * The buttons for what is on the stage, in the Schema's order. An entry the code has no handler for is
 * left out. `group` narrows to one toolbar group.
 */
export function stageActionViews(schema: ProductSchema, kind: StageActionKind, group?: StageActionGroup): StageActionView[] {
  const views: StageActionView[] = [];
  for (const action of schema.canvas.frontend.stage_actions) {
    if (!isStageActionId(action.id) || !action.kinds.includes(kind)) continue;
    if (group !== undefined && action.group !== group) continue;
    views.push(actionView(schema, { ...action, id: action.id }));
  }
  return views;
}

/**
 * A 3D processing step. Steps are not in the stage-actions table: they follow the processing capabilities
 * that currently have an Offering, so a step is offered here and in the processing panel or in neither.
 * Clicking one only opens the processing panel with the step ticked. The chosen Offering determines
 * execution and cost there; a capability may offer both free local and paid upstream processing.
 */
export function processStepView(schema: ProductSchema, capability: CapabilitySchema): ActionView {
  return {
    id: capability.id,
    testId: `stage-process-${capability.id}`,
    icon: capability.icon ?? '',
    label: capability.label_cn,
    hint: schemaText(schema, `ui.studio.stage.action.process_hint.${capability.id}`),
    target: targetText(schema, 'ui.studio.stage.action.process_target'),
    costText: schemaText(schema, costMessageKey(schema.canvas.frontend.processing_action.cost_hint)),
    callsModel: schema.canvas.frontend.processing_action.calls_model,
    group: schema.canvas.frontend.processing_action.group,
  };
}

/** The same button acting on something other than its usual target (a three-view group's front picture). */
export function withTarget<T extends ActionView>(schema: ProductSchema, view: T, target: string): T {
  return { ...view, target: formatSchemaText(schemaText(schema, 'ui.studio.hint.target'), { target }) };
}

/** Key caps of a keyboard setting ("Space" prints as 空格), or undefined when the setting is not a key. */
export function shortcutCaps(schema: ProductSchema, setting: string | undefined, platform?: PlatformInfo): string[] | undefined {
  if (!setting) return undefined;
  const value = (schema.canvas.frontend.keyboard as unknown as Record<string, unknown>)[setting];
  if (typeof value !== 'string' || value === '') return undefined;
  return descriptorCaps(value, (key) => schemaText(schema, key), platform);
}

/** What the hover hint of a button says; `reason` is why it is disabled right now, when known. */
export function hintContentOf(schema: ProductSchema, view: ActionView, options: { reason?: string; platform?: PlatformInfo } = {}): HintContent {
  const caps = shortcutCaps(schema, view.shortcut, options.platform);
  return {
    name: view.label,
    ...(caps ? { shortcut: caps } : {}),
    ...(options.reason ? { reason: options.reason } : {}),
    hint: view.hint,
    target: view.target,
    cost: { text: view.costText, ai: view.callsModel },
  };
}

/**
 * How early a label shows beside its icon, by its length: the toolbar of a stage that is not wide shows
 * icons only, a wider one the short labels, a very wide one every label. The ✦ mark follows the label.
 */
export type LabelBreakpoint = '5xl' | '6xl' | '7xl';

export function labelBreakpoint(label: string): LabelBreakpoint {
  const length = Array.from(label).length;
  if (length <= 5) return '5xl';
  if (length <= 8) return '6xl';
  return '7xl';
}
