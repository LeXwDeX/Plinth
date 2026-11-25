import { describe, expect, it } from 'vitest';

import { cardFromConsent, cardFromPending } from '@/lib/chat/approval-card';
import { parseUsedSkills } from '@/lib/chat/browser-client';
import { parsePendingApprovals, parseSkillChoice } from '@/lib/chat/pending-approvals';

describe('#548 used Skills and the approval card Skill line', () => {
  it('reads the used Skills of a reply and drops malformed entries', () => {
    expect(parseUsedSkills([
      { id: 'system-three-view', display_name: '三视图', source: 'agent', applied: true },
      { id: 'personal-a', display_name: '水彩', source: 'user', applied: false },
      { id: '', display_name: 'x', source: 'agent' },
      { id: 'b', display_name: 'y', source: 'someone' },
      'junk',
    ])).toEqual([
      { id: 'system-three-view', displayName: '三视图', source: 'agent', applied: true },
      { id: 'personal-a', displayName: '水彩', source: 'user', applied: false },
    ]);
    expect(parseUsedSkills(undefined)).toEqual([]);
  });

  it('carries the style Skill from the pending item and the in-turn record to the card', () => {
    expect(parseSkillChoice({ id: 'personal-a', name: '水彩', source: 'agent' })).toEqual({ name: '水彩', source: 'agent' });
    expect(parseSkillChoice({ name: '水彩', source: 'robot' })).toBeUndefined();
    const [pending] = parsePendingApprovals({ items: [{
      request_id: 'r1', status: 'pending', tool: 'refine_image', capability: 'text-to-image',
      skill_choice: { id: 'personal-a', name: '水彩', source: 'user' },
    }] });
    expect(pending.skillChoice).toEqual({ name: '水彩', source: 'user' });
    expect(cardFromPending(pending).skillChoice).toEqual({ name: '水彩', source: 'user' });
    expect(cardFromConsent({ capability: 'text-to-image', skill_choice: { name: '水墨', source: 'agent' } }, 'r2')?.skillChoice)
      .toEqual({ name: '水墨', source: 'agent' });
    expect(cardFromConsent({ capability: 'text-to-image' }, 'r3')?.skillChoice).toBeUndefined();
  });
});
