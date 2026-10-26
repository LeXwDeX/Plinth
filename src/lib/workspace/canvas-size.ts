/**
 * How the 2D model's output size and the canvas pixel size relate (#246). The canvas size is saved with
 * the canvas and is its own truth; the output-size settings are the model's. A size the person picks in the
 * 参数 panel resizes the canvas; switching models never does: the new model's settings are aligned to the
 * canvas when one of its options produces exactly that size, and otherwise the mismatch is shown.
 * A ratio-only setting ("16:9") is read through the Schema's ratio_pixels table, or through ratio_pixel_tiers
 * when the Offering also has a resolution tier parameter (Gemini image_size 1K/2K/4K).
 */

import type { ImageSizeConstraintWire, OfferingParameterWire, OfferingWire } from '@/lib/agent-offerings';
import { parameterConditionMatches, type CanvasProductSchema } from '@/lib/product-schema';
import type { ParamSchema } from '@/lib/providers/types';
import { clampCanvasPixels, samePixels, type CanvasPixels } from '@/lib/canvas/artboard';
import { deliverySize, effectiveOfferingValues } from '@/lib/workspace/generation-params';

type ArtboardSchema = CanvasProductSchema['frontend']['artboard'];
/** The Schema tables that turn a ratio into pixels. */
export type RatioTables = Pick<ArtboardSchema, 'ratio_pixels' | 'ratio_pixel_tiers'>;

/**
 * The pixels these output-size settings ask for: the size itself, or its ratio through the tier table of a
 * tier parameter set in `values` (image_size: "4K"), else through ratio_pixels.
 */
export function outputPixels(
  sizeParams: readonly ParamSchema[],
  values: Record<string, unknown>,
  tables: RatioTables,
): CanvasPixels | null {
  const size = deliverySize(sizeParams, values);
  if (size.pixels) return size.pixels;
  if (!size.ratio) return null;
  for (const [parameter, tiers] of Object.entries(tables.ratio_pixel_tiers ?? {})) {
    const tier = values[parameter];
    const tiered = tier === undefined || tier === null ? undefined : tiers[String(tier)]?.[size.ratio];
    if (tiered) return { width: tiered.width, height: tiered.height };
  }
  const mapped = tables.ratio_pixels[size.ratio];
  return mapped ? { width: mapped.width, height: mapped.height } : null;
}

/** The size a canvas starts with: what the chosen 2D model would produce, else the Schema's default. */
export function initialCanvasPixels(
  artboard: ArtboardSchema,
  sizeParams: readonly ParamSchema[],
  values: Record<string, unknown>,
): CanvasPixels {
  return outputPixels(sizeParams, values, artboard) ?? { width: artboard.default_width, height: artboard.default_height };
}

/**
 * The pixels the model produces once `name` is set to `value`, with the Offering's fixed and forced
 * values applied (Seedream turns a 2K choice into its 4K size, for example).
 */
export function pixelsForChoice(
  offering: OfferingWire | undefined,
  sizeParams: readonly ParamSchema[],
  entered: Record<string, unknown>,
  name: string,
  value: unknown,
  tables: RatioTables,
): CanvasPixels | null {
  const values = offering ? effectiveOfferingValues(offering, { ...entered, [name]: value }) : { ...entered, [name]: value };
  return outputPixels(sizeParams, values, tables);
}

export type CanvasSizeFit =
  /** No 2D model, or one without output-size settings: nothing to compare. */
  | { kind: 'none' }
  /** The settings produce exactly the canvas size. */
  | { kind: 'match' }
  /** Different now, but these settings of the same model produce the canvas size. */
  | { kind: 'align'; settings: Record<string, string>; output: CanvasPixels | null }
  /** No option of this model produces the canvas size; `output` is what it would produce. */
  | { kind: 'conflict'; output: CanvasPixels | null };

