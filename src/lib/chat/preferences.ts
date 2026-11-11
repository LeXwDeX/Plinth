import { schemaText, type ProductSchema } from '@/lib/product-schema';

/** One stored choice fact of a session (#241). */
export interface ChatPreference {
  id: string;
  /** selected / rejected / signal; unknown values stay readable. */
  kind: string;
  source: string;
  /** The Agent prompt of the direction. Empty when the history no longer carries it. */
  direction: string;
  reasonPreset: string;
  reasonText: string;
  handle: string;
  taskId: string;
  assetId: string;
  /** Handles of the candidates of the same result card that were not chosen. */
  alternatives: string[];
  /** True for a person's pick/rejection, false for a weak redo signal. */
  explicit: boolean;
  active: boolean;
  userEdited: boolean;
  createdAt: string;
}

/** A weak signal the workspace reports; the server never treats it as a decision. */
export type PreferenceSignal = 'regenerate' | 'inpaint' | 'prompt_edit';

type WireObject = Record<string, unknown>;

function record(value: unknown): WireObject {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as WireObject : {};
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string' && item !== '') : [];
}

/**
 * Decode the preference records of a session response. Member names come from the public Schema; a server
 * without the preference projection or the routes yields no records, so the panel stays hidden.
 */
export function parsePreferences(schema: ProductSchema, payload: unknown): ChatPreference[] {
  const names = schema.chat.api.preference;
  const response = schema.chat.api.response_fields;
  const attachment = schema.chat.api.attachment;
  const member = response.preferences;
  if (!names || !member) return [];
  const raw = record(payload)[member];
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((item) => {
    const source = record(item);
    const id = text(source[response.id]);
    if (!id) return [];
    return [{
      id,
      kind: text(source[names.kind]),
      source: text(source[names.source]),
      direction: text(source[names.direction]),
      reasonPreset: text(source[names.reason_preset]),
      reasonText: text(source[names.reason_text]),
      handle: text(source[attachment.handle]),
      taskId: text(source[attachment.task_id]),
      assetId: text(source[attachment.asset_id]),
      alternatives: strings(source[names.alternatives]),
      explicit: source[names.explicit] === true,
      active: source[names.active] !== false,
      userEdited: source[names.user_edited] === true,
      createdAt: text(source[response.created_at]),
    }];
  });
}

/** Whether the deployment declares the preference feature at all. */
export function preferencesAvailable(schema: ProductSchema): boolean {
  return Boolean(schema.chat.preferences && schema.chat.api.preference &&
    schema.runtime.http.routes.chat_session_preferences && schema.runtime.http.routes.chat_session_preference_item);
}

/** The declared reason ids, accepting the raw server Catalog's {id,label} objects too. */
export function preferenceReasonPresetIds(schema: ProductSchema): string[] {
  return (schema.chat.preferences?.reason_presets ?? []).flatMap((preset) =>
    typeof preset === 'string' ? [preset] : preset && typeof preset.id === 'string' ? [preset.id] : []);
}

/** The localized label of one declared reason; an unknown id falls back to the id itself. */
export function preferenceReasonLabel(schema: ProductSchema, id: string): string {
  if (!id) return '';
  const declared = (schema.chat.preferences?.reason_presets ?? []).find(
    (preset): preset is { id: string; label: string } => typeof preset !== 'string' && preset.id === id,
  );
  const label = schemaText(schema, `ui.workspace.preferences.reason.${id}`);
  return label !== '' ? label : declared?.label ?? id;
}