import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';

const stage = readFileSync(new URL('./stage.tsx', import.meta.url), 'utf8');
const allOutputs = readFileSync(new URL('./all-outputs.tsx', import.meta.url), 'utf8');

describe('stage wiring', () => {
  it('opens a session that has finished versions on its newest one, only once versions are loaded', () => {
    expect(stage).toContain('versions.isLoading');
    // restoreTarget only ever picks finished versions (last-object.test.ts), newest as the last fallback.
    expect(stage).toContain('restoreTarget(versions.tiles');
    expect(stage).toContain('objectOfTile(target.tile, sessionId, target.pick)');
    // Only when nothing is on the stage: a chosen object is never replaced.
    expect(stage).toContain('if (object || !sessionId');
  });

  it('puts the all-outputs grid on the float layer: over the board bars, under later popovers', () => {
    // Board bars are on z-float too; the grid comes later in the DOM so it wins, and popovers
    // portal to the end of the body so they stay above it. z-modal would cover them.
    expect(allOutputs).toContain('z-float');
    expect(allOutputs).not.toContain('z-modal');
  });
});

describe('stage, right panel, lightbox and compare wiring', () => {
  const read = (relative: string) => readFileSync(new URL(relative, import.meta.url), 'utf8');
  const board = read('../canvas/canvas-board.tsx');
  const details = read('./object-details.tsx');
  const lightbox = read('./lightbox.tsx');
  const viewer = read('../../hooks/use-stage-viewer.ts');
  const dispatch = read('../../lib/workspace/key-dispatch.ts');
  const compare = read('./compare-view.tsx');
  const stage3d = read('./stage-3d.tsx');
  const chat = read('./chat-column.tsx');
  const header = read('./stage-header.tsx');
  const actionsModule = read('../../lib/workspace/stage-actions.ts');

  it('shows no text or card on an empty canvas', () => {
    expect(board).not.toContain('ui.canvas.board.empty');
    expect(board).not.toContain('emptyState');
  });

  it('shows the prompt and the pixel size with the ratio in the details', () => {
    expect(details).toContain('tracked?.prompt || promptOfObject(messages, object)');
    expect(details).toContain('useImageSize(');
    expect(details).toContain("'ui.studio.panel.details.size'");
    expect(details).toContain('ratioOfSize(`${size.width}x${size.height}`)');
  });

  it('pages through the candidates of a group, on the stage and in the chat\'s result detail, and can pick one on the stage', () => {
    expect(viewer).toContain('const candidates = currentTile ? candidateItemsOf(currentTile) : [];');
    expect(viewer).toContain('pick: { taskId: tile.taskId, assetId: asset.id }');
    // A chat picture opens the shared result detail; ←/→ there pages the message's pictures, the card's own pick buttons pick.
    expect(chat).toContain('showResultDetail(group.length > 1 ? { ...target, group } : target);');
    expect(lightbox).toContain('data-testid="lightbox-pick"');
    expect(lightbox).toContain("'ui.studio.lightbox.pick'");
    // Picking puts the candidate on the stage, like the pick button in the chat.
    expect(lightbox).toMatch(/choose\(\{ kind: 'image', taskId: target\.taskId, assetId: target\.assetId \}\)[\s\S]{0,400}openOnStage\(object\)/);
  });

  it('zooms and pans every compared picture together, and says why a version was not taken', () => {
    expect(compare).toContain('const [view, setView] = useState<View>(FIT);');
    expect(compare).toContain("addEventListener('wheel', onWheel, { passive: false })");
    expect(compare).toContain('view={view} onView={setView}');
    expect(compare).toContain('data-testid="compare-notice"');
    expect(viewer).toContain("setCompareNotice('unavailable')");
  });

  it('puts models side by side with one shared orbit, and keeps the slider for pictures', () => {
    // A model version is a compare slot like a picture; the stage header offers compare for both.
    expect(viewer).toContain("kind: object.kind");
    // The Schema's stage_actions say where each button appears: compare on pictures and models, the large view on pictures only.
    const actions = (defaultCatalog.application as unknown as { canvas: { frontend: { stage_actions: Array<{ id: string; kinds: string[]; group: string }> } } }).canvas.frontend.stage_actions;
    expect(actions.find((action) => action.id === 'compare')).toMatchObject({ kinds: ['image', 'model'], group: 'view' });
    expect(actions.find((action) => action.id === 'enlarge')).toMatchObject({ kinds: ['image'], group: 'view' });
    // The header binds both: compare toggles, the large view opens on the stage's own picture.
    expect(header).toMatch(/compare: object \? \{ run: \(\) => \(comparing \? endCompare\(\) : startCompare\(\)\), pressed: comparing \}/);
    expect(header).toContain('enlarge: object ? { run: () => openStageLightbox() }');
    expect(actionsModule).toContain("compare: 'stage-compare'");
    expect(actionsModule).toContain("enlarge: 'stage-enlarge'");
    // Models turn together: a person's own camera move is copied to the other panes, nothing else is.
    expect(compare).toContain("detail?.source === 'user-interaction'");
    expect(compare).toContain("addEventListener('camera-change', onChange)");
    expect(compare).toContain('autoRotate={false}');
    expect(compare).toContain("'ui.studio.compare.sync_hint'");
    expect(compare).toContain("'ui.studio.compare.slider_images_only'");
    // Esc leaves the comparison even while a mask is armed under it, and the mask's exit question does not fire.
    // The order lives in the key dispatcher (and is tested in key-dispatch.test.ts); the hook only applies the command.
    expect(dispatch).toContain("if (context.compareOpen) return resolution({ type: 'compare-end' }, area, { stopPropagation: true });");
    expect(viewer).toContain("case 'compare-end': studio.endCompare(); break;");
    expect(viewer).toContain('if (resolved.stopPropagation) event.stopPropagation();');
  });

  it('shows the triangle count and the texture size at the bottom right of the 3D stage, a dash when unknown', () => {
    expect(stage3d).toContain('absolute bottom-6 right-6');
    expect(stage3d).toContain('onTextureSize=');
    expect(stage3d).not.toContain('fetch(');
    expect(stage3d).toContain("'ui.studio.stage.stats.unknown'");
    expect(stage3d).toContain('data-testid="stage-model-stats"');
  });

  it('downloads one file per selected version, groups by day and selects a range with Shift', () => {
    expect(allOutputs).toContain("{ scope: 'chosen', chosenAssetId }");
    expect(allOutputs).toContain('groupByDay(shown');
    expect(allOutputs).toContain('data-testid="all-outputs-day-title"');
    expect(allOutputs).toContain('shiftHeld.current = event.shiftKey');
    expect(allOutputs).toContain('selectionAfterClick(pageOrder, current, anchor.current, key, shift)');
  });

  it('opens the shared details on a card click, selects only through the checkbox, and keeps the canvas out of the click', () => {
    const strip = read('./version-strip.tsx');
    expect(allOutputs).toContain('onClick={onDetail}');
    expect(allOutputs).toContain('openResultDetail(targetOfTile(tile, sessionId, labelOf(tile), picked, \'\', schema))');
    expect(allOutputs).not.toContain('onDoubleClick');
    expect(allOutputs).not.toContain('openOnStage');
    expect(allOutputs).toContain('onCheckedChange={() => { const shift = shiftHeld.current; shiftHeld.current = false; onCheck(shift); }}');
    expect(allOutputs).toContain('data-testid="all-outputs-bulk-bar"');
    expect(allOutputs).toContain("'ui.studio.outputs.selected_count'");
    expect(allOutputs).toContain("'ui.studio.outputs.clear_selection'");
    expect(allOutputs).toContain("addEventListener('keydown', onKeyDown, true)");
    expect(allOutputs).toContain('escapeClearsSelection(chosenCount, blockers)');
    // The strip retains comparison toggles; ordinary activation selects the stage and details stays a separate action.
    expect(strip).toContain('if (tile.status === \'ready\' && !toggleTileInCompare(tile))');
    expect(strip).toContain('if (selected) openOnStage(selected)');
    expect(strip).toContain('data-testid="version-detail"');
    expect(strip).toContain('openResultDetail(target)');
    expect(strip).not.toMatch(/const open = [\s\S]*openOnStage\(next\)/);
  });

  it('makes finished picture cards drag sources and gives every card a download, a menu and the details', () => {
    const parts = read('../result/result-card-parts.tsx');
    const chat = read('../chat/result-card.tsx');
    expect(parts).toContain('startCanvasDrag(event.dataTransfer, item');
    expect(parts).toContain("target.status === 'pending' || target.status === 'running' || target.status === 'failed'");
    expect(parts).toContain('<ContextMenuTrigger render={render}>');
    for (const source of [read('./version-strip.tsx'), allOutputs]) {
      expect(source).toContain('useCanvasDragSource(target)');
      expect(source).toContain('<CardDownload target={target}');
      expect(source).toContain('<ResultContextMenu');
    }
    expect(chat).toContain('startCanvasDrag(event.dataTransfer, item');
    expect(chat).toContain('data-testid="chat-result-detail"');
    expect(read('./task-center.tsx')).toContain('onClick={() => openTaskDetail(task)}');
    expect(read('../account/asset-preview-dialog.tsx')).toContain('<ResultDetailDialog target={target} onClose={onClose} />');
    expect(read('./studio-shell.tsx')).toContain('<ResultDetailHost />');
  });

  it('puts a clicked chat picture on the stage, keeps one live 3D viewer in the chat and leaves the details to a small icon', () => {
    const chat = read('../chat/result-card.tsx');
    // 2D: the click opens the stage (a picture with no task of its own opens large instead); the details are the icon, not the click.
    expect(chat).toContain('if (!object || !openOnStage(object)) onPreview(entry.index);');
    expect(chat).toContain('onClick={() => { const target = detailTarget(activeEntry); if (target) openResultDetail(target); }}');
    expect(chat).not.toContain('chat-result-open');
    // 3D: the cover opens the stage, the corner button holds the card's one live viewer, and the card gives it back when it goes.
    expect(chat).toContain('onClick={() => openOnStage(object)}');
    expect(chat).toContain('<ModelViewer3D');
    expect(chat).toContain('useInlineModelPreviewStore.getState().close(taskId)');
    // The message decides which pictures are candidates and which belong to a model.
    const message = read('../chat/chat-message.tsx');
    expect(message).toContain('splitMessagePictures(message,');
    // A 3D task counts from the moment the tracker knows it, by the Schema's capability domain.
    expect(message).toContain("domainOfCapability(schema, capability) === 'model'");
  });

  it('keeps automatic defaults GET-only and flushes only explicit Offering edits before send', () => {
    const hook = read('../../hooks/use-session-agent-params.ts');
    const generation = read('../../hooks/use-generation-params.ts');
    expect(generation).not.toContain('productParamSeeds');
    expect(generation).toContain('await params.flush();');
    expect(hook).toContain('const patch = { ...snapshot.values };');
    expect(generation).toContain('useSessionAgentParams(offering ? sessionId : null, model.paramSchemas, offering?.id)');
    expect(hook).not.toContain('seeded.current');
    expect(chat).toContain('imageGeneration, modelGeneration');
    expect(chat).toContain('params.nextTurn');
    expect(chat.indexOf('params.flush()')).toBeLessThan(chat.indexOf('await chatClient.startTurn('));
  });

  it('puts the 贴图 / 白模 / 线框 view bar on the left of the 3D stage, drawn by the shared ToolRail from the Schema tools', () => {
    const stage3d = read('./stage-3d.tsx');
    const views = read('./stage-3d-views.tsx');
    expect(stage3d).toContain('<Stage3DViews mode={viewMode}');
    expect(stage3d).toContain('viewMode={viewMode}');
    // The choice belongs to the model on the stage; a new model opens with its textures.
    expect(stage3d).toContain('view?.key === measuredKey');
    // The bar is the viewer rail of the same component as the 2D tools, at the top-left of the stage.
    expect(views).toContain('<ToolRail');
    expect(views).toContain('rail="viewer"');
    expect(views).toContain('className="left-6 top-6"');
    expect(views).toContain('itemTestId={(id) => `stage-3d-view-${id}`}');
    expect(views).toContain('testId="stage-3d-views"');
    // The current view is pressed; until the model has loaded, and for a model whose materials cannot be read,
    // only the texture view stays, and the reason names which of the two it is (#264).
    expect(views).toContain('pressed: mode === value');
    expect(views).toContain("value !== 'texture' && !(loaded && supported)");
    expect(views).toContain("reason: disabled ? why : undefined");
    expect(views).toContain("loaded ? text('ui.studio.stage.view.unsupported') : text('ui.studio.stage.view.not_loaded')");
    expect(stage3d).toContain('loaded={control?.ready ?? false} supported={control?.viewModes ?? false}');
    // No UV placeholder, no native title, no button list of its own.
    expect(views).not.toMatch(/uv/i);
    expect(views).not.toContain('title=');
    expect(views).not.toContain('<button');
  });

  it('keeps the reset-view button clear of the view bar by moving it to the viewer\'s bottom-left on the stage only', () => {
    const stage3d = read('./stage-3d.tsx');
    const viewer = read('../viewer/model-viewer-3d.tsx');
    expect(stage3d).toContain('resetPlacement="bottom-left"');
    expect(viewer).toContain("resetPlacement = 'top-left'");
    expect(viewer).toContain("resetPlacement === 'bottom-left' ? 'bottom-3' : 'top-3'");
    // Every other viewer keeps the button where it was.
    for (const other of ['../chat/result-card.tsx', '../result/result-detail.tsx', './compare-view.tsx']) {
      expect(read(other), other).not.toContain('resetPlacement');
    }
  });

  it('draws the dashed artboard frame and its label above the node layer, never listening', () => {
    const boardSource = read('../canvas/canvas-board.tsx');
    const nodesLayer = boardSource.indexOf('{sorted.map((node) => {');
    const frame = boardSource.indexOf('strokeScaleEnabled={false}', boardSource.indexOf('frame_stroke_width'));
    const label = boardSource.indexOf('text={artboardLabel}');
    expect(frame).toBeGreaterThan(nodesLayer);
    expect(label).toBeGreaterThan(nodesLayer);
    expect(boardSource).toContain('<Layer listening={false}>\n          {artboard\n            ? outsideDimRects(');
    // The veil outside the frame is drawn first in that layer, under the frame line.
    expect(boardSource.indexOf('outsideDimRects(visibleWorld')).toBeLessThan(frame);
    expect(boardSource).toContain('{artboard ? (\n            <Rect\n              x={artboard.x}');
    // #282: the label is a non-listening group inside the frame, clipped to it, drawn just before the snap guides.
    const labelGroup = boardSource.slice(boardSource.indexOf('name="artboard-label"'), label);
    expect(labelGroup).toContain('clipWidth={artboard.width}');
    expect(labelGroup).toContain('listening={false}');
    expect(boardSource).toContain('</Group>\n            );\n          })() : null}\n          {snapGuides');
  });

  it('keeps the task drawer a plain list whose whole row opens the shared task details', () => {
    const drawer = read('task-center.tsx');
    expect(drawer).toContain('className="absolute inset-0 rounded-xl');
    expect(drawer).toContain('data-testid="workspace-task-open"');
    for (const removed of ['ModelFormatsPanel', 'ModelArchiveLink', 'ResourceStatusList', 'task.downloadables.map', 'workspace-task-detail']) {
      expect(drawer).not.toContain(removed);
    }
  });

  it('reports a failed task load, with its retry, under every task filter', () => {
    const drawer = read('task-center.tsx');
    expect(drawer).toContain('{skillRunsError ? <p role="alert"');
    expect(drawer).not.toContain('{skillRunsError && showLiveRows');
    expect(drawer).toContain('data-testid="workspace-task-load-retry"');
  });
});
