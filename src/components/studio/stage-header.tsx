'use client';

import { Fragment, type ReactNode } from 'react';
import { Link2 } from 'lucide-react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { ActionButton } from '@/components/studio/action-button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { KindBadge } from '@/components/studio/kind-badge';
import { RetextureWithImageAction } from '@/components/studio/retexture-with-image';
import { useModelProcessingCapabilities } from '@/hooks/use-model-processing-capabilities';
import { useCanvasArtboard } from '@/hooks/use-canvas-artboard';
import { artboardContent } from '@/lib/canvas/export-canvas';
import { selectedImageLayer } from '@/lib/canvas/selected-layer';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useObjectActions } from '@/hooks/use-object-actions';
import { useStageViewer } from '@/hooks/use-stage-viewer';
import { useThreeViews } from '@/hooks/use-three-views';
import { taskAssetRoute } from '@/lib/workspace/model-formats';
import { tileOfObject, useSessionVersions } from '@/hooks/use-session-versions';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { artboardDiffersFromObject } from '@/lib/canvas/current-object';
import { currentObjectLayer } from '@/lib/canvas/inpaint-entry';
import { handleOfObject } from '@/lib/workspace/object-handle';
import { HandleTag } from '@/components/chat/handle-tag';
import { useHandleReferences } from '@/hooks/use-handle-references';
import { objectReference } from '@/lib/chat/composer-reference';
import {
  processStepView, stageActionViews, stageKindOf, STAGE_ACTION_GROUPS, withTarget,
  type ActionView, type StageActionId,
} from '@/lib/workspace/stage-actions';
import { objectOfTile } from '@/lib/workspace/version-object';
import { versionLabel } from '@/lib/workspace/version-label';
import { isOlderVersion, primaryParent } from '@/lib/workspace/versions';
import { useCanvasStore } from '@/store/canvas-store';
import { CAPTURE_BLOCK_TEXT, useModelCaptureBlock } from '@/hooks/use-model-capture-block';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import type { CurrentObject } from '@/store/workspace-store';

const NO_MESSAGES: never[] = [];

/** What clicking a toolbar button does. The Schema decides which buttons exist; the code decides what each id does. */
interface PlainBinding {
  run: () => void;
  variant?: 'ghost' | 'outline';
  disabled?: boolean;
  /** Why it is disabled, written first in its hover hint. */
  reason?: string;
  pressed?: boolean;
  /** The Schema's "acts on" sentence for a state where the button acts on something else. */
  target?: string;
}
/** A button that opens a panel of its own (the face is the same ActionFace). */
interface PanelBinding {
  render: (view: ActionView) => ReactNode;
}
type ActionBinding = PlainBinding | PanelBinding;

/**
 * 舞台头（高 44px）：从左到右四组，组间一条竖线。
 *   身份  类型标记、版本号、对话里的句柄、来源、「旧版本」标签；永不收起。
 *   产出  会产出新版本或调用模型的动作（局部重绘、去背景、转 3D、用这张图换贴图；3D 的截图和各加工步骤）。
 *   输出  发送到对话、导出画板（只用图标）。
 *   查看  比较、大图（只用图标）。
 * 有哪些按钮、顺序、图标、文字、✦ 和悬停说明都来自 Schema 的 `canvas.frontend.stage_actions`（3D 加工步骤来自
 * 当前可用的加工能力）；这里只决定当前状态下哪些按钮出现，以及每个动作 id 点下去做什么。
 * 空间不够时文字先收：只有产出组带文字，且按文字长短分别在更宽的舞台上才显示；输出、查看组始终只有图标。
 * 画板有内容但还没有版本时，身份组写「画板」，产出组有「合并为新版本」和「去背景」（画板上恰好选中一个图片图层才可点，
 * 否则置灰并写原因），输出组有「发送到对话」「导出画板」。
 */
