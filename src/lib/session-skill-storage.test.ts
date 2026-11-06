import { beforeEach, describe, expect, it } from 'vitest';
import { readSessionSkill, writeSessionSkill } from './session-skill-storage';

describe('session Skill storage', () => {
  const data = new Map<string, string>();
  const storage = {
    getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => { data.set(key, value); },
    removeItem: (key: string) => { data.delete(key); },
  };
  beforeEach(() => data.clear());

  it('isolates selections by owner and session and removes a cleared selection', () => {
    writeSessionSkill(storage, 'owner-a', 'session-a', 'skill-a');
    expect(readSessionSkill(storage, 'owner-a', 'session-a')).toBe('skill-a');
    expect(readSessionSkill(storage, 'owner-a', 'session-b')).toBeNull();
    expect(readSessionSkill(storage, 'owner-b', 'session-a')).toBeNull();
    writeSessionSkill(storage, 'owner-a', 'session-a', null);
    expect(readSessionSkill(storage, 'owner-a', 'session-a')).toBeNull();
  });

  it('rejects malformed stored values and tolerates unavailable storage', () => {
    writeSessionSkill(storage, 'owner-a', 'session-a', 'skill-a');
    const key = [...data.keys()][0];
    for (const value of ['{', 'null', '42', '""', '{}']) {
      data.set(key, value);
      expect(readSessionSkill(storage, 'owner-a', 'session-a')).toBeNull();
    }
    const broken = { getItem: () => { throw Error(); }, setItem: () => { throw Error(); }, removeItem: () => { throw Error(); } };
    expect(readSessionSkill(broken, 'owner-a', 'session-a')).toBeNull();
    expect(() => writeSessionSkill(broken, 'owner-a', 'session-a', 'skill-a')).not.toThrow();
  });
});
