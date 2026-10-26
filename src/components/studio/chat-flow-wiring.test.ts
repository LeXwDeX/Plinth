import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

function source(relative: string): string {
  return readFileSync(new URL(relative, import.meta.url), 'utf8');
}

const chat = source('./chat-column.tsx');
const composer = source('./composer.tsx');
const studioStore = source('../../store/studio-store.ts');
const objectActions = source('../../hooks/use-object-actions.ts');

describe('the composer while a turn runs', () => {
  it('stays open: only an upload or a Skill check locks it, and Send remains next to Stop', () => {
    expect(composer).toContain('const disabled = locked;');
    expect(composer).not.toContain('streaming || locked');
    expect(composer).toContain('data-testid="composer-stop"');
    expect(composer).toContain('data-testid="composer-send"');
    expect(chat).toContain('locked={uploading || preparing}');
  });

  it('queues a message sent during a turn, shows it as waiting and can cancel it', () => {
    expect(chat).toContain('const turnRunning = Boolean(useChatStore.getState().streamingBuffers[activeSessionId]);');
    expect(chat).toContain('enqueueTurn(current, sessionId');
    expect(chat).toContain('<QueuedTurns');
    expect(chat).toContain('cancelQueuedTurn(current, activeSessionId, id)');
  });

  it('sends the oldest queued message when the turn ends and puts the text back if it never started', () => {
    expect(chat).toContain('takeQueuedTurn(queue, activeSessionId)');
    expect(chat).toContain("'ui.studio.chat.queued_restored'");
    expect(chat).toContain('restoreAttachments(turn.images, turn.claims)');
  });

  it('does not queue the same message twice while its turn is still starting, and restores a queued one whose start failed', () => {
    expect(chat).toContain('startingRef.current?.sessionId === sessionId && startingRef.current.text === text) return;');
    expect(chat).toContain('const restore = !sent && (replaced || queued) && isCurrentSessionScope(scope) && !controller.signal.aborted;');
  });
});

describe('a late approval', () => {
  it('registers the submitted task at once and falls back to the session task list', () => {
    expect(chat).toMatch(/registerTask\(\{\s*taskId: result\.taskId,/);
    expect(chat).toContain('hydrateSessionTasks');
    expect(chat).toContain('LATE_TASK_HYDRATE_MILLISECONDS');
  });

  it('re-reads the messages when a watched task ends outside a turn, and the session credits whenever tasks change', () => {
    expect(chat).toContain('LATE_RESULT_RELOAD_MILLISECONDS');
    expect(chat).toMatch(/refreshApprovalRef\.current\(\);\s*\}, \[activeSessionId, settledKey, viewsKey\]/);
  });

  it('resets the card clock whenever the set of cards changes, so a fresh 24-hour card never reads 25 hours', () => {
    expect(chat).toContain("const pendingCardKey = pending.items.map((item) => item.requestId).join('\\n');");
    expect(chat).toMatch(/if \(pendingCardKey === ''\) return;\s*setNow\(Date\.now\(\)\);/);
    expect(chat).toContain('}, [pendingCardKey]);');
  });

  it('shows the server refusal on the card instead of a toast that vanishes', () => {
    expect(chat).toContain('error={decisionError[card.requestId] ?? null}');
    expect(chat).toContain("'ui.studio.approval.card.decide_error'");
  });
});

describe('the extra-credit consent', () => {
  it('is asked on the approval card in the chat, not in a checkbox card before the request is sent', () => {
    expect(objectActions).not.toContain('threeD');
    expect(studioStore).not.toContain('threeD');
    expect(chat).not.toContain('ExtraCreditConsentCard');
    expect(chat).not.toContain('confirmConsent');
    expect(chat).toContain('void sendRef.current(turnRequest.text, undefined, undefined, undefined, turnRequest.selection);');
    expect(chat).toContain('<ApprovalCard');
  });
});

describe('task progress in the chat', () => {
  it('draws progress and failure cards from the tracked tasks', () => {
    expect(chat).toContain('<TaskStatusCards part="failed" />');
    expect(chat).toContain('<TaskStatusCards part="running" />');
  });

  // #244: running tasks float above the composer instead of growing the scroll area.
  it('keeps running tasks out of the message scroll container', () => {
    const messagesEnd = chat.indexOf('<div ref={messagesEndRef} />');
    const running = chat.indexOf('<TaskStatusCards part="running" />');
    const failed = chat.indexOf('<TaskStatusCards part="failed" />');
    expect(failed).toBeGreaterThan(-1);
    expect(failed).toBeLessThan(messagesEnd);
    expect(running).toBeGreaterThan(messagesEnd);
    expect(chat).toContain('data-testid="chat-task-float"');
    expect(chat).toContain('paddingBottom: `calc(0.75rem + ${floatingHeight}px)`');
  });
});
