'use client';

import { useMemo, useRef, useState } from 'react';
import { ArrowDown, ArrowUp, ImagePlus, Layers, Upload, X } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useStyleLock, EMPTY_STYLE_LOCK } from '@/hooks/use-style-lock';
import { createChatBrowserClient, type ChatStyleLock } from '@/lib/chat/browser-client';
import { exportCanvasToBlob } from '@/lib/canvas/export-canvas';
import { flushPendingEdits } from '@/lib/canvas/pending-edits';
import { exportAndUploadSnapshot, snapshotFilename } from '@/lib/canvas/send-snapshot';
import { prepareImageUploadBlob } from '@/lib/image-upload';
import { uploadWorkspaceInputAssetResult } from '@/lib/workspace/input-asset-upload';
import { safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { useCanvasStore, type CanvasNode } from '@/store/canvas-store';
import { useChatStore, type ChatMessage } from '@/store/chat-store';

const NO_NODES: readonly CanvasNode[] = [];

export interface StyleLockDraft {
  draft: ChatStyleLock;
  base: ChatStyleLock;
}

function sameLock(left: ChatStyleLock, right: ChatStyleLock): boolean {
  return left.enabled === right.enabled && left.text.trim() === right.text.trim()
    && left.references.length === right.references.length
    && left.references.every((reference, index) => reference === right.references[index]);
}

export function followSavedStyleLock(state: StyleLockDraft, saved: ChatStyleLock): StyleLockDraft {
  if (sameLock(state.base, saved)) return state;
  const untouched = sameLock(state.draft, state.base);
  return { draft: untouched ? { ...saved, references: [...saved.references] } : state.draft, base: { ...saved, references: [...saved.references] } };
}

interface SessionReference {
  assetId: string;
  url: string;
  label: string;
}

function sessionReferences(messages: readonly ChatMessage[], labelOf: (index: number) => string): SessionReference[] {
  const references: SessionReference[] = [];
  const seen = new Set<string>();
  for (const message of messages) {
    if (message.role !== 'assistant' || message.isPending || message.isStreaming || !message.images) continue;
    for (const [index, image] of message.images.entries()) {
      const attachment = message.attachments?.find((item) => item.kind === 'image' && item.imageSlot === index + 1);
      const assetId = attachment?.assetId ?? '';
      if (!assetId || seen.has(assetId) || !image.url) continue;
      seen.add(assetId);
      references.push({ assetId, url: image.url, label: labelOf(references.length + 1) });
    }
  }
  return references;
}

function assetURL(schema: ReturnType<typeof useProductSchema>['schema'], assetId: string): string {
  const route = schema.runtime.http.routes.asset_item;
  return route ? route.replace('{assetID}', encodeURIComponent(assetId)) : '';
}

export function StyleLockPanel({ sessionId }: { sessionId: string | null }) {
  const { schema } = useProductSchema();
  const client = useMemo(() => createChatBrowserClient(schema), [schema]);
  const lockState = useStyleLock(client, sessionId);
  const { previewAssetUrl } = useAssetURLRewriter();
  const messages = useChatStore((state) => (sessionId ? state.messagesBySession?.[sessionId] ?? [] : []));
  const nodes = useCanvasStore((state) => (sessionId ? state.nodesBySession[sessionId] ?? NO_NODES : NO_NODES));
  const maximumText = schema.chat.style_lock?.maximum_text_characters ?? 0;
  const maximumReferences = schema.chat.style_lock?.maximum_reference_images ?? 0;
  const [draftState, setDraftState] = useState<StyleLockDraft>(() => ({ draft: EMPTY_STYLE_LOCK, base: EMPTY_STYLE_LOCK }));
  const [chooserOpen, setChooserOpen] = useState(false);
  const [layerChooserOpen, setLayerChooserOpen] = useState(false);
  const [layerBusy, setLayerBusy] = useState<string | null>(null);
  const [localURLs, setLocalURLs] = useState<Record<string, string>>({});
  const [saveState, setSaveState] = useState<'idle' | 'saved' | 'failed'>('idle');
  const [uploadFailed, setUploadFailed] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const text = (key: string) => schemaText(schema, `ui.studio.style_lock.${key}`);
  const labelOf = (index: number) => formatSchemaText(schemaText(schema, 'ui.studio.strip.candidate_n'), { index });
  const choices = useMemo(() => sessionReferences(messages, labelOf), [labelOf, messages]);
  // Canvas layers the person can turn into a style reference: visible, non-guide nodes, top layer first.
  const layers = useMemo(
    () => nodes.filter((node) => node.type !== 'frame' && node.visible !== false && !(node.blank && !node.imageUrl))
      .sort((left, right) => right.zIndex - left.zIndex),
    [nodes],
  );
  const followed = followSavedStyleLock(draftState, lockState.lock);
  if (followed !== draftState) setDraftState(followed);
  if (!sessionId) return null;
  const draft = followed.draft;
  const state = {
    count: Array.from(draft.text.trim()).length,
    dirty: !sameLock(draft, followed.base),
  };
  const overLimit = state.count > maximumText;
  const refsAtLimit = draft.references.length >= maximumReferences;
  const updateDraft = (next: ChatStyleLock) => setDraftState({ draft: { ...next, references: [...next.references] }, base: followed.base });
  const previewFor = (assetId: string) => {
    const raw = localURLs[assetId] ?? choices.find((item) => item.assetId === assetId)?.url ?? assetURL(schema, assetId);
    return previewAssetUrl(raw) || safePreviewURL(raw);
  };
  const save = async () => {
    if (!state.dirty || overLimit || draft.references.length > maximumReferences || lockState.busy) return;
    const ok = await lockState.save({ ...draft, text: draft.text.trim() });
    setSaveState(ok ? 'saved' : 'failed');
  };
  const upload = async (file: File) => {
    if (refsAtLimit) return;
    setUploadFailed(false);
    try {
      const blob = await prepareImageUploadBlob(file, {
        accept: schema.chat.frontend.image_file_accept,
        maximumBytes: schema.runtime.input_assets.upload_max_bytes,
        policy: schema.runtime.frontend.image_upload,
      });
      const uploaded = await uploadWorkspaceInputAssetResult(schema, blob, file.name);
      setLocalURLs((current) => ({ ...current, [uploaded.id]: uploaded.url }));
      updateDraft({ ...draft, references: [...draft.references, uploaded.id] });
      setSaveState('idle');
    } catch {
      setUploadFailed(true);
      setSaveState('failed');
    } finally {
      if (fileInput.current) fileInput.current.value = '';
    }
  };
  const choose = (reference: SessionReference) => {
    if (draft.references.includes(reference.assetId) || refsAtLimit) return;
    setLocalURLs((current) => ({ ...current, [reference.assetId]: reference.url }));
    updateDraft({ ...draft, references: [...draft.references, reference.assetId] });
    setChooserOpen(false);
    setSaveState('idle');
  };
  // A canvas layer is rasterised through the shared export path, then uploaded; the reference is the
  // uploaded input asset, never a canvas URL.
  const chooseLayer = async (node: CanvasNode) => {
    if (refsAtLimit || layerBusy) return;
    setUploadFailed(false);
    setLayerBusy(node.id);
    try {
      const captured: { value: { id: string; url: string } | null } = { value: null };
      await exportAndUploadSnapshot({
        flush: () => flushPendingEdits(sessionId),
        exportPicture: () => exportCanvasToBlob([node], schema.canvas, {
          backgroundColor: schema.canvas.frontend.export.artboard_background_color,
        }),
        upload: async (picture) => {
          captured.value = await uploadWorkspaceInputAssetResult(schema, picture, snapshotFilename(schema));
          return captured.value.url;
        },
        maximumBytes: schema.runtime.input_assets.upload_max_bytes,
      });
      if (!captured.value) throw new Error('layer upload returned no asset');
      const uploaded = captured.value;
      setLocalURLs((current) => ({ ...current, [uploaded.id]: uploaded.url }));
      updateDraft({ ...draft, references: [...draft.references, uploaded.id] });
      setLayerChooserOpen(false);
      setSaveState('idle');
    } catch {
      setUploadFailed(true);
      setSaveState('failed');
    } finally {
      setLayerBusy(null);
    }
  };
  const remove = (index: number) => updateDraft({ ...draft, references: draft.references.filter((_, current) => current !== index) });
  const move = (index: number, delta: -1 | 1) => {
    const next = index + delta;
    if (next < 0 || next >= draft.references.length) return;
    const references = [...draft.references];
    [references[index], references[next]] = [references[next], references[index]];
    updateDraft({ ...draft, references });
  };
  return (
    <section className="shrink-0 border-b border-border px-3 py-3" data-testid="style-lock-panel">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold text-foreground">{text('title')}</h2>
        {saveState === 'saved' && !state.dirty ? <span className="text-[11px] text-muted-foreground" role="status">{text('saved')}</span> : null}
      </div>
      <p className="mt-1 text-xs leading-5 text-muted-foreground">{text('description')}</p>
      {lockState.failed ? (
        <div className="mt-2 flex items-center justify-between gap-2 rounded-md border border-destructive/40 px-2 py-1.5 text-xs" role="alert">
          <span>{text('unavailable')} · {text('load_failed')}</span>
          <button type="button" className="font-medium underline" onClick={() => void lockState.refresh()}>{text('retry')}</button>
        </div>
      ) : null}
      <label className="mt-3 flex items-center gap-2 text-xs font-medium">
        <input
          type="checkbox"
          checked={draft.enabled}
          disabled={lockState.busy || lockState.failed}
          onChange={(event) => { updateDraft({ ...draft, enabled: event.target.checked }); setSaveState('idle'); }}
          data-testid="style-lock-enabled"
        />
        {text('enabled')}
      </label>
      <label className="mt-3 block text-xs font-medium" htmlFor="style-lock-text">{text('text_label')}</label>
      <textarea
        id="style-lock-text"
        value={draft.text}
        maxLength={maximumText}
        disabled={lockState.busy || lockState.failed}
        placeholder={text('text_placeholder')}
        onChange={(event) => { updateDraft({ ...draft, text: event.target.value }); setSaveState('idle'); }}
        rows={4}
        className="mt-1 w-full resize-y rounded-md border border-border bg-background px-2 py-1.5 text-sm leading-5 outline-none focus-visible:ring-1 focus-visible:ring-primary"
        data-testid="style-lock-text"
      />
      <div className="mt-1 flex items-center justify-between text-xs">
        <span className={overLimit ? 'text-destructive' : 'text-muted-foreground'} data-testid="style-lock-counter">
          {schemaCountMessage(schema, 'ui.studio.style_lock.counter', state.count, { count: state.count, maximum: maximumText })}
        </span>
        {overLimit ? <span className="text-destructive">{text('too_long')}</span> : null}
      </div>
      <div className="mt-3 flex items-center justify-between gap-2">
        <span className="text-xs font-medium">{text('references_label')}</span>
        <span className="text-[11px] text-muted-foreground">{formatSchemaText(text('reference_limit'), { maximum: maximumReferences })}</span>
      </div>
      {draft.references.length > 0 ? (
        <ul className="mt-2 flex flex-wrap gap-1.5" data-testid="style-lock-references">
          {draft.references.map((assetId, index) => (
            <li key={`${assetId}-${index}`} className="group relative size-14 overflow-hidden rounded-md border border-border bg-muted">
              {previewFor(assetId) ? <img src={previewFor(assetId)} alt={labelOf(index + 1)} className="size-full object-cover" /> : null}
              <span className="sr-only">{labelOf(index + 1)}</span>
              <span className="absolute inset-x-0 bottom-0 flex justify-center gap-0.5 bg-black/60 py-0.5 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
                <button type="button" aria-label={text('move_up')} disabled={index === 0} onClick={() => move(index, -1)} className="text-white disabled:opacity-40"><ArrowUp className="size-3" /></button>
                <button type="button" aria-label={text('move_down')} disabled={index === draft.references.length - 1} onClick={() => move(index, 1)} className="text-white disabled:opacity-40"><ArrowDown className="size-3" /></button>
                <button type="button" aria-label={text('remove_reference')} onClick={() => remove(index)} className="text-white"><X className="size-3" /></button>
              </span>
            </li>
          ))}
        </ul>
      ) : null}
      <div className="mt-2 flex flex-wrap gap-1.5">
        <input ref={fileInput} type="file" accept={schema.chat.frontend.image_file_accept} className="sr-only" onChange={(event) => { const file = event.target.files?.[0]; if (file) void upload(file); }} />
        <button type="button" disabled={refsAtLimit || lockState.busy} onClick={() => fileInput.current?.click()} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50">
          <Upload className="size-3.5" aria-hidden="true" />{text('upload_reference')}
        </button>
        <button type="button" disabled={refsAtLimit || lockState.busy} onClick={() => setChooserOpen((open) => !open)} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50">
          <ImagePlus className="size-3.5" aria-hidden="true" />{text('choose_session_reference')}
        </button>
        <button type="button" disabled={refsAtLimit || lockState.busy} onClick={() => setLayerChooserOpen((open) => !open)} aria-expanded={layerChooserOpen} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50">
          <Layers className="size-3.5" aria-hidden="true" />{text('canvas_layers')}
        </button>
      </div>
      {chooserOpen ? (
        <div className="mt-2 rounded-md border border-border p-1.5" data-testid="style-lock-session-choices">
          {choices.length === 0 ? <p className="px-1 py-1 text-xs text-muted-foreground">{text('no_session_references')}</p> : (
            <ul className="flex flex-wrap gap-1.5">
              {choices.map((choice) => (
                <li key={choice.assetId}>
                  <button type="button" disabled={draft.references.includes(choice.assetId)} onClick={() => choose(choice)} className="size-12 overflow-hidden rounded border border-border disabled:opacity-40" aria-label={choice.label} data-testid={`style-lock-session-reference-${choice.assetId}`}>
                    <img src={previewAssetUrl(choice.url) || safePreviewURL(choice.url)} alt={text('references_label')} className="size-full object-cover" />
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      ) : null}
      {layerChooserOpen ? (
        <div className="mt-2 rounded-md border border-border p-1.5" data-testid="style-lock-canvas-choices">
          {layers.length === 0 ? <p className="px-1 py-1 text-xs text-muted-foreground">{text('no_canvas_layers')}</p> : (
            <ul className="flex flex-col gap-1">
              {layers.map((layer) => (
                <li key={layer.id}>
                  <button type="button" disabled={layerBusy !== null} onClick={() => void chooseLayer(layer)} data-testid={`style-lock-canvas-layer-${layer.id}`}
                    className="flex w-full items-center justify-between gap-2 rounded px-1 py-1 text-left text-xs hover:bg-muted disabled:opacity-50">
                    <span className="truncate">{layer.name || schemaText(schema, `ui.canvas.layers.type_${layer.type}`)}</span>
                    {layerBusy === layer.id ? <span className="shrink-0 text-muted-foreground">{text('upload_reference')}…</span> : null}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      ) : null}
      <p className="mt-2 text-[11px] leading-4 text-muted-foreground">{text('cost_note')}</p>
      {lockState.saveError || saveState === 'failed' || uploadFailed ? <p className="mt-2 text-xs text-destructive" role="alert">{uploadFailed ? text('upload_failed') : lockState.saveError?.message || text('save_failed')}</p> : null}
      <div className="mt-3 flex justify-end gap-1.5">
        <button type="button" disabled={lockState.busy || !state.dirty} onClick={() => setDraftState({ draft: { ...followed.base, references: [...followed.base.references] }, base: followed.base })} className="rounded-md px-2 py-1 text-xs text-muted-foreground hover:text-foreground disabled:opacity-50">{text('revert')}</button>
        <button type="button" disabled={lockState.busy || !state.dirty || overLimit} onClick={() => void save()} className="rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground disabled:opacity-50">{text('save')}</button>
      </div>
    </section>
  );
}