/** How the current model's output size relates to the canvas size. */
export function canvasSizeFit(
  offering: OfferingWire | undefined,
  sizeParams: readonly ParamSchema[],
  values: Record<string, unknown>,
  canvas: CanvasPixels,
  tables: RatioTables,
): CanvasSizeFit {
  if (!offering || sizeParams.length === 0) return { kind: 'none' };
  const output = outputPixels(sizeParams, values, tables);
  if (samePixels(output, canvas)) return { kind: 'match' };
  // An option named after the canvas size wins over one the Offering forces to it.
  const named = `${canvas.width}x${canvas.height}`;
  for (const param of sizeParams) {
    const options = [...(param.options ?? [])].sort((left, right) => Number(right.value === named) - Number(left.value === named));
    for (const option of options) {
      if (samePixels(pixelsForChoice(offering, sizeParams, values, param.name, option.value, tables), canvas)) {
        return { kind: 'align', settings: { [param.name]: option.value }, output };
      }
    }
  }
  return { kind: 'conflict', output };
}

/*
 * #272/#284: on a turn whose source is the artboard, the server sets the output size from the canvas, not from
 * the size remembered for the session (internal/chat/generationtool/canvas_size.go). The functions below are the
 * same rule over the same Schema declarations (options, image_size, forced_when), so the notice says the size
 * that is really sent. The shared cases in internal/chat/generationtool/testdata/canvas_turn_sizes.json run
 * against both implementations.
 */

/** How close two aspect ratios must be (as a log ratio) to count as the same shape (server: sameRatioTolerance). */
const SAME_RATIO_TOLERANCE = 1e-3;

/** One output-size value read as pixels ("2048x1536") or as a ratio alone ("4:3"; width and height are 0). */
interface SizeValue {
  value: string;
  width: number;
  height: number;
  ratio: number;
}

const POSITIVE_INTEGER = /^\d+$/;
const POSITIVE_DECIMAL = /^\d+(?:\.\d+)?$/;

function parseSizeValue(value: string): SizeValue | null {
  const text = value.trim();
  const pixels = text.toLowerCase().replaceAll('×', 'x');
  const cross = pixels.indexOf('x');
  if (cross >= 0) {
    const [left, right] = [pixels.slice(0, cross), pixels.slice(cross + 1)];
    if (!POSITIVE_INTEGER.test(left) || !POSITIVE_INTEGER.test(right)) return null;
    const width = Number(left);
    const height = Number(right);
    return width > 0 && height > 0 ? { value, width, height, ratio: width / height } : null;
  }
  const colon = text.indexOf(':');
  if (colon < 0) return null;
  const [left, right] = [text.slice(0, colon), text.slice(colon + 1)];
  if (!POSITIVE_DECIMAL.test(left) || !POSITIVE_DECIMAL.test(right)) return null;
  const width = Number(left);
  const height = Number(right);
  return width > 0 && height > 0 ? { value, width: 0, height: 0, ratio: width / height } : null;
}

function sameShape(ratio: number, canvas: CanvasPixels): boolean {
  return Math.abs(Math.log(ratio * canvas.height / canvas.width)) < SAME_RATIO_TOLERANCE;
}

/** The size produces the canvas: the same pixels, or for a ratio-only value the same shape. */
function producesCanvas(size: SizeValue, canvas: CanvasPixels): boolean {
  return size.width > 0 ? size.width === canvas.width && size.height === canvas.height : sameShape(size.ratio, canvas);
}

/**
 * The size still shows the artboard as it is: the canvas size, a ratio-only value of its shape, or a larger size
 * of the same shape (an Offering that forces its highest resolution). A smaller size of the same shape (a canvas
 * above the model's maximum) is the nearest legal size. Same rule as the server's size note (followsCanvas).
 */
function followsCanvas(size: SizeValue, canvas: CanvasPixels): boolean {
  if (producesCanvas(size, canvas)) return true;
  return sameShape(size.ratio, canvas) && (size.width === 0 || size.width * size.height >= canvas.width * canvas.height);
}

/** Whether a custom width×height is within the Schema image_size bounds (server: ImageSizeConstraint.Check). */
export function imageSizeAllows(constraint: ImageSizeConstraintWire, width: number, height: number): boolean {
  if (width <= 0 || height <= 0) return false;
  const multiple = constraint.multiple ?? 0;
  if (multiple > 0 && (width % multiple !== 0 || height % multiple !== 0)) return false;
  const maxEdge = constraint.max_edge ?? 0;
  if (maxEdge > 0 && (width > maxEdge || height > maxEdge)) return false;
  if (width * height < constraint.min_pixels || width * height > constraint.max_pixels) return false;
  return width / height <= constraint.max_ratio && height / width <= constraint.max_ratio;
}

