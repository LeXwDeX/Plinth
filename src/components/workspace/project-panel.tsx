'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Loader2, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useProjects } from '@/hooks/use-projects';
import { useStyleLock } from '@/hooks/use-style-lock';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import type { ProjectAsset, ProjectConventions, ProjectReference, ProjectStyleGuide } from '@/lib/projects/browser-client';
import { safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { withErrorReference } from '@/lib/problem-reference';
import { projectContract, schemaText } from '@/lib/product-schema';
import { useChatStore, type ChatMessage } from '@/store/chat-store';
import { PromoteAssetDialog } from '@/components/workspace/promote-asset-dialog';

const NO_MESSAGES: readonly ChatMessage[] = [];

interface SessionPicture {
  reference: ProjectReference;
  url: string;
  label: string;
}

/** Finished assistant pictures of the session that can be promoted. */
function sessionPictures(messages: readonly ChatMessage[], label: (index: number) => string): SessionPicture[] {
  const result: SessionPicture[] = [];
  const seen = new Set<string>();
  for (const message of messages) {
    if (message.role !== 'assistant' || message.isPending || message.isStreaming || !message.images) continue;
    for (const [index, image] of message.images.entries()) {
      const attachment = message.attachments?.find((item) => item.kind === 'image' && item.imageSlot === index + 1);
      const assetId = attachment?.assetId ?? '';
      if (!assetId || !image.url || seen.has(assetId)) continue;
      seen.add(assetId);
      result.push({
        reference: attachment?.taskId
          ? { task_id: attachment.taskId, asset_id: assetId }
          : { input_asset_id: assetId },
        url: image.url,
        label: label(result.length + 1),
      });
    }
  }
  return result;
}

function referenceURL(schema: ReturnType<typeof useProductSchema>['schema'], reference: ProjectReference): string {
  if (reference.input_asset_id) {
    const route = schema.runtime.http.routes.asset_item;
    return route ? route.replace('{assetID}', encodeURIComponent(reference.input_asset_id)) : '';
  }
  if (reference.task_id && reference.asset_id) {
    const template = schema.runtime.http.routes.task_asset;
    return template
      ? template.replace('{taskID}', encodeURIComponent(reference.task_id)).replace('{assetID}', encodeURIComponent(reference.asset_id))
      : '';
  }
  return '';
}

/**
 * #240: the 项目 tab of the right panel. It reads the open session's project:
 * style guide and palette, production conventions and the asset list, with
 * save/delete confirmations and the promotion entry for finished session
 * pictures and the session style lock. All copy comes from content.messages.
 */
export function ProjectPanel({ sessionId }: { sessionId: string | null }) {
  const { schema } = useProductSchema();
  const { previewAssetUrl } = useAssetURLRewriter();
  const projects = useProjects(sessionId);
  const projectSchema = projectContract(schema);
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const lockState = useStyleLock(chat, sessionId);
  const messages = useChatStore((state) => (sessionId ? state.messagesBySession?.[sessionId] ?? NO_MESSAGES : NO_MESSAGES));
  const projectId = projects.activeProjectId;
  const text = (key: string) => schemaText(schema, `ui.project.${key}`);
  const [guide, setGuide] = useState<ProjectStyleGuide | null>(null);
  const [conventions, setConventions] = useState<ProjectConventions | null>(null);
  const [assets, setAssets] = useState<ProjectAsset[]>([]);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);
  // Style guide draft.
  const [draftEnabled, setDraftEnabled] = useState(false);
  const [draftText, setDraftText] = useState('');
  const [draftPalette, setDraftPalette] = useState('');
  // Conventions draft (the public projection names fields, not their labels).
  const [draftBrief, setDraftBrief] = useState('{}');
  const [draftTexts, setDraftTexts] = useState<Record<string, string>>({});
  const [savingGuide, setSavingGuide] = useState(false);
  const [savingConventions, setSavingConventions] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [confirmingDelete, setConfirmingDelete] = useState<string | null>(null);
  const [promotion, setPromotion] = useState<SessionPicture | null>(null);
  const [promoting, setPromoting] = useState(false);
  const [promotionError, setPromotionError] = useState('');
  const [confirmStyle, setConfirmStyle] = useState(false);
  const [promotingStyle, setPromotingStyle] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    if (!projectId) {
      setGuide(null); setConventions(null); setAssets([]); setFailed(false);
      return;
    }
    setLoading(true);
    setFailed(false);
    try {
      const [nextGuide, nextConventions, nextAssets] = await Promise.all([
        projects.client.getStyleGuide(projectId, signal),
        projects.client.getConventions(projectId, signal),
        projects.client.listAssets(projectId, signal),
      ]);
      if (signal?.aborted) return;
      setGuide(nextGuide);
      setConventions(nextConventions);
      setAssets(nextAssets);
      setDraftEnabled(nextGuide.enabled);
      setDraftText(nextGuide.text);
      setDraftPalette(nextGuide.palette.join(', '));
      setDraftBrief(JSON.stringify(nextConventions.brief ?? {}, null, 2));
      setDraftTexts(nextConventions.texts ?? {});
    } catch {
      if (!signal?.aborted) setFailed(true);
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, [projectId, projects.client]);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const maximumText = projectSchema?.style_guide.maximum_text_characters ?? 0;
  const maximumReferences = projectSchema?.style_guide.maximum_references ?? 0;
  const maximumPalette = projectSchema?.style_guide.maximum_palette_colors ?? 0;
  const dirtyGuide = guide !== null && (
    draftEnabled !== guide.enabled
    || draftText.trim() !== guide.text
    || draftPalette.split(',').map((color) => color.trim()).filter(Boolean).join(',') !== guide.palette.join(',')
  );
  const dirtyConventions = conventions !== null && (
    draftBrief !== JSON.stringify(conventions.brief ?? {}, null, 2)
    || Object.entries(draftTexts).some(([name, value]) => value !== (conventions.texts[name] ?? ''))
  );
  const pictures = useMemo(
    () => sessionPictures(messages, (index) => schemaText(schema, 'ui.studio.strip.candidate_n').replace('{index}', String(index))),
    [messages, schema],
  );
  if (!projectSchema) return null;
  if (!projectId) {
    return <p className="px-3 py-3 text-xs leading-5 text-muted-foreground" data-testid="project-panel-empty">{text('no_project_hint')}</p>;
  }

  const palette = draftPalette.split(',').map((color) => color.trim()).filter(Boolean);
  // A failed save reports the server's reason (the Schema copy for a refused
  // guide or conventions), falling back to a save-specific line instead of the
  // load failure text (#240 review).
  const saveFailure = (error: unknown, fallbackKey: string) => (
    error instanceof Error && error.message.trim()
      ? withErrorReference(error.message, error)
      : text(fallbackKey)
  );
  const saveGuide = async () => {
    if (!guide || !dirtyGuide || savingGuide || palette.length > maximumPalette || draftText.trim().length > maximumText) return;
    setSavingGuide(true);
    try {
      const saved = await projects.client.setStyleGuide(projectId, {
        enabled: draftEnabled, text: draftText.trim(), palette, references: guide.references,
      });
      setGuide(saved);
      setPaletteMirror(saved);
      toast.success(text('style_saved'));
    } catch (error) {
      toast.error(saveFailure(error, 'style_save_failed'));
    } finally {
      setSavingGuide(false);
    }
  };
  const setPaletteMirror = (saved: ProjectStyleGuide) => {
    setDraftEnabled(saved.enabled);
    setDraftText(saved.text);
    setDraftPalette(saved.palette.join(', '));
  };
  const saveConventions = async () => {
    if (!conventions || !dirtyConventions || savingConventions) return;
    let brief: Record<string, unknown>;
    try {
      const parsed = JSON.parse(draftBrief || '{}');
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('invalid');
      brief = parsed as Record<string, unknown>;
    } catch {
      toast.error(text('conventions_save_failed'));
      return;
    }
    setSavingConventions(true);
    try {
      const saved = await projects.client.setConventions(projectId, { brief, texts: draftTexts });
      setConventions(saved);
      setDraftBrief(JSON.stringify(saved.brief ?? {}, null, 2));
      setDraftTexts(saved.texts ?? {});
      toast.success(text('conventions_saved'));
    } catch (error) {
      toast.error(saveFailure(error, 'conventions_save_failed'));
    } finally {
      setSavingConventions(false);
    }
  };
  const removeAsset = async (assetId: string) => {
    setDeleting(assetId);
    try {
      await projects.client.deleteAsset(projectId, assetId);
      setAssets((current) => current.filter((asset) => asset.id !== assetId));
      setConfirmingDelete(null);
    } catch {
      toast.error(text('asset_delete_failed'));
    } finally {
      setDeleting(null);
    }
  };
  const promote = async (input: { kind: string; name: string; description: string }) => {
    if (!promotion) return;
    setPromoting(true);
    setPromotionError('');
    try {
      const created = await projects.client.promoteAsset(projectId, {
        source: promotion.reference, kind: input.kind, name: input.name, description: input.description || undefined,
      });
      setAssets((current) => [created, ...current]);
      setPromotion(null);
      toast.success(text('promotion_saved'));
    } catch (error) {
      setPromotionError(error instanceof Error && error.message ? error.message : text('promote_failed'));
    } finally {
      setPromoting(false);
    }
  };
  const promoteStyle = async () => {
    // Without the loaded guide there is no palette to preserve; promoting
    // then would overwrite the project's guide with an empty palette (#240
    // review). The button stays disabled until the guide is readable.
    if (!lockState.lock || promotingStyle || !guide) return;
    setPromotingStyle(true);
    try {
      const references: ProjectReference[] = lockState.lock.references.map((assetID) => ({ input_asset_id: assetID }));
      const saved = await projects.client.setStyleGuide(projectId, {
        enabled: true,
        text: lockState.lock.text,
        palette: guide.palette,
        references: references.slice(0, maximumReferences),
      });
      setGuide(saved);
      setPaletteMirror(saved);
      setConfirmStyle(false);
      toast.success(text('style_saved'));
    } catch {
      toast.error(text('promote_style_failed'));
    } finally {
      setPromotingStyle(false);
    }
  };
  const styleSourceHint = lockState.lock.source === 'off'
    ? text('style_source_off_hint')
    : lockState.lock.source === 'session'
      ? text('style_source_session_hint')
      : text('inherit_hint').replace('{project}', projects.activeProject?.name ?? '');
  const labelOfKind = (kind: string) => {
    const declared = projectSchema?.assets.kinds.find((candidate) => candidate.id === kind);
    return declared ? schemaText(schema, declared.label_key) : kind;
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-3 py-3" data-testid="project-panel">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="truncate text-sm font-semibold">{projects.activeProject?.name ?? text('title')}</h2>
          <p className="mt-1 text-[11px] leading-4 text-muted-foreground">{styleSourceHint}</p>
        </div>
        {loading ? <Loader2 aria-hidden className="mt-0.5 size-3.5 shrink-0 animate-spin text-muted-foreground" /> : null}
      </div>
      {failed ? (
        <div role="alert" className="flex items-center justify-between gap-2 rounded-md border border-destructive/40 px-2 py-1.5 text-xs">
          <span>{text('load_failed')}</span>
          <button type="button" className="font-medium underline" onClick={() => void load()}>{text('save')}</button>
        </div>
      ) : null}

      <section data-testid="project-style-section">
        <h3 className="text-xs font-semibold">{text('style_section')}</h3>
        <label className="mt-2 flex items-center gap-2 text-xs font-medium">
          <input type="checkbox" checked={draftEnabled} disabled={loading || savingGuide} onChange={(event) => setDraftEnabled(event.target.checked)} data-testid="project-style-enabled" />
          {text('style_enabled')}
        </label>
        <label className="mt-2 block text-xs font-medium" htmlFor="project-style-text">{text('style_text')}</label>
        <textarea id="project-style-text" value={draftText} maxLength={maximumText} disabled={loading || savingGuide} rows={3} onChange={(event) => setDraftText(event.target.value)} className="mt-1 w-full resize-y rounded-md border border-border bg-background px-2 py-1.5 text-xs outline-none focus-visible:ring-1 focus-visible:ring-primary" data-testid="project-style-text" />
        <label className="mt-2 block text-xs font-medium" htmlFor="project-style-palette">{text('palette')}</label>
        <input id="project-style-palette" value={draftPalette} disabled={loading || savingGuide} onChange={(event) => setDraftPalette(event.target.value)} placeholder={text('palette_placeholder')} className="mt-1 w-full rounded-md border border-border bg-background px-2 py-1.5 text-xs outline-none focus-visible:ring-1 focus-visible:ring-primary" data-testid="project-style-palette" />
        <div className="mt-2 flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
          <span>{text('references')} · {schemaText(schema, 'ui.project.reference_limit').replace('{maximum}', String(maximumReferences))}</span>
          <span>{palette.length}/{maximumPalette}</span>
        </div>
        {guide && guide.references.length > 0 ? (
          <ul className="mt-1.5 flex flex-wrap gap-1.5" data-testid="project-style-references">
            {guide.references.map((reference, index) => {
              const raw = referenceURL(schema, reference);
              return (
                <li key={`${raw}-${index}`} className="size-12 overflow-hidden rounded border border-border bg-muted">
                  {raw ? <img src={previewAssetUrl(raw) || safePreviewURL(raw)} alt={text('references')} className="size-full object-cover" /> : null}
                </li>
              );
            })}
          </ul>
        ) : null}
        <div className="mt-2 flex justify-end">
          <Action type="button" size="sm" disabled={loading || savingGuide || !dirtyGuide || palette.length > maximumPalette || draftText.trim().length > maximumText} onClick={() => void saveGuide()} data-testid="project-style-save">{text('save')}</Action>
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          <Action type="button" variant="ghost" size="xs" disabled={promotingStyle || !guide || !lockState.lock || (!lockState.lock.text.trim() && lockState.lock.references.length === 0)} onClick={() => setConfirmStyle(true)} data-testid="project-promote-style">{text('promote_style')}</Action>
        </div>
        {confirmStyle ? (
          <div className="mt-2 rounded-md border border-border p-2" data-testid="project-promote-style-confirm-dialog">
            <p className="text-xs leading-5 text-muted-foreground">{text('promote_style_confirm')}</p>
            <div className="mt-2 flex justify-end gap-1.5">
              <Action type="button" variant="ghost" size="xs" disabled={promotingStyle} onClick={() => setConfirmStyle(false)}>{text('cancel')}</Action>
              <Action type="button" size="xs" disabled={promotingStyle} onClick={() => void promoteStyle()} data-testid="project-promote-style-confirm">{text('confirm')}</Action>
            </div>
          </div>
        ) : null}
      </section>

      <section data-testid="project-conventions-section">
        <h3 className="text-xs font-semibold">{text('conventions_section')}</h3>
        <label className="mt-2 block text-xs font-medium" htmlFor="project-conventions-brief">{text('brief_json')}</label>
        <textarea id="project-conventions-brief" value={draftBrief} disabled={loading || savingConventions} rows={3} onChange={(event) => setDraftBrief(event.target.value)} className="mt-1 w-full resize-y rounded-md border border-border bg-background px-2 py-1.5 font-mono text-[11px] outline-none focus-visible:ring-1 focus-visible:ring-primary" data-testid="project-conventions-brief" />
        {(projectSchema?.conventions.texts ?? []).map((name) => (
          <label key={name} className="mt-1.5 block text-xs font-medium">
            {name}
            <input value={draftTexts[name] ?? ''} disabled={loading || savingConventions} onChange={(event) => setDraftTexts((current) => ({ ...current, [name]: event.target.value }))} className="mt-1 w-full rounded-md border border-border bg-background px-2 py-1.5 text-xs outline-none focus-visible:ring-1 focus-visible:ring-primary" data-testid={`project-conventions-text-${name}`} />
          </label>
        ))}
        <div className="mt-2 flex justify-end">
          <Action type="button" size="sm" disabled={loading || savingConventions || !dirtyConventions} onClick={() => void saveConventions()} data-testid="project-conventions-save">{text('save')}</Action>
        </div>
      </section>

      <section data-testid="project-assets-section">
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-xs font-semibold">{text('assets_section')}</h3>
          <span className="text-[11px] text-muted-foreground">{schemaText(schema, 'ui.project.asset_count').replace('{count}', String(assets.length))}</span>
        </div>
        {assets.length === 0 ? <p className="mt-1 text-xs text-muted-foreground">{text('asset_empty')}</p> : (
          <ul className="mt-2 flex flex-col gap-1.5" data-testid="project-asset-list">
            {assets.map((asset) => (
              <li key={asset.id} className="flex items-start gap-2 rounded-md border border-border p-1.5" data-testid={`project-asset-${asset.id}`}>
                {asset.images.length > 0 ? (() => {
                  const raw = referenceURL(schema, asset.images[0]);
                  return <span className="size-10 shrink-0 overflow-hidden rounded border border-border bg-muted">{raw ? <img src={previewAssetUrl(raw) || safePreviewURL(raw)} alt={asset.name} className="size-full object-cover" /> : null}</span>;
                })() : null}
                <div className="min-w-0 flex-1">
                  <p className="truncate text-xs font-medium">{asset.name} <span className="font-normal text-muted-foreground">· {labelOfKind(asset.kind)}</span></p>
                  {asset.description ? <p className="mt-0.5 line-clamp-2 text-[11px] leading-4 text-muted-foreground">{asset.description}</p> : null}
                  {asset.models.length > 0 ? <p className="mt-0.5 text-[11px] text-muted-foreground">{text('asset_models')} · {asset.models.length}</p> : null}
                </div>
                {confirmingDelete === asset.id ? (
                  <div className="flex shrink-0 flex-col items-end gap-1" data-testid={`project-asset-delete-confirm-${asset.id}`}>
                    <span className="max-w-40 text-[10px] leading-3 text-muted-foreground">{text('asset_delete_confirm')}</span>
                    <span className="flex gap-1">
                      <Action type="button" variant="ghost" size="xs" onClick={() => setConfirmingDelete(null)}>{text('cancel')}</Action>
                      <Action type="button" variant="outline" size="xs" disabled={deleting === asset.id} className="text-destructive" onClick={() => void removeAsset(asset.id)} data-testid={`project-asset-delete-confirm-button-${asset.id}`}>{text('delete_asset')}</Action>
                    </span>
                  </div>
                ) : (
                  <button type="button" aria-label={text('delete_asset')} className="shrink-0 rounded p-1 text-muted-foreground hover:bg-muted hover:text-destructive" onClick={() => setConfirmingDelete(asset.id)} data-testid={`project-asset-delete-${asset.id}`}>
                    <Trash2 aria-hidden className="size-3.5" />
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section data-testid="project-promotion-section">
        <h3 className="text-xs font-semibold">{text('session_images')}</h3>
        <p className="mt-1 text-[11px] leading-4 text-muted-foreground">{text('promote_confirm')}</p>
        {pictures.length === 0 ? <p className="mt-1 text-xs text-muted-foreground">{text('asset_empty')}</p> : (
          <ul className="mt-2 flex flex-wrap gap-1.5">
            {pictures.map((picture, index) => (
              <li key={`${picture.reference.input_asset_id ?? picture.reference.asset_id}-${index}`} className="relative size-14 overflow-hidden rounded-md border border-border bg-muted">
                <img src={previewAssetUrl(picture.url) || safePreviewURL(picture.url)} alt={picture.label} className="size-full object-cover" />
                <button type="button" className="absolute inset-x-0 bottom-0 bg-black/60 px-0.5 py-0.5 text-[9px] leading-3 text-white" onClick={() => { setPromotionError(''); setPromotion(picture); }} data-testid={`project-promote-picture-${index + 1}`}>
                  {text('promote')}
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <PromoteAssetDialog
        open={promotion !== null}
        source={promotion?.reference ?? null}
        previewURL={promotion ? previewAssetUrl(promotion.url) || safePreviewURL(promotion.url) : undefined}
        busy={promoting}
        error={promotionError}
        onCancel={() => { setPromotion(null); setPromotionError(''); }}
        onConfirm={(input) => void promote(input)}
      />
    </div>
  );
}