export function StageHeader({ object }: { object: CurrentObject | null }) {
  const { schema } = useProductSchema();
  const { proxyAssetUrl } = useAssetURLRewriter();
  const { openOnStage, toThreeD, captureToTwoD } = useObjectActions();
  const captureBlock = useModelCaptureBlock(object?.taskId);
  const sessionId = useChatStore((state) => state.activeSessionId);
  const messages = useChatStore((state) => state.messagesBySession[sessionId ?? ''] ?? NO_MESSAGES);
  const handleReferences = useHandleReferences();
  const versions = useSessionVersions();
  const boardNodes = useCanvasStore((state) => state.nodesBySession[sessionId ?? '']);
  const selectedNodeIds = useCanvasStore((state) => state.selectedNodeIds);
  const { groups: threeViews } = useThreeViews();
  const { artboard } = useCanvasArtboard(sessionId);
  // Export board depends on the artboard having something visible inside it, not on a generated version.
  const boardHasContent = boardNodes !== undefined && artboardContent(boardNodes, artboard).length > 0;
  // Without a version the board has no picture of its own to cut out: 去背景 acts on the one picture layer selected on it.
  const boardPicture = selectedImageLayer(boardNodes, selectedNodeIds);
  // The version's own layer; the person may have deleted it while other layers stay (#278).
  const objectLayer = currentObjectLayer(boardNodes, object, sessionId, proxyAssetUrl);
  const showProcess = useStudioStore((state) => state.showProcess);
  const processing = useModelProcessingCapabilities().available;
  const requestImageCommand = useStudioStore((state) => state.requestImageCommand);
  const requestContinue = useStudioStore((state) => state.requestContinue);
  const comparing = useStudioStore((state) => state.compare !== null);
  const endCompare = useStudioStore((state) => state.endCompare);
  const { openStageLightbox, startCompare } = useStageViewer();

  const text = (key: string) => schemaText(schema, key);
  const tile = object ? tileOfObject(versions, object) : null;
  const older = object !== null && isOlderVersion(versions.tiles, tile?.key ?? null);
  const label = object ? versionLabel(schema, tile, object.kind) : text('ui.studio.stage.board');
  const group = tile && tile.views.length > 0 ? threeViews.find((item) => `g:${item.runId}` === tile.key) ?? null : null;
  // A three-view group makes a 3D model from its front view only, whichever view is on the stage.
  const frontObject: CurrentObject | null = group && group.modelSource.available && sessionId
    ? (() => {
      const url = proxyAssetUrl(taskAssetRoute(schema, group.modelSource.taskId, group.modelSource.assetId), group.modelSource.taskId);
      return url ? { taskId: group.modelSource.taskId, kind: 'image' as const, url, assetId: group.modelSource.assetId, imageUrls: [url], sessionId } : null;
    })()
    : null;
  // Merging stamps everything visible in the dashed frame (free, no model). With a picture on the stage it
  // applies whenever the frame holds more than that picture's own untouched pixels (a second picture, a drawing,
  // a crop, a hand-painted change); with only the board (no version yet) whenever the frame holds anything.
  const mergeable = object === null
    ? boardHasContent
    : object.kind === 'image' && boardNodes !== undefined && artboard !== null
      && artboardDiffersFromObject(boardNodes, artboard, object, (url) => proxyAssetUrl(url, object.taskId) ?? url);

  // One entry per action id the code can handle (the type makes a missing id a compile error). null: not
  // offered in the current state. The Schema's stage_actions decide which of these are drawn, and where.
  const bindings: Record<StageActionId, ActionBinding | null> = {
    merge: mergeable ? { run: () => requestImageCommand('merge-version'), variant: 'outline' } : null,
    continue_from: object && older
      ? {
        run: () => {
          requestContinue();
          toast.success(formatSchemaText(text('ui.studio.continue.armed'), { label }));
        },
        variant: 'outline',
      }
      : null,
    // Inpainting repaints the artboard's content (the mask belongs to the canvas, see inpaintEntry): it is off only
    // while the dashed frame holds nothing, also when the version's own layer was deleted and nothing else is left.
    inpaint: object
      ? {
        run: () => requestImageCommand('inpaint'),
        disabled: !boardHasContent,
        reason: text('ui.studio.stage.action.inpaint_board_empty'),
      }
      : null,
    // With a picture on the stage it acts on the selected layer, else on that picture (the workspace board decides, see
    // removeBackgroundTarget); once that picture's layer is deleted, only a selected picture layer is left to act on.
    // On a board without a version it needs exactly one picture layer selected.
    remove_background: object
      ? {
        run: () => requestImageCommand('remove-background'),
        disabled: boardPicture === null && objectLayer === undefined,
        reason: text('ui.studio.stage.action.remove_background_no_layer'),
      }
      : {
        run: () => requestImageCommand('remove-background'),
        disabled: boardPicture === null,
        reason: text('ui.studio.stage.action.remove_background_board_reason'),
        target: text('ui.studio.stage.action.remove_background_board_target'),
      },
    to_3d: object
      ? {
        run: () => toThreeD(frontObject ?? object),
        disabled: group !== null && frontObject === null,
        reason: text('ui.studio.three_view.to_3d_wait'),
        ...(group?.modelSource.note ? { target: group.modelSource.note } : {}),
      }
      : null,
    retexture_with_image: object ? { render: (view) => <RetextureWithImageAction object={object} view={view} /> } : null,
    capture_2d: object
      ? { run: () => captureToTwoD(object), disabled: !object.assetId || captureBlock !== null, reason: text(captureBlock ?? CAPTURE_BLOCK_TEXT.notReady) }
      : null,
    // A picture can always be sent; with only the board, there must be something in the frame to send.
    send_chat: object || boardHasContent ? { run: () => requestImageCommand('send-board') } : null,
    export_board: { run: () => requestImageCommand('export-board'), disabled: !boardHasContent, reason: text('ui.studio.stage.action.export_board_empty') },
    // Pictures and models both compare (models turn side by side); only a picture has a large view.
    compare: object ? { run: () => (comparing ? endCompare() : startCompare()), pressed: comparing } : null,
    enlarge: object ? { run: () => openStageLightbox() } : null,
  };

  const kind = stageKindOf(object);
  const actionGroups = STAGE_ACTION_GROUPS.map((name) => {
    const nodes: ReactNode[] = [];
    // Only the produce group carries labels; output and view stay icon-only so the toolbar never crowds.
    const iconOnly = name !== 'produce';
    for (const view of stageActionViews(schema, kind, name)) {
      const binding = bindings[view.id];
      if (!binding) continue;
      if ('render' in binding) {
        nodes.push(<Fragment key={view.id}>{binding.render(view)}</Fragment>);
        continue;
      }
      nodes.push(
        <ActionButton
          key={view.id}
          view={binding.target ? withTarget(schema, view, binding.target) : view}
          iconOnly={iconOnly}
          variant={binding.variant}
          disabled={binding.disabled}
          disabledReason={binding.reason}
          pressed={binding.pressed}
          onAct={binding.run}
        />,
      );
    }
    if (name === schema.canvas.frontend.processing_action.group && kind === 'model') {
      // 3D processing steps are not in the Schema table: one per available processing capability.
      for (const capability of processing) {
        nodes.push(<ActionButton key={`process-${capability.id}`} view={processStepView(schema, capability)} iconOnly={iconOnly} onAct={() => showProcess(capability.id)} />);
      }
    }
    return { name, nodes };
  }).filter((entry) => entry.nodes.length > 0);

  const actions = (
    <div className="flex shrink-0 items-center" data-testid="stage-actions">
      {actionGroups.map((entry, index) => (
        <Fragment key={entry.name}>
          {index > 0 ? <span aria-hidden className="mx-2 h-5 w-px shrink-0 bg-border" data-testid="stage-group-rule" /> : null}
          <div role="group" aria-label={text(`ui.studio.stage.group.${entry.name}`)} data-group={entry.name} className="flex items-center gap-1">{entry.nodes}</div>
        </Fragment>
      ))}
    </div>
  );

  if (!object) {
    return (
      <header className="@container flex h-11 shrink-0 items-center gap-3 border-b border-border px-3" data-testid="stage-header" data-kind="board">
        <div className="flex min-w-0 flex-1 items-center gap-2 overflow-hidden" role="group" aria-label={text('ui.studio.stage.group.identity')}>
          <KindBadge kind="image" />
          <h2 className="shrink-0 text-sm font-semibold" data-testid="stage-board-title">{label}</h2>
        </div>
        {actions}
      </header>
    );
  }

  const handle = object.handle ?? handleOfObject(messages, object);
  // The handle is a tag that writes it into the next message (a picture is attached as well).
  const handleReference = handle ? objectReference(object, handle, handleReferences) : null;
  const parent = tile ? primaryParent(tile) : null;
  const parentTile = parent?.known ? versions.tiles.find((item) => item.key === parent.key) ?? null : null;
  const parentObject = parentTile && sessionId ? objectOfTile(parentTile, sessionId) : null;
  const sourceUnknown = Boolean(tile && !parentTile && tile.parents.some((item) => !item.known));

  return (
    <header className="@container flex h-11 shrink-0 items-center gap-3 border-b border-border px-3" data-testid="stage-header" data-kind={object.kind}>
      <div className="flex min-w-0 flex-1 items-center gap-2 overflow-hidden" role="group" aria-label={text('ui.studio.stage.group.identity')}>
        <KindBadge kind={object.kind} />
        <h2 className="shrink-0 text-sm font-semibold" data-testid="stage-version">{label}</h2>
        {handle && handleReference ? (
          <HandleTag reference={handleReference} testId="stage-handle" wrapperClassName="min-w-0" className="min-w-0 truncate bg-muted px-1.5 py-0.5 font-mono text-[11px] leading-normal text-muted-foreground hover:text-foreground" />
        ) : handle ? (
          <span className="min-w-0 truncate rounded-sm bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground" data-testid="stage-handle">{handle}</span>
        ) : null}
        {older ? (
          <HintTooltip name={text('ui.studio.stage.older_version')} hint={text('ui.studio.stage.action.continue_from_hint')} className="shrink-0">
            <span className="shrink-0 rounded-sm bg-warning/15 px-1.5 py-0.5 text-[11px] text-warning" data-testid="stage-older">{text('ui.studio.stage.older_version')}</span>
          </HintTooltip>
        ) : null}
        {parentTile && parentObject ? (
          <HintTooltip
            name={text('ui.studio.stage.source_open')}
            hint={formatSchemaText(text('ui.studio.stage.source_from'), { label: versionLabel(schema, parentTile, parentTile.domain) })}
            className="min-w-0"
          >
            <button
              type="button"
              onClick={() => openOnStage(parentObject)}
              aria-label={text('ui.studio.stage.source_open')}
              data-testid="stage-source"
              className="flex min-w-0 items-center gap-1 rounded-sm text-xs text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
            >
              <Link2 aria-hidden className="size-3 shrink-0" />
              <span className="hidden truncate @4xl:inline">{formatSchemaText(text('ui.studio.stage.source_from'), { label: versionLabel(schema, parentTile, parentTile.domain) })}</span>
            </button>
          </HintTooltip>
        ) : sourceUnknown ? (
          <HintTooltip name={text('ui.studio.stage.source_unknown')} className="min-w-0">
            <span className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground" data-testid="stage-source-unknown">
              <Link2 aria-hidden className="size-3 shrink-0" />
              <span className="hidden truncate @4xl:inline">{text('ui.studio.stage.source_unknown')}</span>
            </span>
          </HintTooltip>
        ) : null}
      </div>
      {actions}
    </header>
  );
}
