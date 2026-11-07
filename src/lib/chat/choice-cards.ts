import { NO_CREDITS, type OfferingWire } from '@/lib/agent-offerings';
import { formatNativePrice, parseNativeQuote, type NativeQuote } from '@/lib/pricing';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { offeringQuoteText } from '@/lib/workspace/processing-cost';

/**
 * In-chat choice cards (#321). A tool that would otherwise pick a service for
 * the person stores a card with the session; the page lists the session's
 * cards, shows each in the conversation and sends the answer with the next
 * turn. The card carries public facts only (Offering IDs, names, quotes); the
 * call the answer leads to stays on the server.
 */

export type ChoiceState = 'pending' | 'answered' | 'superseded';

export interface ChoiceOptionModel {
  id: string;
  providerName: string;
  modelName: string;
  /** #103: the service's own name for this choice; empty on older servers. */
  label: string;
  /** #103: one sentence on what the choice is for; empty on older servers. */
  description: string;
  /** #103: the server's recommendation for this request. */
  recommended: boolean;
  creditsMetered: boolean;
  estimatedCredits: number | null;
  nativeQuote: NativeQuote | null;
}

export interface ChoiceCardAnswer {
  optionId: string;
  useDefault: boolean;
  source: string;
}

export interface ChoiceCardModel {
  id: string;
  assistantMessageId: string;
  tool: string;
  /** #103: the generic choice kind (for example usage_profile); empty on a provider-choice card. */
  kind: string;
  /** The Schema-declared question of a generic card; empty on older servers and provider-choice cards. */
  question: string;
  /**
   * The step a provider-choice card chooses for (#321). A generic card (#103)
   * stores its choice kind here for the server-side continuation, so this is
   * not a capability on that card; see choiceCardOfferingCapability.
   */
  capability: string;
  options: ChoiceOptionModel[];
  defaultOptionId: string;
  allowDefault: boolean;
  state: ChoiceState;
  answer: ChoiceCardAnswer | null;
  createdAt: string;
}

/** What the person picked on a card: one option, or the default. */
export type ChoiceSelection = { optionId: string; useDefault?: false } | { useDefault: true; optionId?: undefined };

export function choicesRoute(schema: ProductSchema, sessionId: string): string | null {
  const template = schema.runtime.http.routes.chat_session_choices;
  return template ? template.replace('{sessionID}', encodeURIComponent(sessionId)) : null;
}

const record = (value: unknown): Record<string, unknown> | null =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null;
const text = (value: unknown): string => (typeof value === 'string' ? value : '');

function parseOption(value: unknown): ChoiceOptionModel | null {
  const item = record(value);
  if (!item || !text(item.id)) return null;
  return {
    id: text(item.id),
    providerName: text(item.provider_name),
    modelName: text(item.model_name),
    label: text(item.label),
    description: text(item.description),
    recommended: item.recommended === true,
    creditsMetered: item.credits_metered === true,
    estimatedCredits: typeof item.estimated_credits === 'number' ? item.estimated_credits : null,
    nativeQuote: parseNativeQuote(item.native_quote),
  };
}

function parseCard(value: unknown): ChoiceCardModel | null {
  const item = record(value);
  if (!item || !text(item.id)) return null;
  const options = (Array.isArray(item.options) ? item.options : []).map(parseOption).filter((option): option is ChoiceOptionModel => option !== null);
  if (options.length === 0) return null;
  const state = item.state === 'answered' || item.state === 'superseded' ? item.state : 'pending';
  const answer = record(item.answer);
  return {
    id: text(item.id),
    assistantMessageId: text(item.assistant_message_id),
    tool: text(item.tool),
    kind: text(item.kind),
    question: text(item.question),
    capability: text(item.capability),
    options,
    defaultOptionId: text(item.default_option_id),
    allowDefault: item.allow_default === true,
    state,
    answer: state === 'answered' && answer && text(answer.option_id)
      ? { optionId: text(answer.option_id), useDefault: answer.use_default === true, source: text(answer.source) }
      : null,
    createdAt: text(item.created_at),
  };
}

/**
 * The Offering capability whose prices a card asks for, or null when it has
 * none. A generic card (#103) stores its choice kind in `capability`; fetching
 * it as an Offering capability sends an invalid name and the browser logs the
 * 400. An unknown capability is left alone too, matching the public Schema.
 */
export function choiceCardOfferingCapability(
  schema: ProductSchema,
  card: Pick<ChoiceCardModel, 'kind' | 'capability'>,
): string | null {
  if (card.kind) return null;
  return schema.capabilities.some((item) => item.id === card.capability) ? card.capability : null;
}

/** The cards of a list response; a malformed card is left out. */
export function parseChoiceCards(payload: unknown): ChoiceCardModel[] {
  const choices = record(payload)?.choices;
  return (Array.isArray(choices) ? choices : []).map(parseCard).filter((card): card is ChoiceCardModel => card !== null);
}

export async function fetchChoiceCards(
  schema: ProductSchema,
  sessionId: string,
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch,
): Promise<ChoiceCardModel[]> {
  const route = choicesRoute(schema, sessionId);
  if (!route) return [];
  const response = await fetcher(route, { signal });
  if (!response.ok) throw new Error(`choices ${response.status}`);
  return parseChoiceCards(await response.json().catch(() => null));
}

