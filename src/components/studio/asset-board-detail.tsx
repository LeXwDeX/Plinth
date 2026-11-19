'use client';

import { Box, Image as ImageIcon } from 'lucide-react';

import { Action } from '@/components/ui/button';
import { SideDrawer } from '@/components/ui/side-drawer';
import { ModelViewer3D } from '@/components/viewer/model-viewer-3d';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import {
  boardArtifacts,
  boardCheckGroups,
  boardSlotKey,
  boardSteps,
  boardTaskID,
  checkActualText,
  checkStatusKey,
  coverAsset,
  inputAssetPath,
  model3DAsset,
  regionText,
  taskAssetPath,
} from '@/lib/asset-lists/board';
import type { AssetListBoardItem } from '@/lib/asset-lists/client';
import { formatDateTime } from '@/lib/i18n/format';
import { activeLocale } from '@/lib/i18n/locale';
import { formatFileSize } from '@/lib/size-text';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { useInlineModelPreviewStore } from '@/store/inline-model-preview-store';

export interface AssetBoardDetailProps {
  item: AssetListBoardItem;
  schema: ProductSchema;
  onClose: () => void;
}

/**
 * #329: one board item's detail drawer. It shows the cover (or the live 3D
 * viewer, through the same single slot the grid uses), every acceptance
 * criterion with its actual value and threshold, the stored artifacts with
 * their downloads, the crop provenance (#331) and the latest recorded
 * decision. The snapshot's facts are shown as recorded; nothing here decides.
 */
