/**
 * #329: a generation model that is no longer offered, and the model that replaces it (Schema
 * `runtime.frontend.retired_generation_models`). A saved or restored pick of a retired model moves to the
 * successor while the successor is offered, and the workspace says so; frozen tasks keep their model.
 */
export interface RetiredGenerationModel {
  provider_id: string;
  model_id: string;
  model_name: string;
  successor_model_id: string;
}

export interface RetiredModelSwitch {
  /** The retired model's display name. */
  from: string;
  /** The successor's model ID. */
  to: string;
  /** The successor's display name. */
  toName: string;
}

/** The switch for a picked model, or null when it is still offered, unknown, or its successor is not offered. */
export function retiredModelSwitch(
  retired: readonly RetiredGenerationModel[] | undefined,
  modelId: string | null,
  choices: ReadonlyArray<{ model_id: string; model_name: string }> | undefined,
): RetiredModelSwitch | null {
  if (!retired || !modelId || !choices) return null;
  if (choices.some((choice) => choice.model_id === modelId)) return null;
  const entry = retired.find((item) => item.model_id === modelId);
  const successor = entry && choices.find((choice) => choice.model_id === entry.successor_model_id);
  return entry && successor ? { from: entry.model_name, to: successor.model_id, toName: successor.model_name } : null;
}
