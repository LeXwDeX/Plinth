import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const chatColumn = readFileSync(new URL('./chat-column.tsx', import.meta.url), 'utf8');

// #335: 确认 on a choice card while the composer's pictures were still being read returned from `send`
// without a word. The answer is now held, the card says it waits, and it goes out once the pictures are read.
describe('a choice answered while attachments are read (#335)', () => {
  const answer = chatColumn.slice(chatColumn.indexOf('const answerChoice = useCallback('), chatColumn.indexOf('const renderChoiceCard ='));

  it('holds the answer instead of sending it into the attachment guard', () => {
    const hold = answer.indexOf('setHeldChoice({');
    expect(hold).toBeGreaterThan(0);
    expect(answer).toMatch(/if \(uploading \|\| attachmentsPending\(attachmentReads, scope\)\) \{\s*setHeldChoice\(\{/);
    expect(hold).toBeLessThan(answer.indexOf('await sendRef.current('));
  });

  it('sends the held answer when the reads end, and drops it after a session change or a closed card', () => {
    expect(answer).toContain('heldChoiceRelease({');
    expect(answer).toMatch(/sameScope: isCurrentSessionScope\(heldChoice\.scope\)/);
    expect(answer).toMatch(/cardOpen: sessionChoices\.cards\.some\(\(card\) => card\.id === heldChoice\.card\.id && card\.state === 'pending'\)/);
    expect(answer).toMatch(/attachmentsBusy: uploading \|\| attachmentsPending\(attachmentReads, heldChoice\.scope\)/);
    expect(answer).toMatch(/streaming: isStreaming/);
    expect(answer).toMatch(/if \(release === 'send'\) void answerChoice\(heldChoice\.card, heldChoice\.selection, heldChoice\.text\);/);
  });

  it('says on the card when the answer was not sent, in the Schema words (ui.chat.choice.failed)', () => {
    const send = chatColumn.slice(chatColumn.indexOf('const send = useCallback('), chatColumn.indexOf('const sendRef = useRef(send);'));
    // `send` tells the caller once the server has the turn; every other way out means the answer did not go.
    expect(send).toMatch(/sent = true;\s*extra\?\.onSent\?\.\(\);/);
    expect(answer).toMatch(/let accepted = false;[\s\S]*onSent: \(\) => \{ accepted = true; \}/);
    expect(answer).toMatch(/if \(!accepted\) setChoiceError\(\{ cardId: card\.id, text: schemaText\(schema, 'ui\.chat\.choice\.failed'\) \}\);/);
    expect(answer).toMatch(/catch \{\s*setChoiceError\(\{ cardId: card\.id, text: schemaText\(schema, 'ui\.chat\.choice\.failed'\) \}\);/);
    // A new attempt clears it; a card that was answered or closed meanwhile shows no failure.
    expect(answer.indexOf('setChoiceError(null);')).toBeLessThan(answer.indexOf('await sendRef.current('));
    const render = chatColumn.slice(chatColumn.indexOf('const renderChoiceCard ='), chatColumn.indexOf('const rerequest = useCallback('));
    expect(render).toContain("error={choiceError?.cardId === card.id && card.state === 'pending' ? choiceError.text : null}");
  });

  it('shows the held card as waiting and locks the other cards', () => {
    const render = chatColumn.slice(chatColumn.indexOf('const renderChoiceCard ='), chatColumn.indexOf('const rerequest = useCallback('));
    expect(render).toContain('waiting={heldChoice?.card.id === card.id}');
    expect(render).toContain('disabled={isStreaming || answeringChoice !== null || heldChoice !== null}');
  });
});

// #374: an option of a direction card may carry an action. The ChatColumn saves the function selection through the
// session Harness (the 功能 panel's own state and CAS save) first and sends the answer only once it is saved.
describe('a choice option with an action (#374)', () => {
  const answer = chatColumn.slice(chatColumn.indexOf('const answerChoice = useCallback('), chatColumn.indexOf('const renderChoiceCard ='));
  const action = readFileSync(new URL('../workspace/harness-choice-action.ts', import.meta.url), 'utf8');

  it('reads the session Harness of the 功能 panel and keeps its latest state for the save', () => {
    expect(chatColumn).toContain("import { useSessionHarness } from '@/hooks/use-session-harness';");
    expect(chatColumn).toMatch(/const sessionHarness = useSessionHarness\(activeSessionId, user\?\.id \?\? ''\);\s*const sessionHarnessRef = useRef\(sessionHarness\);\s*sessionHarnessRef\.current = sessionHarness;/);
  });

  it('runs the action before the answer goes out, and sends nothing when it fails', () => {
    const run = answer.indexOf('runChoiceAnswer(choiceSelectionAction(card, selection), {');
    expect(run).toBeGreaterThan(0);
    // The only send of the answer is the step `runChoiceAnswer` runs after the action: the answer words, or (#709,
    // continue_on_canvas) the previous words as the canvas turn, one or the other.
    expect(answer.match(/await sendRef\.current\(/g)).toHaveLength(2);
    const sendStep = answer.indexOf('send: async (applied) => {');
    expect(run).toBeLessThan(sendStep);
    expect(sendStep).toBeLessThan(answer.indexOf('await sendRef.current('));
    expect(answer).toMatch(/if \(applied\?\.canvasTurnText\) \{[\s\S]*?await sendRef\.current\(applied\.canvasTurnText,[\s\S]*?\}\);\s*return;\s*\}\s*await sendRef\.current\(text,/);
    expect(answer).toMatch(/apply: \(action\) => applyChoiceAction\(action, \{\s*schema,\s*harness: \{ selection: sessionHarnessRef\.current\.view\?\.selection \?\? null, save: sessionHarnessRef\.current\.save \},\s*\/\/ #709[^\n]*\n\s*canvas: \{/);
    expect(answer).toContain('stillHere: () => isCurrentSessionScope(scope),');
    // A failed action is worded on the card in its own Schema words; the existing "not sent" words stay for a send that failed.
    expect(answer).toMatch(/if \(outcome\.status === 'action_failed'\) \{\s*setChoiceError\(\{ cardId: card\.id, text: schemaText\(schema, outcome\.failureKey\) \}\);/);
    expect(answer).toMatch(/\} else if \(outcome\.status === 'sent'\) \{\s*if \(!accepted\) setChoiceError/);
    // The busy state spans the save and the send, and the cards are refreshed afterwards either way.
    expect(answer.indexOf('setAnsweringChoice(card.id);')).toBeLessThan(run);
    expect(answer.indexOf('setAnsweringChoice(null);')).toBeGreaterThan(answer.indexOf('await sendRef.current('));
  });

  it('saves with the panel helpers and the existing Harness save, not a second request', () => {
    expect(action).toContain("from './harness-panel';");
    for (const helper of ['harnessOperationSelection(', 'harnessClearSelection(', 'harnessAutoSelectOffering(', 'harnessSelectionInput(']) expect(action).toContain(helper);
    expect(action).toContain('deps.harness.save(');
    expect(action).not.toMatch(/method: 'PUT'|createHarnessClient|chat_session_harness/);
  });
});
