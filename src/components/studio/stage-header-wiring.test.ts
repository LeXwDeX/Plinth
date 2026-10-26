import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';

const header = readFileSync(new URL('./stage-header.tsx', import.meta.url), 'utf8');
const actionsModule = readFileSync(new URL('../../lib/workspace/stage-actions.ts', import.meta.url), 'utf8');
const stageActions = (defaultCatalog.application as unknown as {
  canvas: { frontend: { stage_actions: Array<{ id: string; kinds: string[]; group: string; label_key: string }> } };
}).canvas.frontend.stage_actions;
const chat = readFileSync(new URL('./chat-column.tsx', import.meta.url), 'utf8');
const actions = readFileSync(new URL('../../hooks/use-object-actions.ts', import.meta.url), 'utf8');

describe('stage header wiring', () => {
  it('places processing navigation in its Schema group and preserves that group’s icon-only rule', () => {
    expect(header).toContain("if (name === schema.canvas.frontend.processing_action.group && kind === 'model')");
    expect(header).toMatch(/view=\{processStepView\(schema, capability\)\} iconOnly=\{iconOnly\}/);
    expect(header).not.toContain("if (name === 'produce' && kind === 'model')");
  });

  it('shows "continue from here" only on an older version and routes it to the chat column', () => {
    expect(header).toContain('isOlderVersion(versions.tiles, tile?.key ?? null)');
    expect(header).toMatch(/continue_from: object && older\s*\?\s*\{\s*run: \(\) => \{\s*requestContinue\(\);/);
    expect(actionsModule).toContain("continue_from: 'stage-continue'");
    expect(stageActions.find((action) => action.id === 'continue_from')).toMatchObject({ group: 'produce', kinds: ['image', 'model'] });
    expect(chat).toContain('continueRequest');
    // The reference goes back on: the "do not include this object" choice is cleared.
    expect(chat).toMatch(/clearContinueRequest\(\);\s*setSkippedObject\(null\);/);
  });

  it('shows the branch chip after "continue from here" and drops it on cancel, on sending and when the stage moves on', () => {
    expect(chat).toMatch(/setBranchFrom\(from && /);
    expect(chat).toContain('branch={branchChip}');
    // Only while the version it was made for is still on the stage.
    expect(chat).toContain('objectKey(sessionSelection) !== branchFrom.key');
    // Cancelling leaves the object out of the turn too, so the new version does not attach to it.
    expect(chat).toMatch(/setBranchFrom\(null\);\s*setSkippedObject\(branchFrom\.key\);/);
    expect(chat.match(/setAttachmentPurpose\(null\);\s*setBranchFrom\(null\);/g)?.length).toBe(2);
  });

  it('turns a three-view group into 3D from its front view only, whichever view is on the stage', () => {
    expect(header).toContain('group.modelSource.available');
    expect(header).toContain('toThreeD(frontObject ?? object)');
    expect(header).toContain('disabled: group !== null && frontObject === null');
    // A disabled 转 3D explains itself first in its hover hint, and a group says it uses the front view only.
    expect(header).toContain("reason: text('ui.studio.three_view.to_3d_wait')");
    expect(header).toContain('group.modelSource.note');
    expect(actionsModule).toContain("to_3d: 'stage-to-3d'");
  });

  it('has no three-view button: three-view is a Skill chosen in the composer, not a stage action', () => {
    expect(header).not.toContain('stage-three-view');
    expect(header).not.toContain('toThreeView');
    expect(actions).not.toContain('toThreeView');
    expect(actions).not.toContain('three_view_turn');
  });

  it('sends a tool-binding Skill (three-view) only with a picture, and fills the default text when the box is empty', () => {
    const chat = readFileSync(new URL('./chat-column.tsx', import.meta.url), 'utf8');
    expect(chat).toContain('toolSkill?.defaultMessage');
    expect(chat).toContain("schemaText(schema, 'ui.studio.skill.needs_image')");
    expect(chat).toMatch(/toolSkill\?\.requiresImage[\s\S]{0,400}claim\.kind === 'image'/);
    expect(chat).toContain('Boolean(currentSkill?.tool)');
    // One-shot comes from the Schema flag and is spent only after startTurn resolved (`sent = true`).
    expect(chat).toMatch(/sent = true;[\s\S]{0,200}selectedSkill\?\.oneShot[\s\S]{0,300}\[sessionId\]: null/);
    expect(chat).not.toMatch(/selectedSkill\?\.tool[^\n]*\[sessionId\]: null/);
  });
});

describe('mask and merge wiring', () => {
  const shell = readFileSync(new URL('./studio-shell.tsx', import.meta.url), 'utf8');
  const board = readFileSync(new URL('../workspace/workspace-canvas-board.tsx', import.meta.url), 'utf8');
  const viewer = readFileSync(new URL('../../hooks/use-stage-viewer.ts', import.meta.url), 'utf8');
  const composer = readFileSync(new URL('./composer.tsx', import.meta.url), 'utf8');

  it('offers the free merge when the dashed frame holds more than the object picture, and runs it on the board', () => {
    expect(header).toContain('artboardDiffersFromObject(boardNodes, artboard, object');
    expect(header).toContain("requestImageCommand('send-board')");
    expect(header).toMatch(/merge: mergeable \? \{ run: \(\) => requestImageCommand\('merge-version'\)/);
    expect(actionsModule).toContain("merge: 'stage-merge'");
    expect(stageActions.find((action) => action.id === 'merge')).toMatchObject({ label_key: 'ui.studio.merge.action', group: 'produce', kinds: ['board', 'image'] });
    expect(board).toContain("imageCommand.kind === 'merge-version'");
    // One undo step, the merged node remembers its source, and the result opens on the stage.
    expect(board).toContain('stampNodes(sessionID');
    expect(board).toContain('base.captureId ?');
    expect(board).toContain('mergedFrom:');
  });

  it('removes the background of the one selected picture layer, else of the current version; the floating bar has no such button', () => {
    expect(board).toContain('const target = removeBackgroundTarget(selectedImageNode, objectNode);');
    expect(board).toContain('if (target.selectLayer) useCanvasStore.getState().selectNode(target.layer.id);');
    expect(board).toContain('setBgTarget(target.layer.imageUrl);');
    // Selected means exactly one picture layer; a text or shape layer, or several layers, fall back to the version's picture.
    expect(board).toContain('const selectedImageNode = useMemo(() => selectedImageLayer(nodes, selectedNodeIds), [nodes, selectedNodeIds]);');
    // The floating bar over a selected layer only copies, downloads or deletes it: 去背景 lives in the toolbar.
    expect(board).not.toContain('onBackgroundRemoval');
    expect(board).toContain('onDownload={() => downloadImage(selectedImageNode.imageUrl)}');
    // #278: nothing is exported here, so a press without a target names what is missing, never 导出失败.
    expect(board).toContain('if (!target) { toast.error(ui.removeBackgroundNoLayer); return; }');
  });

  it('#278: keeps 局部重绘 working on the artboard after the version\'s own layer is deleted, and names the real reason when the frame is empty', () => {
    const messages = (defaultCatalog.application as unknown as { content: { messages: Record<string, string> } }).content.messages;
    // The board no longer requires the version's layer (the old `!objectNode` export-failed branch); the mask
    // belongs to the canvas, so the entry asks the artboard (the same artboardContent rule export and submit use).
    expect(board).not.toMatch(/if \(!objectNode\) \{ toast\.error/);
    expect(board).toMatch(/inpaintEntry\(\{\s*boardHasContent: artboard !== null && artboardContent\(nodes, artboard\)\.length > 0,/);
    expect(board).toContain("if (entry === 'board_empty') { toast.error(ui.inpaintBoardEmpty); return; }");
    expect(board).toContain('if (objectNode) useCanvasStore.getState().selectNode(objectNode.id);');
    expect(board).toContain("if (entry === 'submit_selection') { useMaskStore.getState().requestSubmit(); return; }");
    // The only 导出失败 left is the real export path.
    expect(board.match(/toast\.error\(ui\.exportFailed\)/g)).toHaveLength(1);
    expect(board).toContain('currentObjectLayer(nodes, selection, sessionID, proxyAssetUrl)');
    // The toolbar button follows the same rule as the press: off only while the frame is empty, with that reason.
    expect(header).toMatch(/inpaint: object\s*\? \{\s*run: \(\) => requestImageCommand\('inpaint'\),\s*disabled: !boardHasContent,\s*reason: text\('ui\.studio\.stage\.action\.inpaint_board_empty'\),/);
    expect(header).toContain('const objectLayer = currentObjectLayer(boardNodes, object, sessionId, proxyAssetUrl);');
    expect(messages['ui.studio.stage.action.inpaint_board_empty']).toBeTruthy();
    expect(messages['ui.studio.stage.action.remove_background_no_layer']).toBeTruthy();
    expect(messages['ui.studio.stage.action.inpaint_board_empty']).not.toBe(messages['ui.workspace.canvas.export_failed']);
  });

  it('offers 去背景 on a board without a version only while exactly one picture layer is selected, and says why when it is not', () => {
    const messages = (defaultCatalog.application as unknown as { content: { messages: Record<string, string> } }).content.messages;
    // The Schema lets the board kind carry the action; the header binds it, the image state keeps its own rule.
    expect(stageActions.find((action) => action.id === 'remove_background')).toMatchObject({ group: 'produce', kinds: ['image', 'board'] });
    expect(header).toContain('const boardPicture = selectedImageLayer(boardNodes, selectedNodeIds);');
    expect(header).toMatch(/remove_background: object\s*\? \{\s*run: \(\) => requestImageCommand\('remove-background'\),\s*disabled: boardPicture === null && objectLayer === undefined,\s*reason: text\('ui\.studio\.stage\.action\.remove_background_no_layer'\),\s*\}\s*: \{\s*run: \(\) => requestImageCommand\('remove-background'\),\s*disabled: boardPicture === null,/);
    expect(header).toContain("reason: text('ui.studio.stage.action.remove_background_board_reason')");
    expect(header).toContain("target: text('ui.studio.stage.action.remove_background_board_target')");
    expect(messages['ui.studio.stage.action.remove_background_board_reason']).toBe('先在画板上选中一张图');
    expect(messages['ui.studio.stage.action.remove_background_board_target']).toBeTruthy();
    // The same command runs on the board: with no current picture, the selected layer is the whole target.
    expect(board).toContain('const target = removeBackgroundTarget(selectedImageNode, objectNode);');
  });

  it('asks before a painted mask is dropped by leaving the tool', () => {
    expect(shell).toContain('<MaskDiscardDialog />');
    expect(actions).toContain('return sameObject(useWorkspaceStore.getState().currentObject, object);');
    expect(viewer).toContain('canvasTool: canvas.activeTool');
  });

  it('shows the mask above the composer while the tool is on', () => {
    expect(chat).toContain('mask={maskChip}');
    expect(composer).toContain('testId="composer-mask-chip"');
    expect(composer).toContain("'ui.studio.mask.chip_strokes'");
  });

  it('does not tie the mask to a picture: the chip names no handle and the board keeps one canvas mask', () => {
    expect(chat).not.toContain('maskDraft.nodeId');
    expect(chat).toContain("handle: '',");
  });

  it('gives a merged (hand-drawn) version a composer chip like any other version', () => {
    expect(chat).toContain("if (sessionSelection.taskId.startsWith('canvas:') && !tile) return null;");
  });
});

describe('session list, batch export and balance wiring', () => {
  const sessions = readFileSync(new URL('./session-menu.tsx', import.meta.url), 'utf8');
  const outputs = readFileSync(new URL('./all-outputs.tsx', import.meta.url), 'utf8');
  const tasks = readFileSync(new URL('./task-center.tsx', import.meta.url), 'utf8');
  const stage = readFileSync(new URL('./stage.tsx', import.meta.url), 'utf8');

  it('shows the newest output and the relative time on every session row', () => {
    expect(sessions).toContain('useSessionCover(session.id, session.updatedAt)');
    expect(sessions).toContain('relativeTime(schema.canvas, session.updatedAt)');
    expect(sessions).toContain('data-testid="studio-session-cover"');
  });

  it('opens a session on what it was last on, then the server pick, then the newest version', () => {
    expect(stage).toContain('saveLastObject(sessionId, object)');
    expect(stage).toContain('restoreTarget(versions.tiles');
    expect(stage).toContain('readLastObject(sessionId)');
    // Waits for the server pick instead of treating "not answered yet" as "no pick".
    expect(stage).toContain('!sessionPick.loaded');
  });

  it('exports the selected versions as one client-side ZIP from platform asset routes', () => {
    expect(outputs).toContain('planBatchExport(versions.tiles');
    expect(outputs).toContain('buildArchive(entries, undefined, 4, progress)');
    expect(outputs).toContain('saveArchiveToDisk(entries');
    expect(outputs).toContain('data-testid="all-outputs-download"');
    expect(outputs).toContain('data-testid="all-outputs-select-all"');
    expect(outputs).not.toContain('https://');
  });

  it('keeps retry off a balance failure and offers no model switch, only the contact-the-admin text', () => {
    expect(tasks).toContain("text('failure.insufficient_balance')");
    expect(tasks).toContain('!isBalanceFailure(task.failure)');
    expect(tasks).not.toContain('ChangeModelAction');
    expect(tasks).not.toContain('alternativeOffering');
  });

  it('counts running tasks only in the top bar, not a turn waiting on a person', () => {
    expect(tasks).toContain('liveCount: activeTasks.length,');
    expect(tasks).not.toContain('activeTasks.length + (agentPending');
  });
});

describe('canvas double click', () => {
  const canvas = readFileSync(new URL('../canvas/canvas-board.tsx', import.meta.url), 'utf8');
  it('opens the large view or the text editor only with the select tool, never while painting', () => {
    expect(canvas).toMatch(/onDblClick: \(\) => \{[\s\S]{0,200}if \(activeTool !== 'select'\) return;/);
  });

  it('offers one model-processing button per available capability and no generic 处理 button', () => {
    expect(header).toContain('useModelProcessingCapabilities().available');
    expect(header).toMatch(/for \(const capability of processing\) \{[\s\S]{0,300}processStepView\(schema, capability\)[\s\S]{0,200}showProcess\(capability\.id\)/);
    // Name, icon and hint of a step come from its capability (label_cn / icon) and the Schema text, not from the header.
    expect(actionsModule).toContain('label: capability.label_cn');
    expect(actionsModule).toContain("icon: capability.icon ?? ''");
    expect(actionsModule).toContain('testId: `stage-process-${capability.id}`');
    expect(header).not.toContain('PROCESS_ICONS');
    expect(header).not.toContain('showProcess(null)');
    expect(header).not.toContain("'ui.studio.stage.action.process')");
    expect(header).not.toContain("showProcess('retexture')");
  });
});

describe('stage toolbar structure', () => {
  const stageActionIds = stageActions.map((action) => action.id);

  it('draws only what the Schema table lists and binds a handler to every id the code knows', () => {
    // The record type makes a missing handler a compile error; the loop reads the Schema's order and kinds.
    expect(header).toContain('const bindings: Record<StageActionId, ActionBinding | null> = {');
    expect(header).toContain('stageActionViews(schema, kind, name)');
    expect(header).toContain('const binding = bindings[view.id];');
    expect(header).toContain('if (!binding) continue;');
    for (const id of stageActionIds) expect(header, id).toContain(`${id}:`);
  });

  it('groups the buttons by the Schema group, separated by rules, with labels only in the produce group', () => {
    expect(header).toContain('STAGE_ACTION_GROUPS.map((name) => {');
    expect(header).toContain("const iconOnly = name !== 'produce';");
    expect(header).toContain('data-testid="stage-group-rule"');
    expect(header).toContain('role="group" aria-label={text(`ui.studio.stage.group.${entry.name}`)}');
    expect(header).toContain(".filter((entry) => entry.nodes.length > 0)");
  });

  it('opens the configured 3D processing group with the Schema actions, then the processing steps', () => {
    const processing = header.indexOf("if (name === schema.canvas.frontend.processing_action.group && kind === 'model') {");
    expect(processing).toBeGreaterThan(header.indexOf('stageActionViews(schema, kind, name)'));
    expect(header.indexOf('processStepView(schema, capability)')).toBeGreaterThan(processing);
  });

  it('keeps the identity group first and complete: kind badge, version, handle, source, older tag', () => {
    expect(header).toContain('<KindBadge kind={object.kind} />');
    expect(header).toContain('data-testid="stage-version"');
    expect(header).toContain('<HandleTag reference={handleReference} testId="stage-handle"');
    expect(header).toContain('data-testid="stage-source"');
    expect(header).toContain('data-testid="stage-older"');
    expect(header.indexOf('<KindBadge kind={object.kind} />')).toBeLessThan(header.lastIndexOf('{actions}'));
  });

  it('shows the board without a version as 画板 with the 2D badge, and offers merge, send and export on its content', () => {
    expect(header).toContain('data-kind="board"');
    expect(header).toContain('<KindBadge kind="image" />');
    expect(header).toContain("text('ui.studio.stage.board')");
    expect(header).toMatch(/const mergeable = object === null\s*\?\s*boardHasContent/);
    expect(header).toContain("send_chat: object || boardHasContent ? { run: () => requestImageCommand('send-board') } : null");
    // Export stays in the toolbar while the frame is empty, greyed with its reason in the hover hint.
    expect(header).toMatch(/export_board: \{ run: \(\) => requestImageCommand\('export-board'\), disabled: !boardHasContent, reason: text\('ui.studio.stage.action.export_board_empty'\) \}/);
    for (const id of ['merge', 'send_chat', 'export_board']) {
      expect(stageActions.find((action) => action.id === id)?.kinds, id).toContain('board');
    }
  });

  it('has no focus mode button, state or text', () => {
    expect(header).not.toMatch(/focusMode|toggleFocusMode|stage-focus|focus_mode|ui\.studio\.stage\.action\.focus/);
    expect(stageActionIds).not.toContain('focus');
  });

  it('shows the capture button of a model with the reason it is greyed', () => {
    expect(header).toContain("capture_2d: object\n      ? { run: () => captureToTwoD(object), disabled: !object.assetId, reason: text('ui.studio.stage.action.capture_unavailable') }");
  });
});
