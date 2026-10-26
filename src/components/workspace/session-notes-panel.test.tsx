import React from 'react';
import { readFileSync } from 'node:fs';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { SessionNotesPanel, followSavedNotes, notesDraftState, sessionNotesText } from './session-notes-panel';

const withoutComments = (source: string) => source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

describe('session notes panel', () => {
  it('takes every visible string and the bound from the Product Schema', () => {
    const schema = schemaFixture();
    const text = sessionNotesText(schema);
    expect(text.label).toBe('设定摘要');
    expect(text.counter(12, schema.chat.context.maximum_session_notes_characters)).toBe('12 / 2000');
    schema.content.messages['ui.workspace.notes.label'] = 'configured label';
    schema.content.messages['ui.workspace.notes.counter'] = '{count} of {maximum}';
    const changed = sessionNotesText(schema);
    expect(changed.label).toBe('configured label');
    expect(changed.counter(1, 50)).toBe('1 of 50');
  });

  it('is collapsed by default and shows a marker only when notes exist', () => {
    const text = sessionNotesText(schemaFixture());
    const empty = renderToStaticMarkup(<SessionNotesPanel text={text} saved="" maximum={100} onSave={() => true} />);
    expect(empty).toContain('aria-expanded="false"');
    expect(empty).not.toContain('session-notes-body');
    expect(empty).not.toContain('session-notes-filled');
    const filled = renderToStaticMarkup(<SessionNotesPanel text={text} saved="hero: red scarf" maximum={100} onSave={() => true} />);
    expect(filled).toContain('session-notes-filled');
    expect(filled).not.toContain('hero: red scarf');
  });

  it('shows the saved text, the counter and a disabled save when expanded and unchanged', () => {
    const text = sessionNotesText(schemaFixture());
    const html = renderToStaticMarkup(
      <SessionNotesPanel text={text} saved="hero: red scarf" maximum={100} defaultExpanded onSave={() => true} />,
    );
    expect(html).toContain('aria-expanded="true"');
    expect(html).toContain('>hero: red scarf</textarea>');
    expect(html).toContain('15 / 100');
    expect(html).toMatch(/disabled=""[^>]*>保存</);
  });

  it('judges the draft against the bound and the saved text', () => {
    expect(notesDraftState('abc', 'abc', 5)).toEqual({ count: 3, dirty: false, overLimit: false, canSave: false });
    expect(notesDraftState('abcd ', 'abc', 5)).toMatchObject({ dirty: true, canSave: true, count: 4 });
    expect(notesDraftState('abcdef', 'abc', 5)).toMatchObject({ overLimit: true, canSave: false });
    // Characters, not UTF-16 units: an astral character counts once.
    expect(notesDraftState('😀😀', '', 2)).toMatchObject({ count: 2, overLimit: false, canSave: true });
    expect(notesDraftState('  ', '', 5)).toMatchObject({ dirty: false, canSave: false });
  });

  it('keeps an untouched draft on the saved notes as they load or change, and an edited one as it is', () => {
    // The panel mounts before the notes arrive (#263): the loaded notes must reach the empty, untouched draft.
    const mounted = { draft: '', base: '' };
    const loaded = followSavedNotes(mounted, 'hero: red scarf');
    expect(loaded).toEqual({ draft: 'hero: red scarf', base: 'hero: red scarf' });
    expect(notesDraftState(loaded.draft, 'hero: red scarf', 100)).toMatchObject({ dirty: false, canSave: false });
    // The Agent rewrites the notes: an untouched draft follows them.
    expect(followSavedNotes(loaded, 'hero: blue scarf')).toEqual({ draft: 'hero: blue scarf', base: 'hero: blue scarf' });
    // An edit the user has not saved stays, measured against the new saved text.
    const edited = { draft: 'hero: green scarf', base: 'hero: red scarf' };
    expect(followSavedNotes(edited, 'hero: blue scarf')).toEqual({ draft: 'hero: green scarf', base: 'hero: blue scarf' });
    // A user who cleared the box on purpose keeps the empty draft.
    expect(followSavedNotes({ draft: '', base: 'hero: red scarf' }, 'hero: blue scarf')).toEqual({ draft: '', base: 'hero: blue scarf' });
    // Nothing changed: the very same state comes back (no re-render loop).
    expect(followSavedNotes(loaded, 'hero: red scarf')).toBe(loaded);
  });

  it('derives the draft from the saved notes while rendering, not from the first value it saw', () => {
    const source = withoutComments(readFileSync(new URL('./session-notes-panel.tsx', import.meta.url), 'utf8'));
    expect(source).toContain('followSavedNotes(notes, saved)');
    expect(source).not.toMatch(/useState\(saved\)/);
  });

  it('is mounted per session so an unsaved draft never crosses sessions', () => {
    const source = readFileSync(new URL('../studio/chat-column.tsx', import.meta.url), 'utf8');
    expect(source).toMatch(/<SessionNotesPanel\s+key=\{activeSessionId\}/);
    expect(source).toContain('void refreshNotes();');
  });
});