/**
 * The value of one output-size parameter that comes closest to the canvas: the canvas size itself when the
 * parameter accepts it, else the candidate of the nearest shape and then the nearest area, in declared order on
 * a tie. Candidates are the declared options and, for a parameter that takes custom sizes, the canvas size
 * snapped to its grid. Mirrors canvasSizeChoice on the server.
 */
export function canvasSizeChoice(parameter: Pick<OfferingParameterWire, 'options' | 'image_size'>, canvas: CanvasPixels): string | null {
  const { width, height } = canvas;
  let candidates: SizeValue[] = [];
  for (const option of parameter.options ?? []) {
    const candidate = parseSizeValue(String(option.value));
    if (!candidate) continue;
    if (candidate.width > 0 && producesCanvas(candidate, canvas)) return String(option.value);
    candidates.push(candidate);
  }
  const constraint = parameter.image_size;
  if (constraint) {
    if (imageSizeAllows(constraint, width, height)) return `${width}x${height}`;
    const multiple = constraint.multiple ?? 0;
    if (multiple > 0) {
      const snappedWidth = Math.max(multiple, Math.round(width / multiple) * multiple);
      const snappedHeight = Math.max(multiple, Math.round(height / multiple) * multiple);
      if (imageSizeAllows(constraint, snappedWidth, snappedHeight)) {
        candidates = [{ value: `${snappedWidth}x${snappedHeight}`, width: snappedWidth, height: snappedHeight, ratio: snappedWidth / snappedHeight }, ...candidates];
      }
    }
  }
  const canvasRatio = width / height;
  const canvasArea = width * height;
  let best = '';
  let bestShape = Infinity;
  let bestArea = Infinity;
  for (const candidate of candidates) {
    const shape = Math.abs(Math.log(candidate.ratio / canvasRatio));
    const area = candidate.width > 0 ? Math.abs(Math.log(candidate.width * candidate.height / canvasArea)) : 0;
    if (shape < bestShape - SAME_RATIO_TOLERANCE || (Math.abs(shape - bestShape) <= SAME_RATIO_TOLERANCE && area < bestArea)) {
      best = candidate.value;
      bestShape = shape;
      bestArea = area;
    }
  }
  return best === '' ? null : best;
}

/**
 * The output-size parameters the canvas sets on such a turn: the ones a person may choose (server:
 * Offering.UserParameterAllowed). A fixed, hidden or read-only size, or one with an adjustment policy, is the
 * Offering's own.
 */
export function canvasSizeParameters(offering: OfferingWire): OfferingParameterWire[] {
  return (offering.parameters ?? []).filter((wire) => {
    if (wire.semantic !== 'output-size' || wire.fixed === true || wire.adjustment_policy) return false;
    if (wire.name === offering.extra_credit_consent_parameter) return false;
    const surface = wire.presentation?.surface;
    return surface !== 'hidden' && surface !== 'readonly' && wire.type !== 'task' && wire.type !== 'asset' && wire.type !== 'asset-array';
  });
}

export interface CanvasTurnOutput {
  /** What the server sends for the output size ("2048x2048", "4:3"), after the Offering's forced values. */
  value: string;
  /** Its pixels: the value itself, or a ratio through the Schema ratio tables; null when neither says. */
  pixels: CanvasPixels | null;
  /** The value names a ratio only ("4:3"); its pixels, if any, come from the Schema tables. */
  ratioOnly: boolean;
  /** The model cannot produce the canvas size or shape, so the nearest legal size is used instead. */
  nearest: boolean;
}

/**
 * The output size a turn whose source is the artboard really sends (#272/#284): each output-size parameter the
 * person may choose is set to its canvasSizeChoice, then the Offering's forced_when applies (Seedream turns
 * 2048x2048 into 4096x4096). Like the server's size note, a larger size of the same shape still follows the
 * canvas; a smaller one or another shape is the nearest legal size. Null when the Offering has no such parameter.
 */
