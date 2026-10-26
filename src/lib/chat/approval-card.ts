import { offeringShowsCredits, type OfferingWire } from '@/lib/agent-offerings';
import { parseParameterAdjustments, type ParameterAdjustment, type PendingApproval, type PendingApprovalInput } from '@/lib/chat/pending-approvals';
import { parseNativeQuote, type NativeQuote, type PriceTotal } from '@/lib/pricing';

/**
 * 对话里的批准卡片只有一个形状，来源有两个：本轮还在等待的同意请求（轮次
 * 进行中），和已脱离轮次、由服务端保存 24 小时的待批准步骤。
 */
export interface ApprovalCardInput {
  /** The Offering parameter the input fills ("source_asset"); names the input when nothing else does (#324). */
  parameter: string;
  handle: string;
  taskId: string;
  assetId: string;
  /** 'task' is the earlier task (a 3D model) the step works on; 'asset' is a picture it sends. */
  kind: 'asset' | 'task' | '';
  /** What the input is in the session's own words ("正面", "2D v3"); empty when unknown. */
  label: string;
}

export interface ApprovalCardExtra {
  credits: number;
  baseCredits: number;
  label: string;
  items: string[];
}

export interface ApprovalCardModel {
  requestId: string;
  source: 'turn' | 'detached';
  status: 'pending' | 'expired';
  capability: string;
  model: string;
  provider: string;
  /** The provider's account key, for its balance; empty when the source does not say. */
  providerId: string;
  priceMode?: string;
  nativeQuote?: NativeQuote | null;
  credits: number | null;
  /** The extra credits the step also spends, what they pay for and the price without them; absent when there are none. */
  extra: ApprovalCardExtra | null;
  parameterAdjustments?: ParameterAdjustment[];
  prompt: string;
  /** The earlier same-kind task that was charged but whose result was not stored (#294); the card says approving charges again. */
  rechargeTaskId?: string;
  /** The server's sentence that the step uses only the first of the pictures given (#329 E); the inputs list only those. */
  referenceNote?: string;
  inputs: ApprovalCardInput[];
  /** 处理类步骤要处理的模型任务。 */
  sourceTaskId: string;
  expiresAt: string;
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

export function cardFromPending(
  item: PendingApproval,
  labelOf: (input: PendingApprovalInput) => string = () => '',
): ApprovalCardModel {
  return {
    requestId: item.requestId, source: 'detached', status: item.status,
    capability: item.capability, model: item.model, provider: item.provider, providerId: item.providerId,
    priceMode: item.priceMode, nativeQuote: item.nativeQuote,
    credits: item.estimatedCredits > 0 ? item.estimatedCredits : null,
    extra: item.extraCredits > 0
      ? { credits: item.extraCredits, baseCredits: item.baseCredits, label: item.extraLabel, items: item.extraItems }
      : null,
    parameterAdjustments: item.parameterAdjustments,
    prompt: item.prompt,
    ...(item.rechargeTaskId ? { rechargeTaskId: item.rechargeTaskId } : {}),
    ...(item.referenceNote ? { referenceNote: item.referenceNote } : {}),
    inputs: item.inputs.map((input) => ({
      parameter: input.parameter, handle: input.handle, taskId: input.taskId, assetId: input.assetId,
      kind: input.kind === 'task' ? 'task' : input.kind === 'asset' ? 'asset' : '',
      label: labelOf(input),
    })),
    sourceTaskId: '', expiresAt: item.expiresAt,
  };
}

/** The in-turn consent the harness is waiting on (free-form record from the server). */
export function cardFromConsent(pending: Record<string, unknown> | null, requestId: string | null): ApprovalCardModel | null {
  if (!pending || !requestId) return null;
  const refs = Array.isArray(pending.reference_assets) ? pending.reference_assets : [];
  return {
    requestId, source: 'turn', status: 'pending',
    capability: str(pending.capability), model: str(pending.model), provider: str(pending.provider), providerId: '',
    priceMode: str(pending.price_mode) || (pending.native_quote !== undefined ? 'native' : ''),
    nativeQuote: parseNativeQuote(pending.native_quote),
    credits: typeof pending.credits === 'number' ? pending.credits : null,
    extra: typeof pending.extra_credits === 'number' && pending.extra_credits > 0
      ? {
        credits: pending.extra_credits,
        baseCredits: typeof pending.base_credits === 'number' ? pending.base_credits : 0,
        label: str(pending.extra_label),
        items: Array.isArray(pending.extra_items) ? pending.extra_items.filter((value): value is string => typeof value === 'string' && value !== '') : [],
      }
      : null,
    parameterAdjustments: parseParameterAdjustments(pending.parameter_adjustments),
    prompt: str(pending.prompt),
    inputs: refs.flatMap((ref): ApprovalCardInput[] => {
      if (typeof ref === 'string') return ref ? [{ parameter: '', handle: ref, taskId: '', assetId: '', kind: '', label: '' }] : [];
      if (ref && typeof ref === 'object') {
        const entry = ref as Record<string, unknown>;
        return [{ parameter: str(entry.parameter), handle: str(entry.handle), taskId: str(entry.task_id), assetId: str(entry.asset_id), kind: '', label: '' }];
      }
      return [];
    }),
    sourceTaskId: str(pending.source_task_id), expiresAt: '',
  };
}

export type Validity =
  | { unit: 'none' }
  | { unit: 'expired' }
  | { unit: 'hours' | 'minutes'; value: number };

/** How long a pending step stays valid, from the server's own expiry time. */
export function remainingValidity(expiresAt: string, now: number): Validity {
  const at = Date.parse(expiresAt);
  if (!Number.isFinite(at)) return { unit: 'none' };
  const left = at - now;
  if (left <= 0) return { unit: 'expired' };
  const minutes = Math.ceil(left / 60_000);
  return minutes >= 60 ? { unit: 'hours', value: Math.ceil(minutes / 60) } : { unit: 'minutes', value: minutes };
}

export interface ApprovalOfferingNames { modelName: string; providerName: string }

/** The Offering a card's model and provider id name; the model alone when no provider matches. */
function matchedOffering(
  offerings: readonly OfferingWire[] | undefined,
  model: Pick<ApprovalCardModel, 'model' | 'provider'>,
): OfferingWire | undefined {
  return (offerings ?? []).find((item) => item.model_id === model.model && item.provider_id === model.provider)
    ?? (offerings ?? []).find((item) => item.model_id === model.model);
}

/**
 * The card names the Offering by its Schema display name and provider name. The
 * pending record only carries ids; an id no Offering matches stays visible as the
 * last resort so the step is never anonymous.
 */
export function approvalOfferingNames(
  offerings: readonly OfferingWire[] | undefined,
  model: Pick<ApprovalCardModel, 'model' | 'provider'>,
): ApprovalOfferingNames {
  const match = matchedOffering(offerings, model);
  return {
    modelName: match?.model_name || model.model,
    providerName: match?.provider_name || model.provider,
  };
}

/**
 * Whether the card shows credits: every paid step gets a card, but its credits follow
 * the Offering's Schema `credits_metered` like the rest of the app (offeringShowsCredits).
 * An Offering that is not known yet (its list is still loading) keeps the server's number.
 */
export function approvalShowsCredits(
  offerings: readonly OfferingWire[] | undefined,
  model: Pick<ApprovalCardModel, 'model' | 'provider'>,
): boolean {
  const match = matchedOffering(offerings, model);
  return match === undefined || offeringShowsCredits(match);
}

/**
 * The Offering's own label for the parameter an input fills ("来源模型"): what the card calls an input the
 * session has no handle or version for (#324), so no input is a bare picture placeholder. Empty when the
 * Offering or the parameter is not known.
 */
export function approvalInputParameterLabel(
  offerings: readonly OfferingWire[] | undefined,
  model: Pick<ApprovalCardModel, 'model' | 'provider'>,
  parameter: string,
): string {
  if (!parameter) return '';
  return matchedOffering(offerings, model)?.parameters?.find((item) => item.name === parameter)?.label ?? '';
}

/**
 * The balance a credit-priced card compares with its credits. A provider that also has natively priced
 * Offerings (Tripo, Hyper3D) reports only an exact native amount; it is the same provider credits the
 * step is priced in, so the card reads it instead of saying the balance is unavailable (#324).
 */
export function creditBalanceOf(account: { balance?: number; priceMode?: 'native'; nativeBalance?: PriceTotal | null } | undefined): number | null {
  if (typeof account?.balance === 'number' && Number.isFinite(account.balance)) return account.balance;
  if (account?.priceMode !== 'native' || !account.nativeBalance) return null;
  const amount = Number(account.nativeBalance.amount);
  return Number.isFinite(amount) ? amount : null;
}

/** Whether the step replaces a model's whole texture set. */
export function isRetextureCard(model: Pick<ApprovalCardModel, 'capability'>): boolean {
  return model.capability === 'retexture';
}

/**
 * The provider's account among the balances: by the account key when the card
 * knows it, else by the display name with spaces and case ignored ("Meshy AI"
 * is the account labelled "MeshyAI").
 */
export function accountOfCard<T extends { provider: string; label: string }>(
  accounts: readonly T[],
  card: Pick<ApprovalCardModel, 'provider' | 'providerId'>,
): T | undefined {
  const squash = (value: string) => value.replace(/\s+/g, '').toLowerCase();
  if (card.providerId) {
    const byKey = accounts.find((account) => account.provider === card.providerId);
    if (byKey) return byKey;
  }
  const name = squash(card.provider);
  return name === '' ? undefined : accounts.find((account) => account.provider === card.provider
    || squash(account.label) === name || squash(account.provider) === name);
}
