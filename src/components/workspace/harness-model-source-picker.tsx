'use client';

import { useEffect, useId, useRef, useState } from 'react';
import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { ModelViewer3D } from '@/components/viewer/model-viewer-3d';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { Field } from '@/components/ui/field';
import { OptionSelect } from '@/components/ui/option-select';
import { useOwnedModelSources } from '@/hooks/use-owned-model-sources';
import { useSessionVersions } from '@/hooks/use-session-versions';
import type { OfferingParameterWire, OfferingWire } from '@/lib/agent-offerings';
import type { HarnessSource, HarnessSourceKind, HarnessSourceView, HarnessTaskSourceMode } from '@/lib/harness';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { ModelInputUploadError, modelInputAccept, uploadModelInputSource } from '@/lib/workspace/model-input-upload';
import { sourceParameterRole } from '@/lib/workspace/processing-source';
import { useChatStore } from '@/store/chat-store';
import type { CurrentObject } from '@/store/workspace-store';
import { formatSizeLimit } from '@/lib/size-text';

type ModelProfile = ProductSchema['runtime']['input_assets']['model_profile'];

/** Called only for an explicit new Offering/source-kind choice, never to migrate a saved selection. */
export function harnessDefaultModelSources(schema: ProductSchema, offering: OfferingWire | undefined): HarnessSource[] {
  const declaration = schema.chat.harness;
  if (declaration?.default_task_source_mode !== 'agent' || !declaration.task_source_modes?.some(mode => mode.id === 'agent')
    || !offering?.parameters.some(parameter => ['task', 'asset'].includes(sourceParameterRole(parameter) ?? ''))) return [];
  return [{ kind: 'task_asset', selection_mode: 'agent', parameter: '*' }];
}

export function harnessUploadedParameters(offering: OfferingWire | undefined, profile: ModelProfile) {
  return profile ? offering?.parameters.filter(parameter => sourceParameterRole(parameter) === 'asset' && parameter.uploaded_model?.profile_id === profile.id) ?? [] : [];
}

/** The selected parameter may carry a frozen profile; legacy declarations use runtime defaults. */
export function harnessParameterProfile(profile: ModelProfile, parameter: OfferingParameterWire | undefined): ModelProfile {
  if (!profile || !parameter?.uploaded_model?.profile_limits) return profile;
  return { ...profile, revision: parameter.uploaded_model.profile_revision ?? profile.revision, limits: { ...parameter.uploaded_model.profile_limits } };
}

/** Native uploads apply both policies; this does not validate an existing selection. */
export function harnessModelUploadSchema(schema: ProductSchema, parameter: OfferingParameterWire | undefined): ProductSchema {
  const current = schema.runtime.input_assets.model_profile;
  const frozen = harnessParameterProfile(current, parameter);
  if (!current || !frozen || frozen === current) return schema;
  return {
    ...schema,
    runtime: {
      ...schema.runtime,
      input_assets: {
        ...schema.runtime.input_assets,
        model_profile: {
          ...frozen,
          limits: { ...frozen.limits, max_bytes: Math.min(frozen.limits.max_bytes, current.limits.max_bytes) },
        },
      },
    },
  };
}

/** Metadata enables a read attempt; the preview endpoint validates the actual bytes. */
export function harnessModelPreviewURL(schema: ProductSchema, source: HarnessSource | undefined, view: Pick<HarnessSourceView, 'asset_id' | 'media_type' | 'state' | 'profile_id'> | undefined): string {
  const profile = schema.runtime.input_assets.model_profile;
  const route = schema.runtime.http.routes.asset_model_preview;
  if (!profile || !route || source?.kind !== 'input_asset' || !source.asset_id || !view || typeof view.media_type !== 'string' || view.asset_id !== source.asset_id || view.state !== 'ready' || (view.profile_id && view.profile_id !== profile.id)) return '';
  const media = view.media_type.split(';', 1)[0].trim().toLowerCase();
  if (!profile.preview_media_types?.includes(media)) return '';
  return safeAssetURL(route.replace('{assetID}', encodeURIComponent(source.asset_id)));
}

