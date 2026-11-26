/**
 * #653: the canvas is a ratio. Its saved pixels are only editing precision (and the resolution of the artboard
 * snapshot); a canvas is never migrated, its ratio is read from its pixels (canvasShapeOf). The 2D model decides the
 * output size of a canvas turn: of the sizes it may produce, the one with the most pixels at the canvas ratio
 * (canvasRatioTurnOutput); a model without that ratio takes the nearest ratio it has, and the notice says so.
 * Switching models never changes the canvas. A ratio-only setting ("16:9") is read through the Schema's
 * ratio_pixels table, or through ratio_pixel_tiers when the Offering also has a resolution tier parameter
 * (Gemini image_size 1K/2K/4K).
 */

import type { ImageSizeConstraintWire, OfferingParameterWire, OfferingWire } from '@/lib/agent-offerings';
import { parameterConditionMatches, type CanvasProductSchema } from '@/lib/product-schema';
import type { ParamSchema } from '@/lib/providers/types';
import type { CanvasPixels } from '@/lib/canvas/artboard';
import { deliverySize } from '@/lib/workspace/generation-params';

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

/** The size a new canvas starts with (#653): the Schema default, a 1:1 canvas whatever the model. */
export function initialCanvasPixels(artboard: Pick<ArtboardSchema, 'default_width' | 'default_height'>): CanvasPixels {
  return { width: artboard.default_width, height: artboard.default_height };
}

/*
 * #272/#284/#653: on a turn whose source is the artboard, the server sets the output size from the canvas, not from
 * the size remembered for the session (internal/chat/generationtool/canvas_size.go). The functions below are the
 * same rules over the same Schema declarations (options, image_size, forced_when, the ratio tables), so the notice
 * says the size that is really sent. The shared cases in internal/chat/generationtool/testdata/canvas_turn_sizes.json
 * run against both implementations. canvasRatioTurnOutput is the canvas turn's rule; canvasSizeChoice and
 * canvasTurnOutput (the legal size closest to a picture) remain for one reference picture (#296) and inpainting,
 * whose result must be put back on the canvas pixel for pixel (#308).
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

function pixelSizeValue(pixels: CanvasPixels): SizeValue {
  return { value: `${pixels.width}x${pixels.height}`, width: pixels.width, height: pixels.height, ratio: pixels.width / pixels.height };
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
  return (offering.parameters ?? []).filter((wire) => wire.semantic === 'output-size' && personMayChoose(offering, wire));
}

/** How a person's or the Agent's choice of a parameter is recorded for the session (#213, #246). */
type ParameterStates = Record<string, { source: 'default' | 'agent' | 'user' } | undefined>;

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
 * The legal output size closest to a picture of `canvas` pixels (#284/#308): each output-size parameter the person
 * may choose is set to its canvasSizeChoice, then the Offering's forced_when applies (Seedream turns 2048x2048 into
 * 4096x4096). A larger size of the same shape still follows the picture; a smaller one or another shape is the
 * nearest legal size. Inpainting reads it (its result must match the canvas pixels), and so does one reference
 * picture (referenceTurnOutput). A canvas turn uses canvasRatioTurnOutput since #653. Null when the Offering has no
 * such parameter, or (#614) when the Agent chose one of them with set_params.
 */
export function canvasTurnOutput(
  offering: OfferingWire | undefined,
  values: Record<string, unknown>,
  canvas: CanvasPixels,
  tables: RatioTables,
  states?: ParameterStates,
): CanvasTurnOutput | null {
  if (!offering || canvas.width <= 0 || canvas.height <= 0) return null;
  const parameters = canvasSizeParameters(offering);
  if (parameters.some((parameter) => states?.[parameter.name]?.source === 'agent')) return null;
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
    // A ratio is also read as the pixels the Schema maps it to: Gemini's 9:16 at 4K is 3072x5504, not exactly 9:16,
    // and on a picture of that size it still follows it (server: Executor.ratioSameShape).
    const follows = followsCanvas(size, canvas) || (size.width === 0 && pixels !== null && followsCanvas(pixelSizeValue(pixels), canvas));
    return { value: text, pixels, ratioOnly: size.width === 0, nearest: !follows };
  }
  return null;
}

