import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

function source(relative: string): string {
  return readFileSync(new URL(relative, import.meta.url), 'utf8');
}

const chat = source('./chat-column.tsx');
const composer = source('./composer.tsx');
const browser = source('../../lib/chat/browser-client.ts');

describe('session style lock in the chat column (#154)', () => {
  it('shows the label from the saved lock and sends the one-turn exemption, then resets it', () => {
    expect(chat).toContain('useStyleLock(chatClient, activeSessionId)');
    expect(chat).toContain("styleLock.lock.enabled && (styleLock.lock.text.trim() !== '' || styleLock.lock.references.length > 0)");
    expect(chat).toContain('...(extra?.styleLockSkipped ? { styleLockSkipped: true } : {})');
    expect(chat).toContain('if (extra?.styleLockSkipped) setStyleLockSkipped(false);');
    expect(chat).toContain('useEffect(() => { setStyleLockSkipped(false); }, [activeSessionId]);');
  });

  it('carries the exemption on a queued turn and restores it if that turn never starts', () => {
    expect(chat).toContain('...(styleLockSkipped ? { styleLockSkipped: true } : {})');
    expect(chat).toContain('...(turn.styleLockSkipped ? { styleLockSkipped: true } : {})');
    expect(chat).toContain('setStyleLockSkipped(turn.styleLockSkipped === true);');
  });

  it('shows the model notice from the 2D Offering the turn would run, not from a guessed main image', () => {
    expect(chat).toContain('const styleLockOffering = offeringForTurn(imageGeneration.offerings.filter((item) => item.available), true);');
    expect(chat).toContain('styleLockCapability(styleLockOffering,');
    expect(chat).toContain("'ui.unavailable.reason.style_lock.model_no_reference'");
    expect(chat).toContain("'ui.studio.style_lock.references_trimmed'");
  });

  it('keeps the label and the wire member in the composer and the Schema-bound request', () => {
    expect(composer).toContain('data-testid="composer-style-lock"');
    expect(composer).toContain('data-testid="composer-style-lock-skip"');
    expect(browser).toContain('options.styleLockSkipped');
    expect(browser).toContain('fields.style_lock_skipped');
  });
});