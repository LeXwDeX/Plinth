import { describe, expect, it } from 'vitest';

import { draftFromState, draftKey, loadDraft, saveDraft } from './composer-draft';

function memory() {
  const data = new Map<string, string>();
  return { data, getItem: (k: string) => data.get(k) ?? null, setItem: (k: string, v: string) => { data.set(k, v); }, removeItem: (k: string) => { data.delete(k); } };
}

describe('composer draft', () => {
  it('preserves distinct per-image purposes, drops removed or local entries, and migrates legacy purpose', () => {
    const storage = memory();
    const draft = draftFromState({ text: '', images: ['/a', '/b', 'data:x'], sources: {}, purpose: 'input',
      purposes: { '/a': 'style', '/b': 'input', '/removed': 'layout', 'data:x': 'layout' } });
    saveDraft('s', draft, storage);
    expect(loadDraft('s', storage)?.purposes).toEqual({ '/a': 'style', '/b': 'input' });
    storage.data.set(draftKey('legacy'), JSON.stringify({ text: '', images: ['/a', '/b'], purpose: 'layout' }));
    expect(loadDraft('legacy', storage)).toMatchObject({ purpose: 'layout', purposes: { '/a': 'layout', '/b': 'layout' } });
    storage.data.set(draftKey('untrusted'), JSON.stringify({ images: ['/a', '/b'], purpose: 'style', purposes: { '/a': 'invalid', '/b': null, '/foreign': 'input' } }));
    expect(loadDraft('untrusted', storage)?.purposes).toEqual({ '/b': null });
  });

  it('keeps the last choice when there are no images', () => {
    const storage = memory();
    saveDraft('s', draftFromState({ text: '', images: [], sources: {}, purpose: 'style' }), storage);
    expect(loadDraft('s', storage)?.purpose).toBe('style');
  });
  it('keeps text and server pictures, drops local ones and counts them', () => {
    const draft = draftFromState({
      text: 'hi', images: ['/api/v1/tasks/t/assets/a', 'data:image/png;base64,xx'],
      sources: { '/api/v1/tasks/t/assets/a': { assetId: 'a', taskId: 't' } }, purpose: 'style',
    });
    expect(draft.images).toEqual(['/api/v1/tasks/t/assets/a']);
    expect(draft.dropped).toBe(1);
    expect(draft.sources['/api/v1/tasks/t/assets/a']).toEqual({ assetId: 'a', taskId: 't' });
  });

  it('round-trips per session and removes an empty draft', () => {
    const storage = memory();
    saveDraft('s1', draftFromState({ text: 'hello', images: [], sources: {}, purpose: null }), storage);
    expect(loadDraft('s1', storage)?.text).toBe('hello');
    expect(loadDraft('s2', storage)).toBeNull();
    saveDraft('s1', draftFromState({ text: '  ', images: [], sources: {}, purpose: null }), storage);
    expect(storage.data.has(draftKey('s1'))).toBe(false);
  });

  it('treats stored data as untrusted and survives broken storage', () => {
    const storage = memory();
    storage.data.set(draftKey('s1'), '{"text":5,"images":["data:x","/a"],"purpose":"bogus"}');
    expect(loadDraft('s1', storage)).toEqual({ text: '', images: ['/a'], sources: {}, purpose: null, dropped: 0 });
    storage.data.set(draftKey('s1'), 'not json');
    expect(loadDraft('s1', storage)).toBeNull();
    const broken = { getItem: () => { throw new Error('x'); }, setItem: () => { throw new Error('x'); }, removeItem: () => { throw new Error('x'); } };
    expect(loadDraft('s1', broken)).toBeNull();
    expect(() => saveDraft('s1', draftFromState({ text: 'a', images: [], sources: {}, purpose: null }), broken)).not.toThrow();
  });
});