const liveOffering = (option: ChoiceOptionModel, offerings: readonly OfferingWire[] | undefined) =>
  offerings?.find((offering) => offering.id === option.id);

/** Provider and model, in the reader's language when the public Offering list has them. */
export function choiceOptionTitle(option: ChoiceOptionModel, offerings?: readonly OfferingWire[]): string {
  if (option.label) return option.label;
  const live = liveOffering(option, offerings);
  return [live?.provider_name || option.providerName, live?.model_name || option.modelName].filter(Boolean).join(' · ');
}

/** #103: whether the card carries the rich option fields and should render as option tiles. */
export function choiceCardHasDetails(card: Pick<ChoiceCardModel, 'options'>): boolean {
  return card.options.some((option) => option.label !== '' || option.description !== '' || option.recommended);
}

/** The option's Schema quote: the live Offering's, else the one stored with the card. */
export function choiceOptionQuote(schema: ProductSchema, option: ChoiceOptionModel, offerings?: readonly OfferingWire[]): string {
  const live = liveOffering(option, offerings);
  if (live) return offeringQuoteText(schema, live);
  if (option.nativeQuote) {
    const amount = formatNativePrice(option.nativeQuote.total_amount, option.nativeQuote.unit_label) ?? schemaText(schema, 'ui.pricing.unknown');
    return formatSchemaText(schemaText(schema, 'ui.pricing.default_estimate'), { amount });
  }
  const credits = schemaText(schema, 'ui.workspace.harness.credits');
  if (!option.creditsMetered) return `${credits}: ${NO_CREDITS}`;
  return option.estimatedCredits != null ? `${credits}: ${option.estimatedCredits}` : schemaText(schema, 'ui.workspace.harness.credits_unknown');
}

const optionOf = (card: ChoiceCardModel, id: string) => card.options.find((option) => option.id === id);

/** The words of the user message that answers the card. */
export function choiceAnswerText(schema: ProductSchema, card: ChoiceCardModel, selection: ChoiceSelection, offerings?: readonly OfferingWire[]): string {
  const option = optionOf(card, selection.useDefault ? card.defaultOptionId : selection.optionId ?? '');
  const title = option ? choiceOptionTitle(option, offerings) : '';
  return formatSchemaText(schemaText(schema, selection.useDefault ? 'ui.chat.choice.answer_default_text' : 'ui.chat.choice.answer_text'), { title });
}

/** What a closed card says; '' while it is open. */
export function choiceStatusText(schema: ProductSchema, card: ChoiceCardModel, offerings?: readonly OfferingWire[]): string {
  if (card.state === 'superseded') return schemaText(schema, 'ui.chat.choice.superseded');
  if (card.state !== 'answered' || !card.answer) return '';
  const option = optionOf(card, card.answer.optionId);
  const title = option ? choiceOptionTitle(option, offerings) : card.answer.optionId;
  return formatSchemaText(schemaText(schema, card.answer.useDefault ? 'ui.chat.choice.answered_default' : 'ui.chat.choice.answered'), { title });
}

/** The option a key moves to in a single-choice list (wrapping), or null for other keys. */
export function radioKeyTarget(key: string, index: number, count: number): number | null {
  if (count <= 0) return null;
  switch (key) {
    case 'ArrowDown':
    case 'ArrowRight':
      return (index + 1) % count;
    case 'ArrowUp':
    case 'ArrowLeft':
      return (index - 1 + count) % count;
    case 'Home':
      return 0;
    case 'End':
      return count - 1;
    default:
      return null;
  }
}

/**
 * What becomes of an answer held because 确认 was pressed while the composer's pictures were still being
 * read or uploaded (#335): it waits for them (and for a running turn), goes out once they are done, and is
 * dropped when the person has left the session or the card is no longer open.
 */
export function heldChoiceRelease(state: { sameScope: boolean; cardOpen: boolean; attachmentsBusy: boolean; streaming: boolean }): 'wait' | 'send' | 'drop' {
  if (!state.sameScope || !state.cardOpen) return 'drop';
  return state.attachmentsBusy || state.streaming ? 'wait' : 'send';
}

/**
 * Where the cards go in the conversation: after the assistant message that
 * asked. An open card whose message is not on the page (not loaded yet, or
 * still streaming) is shown at the end so it can still be answered.
 */
export function choiceCardPlacement(cards: readonly ChoiceCardModel[], shownMessageIds: readonly string[]) {
  const shown = new Set(shownMessageIds);
  const byMessage = new Map<string, ChoiceCardModel[]>();
  const loose: ChoiceCardModel[] = [];
  for (const card of [...cards].sort((left, right) => left.createdAt.localeCompare(right.createdAt))) {
    if (shown.has(card.assistantMessageId)) byMessage.set(card.assistantMessageId, [...(byMessage.get(card.assistantMessageId) ?? []), card]);
    else if (card.state === 'pending') loose.push(card);
  }
  return { byMessage, loose };
}