export function AssetBoardDetail({ item, schema, onClose }: AssetBoardDetailProps) {
  const { previewAssetUrl, proxyAssetUrl } = useAssetURLRewriter();
  const text = (key: string) => schemaText(schema, key);
  const slot = boardSlotKey(item.id);
  const live = useInlineModelPreviewStore((state) => state.taskId === slot);
  const taskID = boardTaskID(item);
  const model = model3DAsset(item);
  const cover = coverAsset(item);
  const coverURL = previewAssetUrl(taskAssetPath(schema, taskID, cover));
  const groups = boardCheckGroups(item);
  const artifacts = boardArtifacts(item);
  const steps = boardSteps(item);
  const selfCheck = item.review?.self_check;
  const selfCheckLabel = selfCheck?.status
    ? schema.chat.self_check?.statuses?.find((status) => status.role === selfCheck.status)?.label || selfCheck.status
    : '';
  const stateLabel = schema.asset_list?.item_states.find((state) => state.id === item.state)?.label || item.state;
  const sourceURL = item.source?.source_input_asset_id
    ? previewAssetUrl(inputAssetPath(schema, item.source.source_input_asset_id))
    : '';
  const region = item.region ? regionText(item.region) : null;
  const decision = item.latest_review ?? null;
  // One list separator per locale, so the English copy does not carry a Chinese 、.
  const listSeparator = activeLocale(schema).startsWith('zh') ? '、' : ', ';
  const reasonLabels = (ids: readonly string[]) => ids
    .map((id) => schema.asset_list?.review?.return_reasons.find((reason) => reason.id === id)?.label || id)
    .join(listSeparator);
  const verdictLabel = decision
    ? decision.verdict === 'approved'
      ? text('ui.asset_list.board.verdict_approved')
      : decision.verdict === 'returned'
        ? text('ui.asset_list.board.verdict_returned')
        : decision.verdict
    : '';

  return (
    <SideDrawer
      title={item.name}
      closeLabel={text('ui.asset_list.board.detail_close')}
      onClose={onClose}
      testId="asset-board-detail"
    >
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4 text-xs" data-testid="asset-board-detail-body">
        <section aria-label={text('ui.asset_list.board.detail_preview')} className="flex flex-col gap-2">
          {live && model ? (
            <ModelViewer3D
              src={taskAssetPath(schema, taskID, model)}
              alt={item.name}
              autoRotate
              className="max-h-[min(60vh,24rem)]"
            />
          ) : coverURL ? (
            <img
              src={coverURL}
              alt={item.name}
              className="max-h-[min(60vh,24rem)] w-full rounded-lg border border-border bg-muted object-contain"
              data-testid="asset-board-detail-cover"
            />
          ) : (
            <p className="flex items-center gap-2 rounded-lg border border-border bg-muted/40 p-4 text-muted-foreground" data-testid="asset-board-detail-no-cover">
              <Box aria-hidden className="size-4" />
              {text('ui.asset_list.board.no_cover')}
            </p>
          )}
          {model ? (
            <Action
              type="button"
              variant="outline"
              size="sm"
              className="min-h-11 self-start"
              aria-pressed={live}
              onClick={() => {
                const previews = useInlineModelPreviewStore.getState();
                if (live) previews.close(slot);
                else previews.open(slot);
              }}
              data-testid="asset-board-detail-3d"
            >
              {text(live ? 'ui.asset_list.board.preview_close' : 'ui.asset_list.board.preview_3d')}
            </Action>
          ) : null}
        </section>

        <section className="flex flex-wrap items-baseline gap-x-3 gap-y-1" data-testid="asset-board-detail-facts">
          <span className="rounded-sm bg-muted px-1.5 py-0.5" data-testid="asset-board-detail-state">{stateLabel}</span>
          <span className="text-muted-foreground">
            {formatSchemaText(text('ui.asset_list.board.round'), { round: item.round })}
          </span>
          <span className="ml-auto tabular-nums text-muted-foreground" data-testid="asset-board-detail-steps">
            {steps
              ? steps.cap && steps.cap > 0
                ? formatSchemaText(text('ui.asset_list.board.steps_capped'), { used: steps.used, cap: steps.cap })
                : formatSchemaText(text('ui.asset_list.board.steps'), { steps: steps.used })
              : text('ui.asset_list.board.steps_unknown')}
          </span>
          {steps && !(steps.cap && steps.cap > 0) && steps.model3d > 0 ? (
            <span className="w-full text-right tabular-nums text-muted-foreground">
              {formatSchemaText(text('ui.asset_list.board.steps_3d'), { steps: steps.model3d })}
            </span>
          ) : null}
        </section>

        <section className="flex flex-col gap-2" data-testid="asset-board-detail-checks">
          <h3 className="text-xs font-semibold">{text('ui.asset_list.board.detail_checks')}</h3>
          {groups.length === 0 ? (
            <p className="text-muted-foreground" data-testid="asset-board-detail-checks-none">
              {text('ui.asset_list.board.checks_none')}
            </p>
          ) : groups.map((group) => (
            <div key={group.kind} className="flex flex-col gap-1" data-testid={`asset-board-checks-${group.kind}`}>
              <h4 className="text-[11px] font-medium text-foreground/70">
                {text(group.kind === '3d' ? 'ui.asset_list.board.checks_3d' : 'ui.asset_list.board.checks_2d')}
              </h4>
              <ul className="flex flex-col gap-1">
                {group.checks.map((check, index) => (
                  <li
                    key={`${check.check}-${index}`}
                    className="rounded-md border border-border px-2 py-1.5"
                    data-testid={`asset-board-check-${group.kind}-${check.check}`}
                    data-status={check.status}
                  >
                    <p className="flex flex-wrap items-baseline gap-x-2">
                      <span className="font-medium">{check.check}</span>
                      <span className={check.status === 'fail' ? 'text-destructive' : 'text-muted-foreground'}>
                        {text(checkStatusKey(check.status))}
                      </span>
                    </p>
                    {checkActualText(check.actual) ? (
                      <p className="mt-0.5 tabular-nums text-muted-foreground">
                        {formatSchemaText(text('ui.asset_list.board.detail_actual'), { actual: checkActualText(check.actual) })}
                      </p>
                    ) : null}
                    {check.budget ? (
                      <p className="mt-0.5 tabular-nums text-muted-foreground">
                        {formatSchemaText(text('ui.asset_list.board.detail_budget'), { budget: check.budget })}
                      </p>
                    ) : null}
                    {check.status !== 'pass' && check.detail ? (
                      <p className="mt-0.5 text-[11px] leading-4 text-muted-foreground [overflow-wrap:anywhere]">{check.detail}</p>
                    ) : null}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </section>

        {selfCheck ? (
          <section className="flex flex-col gap-1" data-testid="asset-board-detail-self-check">
            <h3 className="text-xs font-semibold">{text('ui.asset_list.board.detail_self_check')}</h3>
            {selfCheckLabel ? <p className="font-medium">{selfCheckLabel}</p> : null}
            {selfCheck.summary ? <p className="leading-5 text-muted-foreground [overflow-wrap:anywhere]">{selfCheck.summary}</p> : null}
            {selfCheck.remaining && selfCheck.remaining.length > 0 ? (
              <p className="leading-5 text-muted-foreground [overflow-wrap:anywhere]">
                {formatSchemaText(text('ui.asset_list.board.detail_remaining'), { items: selfCheck.remaining.join(listSeparator) })}
              </p>
            ) : null}
          </section>
        ) : null}

        <section className="flex flex-col gap-1" data-testid="asset-board-detail-artifacts">
          <h3 className="text-xs font-semibold">{text('ui.asset_list.board.detail_artifacts')}</h3>
          {artifacts.length === 0 ? (
            <p className="text-muted-foreground" data-testid="asset-board-detail-artifacts-empty">
              {text('ui.asset_list.board.no_artifacts')}
            </p>
          ) : (
            <ul className="flex flex-col gap-1">
              {artifacts.map((artifact) => {
                const path = taskAssetPath(schema, taskID, artifact.assetID);
                const downloadURL = proxyAssetUrl(path);
                return (
                  <li
                    key={artifact.assetID}
                    className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md border border-border px-2 py-1.5"
                    data-testid={`asset-board-artifact-${artifact.assetID}`}
                  >
                    {artifact.mediaType.startsWith('image/') ? (
                      <ImageIcon aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
                    ) : (
                      <Box aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
                    )}
                    <span className="min-w-0 font-medium [overflow-wrap:anywhere]">{artifact.name}</span>
                    <span className="min-w-0 text-[11px] text-muted-foreground [overflow-wrap:anywhere]">
                      {[artifact.mediaType, artifact.size > 0 ? formatFileSize(artifact.size) : ''].filter(Boolean).join(' · ')}
                    </span>
                    {downloadURL ? (
                      <a
                        href={downloadURL}
                        download
                        className="ml-auto flex min-h-11 items-center rounded-md px-2 text-xs font-medium text-primary underline underline-offset-2 hover:bg-muted focus-visible:outline-2 focus-visible:outline-ring"
                        data-testid={`asset-board-artifact-download-${artifact.assetID}`}
                      >
                        {text('ui.asset_list.board.detail_download')}
                      </a>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          )}
        </section>

        {item.source || item.region ? (
          <section className="flex flex-col gap-2" data-testid="asset-board-detail-crop">
            <h3 className="text-xs font-semibold">{text('ui.asset_list.board.detail_crop')}</h3>
            {sourceURL ? (
              <span className="relative block w-fit max-w-full self-center">
                <img
                  src={sourceURL}
                  alt={text('ui.asset_list.board.detail_crop_source')}
                  className="block max-h-56 w-auto max-w-full rounded-md border border-border bg-muted"
                  data-testid="asset-board-detail-crop-source"
                />
                {item.region ? (
                  <span
                    aria-hidden
                    className="absolute rounded-sm border border-primary bg-primary/10"
                    style={{
                      left: `${item.region.x * 100}%`,
                      top: `${item.region.y * 100}%`,
                      width: `${item.region.width * 100}%`,
                      height: `${item.region.height * 100}%`,
                    }}
                    data-testid="asset-board-detail-crop-region"
                  />
                ) : null}
              </span>
            ) : null}
            {item.source ? (
              <p className="text-muted-foreground">
                {[
                  item.source.source_kind,
                  formatSchemaText(text('ui.asset_list.board.detail_crop_source_meta'), {
                    width: item.source.source_width ?? 0,
                    height: item.source.source_height ?? 0,
                  }),
                ].filter(Boolean).join(' · ')}
              </p>
            ) : null}
            {region ? (
              <>
                <p className="tabular-nums text-muted-foreground" data-testid="asset-board-detail-crop-region-text">
                  {formatSchemaText(text('ui.asset_list.board.detail_crop_region'), region)}
                </p>
                {item.region?.pixel_rect ? (
                  <p className="tabular-nums text-muted-foreground">
                    {formatSchemaText(text('ui.asset_list.board.detail_crop_pixels'), region)}
                  </p>
                ) : null}
              </>
            ) : null}
          </section>
        ) : null}

        <section className="flex flex-col gap-1" data-testid="asset-board-detail-previous">
          <h3 className="text-xs font-semibold">{text('ui.asset_list.board.detail_previous')}</h3>
          {decision ? (
            <div className="flex flex-col gap-1 rounded-md border border-border px-2 py-1.5" data-testid="asset-board-detail-decision">
              <p className="flex flex-wrap items-baseline gap-x-2">
                <span className="font-medium">{verdictLabel}</span>
                <span className="text-muted-foreground">
                  {formatSchemaText(text('ui.asset_list.board.detail_round'), { round: decision.round })}
                </span>
                <time className="ml-auto text-[11px] text-muted-foreground" dateTime={decision.created_at}>
                  {formatDateTime(decision.created_at, activeLocale(schema))}
                </time>
              </p>
              {decision.reasons && decision.reasons.length > 0 ? (
                <p className="text-muted-foreground [overflow-wrap:anywhere]">
                  {formatSchemaText(text('ui.asset_list.board.detail_reasons'), { reasons: reasonLabels(decision.reasons) })}
                </p>
              ) : decision.reason ? (
                <p className="text-muted-foreground [overflow-wrap:anywhere]">
                  {formatSchemaText(text('ui.asset_list.board.detail_reasons'), { reasons: decision.reason })}
                </p>
              ) : null}
              {decision.note ? (
                <p className="text-muted-foreground [overflow-wrap:anywhere]">
                  {formatSchemaText(text('ui.asset_list.board.detail_note'), { note: decision.note })}
                </p>
              ) : null}
            </div>
          ) : (
            <p className="text-muted-foreground" data-testid="asset-board-detail-no-decision">
              {text('ui.asset_list.board.detail_no_previous')}
            </p>
          )}
        </section>
      </div>
    </SideDrawer>
  );
}