/** The aspect ratio a canvas stands for (#653): `label` names it ("16:9"), `ratio` is its width over height. */
export interface CanvasShape {
  label: string;
  ratio: number;
}

function sameRatio(left: number, right: number): boolean {
  return Math.abs(Math.log(left / right)) < SAME_RATIO_TOLERANCE;
}

/** The denominator bound of the simple ratio an uncommon canvas is named by (server: ratioLabelMaxTerm). */
const RATIO_LABEL_MAX_TERM = 32;

/** The ratios the Schema names (ratio_pixels keys), sorted like the server sorts them. */
function namedRatios(tables: RatioTables): string[] {
  return Object.keys(tables.ratio_pixels ?? {}).filter((name) => parseSizeValue(name)?.width === 0).sort();
}

/** The Schema maps the ratio to exactly these pixels, in ratio_pixels or in any resolution tier. */
function ratioMapsTo(tables: RatioTables, ratio: string, canvas: CanvasPixels): boolean {
  const same = (pixels: { width: number; height: number } | undefined) => pixels !== undefined && pixels.width === canvas.width && pixels.height === canvas.height;
  if (same(tables.ratio_pixels[ratio])) return true;
  return Object.values(tables.ratio_pixel_tiers ?? {}).some((tiers) => Object.values(tiers).some((table) => same(table[ratio])));
}

/**
 * The ratio of a canvas, read from its pixels (server: canvasShapeOf): a ratio the Schema names when the canvas has
 * its shape or exactly the pixels the Schema maps it to (a canvas made for a ratio-only model before #653, such as
 * Gemini's 3072x5504 for 9:16); otherwise its own width over height, named by the simplest ratio of that shape.
 */
export function canvasShapeOf(canvas: CanvasPixels, tables: RatioTables): CanvasShape {
  const ratio = canvas.width / canvas.height;
  const names = namedRatios(tables);
  for (const name of names) {
    const named = parseSizeValue(name);
    if (named && sameRatio(named.ratio, ratio)) return { label: name, ratio: named.ratio };
  }
  for (const name of names) {
    if (ratioMapsTo(tables, name, canvas)) return { label: name, ratio: parseSizeValue(name)?.ratio ?? ratio };
  }
  for (let denominator = 1; denominator <= RATIO_LABEL_MAX_TERM; denominator += 1) {
    const numerator = Math.round(ratio * denominator);
    if (numerator >= 1 && sameRatio(numerator / denominator, ratio)) return { label: `${numerator}:${denominator}`, ratio };
  }
  let divisor = canvas.width;
  for (let rest = canvas.height; rest !== 0;) [divisor, rest] = [rest, divisor % rest];
  return { label: `${canvas.width / divisor}:${canvas.height / divisor}`, ratio };
}

/** The common ratios a canvas offers (#653): the Schema's ratio_pixels, from the tallest to the widest. */
export function canvasRatioOptions(tables: RatioTables): string[] {
  return namedRatios(tables).sort((left, right) => (parseSizeValue(left)?.ratio ?? 0) - (parseSizeValue(right)?.ratio ?? 0));
}

/**
 * The canvas pixels for a ratio the person picks (#653): the long edge stays (at least the Schema default's), so
 * content keeps its pixel size; the short edge follows the ratio, within the Schema's canvas range.
 */
export function canvasPixelsForRatio(
  ratio: string,
  current: CanvasPixels | null,
  artboard: Pick<ArtboardSchema, 'default_width' | 'default_height'>,
  limits: Pick<CanvasProductSchema['limits'], 'minimum_dimension' | 'maximum_dimension'>,
): CanvasPixels | null {
  const parts = ratio.split(':').map(Number);
  if (parts.length !== 2 || !(parts[0] > 0) || !(parts[1] > 0)) return null;
  const [width, height] = parts;
  const clamp = (value: number) => Math.min(limits.maximum_dimension, Math.max(limits.minimum_dimension, Math.round(value)));
  const long = clamp(Math.max(current ? Math.max(current.width, current.height) : 0, artboard.default_width, artboard.default_height));
  return width >= height ? { width: long, height: clamp(long * height / width) } : { width: clamp(long * width / height), height: long };
}