export function canvasTurnOutput(
  offering: OfferingWire | undefined,
  values: Record<string, unknown>,
  canvas: CanvasPixels,
  tables: RatioTables,
): CanvasTurnOutput | null {
  if (!offering || canvas.width <= 0 || canvas.height <= 0) return null;
  const parameters = canvasSizeParameters(offering);
  const input: Record<string, unknown> = { ...values };
  for (const parameter of parameters) {
    const choice = canvasSizeChoice(parameter, canvas);
    if (choice !== null) input[parameter.name] = choice;
  }
  for (const wire of offering.parameters ?? []) {
    for (const rule of wire.forced_when ?? []) {
      if (parameterConditionMatches(rule.when, input)) { input[wire.name] = rule.value; break; }
    }
  }
  for (const parameter of parameters) {
    const text = typeof input[parameter.name] === 'string' ? String(input[parameter.name]) : '';
    const size = parseSizeValue(text);
    if (!size) continue;
    const pixels = size.width > 0
      ? { width: size.width, height: size.height }
      : outputPixels([{ name: parameter.name, label: parameter.label, type: 'select' }], input, tables);
    return { value: text, pixels, ratioOnly: size.width === 0, nearest: !followsCanvas(size, canvas) };
  }
  return null;
}

export interface ReferenceTurnOutput extends CanvasTurnOutput {
  /** The size differs from what the remembered settings (here: the defaults) would send, so it is worth saying. */
  changed: boolean;
}

/** How a person's or the Agent's choice of a parameter is recorded for the session (#213, #246). */
type ParameterStates = Record<string, { source: 'default' | 'agent' | 'user' } | undefined>;

/**
 * #296: the output size a chat edit of one picture sends when nobody chose a size. Outside a canvas turn the server
 * (internal/chat/generationtool/reference_size.go) gives a tool that declares reference_size "single" and sends one
 * reference the legal size closest to that picture, by the same rule as the canvas (canvasTurnOutput over the
 * picture's pixels). A size the person or the Agent chose for the session wins, so there is nothing to say then
 * (null). For a reference, any size of its shape keeps it (a large photo gets a smaller legal size of the same
 * shape); `nearest` means another shape.
 */
export function referenceTurnOutput(
  offering: OfferingWire | undefined,
  values: Record<string, unknown>,
  states: ParameterStates | undefined,
  reference: CanvasPixels,
  tables: RatioTables,
): ReferenceTurnOutput | null {
  if (!offering) return null;
  const parameters = canvasSizeParameters(offering);
  if (parameters.some((parameter) => {
    const source = states?.[parameter.name]?.source;
    return source === 'user' || source === 'agent';
  })) return null;
  const turn = canvasTurnOutput(offering, values, reference, tables);
  if (!turn) return null;
  const size = parseSizeValue(turn.value);
  const changed = !parameters.some((parameter) => String(values[parameter.name] ?? '') === turn.value);
  return { ...turn, nearest: !size || !sameShape(size.ratio, reference), changed };
}

/**
 * The canvas size a 2D size choice in the 参数 panel asks for (#246): the choice's effective pixels, in the
 * Schema's canvas range. Null when the canvas has no size yet (it starts from the choice anyway) or the
 * choice names no pixels.
 */
export function canvasResizeForChoice(
  canvasKnown: boolean,
  offering: OfferingWire | undefined,
  sizeParams: readonly ParamSchema[],
  entered: Record<string, unknown>,
  name: string,
  value: unknown,
  artboard: RatioTables,
  limits: Pick<CanvasProductSchema['limits'], 'minimum_dimension' | 'maximum_dimension'>,
): CanvasPixels | null {
  if (!canvasKnown) return null;
  return clampCanvasPixels(pixelsForChoice(offering, sizeParams, entered, name, value, artboard), limits);
}

/**
 * The model's size settings to write after the person switched the 2D model while the board was open
 * (#246): only on a real switch within the same session, and only when the new model offers the canvas size.
 */
export function settingsAfterModelSwitch(
  seen: { sessionId: string | null; offering: string },
  current: { sessionId: string; offering: string },
  fit: CanvasSizeFit,
): Record<string, string> | null {
  const switched = seen.sessionId === current.sessionId && seen.offering !== '' && current.offering !== '' && seen.offering !== current.offering;
  return switched && fit.kind === 'align' ? fit.settings : null;
}
