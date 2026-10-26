import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import { deleteConfirmKeyIntent } from './session-menu';

const key = (value: string, extra = {}) => ({ key: value, isComposing: false, keyCode: 0, repeat: false, ...extra });

describe('session delete confirmation keys', () => {
  it('confirms on Enter and cancels on Escape', () => {
    expect(deleteConfirmKeyIntent(key('Enter'))).toBe('confirm');
    expect(deleteConfirmKeyIntent(key('Escape'))).toBe('cancel');
    expect(deleteConfirmKeyIntent(key('a'))).toBeNull();
  });

  it('ignores composition, repeated and anonymous keys', () => {
    expect(deleteConfirmKeyIntent(key('Enter', { isComposing: true }))).toBeNull();
    expect(deleteConfirmKeyIntent(key('Enter', { keyCode: 229 }))).toBeNull();
    expect(deleteConfirmKeyIntent(key('Enter', { repeat: true }))).toBeNull();
    expect(deleteConfirmKeyIntent(key('Escape', { repeat: true }))).toBeNull();
  });
});

describe('session delete confirmation wiring', () => {
  const source = readFileSync(new URL('./session-menu.tsx', import.meta.url), 'utf8');

  it('autofocuses the confirm button and handles Escape inside that row only', () => {
    expect(source).toContain('autoFocus');
    expect(source).toMatch(/role="alert"[\s\S]{0,200}onKeyDown=/);
    expect(source).not.toContain("window.addEventListener('keydown'");
    expect(source).not.toContain('document.addEventListener(\'keydown\'');
  });

  it('keeps the confirmation and its rows on a failed delete, guarded against double submission', () => {
    expect(source).toContain('deletingRef.current');
    expect(source).toMatch(/catch \{[\s\S]{0,200}toast\.error\(schemaText\(schema, 'ui\.studio\.session\.delete_failed'\)\)/);
    expect(source).toMatch(/finally \{[\s\S]{0,120}setDeleting\(null\)/);
    expect(source).toMatch(/await chat\.deleteSession[\s\S]{0,600}setConfirming\(null\)/);
  });
});