/** Owned uploaded bytes are a separate source choice; they never become a workspace Task. */
export function HarnessModelSourcePicker({ ownerId, sessionId, object, offering, source, sourceViews, unavailableReason, disabled, onChange }: {
  ownerId: string; sessionId: string; object: CurrentObject | null; offering?: OfferingWire;
  source?: HarnessSource; sourceViews: HarnessSourceView[]; unavailableReason?: string; disabled: boolean;
  onChange: (sources: HarnessSource[]) => void;
}) {
  const { schema, text } = useProductSchema();
  const id = useId();
  const sessionRevision = useChatStore(state => state.activeSessionRevision);
  const runtimeProfile = schema.runtime.input_assets.model_profile;
  const scope = JSON.stringify([ownerId, sessionId, sessionRevision, schema.revision, offering?.id, offering?.revision]);
  const scopeRef = useRef(scope); scopeRef.current = scope;
  const request = useRef<{ controller?: AbortController; sequence: number }>({ sequence: 0 });
  const fileInput = useRef<HTMLInputElement>(null);
  const [mode, setMode] = useState<{ scope: string; kind: HarnessSourceKind; taskMode: HarnessTaskSourceMode }>({ scope, kind: source?.kind ?? 'task_asset', taskMode: source?.selection_mode === 'agent' ? 'agent' : 'fixed' });
  const kind = mode.scope === scope ? mode.kind : source?.kind ?? 'task_asset';
  const taskMode = mode.scope === scope ? mode.taskMode : source?.selection_mode === 'agent' ? 'agent' : 'fixed';
  const taskModes = schema.chat.harness?.task_source_modes ?? [];
  const agentSource = kind === 'task_asset' && taskMode === 'agent';
  const [upload, setUpload] = useState<{ scope: string; busy: boolean; uploaded?: boolean; message?: string; view?: HarnessSourceView }>({ scope, busy: false });
  const busy = disabled || (upload.scope === scope && upload.busy);
  const versions = useSessionVersions(sessionId);
  const owned = useOwnedModelSources(ownerId, sessionId, kind === 'input_asset');
  const taskParameters = offering?.parameters.filter(parameter => ['task', 'asset'].includes(sourceParameterRole(parameter) ?? '')) ?? [];
  const uploadedParameters = harnessUploadedParameters(offering, runtimeProfile);
  const parameters = kind === 'input_asset' ? uploadedParameters : taskParameters;
  const parameter = parameters.find(item => item.name === source?.parameter) ?? parameters[0];
  const profile = harnessParameterProfile(runtimeProfile, parameter);
  const sources = versions.tiles.filter(tile => tile.origin === 'task' && tile.domain === 'model' && tile.status === 'ready' && tile.assets.some(asset => asset.kind === 'model'));
  const sourceTile = source?.kind !== 'input_asset' ? sources.find(tile => tile.taskId === source?.task_id) : undefined;
  const assets = sourceTile?.assets?.filter(asset => asset.kind === 'model') ?? [];
  const savedView = sourceViews.find(view => view.kind === 'input_asset' && view.asset_id === source?.asset_id);
  const localView = upload.scope === scope && upload.view?.asset_id === source?.asset_id ? upload.view : undefined;
  const ownedView = owned.assets.find(asset => asset.id === source?.asset_id);
  const filename = savedView?.original_filename || localView?.original_filename || ownedView?.original_filename;
  // A saved unavailable view takes precedence over cached/list metadata.
  const previewView = savedView ?? localView ?? (ownedView ? { asset_id: ownedView.id, media_type: ownedView.media_type, state: 'ready' as const } : undefined);
  const previewURL = kind === 'input_asset' && uploadedParameters.some(item => item.name === source?.parameter) ? harnessModelPreviewURL(schema, source, previewView) : '';
  const previewScope = JSON.stringify([scope, kind, source?.kind, source?.parameter, source?.asset_id, previewURL]);
  const [previewRequest, setPreviewRequest] = useState<{ scope: string; src: string } | null>(null);
  const previewOpen = Boolean(previewURL && previewRequest?.scope === previewScope);
  const openPreview = () => {
    if (!previewURL) return;
    // model-viewer caches by URL even after unmount. A fresh non-identity nonce
    // makes every open/retry repeat the owner's authenticated byte validation.
    const url = new URL(previewURL, window.location.origin);
    url.searchParams.set('_preview', Array.from(crypto.getRandomValues(new Uint32Array(4)), value => value.toString(36)).join('-'));
    setPreviewRequest({ scope: previewScope, src: url.pathname + url.search });
  };
  useEffect(() => { setPreviewRequest(null); }, [previewScope]);
  const explain = (message: string) => schemaText(schema, message) || message;
  const invalidate = () => { request.current.controller?.abort(); request.current.sequence += 1; setUpload({ scope, busy: false }); };
  useEffect(() => {
    // React may replay mount effects in StrictMode; setup must restore liveness.
    scopeRef.current = scope;
    return () => { scopeRef.current = ''; request.current.controller?.abort(); };
  }, [scope]);
  const changeTask = (taskId: string, assetId?: string) => {
    if (!parameter) return;
    invalidate();
    onChange(taskId ? [{ parameter: parameter.name, kind: 'task_asset', task_id: taskId, ...(assetId ? { asset_id: assetId } : {}) }] : []);
  };
  const changeUpload = (assetId: string) => {
    if (!parameter || !owned.assets.some(asset => asset.id === assetId)) return;
    invalidate();
    onChange([{ kind: 'input_asset', parameter: parameter.name, asset_id: assetId }]);
  };
  const uploadFile = async (file: File) => {
    if (busy || !parameter || !profile) return;
    request.current.controller?.abort();
    const controller = new AbortController(); const sequence = ++request.current.sequence;
    request.current.controller = controller;
    const isCurrent = () => scopeRef.current === scope && request.current.sequence === sequence && !controller.signal.aborted;
    setUpload(previous => ({ scope, busy: true, view: previous.scope === scope ? previous.view : undefined }));
    try {
      // New native uploads must satisfy both the current upload gateway and
      // this Offering's frozen parameter policy. Existing selections stay server-validated.
      const uploadSchema = harnessModelUploadSchema(schema, parameter);
      const result = await uploadModelInputSource(uploadSchema, file, parameter.name, { signal: controller.signal, isCurrent });
      if (!result || !isCurrent()) return;
      setUpload({ scope, busy: false, view: result.view, uploaded: true });
      onChange([result.source]);
      void owned.refresh().catch(() => undefined);
    } catch (error) {
      if (!isCurrent()) return;
      setUpload(previous => ({ scope, busy: false, view: previous.scope === scope ? previous.view : undefined, message: error instanceof ModelInputUploadError ? error.message : 'ui.harness.upload.failed' }));
    }
  };
  const kinds = schema.chat.harness?.source_kinds ?? [];
  return <div className="space-y-3" data-testid="harness-model-source-picker">
    {kinds.length ? <Field id={`${id}-kind`} label={text('ui.harness.source_kind')} help={kinds.find(item => item.id === kind)?.description}>
      {control => <OptionSelect {...control} value={kind} options={kinds.map(item => ({ value: item.id, label: item.label }))} disabled={busy} onValueChange={value => { if (!kinds.some(item => item.id === value) || value === kind) return; invalidate(); const sources = value === 'task_asset' ? harnessDefaultModelSources(schema, offering) : []; setMode({ scope, kind: value as HarnessSourceKind, taskMode: sources[0]?.selection_mode === 'agent' ? 'agent' : 'fixed' }); onChange(sources); }} />}
    </Field> : null}
    {kind === 'task_asset' && taskModes.length ? <Field id={`${id}-mode`} label={text('ui.harness.task_source_mode')} help={taskModes.find(item => item.id === taskMode)?.description}>
      {control => <OptionSelect {...control} value={taskMode} options={taskModes.map(item => ({ value: item.id, label: item.label }))} disabled={busy} onValueChange={value => {
        if (!taskModes.some(item => item.id === value) || value === taskMode) return;
        invalidate(); setMode({ scope, kind: 'task_asset', taskMode: value as HarnessTaskSourceMode });
        onChange(value === 'agent' ? [{ kind: 'task_asset', selection_mode: 'agent', parameter: '*' }] : []);
      }} />}
    </Field> : null}
    {agentSource ? <p className="leading-5 text-muted-foreground" data-testid="harness-agent-source-help">{text('ui.harness.agent_source_help')}</p> : kind === 'input_asset' ? <>
      {filename ? <p className="break-all leading-5" data-testid="harness-upload-filename">{filename}</p> : source?.asset_id ? <p className="text-muted-foreground">{text('ui.harness.source_unavailable')}</p> : null}
      {savedView?.state === 'unavailable' ? <p role="status" className="leading-5 text-warning">{savedView.reason ? explain(savedView.reason) : text('ui.harness.source_unavailable')}</p> : null}
      {!parameter || !profile ? <p role="status" className="leading-5 text-muted-foreground">{unavailableReason ? explain(unavailableReason) : text('ui.harness.upload.unavailable')}</p> : <>
        <p className="leading-5 text-muted-foreground">{profile.description}</p>
        {parameter.uploaded_model?.requirements ? <p className="leading-5 text-muted-foreground">{explain(parameter.uploaded_model.requirements)}</p> : null}
        <Field id={`${id}-uploaded`} label={text('ui.harness.upload.existing')} help={text('ui.harness.upload.validation_help')}>
          {control => <OptionSelect {...control} value={ownedView?.id ?? ''} placeholder={text('ui.harness.choose_source')} options={owned.assets.map(asset => ({ value: asset.id, label: asset.original_filename }))} disabled={busy || owned.isLoading} onValueChange={changeUpload} />}
        </Field>
      <Dialog key={previewScope} open={previewOpen} onOpenChange={open => { if (open) openPreview(); else setPreviewRequest(null); }}>
        <DialogTrigger render={<Action variant="outline" size="sm" disabled={busy || !previewURL} />} data-testid="harness-model-preview-open">{text('ui.harness.upload.preview')}</DialogTrigger>
        <DialogContent closeLabel={text('ui.harness.upload.preview_close')} className="w-[min(56rem,calc(100vw-3rem))] max-w-none sm:max-w-none" data-testid="harness-model-preview">
          <DialogHeader className="pr-8">
            <DialogTitle>{text('ui.harness.upload.preview_title')}</DialogTitle>
            <p className="break-all leading-5">{filename}</p>
            <DialogDescription>{text('ui.harness.upload.preview_help')}</DialogDescription>
          </DialogHeader>
          {previewOpen ? <ModelViewer3D key={previewScope} src={previewRequest!.src} onRetry={openPreview} failureMessage={text('ui.harness.upload.preview_failed')} alt={filename} autoRotate={false} className="h-[min(60vh,34rem)] aspect-auto" /> : null}
        </DialogContent>
      </Dialog>
      {!previewURL ? <p className="leading-5 text-muted-foreground">{text(source?.asset_id ? 'ui.harness.upload.preview_unavailable' : 'ui.harness.upload.preview_choose')}</p> : null}
        {owned.error ? <p role="alert" className="text-destructive">{text('ui.harness.sources_failed')}</p> : owned.isLoading ? <p role="status" className="text-muted-foreground">{text('ui.harness.loading')}</p> : !owned.assets.length ? <p className="leading-5 text-muted-foreground">{text('ui.harness.upload.no_files')}</p> : null}
        <div className="flex flex-wrap gap-2">
          <Action variant="outline" size="sm" disabled={busy} onClick={() => fileInput.current?.click()}>{text(upload.scope === scope && upload.busy ? 'ui.harness.upload.uploading' : 'ui.harness.upload.choose_file')}</Action>
          <Action variant="ghost" size="sm" disabled={busy || owned.isLoading} onClick={() => void owned.refresh().catch(() => undefined)}>{text('ui.harness.reload')}</Action>
          {owned.page > 1 ? <Action variant="ghost" size="sm" disabled={busy} onClick={() => owned.setPage(owned.page - 1)}>{text('ui.harness.upload.previous')}</Action> : null}
          {owned.hasNext ? <Action variant="ghost" size="sm" disabled={busy} onClick={() => owned.setPage(owned.page + 1)}>{text('ui.harness.upload.next')}</Action> : null}
        </div>
        <input ref={fileInput} type="file" accept={modelInputAccept(profile)} className="hidden" tabIndex={-1} aria-label={text('ui.harness.upload.choose_file')} disabled={busy} onChange={event => { const file = event.currentTarget.files?.[0]; event.currentTarget.value = ''; if (file) void uploadFile(file); }} />
        <p className="leading-5 text-muted-foreground">{formatSchemaText(text('ui.harness.upload.limits'), { formats: profile.extensions.join(text('ui.harness.separator')), size: formatSizeLimit(parameter.uploaded_model?.profile_limits ? profile.limits.max_bytes : Math.min(profile.limits.max_bytes, schema.runtime.input_assets.upload_max_bytes)) })}</p>
      </>}
      {upload.scope === scope && (upload.uploaded || upload.message) ? <p role={upload.uploaded ? 'status' : 'alert'} className={`leading-5 break-words ${upload.uploaded ? 'text-muted-foreground' : 'text-destructive'}`}>{upload.uploaded ? text('ui.harness.upload.uploaded') : explain(upload.message!)}</p> : null}
    </> : <>
      <Field id={`${id}-source`} label={text('ui.harness.source_model')} help={text('ui.harness.source_help')}>
        {control => <OptionSelect {...control} value={sourceTile?.taskId ?? ''} placeholder={text(source?.task_id ? 'ui.harness.source_unavailable' : 'ui.harness.choose_source')} options={sources.map(tile => ({ value: tile.taskId, label: formatSchemaText(text('ui.harness.model_version'), { version: tile.version }) }))} disabled={busy || versions.isLoading} onValueChange={taskId => changeTask(taskId)} />}
      </Field>
      {versions.error ? <div role="alert" className="space-y-2"><p>{text('ui.harness.sources_failed')}</p><Action size="sm" variant="outline" onClick={() => void versions.refresh().catch(() => undefined)}>{text('ui.harness.reload')}</Action></div> : !versions.isLoading && !sources.length ? <p className="leading-5 text-muted-foreground">{text('ui.harness.no_sources')}</p> : null}
      {object?.kind === 'model' && object.sessionId === sessionId && sources.some(tile => tile.taskId === object.taskId) ? <Action variant="outline" size="sm" disabled={busy} onClick={() => changeTask(object.taskId, object.assetId)}>{text('ui.harness.use_current')}</Action> : null}
      {sourceTile && assets.length ? <Field id={`${id}-asset`} label={text('ui.harness.source_asset')}>
        {control => <OptionSelect {...control} value={source?.asset_id ?? ''} placeholder={text(source?.asset_id ? 'ui.harness.source_unavailable' : 'ui.harness.asset_default')} options={[{ value: '', label: text('ui.harness.asset_default') }, ...assets.map((asset, index) => ({ value: asset.id, label: asset.variant || formatSchemaText(text('ui.harness.model_file'), { number: index + 1 }) }))]} disabled={busy} onValueChange={assetId => changeTask(sourceTile.taskId, assetId)} />}
      </Field> : null}
    </>}
    {!agentSource && parameters.length > 1 ? <Field id={`${id}-binding`} label={text('ui.harness.source_binding')}>
      {control => <OptionSelect {...control} value={parameter?.name ?? ''} options={parameters.map(item => ({ value: item.name, label: item.label }))} disabled={busy || !source} onValueChange={value => { if (source && parameters.some(item => item.name === value)) { invalidate(); onChange([{ ...source, parameter: value }]); } }} />}
    </Field> : null}
  </div>;
}
