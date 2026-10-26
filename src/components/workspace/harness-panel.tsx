'use client';

import { useEffect, useId, useMemo, useState } from 'react';
import useSWR from 'swr';

import { SchemaParameters, schemaParameterValues } from '@/components/form/schema-parameters';
import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { Field } from '@/components/ui/field';
import { OptionSelect } from '@/components/ui/option-select';
import { useAuth } from '@/hooks/use-auth';
import { useSessionHarness } from '@/hooks/use-session-harness';
import { HarnessModelSourcePicker, harnessDefaultModelSources } from './harness-model-source-picker';
import { fetchCapabilityOfferings, offeringParamSchemas, type OfferingWire } from '@/lib/agent-offerings';
import { HarnessBrowserError, type HarnessPublicOperation, type HarnessSelection, type HarnessSelectionInput } from '@/lib/harness';
import { formatSchemaText, schemaCountMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import { formatNativePrice, parseNativeQuote } from '@/lib/pricing';
import { sourceParameterRole } from '@/lib/workspace/processing-source';
import { cn } from '@/lib/utils';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';

/** Common operations are independent of the generation-model picker. */
export function harnessOperationsForModel(operations: HarnessPublicOperation[], group: string, models: Array<string | null>) {
  return operations.filter(operation => operation.group === group && (group !== 'model' || !operation.model_ids?.length || operation.model_ids.some(model => models.includes(model))));
}

/** The same public Offering controls are used, without exposing sources, uploads or consent as editable parameters. */
export function harnessParameterFields(offering: OfferingWire) {
  const forbidden = new Set(offering.parameters.filter(parameter => sourceParameterRole(parameter) || parameter.semantic === 'reference-image' || parameter.semantic === 'mask' || parameter.name === offering.extra_credit_consent_parameter || ['task', 'asset', 'asset-array'].includes(parameter.type)).map(parameter => parameter.name));
  return offeringParamSchemas(offering).filter(parameter => !forbidden.has(parameter.name));
}

/**
 * Reference images the Agent takes from the conversation (#187 D): a picture belongs to one request, so it is
 * attached to (or named with @ in) the message, never saved here. Mirrors resolve.ConversationParameter.
 */
export function harnessConversationFields(offering: OfferingWire) {
  return offering.parameters.filter(parameter => parameter.semantic === 'reference-image' && ['asset', 'asset-array'].includes(parameter.type)
    && !sourceParameterRole(parameter) && !parameter.fixed && parameter.presentation?.surface !== 'hidden');
}

export function harnessSelectionInput(draft: HarnessSelectionInput, offering: OfferingWire | undefined): HarnessSelectionInput {
  if (!offering) return draft;
  const fields = harnessParameterFields(offering);
  const allowed = new Set(fields.map(field => field.name));
  return { ...draft, fixed_parameters: schemaParameterValues(fields, draft.fixed_parameters), fillable_parameters: draft.fillable_parameters.filter(name => allowed.has(name)) };
}

/**
 * The first explicit goal (nothing enabled yet) adopts the Schema's allowed-operation defaults; later edits, also after
 * 清空选择, keep the saved bounds (#327).
 */
export function harnessOperationSelection(current: HarnessSelectionInput, operationId: string, defaultEnabled: string[], defaultAllowPrerequisites: boolean): HarnessSelectionInput {
  const enabled = current.operation_id || current.enabled_operations.length > 0 ? current.enabled_operations : defaultEnabled;
  return {
    ...current, operation_id: operationId, enabled_operations: [...new Set([...enabled, operationId])],
    allow_prerequisites: current.operation_id ? current.allow_prerequisites : defaultAllowPrerequisites,
    offering_id: undefined, offering_revision: undefined, sources: [], fixed_parameters: {}, fillable_parameters: [],
  };
}

/**
 * 清空选择 (#327): the saved selection without its operation, service, sources and parameters. The enabled operations
 * stay as saved — never the draft's, which picking an operation extends — so clearing changes nothing else.
 */
export function harnessClearSelection(saved: HarnessSelectionInput): HarnessSelectionInput {
  return { ...saved, enabled_operations: [...saved.enabled_operations], operation_id: undefined, offering_id: undefined, offering_revision: undefined, sources: [], fixed_parameters: {}, fillable_parameters: [] };
}

/** Explicit and automatic selection share the same editable-parameter defaults. */
export function harnessOfferingSelection(current: HarnessSelectionInput, offering: OfferingWire, schema?: ProductSchema): HarnessSelectionInput {
  return { ...current, offering_id: offering.id, offering_revision: offering.revision, sources: schema ? harnessDefaultModelSources(schema, offering) : [], fixed_parameters: {}, fillable_parameters: harnessParameterFields(offering).map(field => field.name) };
}

/** Only an unconfigured draft may adopt a unique available service. */
export function harnessAutoSelectOffering(current: HarnessSelectionInput, operation: HarnessPublicOperation | undefined, choices: OfferingWire[], busy = false, schema?: ProductSchema): HarnessSelectionInput {
  if (busy || !operation || operation.id !== current.operation_id || operation.unavailable_reason || current.offering_id || current.offering_revision) return current;
  const available = choices.filter(item => item.available && item.capability === operation.capability && (!operation.model_ids?.length || operation.model_ids.includes(item.model_id)));
  return available.length === 1 ? harnessOfferingSelection(current, available[0], schema) : current;
}

export function HarnessPanel({ sessionId, object }: { sessionId: string | null; object: CurrentObject | null }) {
  const { schema, text } = useProductSchema();
  const { user, isLoading: authLoading } = useAuth();
  const harness = useSessionHarness(sessionId, user?.id ?? '');
  const editorScope = `${user?.id ?? ''}:${sessionId ?? ''}:${schema.revision}`;
  const [feedback, setFeedback] = useState({ scope: '', message: '' });
  // A new scope/revision remounts the editor, so another session's draft or a CAS conflict cannot be resubmitted accidentally.
  const scope = `${user?.id ?? ''}:${sessionId ?? ''}:${schema.revision}:${harness.view?.selection.revision ?? ''}:${harness.view?.selection.digest ?? ''}`;
  if (!schema.chat.harness) return <p className="p-3 text-xs text-muted-foreground">{text('ui.harness.not_connected')}</p>;
  if (!sessionId || !user) return <p className="p-3 text-xs text-muted-foreground">{text(!sessionId ? 'ui.harness.select_session' : authLoading ? 'ui.harness.loading' : 'ui.harness.sign_in')}</p>;
  if (!harness.view) return <div className="space-y-3 p-3" aria-busy={harness.busy}>
    <p role={harness.error ? 'alert' : 'status'} className="text-xs text-muted-foreground">{text(harness.error ? 'ui.harness.load_failed' : 'ui.harness.loading')}</p>
    {harness.error ? <Action variant="outline" size="sm" onClick={() => void harness.reload().catch(() => undefined)}>{text('ui.harness.reload')}</Action> : <div className="h-20 animate-pulse rounded-md bg-muted" aria-hidden="true" />}
  </div>;
  return <HarnessEditor key={scope} sessionId={sessionId} ownerId={user.id} object={object} harness={harness} initial={harness.view.selection} message={feedback.scope === editorScope ? feedback.message : ''} setMessage={message => setFeedback({ scope: editorScope, message })} />;
}

function HarnessEditor({ sessionId, ownerId, object, harness, initial, message, setMessage }: {
  sessionId: string; ownerId: string; object: CurrentObject | null;
  harness: ReturnType<typeof useSessionHarness>; initial: HarnessSelection; message: string; setMessage: (message: string) => void;
}) {
  const { schema, text } = useProductSchema();
  const declarations = schema.chat.harness!;
  const id = useId();
  const [draft, setDraft] = useState<HarnessSelectionInput>(() => ({ ...initial, sources: [...initial.sources], fixed_parameters: { ...initial.fixed_parameters }, fillable_parameters: [...initial.fillable_parameters] }));
  const [automatic, setAutomatic] = useState<{ operation: string; offering: string; revision: string } | null>(null);
  const models = useWorkspaceStore(state => state.modelByDomain);
  const capabilities = useMemo(() => [...new Set(declarations.operations.map(operation => operation.capability))], [declarations]);
  const { data: offerings, error: offeringsError, mutate: refreshOfferings } = useSWR<Record<string, OfferingWire[]>>(['harness-offerings', ownerId, schema.revision], async () => Object.fromEntries(await Promise.all(capabilities.map(async capability => [capability, await fetchCapabilityOfferings(schema, capability)]))), { revalidateOnFocus: true });
  const operation = declarations.operations.find(item => item.id === draft.operation_id);
  const choices = useMemo(() => operation ? (offerings?.[operation.capability] ?? []).filter(offering => !operation.model_ids?.length || operation.model_ids.includes(offering.model_id)) : [], [operation, offerings]);
  useEffect(() => {
    const selected = harnessAutoSelectOffering(draft, operation, choices, harness.busy, schema);
    if (selected === draft) return;
    // Recheck the live draft so a discovery refresh cannot replace a user choice.
    setDraft(current => harnessAutoSelectOffering(current, operation, choices, harness.busy, schema));
    setAutomatic({ operation: selected.operation_id!, offering: selected.offering_id!, revision: selected.offering_revision! });
  }, [draft, operation, choices, harness.busy, schema]);
  const offering = choices.find(item => item.id === draft.offering_id && item.revision === draft.offering_revision);
  const fields = offering ? harnessParameterFields(offering) : [];
  const conversationFields = offering ? harnessConversationFields(offering) : [];
  const sourceParameters = offering?.parameters.filter(parameter => ['task', 'asset'].includes(sourceParameterRole(parameter) ?? '')) ?? [];
  const values = Object.fromEntries(fields.map(field => [field.name, draft.fixed_parameters[field.name] ?? field.default]));
  const dirty = JSON.stringify(harnessSelectionInput(draft, offering)) !== JSON.stringify(harnessSelectionInput(initial, offering));
  const unavailable = Boolean(draft.operation_id && (!operation || operation.unavailable_reason || (draft.offering_id && offerings && !offering?.available)));
  const selectOperation = (next: string) => {
    setDraft(current => harnessOperationSelection(current, next, declarations.default_enabled_operations, declarations.default_allow_prerequisites));
    setAutomatic(null);
    setMessage('');
  };
  const selectOffering = (value: string) => {
    const next = choices.find(item => item.id === value && item.available);
    if (!next) return;
    setDraft(current => harnessOfferingSelection(current, next, schema));
    setAutomatic(null);
    setMessage('');
  };
  const save = async (selection: HarnessSelectionInput) => {
    setMessage('');
    try { await harness.save(selection); setMessage('ui.harness.saved'); }
    catch (error) { setMessage(error instanceof HarnessBrowserError && error.status === 409 ? 'ui.harness.conflict' : 'ui.harness.save_failed'); }
  };
  const clear = async () => {
    if (!initial.operation_id) { setDraft(initial); setMessage(''); return; }
    await save(harnessClearSelection(initial));
  };
  const status = harness.view!;
  const savedOffering = Object.values(offerings ?? {}).flat().find(item => item.id === initial.offering_id && item.revision === initial.offering_revision);
  return <section className="flex h-full min-h-0 flex-col text-xs" aria-label={text('ui.harness.title')} data-testid="harness-panel">
    <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto p-3 [scrollbar-gutter:stable]">
      <div><h3 className="text-sm font-semibold">{text('ui.harness.title')}</h3><p className="mt-1 leading-5 text-muted-foreground">{text('ui.harness.description')}</p></div>
      {declarations.groups.map(group => {
        const operations = harnessOperationsForModel(declarations.operations, group.id, [models.image, models['model-3d']]);
        return <fieldset key={group.id} className="space-y-2"><legend className="mb-2 font-medium">{group.label}</legend>
          {operations.length ? operations.map(item => <label key={item.id} className={cn('flex cursor-pointer items-start gap-2 rounded-md px-2 py-2 hover:bg-muted focus-within:outline-2 focus-within:outline-ring', draft.operation_id === item.id && 'bg-primary/10')}>
            <input type="radio" name={`${id}-operation`} value={item.id} checked={draft.operation_id === item.id} disabled={harness.busy || Boolean(item.unavailable_reason)} onChange={() => selectOperation(item.id)} className="mt-0.5" />
            <span className="min-w-0"><span className="font-medium">{item.label}</span><span className="mt-0.5 block leading-4 text-muted-foreground">{item.unavailable_reason || item.description}</span></span>
          </label>) : <p className="leading-5 text-muted-foreground">{group.empty_message}</p>}
        </fieldset>;
      })}
      {draft.operation_id ? <div className="space-y-4 border-t border-border pt-4">
        {!operation ? <p role="status" className="text-warning">{text('ui.harness.selection_unavailable')}</p> : null}
        <Field id={`${id}-offering`} label={text('ui.harness.offering')}>
          {control => <OptionSelect {...control} value={offering?.id ?? ''} placeholder={text(draft.offering_id ? 'ui.harness.selection_unavailable' : 'ui.harness.choose_offering')} options={choices.map(item => ({ value: item.id, label: `${item.provider_name} · ${item.model_name}`, disabled: !item.available }))} disabled={harness.busy || !choices.some(item => item.available)} onValueChange={selectOffering} />}
        </Field>
        {automatic?.operation === draft.operation_id && automatic?.offering === draft.offering_id && automatic?.revision === draft.offering_revision ? <p role="status" className="leading-5 text-muted-foreground">{text('ui.harness.auto_selected_service')}</p> : null}
        {offeringsError ? <div role="alert" className="space-y-2"><p>{text('ui.harness.offerings_failed')}</p><Action size="sm" variant="outline" onClick={() => void refreshOfferings().catch(() => undefined)}>{text('ui.harness.reload')}</Action></div> : !offerings ? <p role="status" className="text-muted-foreground">{text('ui.harness.loading')}</p> : !choices.some(item => item.available) ? <p role="status" className="text-muted-foreground">{text('ui.harness.not_connected')}</p> : null}
        {unavailable ? <p role="status" className="text-warning">{text('ui.harness.selection_unavailable')}</p> : null}
        {sourceParameters.length || draft.sources.length ? <HarnessModelSourcePicker ownerId={ownerId} sessionId={sessionId} object={object} offering={offering} source={draft.sources[0]} sourceViews={harness.view?.source_views ?? []} unavailableReason={operation?.uploaded_source_unavailable_reason} disabled={harness.busy} onChange={sources => setDraft(current => ({ ...current, sources }))} /> : null}
        {fields.length ? <div className="space-y-4"><div><h4 className="font-medium">{text('ui.harness.parameters')}</h4><p className="mt-1 leading-5 text-muted-foreground">{text('ui.harness.parameter_help')}</p></div>
          {fields.map(field => {
            const fillable = draft.fillable_parameters.includes(field.name);
            return <div key={field.name} className="space-y-2"><SchemaParameters parameters={[field]} values={values} disabled={harness.busy || fillable} onChange={(name, value) => setDraft(current => ({ ...current, fixed_parameters: { ...current.fixed_parameters, [name]: value } }))} />
              <label className="flex items-center gap-2 text-muted-foreground"><input type="checkbox" checked={fillable} disabled={harness.busy} onChange={event => setDraft(current => {
                const fixed = { ...current.fixed_parameters };
                if (event.target.checked) delete fixed[field.name];
                else if (values[field.name] !== undefined) fixed[field.name] = values[field.name];
                return { ...current, fixed_parameters: fixed, fillable_parameters: event.target.checked ? [...current.fillable_parameters, field.name] : current.fillable_parameters.filter(name => name !== field.name) };
              })} />{text(fillable ? 'ui.harness.agent_fills' : 'ui.harness.fixed')}</label>
            </div>;
          })}
        </div> : null}
        {conversationFields.length ? <div className="space-y-1 leading-5" data-testid="harness-conversation-inputs">
          {conversationFields.map(field => <p key={field.name} className="font-medium">{formatSchemaText(text(field.required ? 'ui.harness.conversation_input_required' : 'ui.harness.conversation_input'), { parameter: field.label })}</p>)}
          <p className="text-muted-foreground">{text('ui.harness.conversation_input_help')}</p>
        </div> : null}
        {operation?.prerequisites?.length ? <label className="flex items-start gap-2 leading-5"><input type="checkbox" checked={draft.allow_prerequisites} disabled={harness.busy} onChange={event => setDraft(current => ({ ...current, allow_prerequisites: event.target.checked }))} className="mt-1" /><span>{text('ui.harness.allow_prerequisites')}</span></label> : null}
      </div> : null}
      <div role="status" className="space-y-1 leading-5 text-muted-foreground" data-testid="harness-status"><h4 className="font-medium text-foreground">{text('ui.harness.saved_status')}</h4><p>{text(`ui.harness.status_${status.status}`)}</p>{status.reason ? <p>{schemaText(schema, status.reason) || status.reason}</p> : null}{status.price_mode === 'native' || savedOffering?.price_mode === 'native' ? <p>{parseNativeQuote(status.native_quote) ? formatSchemaText(schemaText(schema, 'ui.pricing.default_estimate'), { amount: formatNativePrice(parseNativeQuote(status.native_quote)!.total_amount, parseNativeQuote(status.native_quote)!.unit_label) ?? schemaText(schema, 'ui.pricing.unknown') }) : schemaText(schema, 'ui.pricing.unknown')}</p> : savedOffering?.credits_metered ? <p>{schemaCountMessage(schema, 'ui.harness.estimated_credits', status.estimated_credits ?? 0, { credits: status.estimated_credits ?? 0 })}</p> : null}{status.can_propose ? <p>{text('ui.harness.can_propose')}</p> : null}{status.missing_parameters.length ? <p>{formatSchemaText(text('ui.harness.missing'), { parameters: status.missing_parameters.map(name => savedOffering?.parameters.find(parameter => parameter.name === name)?.label ?? text('ui.harness.required_input')).join(text('ui.harness.separator')) })}</p> : null}</div>
    </div>
    <footer className="space-y-2 border-t border-border p-3">
      <div className="min-h-5 space-y-1" data-testid="harness-feedback">
        {message && !(dirty && message === 'ui.harness.saved') ? <p role={message === 'ui.harness.saved' ? 'status' : 'alert'} className={cn('leading-5', message !== 'ui.harness.saved' && 'text-destructive')}>{text(message)}</p> : dirty ? <p role="status" className="leading-5 text-muted-foreground">{text('ui.harness.unsaved')}</p> : null}
      </div>
      <div className="flex gap-2"><Action className="flex-1" disabled={harness.busy || !dirty || Boolean(draft.operation_id && (!offering?.available || operation?.unavailable_reason))} loading={harness.busy} onClick={() => void save(harnessSelectionInput(draft, offering))}>{text('ui.harness.save')}</Action><Action variant="outline" disabled={harness.busy || (!initial.operation_id && !draft.operation_id)} onClick={() => void clear()}>{text('ui.harness.clear')}</Action></div>
      <p className="leading-4 text-muted-foreground">{text('ui.harness.save_note')}</p>
    </footer>
  </section>;
}
