import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import { retainOnRefresh, type ResourceSnapshot } from '../use-retained-on-refresh';

const loading: ResourceSnapshot<string[]> = { data: null, failed: false, loading: true };
const loaded = (data: string[]): ResourceSnapshot<string[]> => ({ data, failed: false, loading: false });

describe('retainOnRefresh (#238)', () => {
  it('keeps the last list on screen while the same resource refreshes, then shows the new one', () => {
    let step = retainOnRefresh(null, loaded(['a', 'b']), 'u|r');
    step = retainOnRefresh(step.last, loading, 'u|r');
    expect(step.shown).toEqual({ data: ['a', 'b'], failed: false, loading: false });
    step = retainOnRefresh(step.last, loaded(['a']), 'u|r');
    expect(step.shown.data).toEqual(['a']);
  });

  it('starts empty for a different user, session, model or revision', () => {
    const first = retainOnRefresh(null, loaded(['a']), 'u|r1');
    expect(retainOnRefresh(first.last, loading, 'u|r2').shown).toEqual(loading);
  });

  it('shows a failed refresh as a failure and forgets the old list', () => {
    const first = retainOnRefresh(null, loaded(['a']), 'u|r');
    const failed = retainOnRefresh(first.last, { data: null, failed: true, loading: false }, 'u|r');
    expect(failed.shown.failed).toBe(true);
    expect(failed.last).toBeNull();
  });

  it('is what both Agent configuration hooks return, keyed without the refresh version', () => {
    const catalog = readFileSync('src/hooks/use-agent-skill-catalog.ts', 'utf8');
    const effective = readFileSync('src/hooks/use-agent-effective-configuration.ts', 'utf8');
    expect(catalog).toContain("useRetainedOnRefresh(live, user?.id ?? '')");
    expect(effective).toContain("useRetainedOnRefresh(live, `${user?.id ?? ''}|${sessionId ?? ''}|${domain}|${modelId ?? ''}`)");
  });
});