/** Whether a person may choose the parameter (server: Offering.UserParameterAllowed). */
function personMayChoose(offering: OfferingWire, wire: OfferingParameterWire): boolean {
  if (wire.fixed === true || wire.adjustment_policy || wire.name === offering.extra_credit_consent_parameter) return false;
  const surface = wire.presentation?.surface;
  return surface !== 'hidden' && surface !== 'readonly' && wire.type !== 'task' && wire.type !== 'asset' && wire.type !== 'asset-array';
}

/**
 * The resolution tiers a person may choose (server: canvasTierParameters): the parameters the Schema's
 * ratio_pixel_tiers covers. Gemini declares its image_size read-only at 4K, so it is the Offering's own.
 */
export function canvasTierParameters(offering: OfferingWire, tables: RatioTables): OfferingParameterWire[] {
  const tiers = tables.ratio_pixel_tiers ?? {};
  return (offering.parameters ?? []).filter((wire) => wire.name in tiers && personMayChoose(offering, wire));
}

/** The custom size with the most pixels of the ratio within the bounds; same search order as the server. */
function largestOfRatio(constraint: ImageSizeConstraintWire, ratio: number): SizeValue | null {
  if (!(ratio > 0) || !Number.isFinite(ratio) || !(constraint.max_pixels > 0)) return null;
  const step = Math.max(constraint.multiple ?? 0, 1);
  const maxEdge = constraint.max_edge ?? 0;
  const limit = maxEdge > 0 ? maxEdge : Math.ceil(Math.sqrt(constraint.max_pixels * Math.max(ratio, 1 / ratio)));
  const snap = (value: number) => Math.round(value / step) * step;
  let best: SizeValue | null = null;
  for (let multiple = Math.floor(limit / step); multiple >= 1; multiple -= 1) {
    const edge = multiple * step;
    for (const [width, height] of [[edge, snap(edge / ratio)], [snap(edge * ratio), edge]]) {
      if (width <= 0 || height <= 0 || width * height <= (best ? best.width * best.height : 0)) continue;
      if (!imageSizeAllows(constraint, width, height) || !sameRatio(width / height, ratio)) continue;
      best = pixelSizeValue({ width, height });
    }
  }
  return best;
}

function boundedRatio(constraint: ImageSizeConstraintWire, ratio: number): number {
  if (!(constraint.max_ratio > 0)) return ratio;
  return Math.min(Math.max(ratio, 1 / constraint.max_ratio), constraint.max_ratio);
}

/**
 * The value of one output-size parameter for a canvas of this shape (#653, server: canvasRatioChoice): of its options
 * and, when it takes custom sizes, the largest custom size of the canvas ratio (or of its most extreme legal ratio),
 * the candidates of the canvas ratio, else those of the nearest ratio; among them the one with the most pixels (a
 * ratio-only value counts none), the first in declared order on a tie.
 */
export function canvasRatioChoice(parameter: Pick<OfferingParameterWire, 'options' | 'image_size'>, shape: CanvasShape): string | null {
  const candidates: SizeValue[] = [];
  for (const option of parameter.options ?? []) {
    const candidate = parseSizeValue(String(option.value));
    if (candidate) candidates.push(candidate);
  }
  const constraint = parameter.image_size;
  if (constraint) {
    const custom = largestOfRatio(constraint, shape.ratio);
    if (custom) candidates.push(custom);
    else {
      const bounded = boundedRatio(constraint, shape.ratio);
      const extreme = bounded !== shape.ratio ? largestOfRatio(constraint, bounded) : null;
      if (extreme) candidates.push(extreme);
    }
  }
  let nearest = Infinity;
  for (const candidate of candidates) nearest = Math.min(nearest, Math.abs(Math.log(candidate.ratio / shape.ratio)));
  let best = '';
  let bestArea = -1;
  for (const candidate of candidates) {
    const distance = Math.abs(Math.log(candidate.ratio / shape.ratio));
    // The canvas ratio when there is one; else the nearest ratio.
    const eligible = distance < SAME_RATIO_TOLERANCE || (nearest >= SAME_RATIO_TOLERANCE && distance <= nearest + SAME_RATIO_TOLERANCE);
    if (!eligible) continue;
    const area = candidate.width * candidate.height;
    if (area > bestArea) { best = candidate.value; bestArea = area; }
  }
  return best === '' ? null : best;
}

