import { describe, expect, it } from 'vitest';

import { draftFromState, draftKey, loadDraft, saveDraft } from './composer-draft';

function memory() {
  const data = new Map<string, string>();
  return { data, getItem: (k: string) => data.get(k) ?? null, setItem: (k: string, v: string) => { data.set(k, v); }, removeItem: (k: string) => { data.delete(k); } };
}

describe('composer draft', () => {
  // #697: drafts stored before the purpose control was removed still carry `purpose`/`purposes`; they are ignored.
  it('reads an older draft with purposes without error and drops them', () => {
    const storage = memory();
    storage.data.set(draftKey('legacy'), JSON.stringify({ text: 'hi', images: ['/a', '/b'], purpose: 'layout', purposes: { '/a': 'style', '/b': 'input' } }));
    const draft = loadDraft('legacy', storage);
    expect(draft).toEqual({ text: 'hi', images: ['/a', '/b'], sources: {}, dropped: 0 });
    storage.data.set(draftKey('untrusted'), JSON.stringify({ images: ['/a'], purpose: 'bogus', purposes: 'not an object' }));
    expect(loadDraft('untrusted', storage)).toEqual({ text: '', images: ['/a'], sources: {}, dropped: 0 });
    // A draft that held nothing but a purpose is empty now.
    storage.data.set(draftKey('only-purpose'), JSON.stringify({ text: '', images: [], purpose: 'style' }));
    expect(loadDraft('only-purpose', storage)).toBeNull();
    saveDraft('legacy', draft!, storage);
    const stored = JSON.parse(storage.data.get(draftKey('legacy'))!);
    expect(stored).not.toHaveProperty('purpose');
    expect(stored).not.toHaveProperty('purposes');
  });

  it('keeps text and server pictures, drops local ones and counts them', () => {
    const draft = draftFromState({
      text: 'hi', images: ['/api/v1/tasks/t/assets/a', 'data:image/png;base64,xx'],
      sources: { '/api/v1/tasks/t/assets/a': { assetId: 'a', taskId: 't' } }
    });
    expect(draft.images).toEqual(['/api/v1/tasks/t/assets/a']);
    expect(draft.dropped).toBe(1);
    expect(draft.sources['/api/v1/tasks/t/assets/a']).toEqual({ assetId: 'a', taskId: 't' });
  });

  it('round-trips per session and removes an empty draft', () => {
    const storage = memory();
    saveDraft('s1', draftFromState({ text: 'hello', images: [], sources: {} }), storage);
    expect(loadDraft('s1', storage)?.text).toBe('hello');
    expect(loadDraft('s2', storage)).toBeNull();
    saveDraft('s1', draftFromState({ text: '  ', images: [], sources: {} }), storage);
    expect(storage.data.has(draftKey('s1'))).toBe(false);
  });

  it('treats stored data as untrusted and survives broken storage', () => {
    const storage = memory();
    storage.data.set(draftKey('s1'), '{"text":5,"images":["data:x","/a"],"purpose":"bogus"}');
    expect(loadDraft('s1', storage)).toEqual({ text: '', images: ['/a'], sources: {}, dropped: 0 });
    storage.data.set(draftKey('s1'), 'not json');
    expect(loadDraft('s1', storage)).toBeNull();
    const broken = { getItem: () => { throw new Error('x'); }, setItem: () => { throw new Error('x'); }, removeItem: () => { throw new Error('x'); } };
    expect(loadDraft('s1', broken)).toBeNull();
    expect(() => saveDraft('s1', draftFromState({ text: 'a', images: [], sources: {} }), broken)).not.toThrow();
  });
});
