import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';

function source(relative: string): string {
  return readFileSync(new URL(relative, import.meta.url), 'utf8');
}

const chat = source('./chat-column.tsx');
const board = source('../workspace/workspace-canvas-board.tsx');

// #644: the 以画板为主 toggle decides whether a message sends the board; it is the message's own and resets once sent.
describe('the board is the turn input only on a canvas turn', () => {
  it('no longer sends the board because the current picture sits on it', () => {
    expect(chat).not.toContain('currentObjectSource(');
    const send = chat.slice(chat.indexOf('let canvasSelection: ChatAttachmentClaim[] | null = null;'), chat.indexOf('const stageSelection ='));
    expect(send).toContain('const fromBox = reuseImages === undefined && queuedSelection === undefined && !choiceAnswer;');
    expect(send).toContain('chosen: fromBox && boardModeChosenRef.current, artboard: artboard !== null, maskSteps: maskStrokesNow, namesBoard,');
    expect(send).toContain('if (!queuedSelection && boardTurn && artboard) {');
  });

  it('maps the toggle to the turn input source and sends none otherwise', () => {
    expect(chat).toContain(': boardInputSource(canvasSelection !== null, schema.chat.context.canvas_input_source);');
  });

  it('resets the toggle once the message has left the box, queued or taken by the server', () => {
    const queued = chat.slice(chat.indexOf('if (turnRunning) {'), chat.indexOf('const skillId = choiceAnswer'));
    expect(queued).toContain('setBoardModeChosen(false);');
    const accepted = chat.slice(chat.indexOf('sent = true;'), chat.indexOf('followed = await followTurnStream'));
    expect(accepted).toContain('setBoardModeChosen(false);');
    // A failed send keeps the choice: nothing before the server took the turn clears it.
    const beforeSent = chat.slice(chat.indexOf('const skillId = choiceAnswer'), chat.indexOf('sent = true;'));
    expect(beforeSent).not.toContain('setBoardModeChosen(false)');
  });

  it('shows the toggle and the chip from the same rule the send reads', () => {
    expect(chat).toContain('chosen: boardModeChosen, artboard: artboard !== null, maskSteps: maskStrokes, namesBoard: mentionsBoard(input, mentionList),');
    expect(chat).toContain('boardMode={composerBoardMode}');
    expect(chat).toContain('chip={objectDeselected ? null : composerChip}');
  });

  it('turns the toggle on for 「发送到对话」 from the board instead of attaching a plain upload', () => {
    expect(board).toContain('useWorkspaceStore.getState().requestBoardMode(sessionID);');
    expect(board).not.toContain("reportSnapshotFailure(schema, 'board-to-chat'");
    expect(chat).toContain('if (!boardModeRequest || boardModeRequest !== activeSessionId) return;');
    expect(chat).toContain('setBoardModeChosen(true);');
  });

  it('#677: answers every press of 「发送到对话」: lights the toggle, opens and focuses the composer, and says so', () => {
    const effect = chat.slice(chat.indexOf('if (!boardModeRequest || boardModeRequest !== activeSessionId) return;'), chat.indexOf('const mentions = useMemo('));
    expect(effect).toContain("if (boardTurnOnRef.current) toast.info(schemaText(schema, 'ui.studio.stage.action.send_chat_already_on'));");
    expect(effect).toContain("toast.success(schemaText(schema, 'ui.studio.stage.action.send_chat_done'));");
    expect(effect).toContain('if (useStudioStore.getState().chatCollapsed) setCollapsed(false);');
    expect(effect).toContain("focusWhenUnblocked(() => document.getElementById('art-ai-chat-composer'))");
    const messages = (catalog.application as unknown as { content: { messages: Record<string, string> } }).content.messages;
    // The hint says what the button really does now: no attachment, the next message is a canvas turn.
    expect(messages['ui.studio.stage.action.send_chat_hint']).toContain('以画板为主');
    expect(messages['ui.studio.stage.action.send_chat_hint']).not.toContain('附件');
    for (const key of ['send_chat_done', 'send_chat_already_on', 'send_chat_empty']) expect(messages[`ui.studio.stage.action.${key}`], key).toBeTruthy();
  });
});