/**
 * The values the canvas sets on a canvas turn (#653, server: canvasRatioValues): each output-size parameter a person
 * may choose gets canvasRatioChoice; each resolution tier a person may choose, the option whose pixels for the chosen
 * ratio are the most.
 */
export function canvasRatioValues(offering: OfferingWire, canvas: CanvasPixels, tables: RatioTables): Record<string, string> {
  const shape = canvasShapeOf(canvas, tables);
  const tiers = tables.ratio_pixel_tiers ?? {};
  const values: Record<string, string> = {};
  let ratio = '';
  for (const parameter of canvasSizeParameters(offering)) {
    if (parameter.name in tiers) continue;
    const choice = canvasRatioChoice(parameter, shape);
    if (choice === null) continue;
    values[parameter.name] = choice;
    if (parseSizeValue(choice)?.width === 0 && ratio === '') ratio = choice;
  }
  for (const parameter of canvasTierParameters(offering, tables)) {
    let best = '';
    let bestArea = 0;
    for (const option of parameter.options ?? []) {
      const pixels = tiers[parameter.name]?.[String(option.value)]?.[ratio];
      const area = pixels ? pixels.width * pixels.height : 0;
      if (area > bestArea) { best = String(option.value); bestArea = area; }
    }
    if (best !== '') values[parameter.name] = best;
  }
  return values;
}

export interface CanvasRatioTurnOutput extends CanvasTurnOutput {
  /** The canvas ratio ("16:9"). */
  ratio: string;
  /** The ratio of the size sent: the canvas ratio, or the nearest one the model has (`nearest`). */
  outputRatio: string;
}

/**
 * The output size a turn whose source is the artboard really sends (#653): canvasRatioValues, then the Offering's
 * forced_when. `nearest` means the model has no size of the canvas ratio. Null when the Offering has no output size a
 * person may choose, or (#614) when the Agent chose one of them with set_params: the server then carries out the
 * Agent's size instead of the canvas's (applyCanvasOutputSize), so the canvas sets nothing.
 */
export function canvasRatioTurnOutput(
  offering: OfferingWire | undefined,
  values: Record<string, unknown>,
  canvas: CanvasPixels,
  tables: RatioTables,
  states?: ParameterStates,
): CanvasRatioTurnOutput | null {
  if (!offering || canvas.width <= 0 || canvas.height <= 0) return null;
  const parameters = canvasSizeParameters(offering);
  if ([...parameters, ...canvasTierParameters(offering, tables)].some((parameter) => states?.[parameter.name]?.source === 'agent')) return null;
  const input: Record<string, unknown> = { ...values, ...canvasRatioValues(offering, canvas, tables) };
  for (const wire of offering.parameters ?? []) {
    for (const rule of wire.forced_when ?? []) {
      if (parameterConditionMatches(rule.when, input)) { input[wire.name] = rule.value; break; }
    }
  }
  const shape = canvasShapeOf(canvas, tables);
  for (const parameter of parameters) {
    const text = typeof input[parameter.name] === 'string' ? String(input[parameter.name]) : '';
    const size = parseSizeValue(text);
    if (!size) continue;
    const pixels = size.width > 0
      ? { width: size.width, height: size.height }
      : outputPixels([{ name: parameter.name, label: parameter.label, type: 'select' }], input, tables);
    const nearest = !sameRatio(size.ratio, shape.ratio);
    const outputRatio = !nearest ? shape.label : size.width === 0 ? text : canvasShapeOf({ width: size.width, height: size.height }, tables).label;
    return { value: text, pixels, ratioOnly: size.width === 0, nearest, ratio: shape.label, outputRatio };
  }
  return null;
}

export interface ReferenceTurnOutput extends CanvasTurnOutput {
  /** The size differs from what the remembered settings (here: the defaults) would send, so it is worth saying. */
  changed: boolean;
}

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
  const shapeKept = size !== null && (sameShape(size.ratio, reference) || (turn.pixels !== null && sameShape(turn.pixels.width / turn.pixels.height, reference)));
  return { ...turn, nearest: !shapeKept, changed };
}
