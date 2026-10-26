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
