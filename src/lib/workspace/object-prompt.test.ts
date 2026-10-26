import { describe, expect, it } from 'vitest';

import { promptOfObject } from './object-prompt';

const user = (content: string) => ({ role: 'user' as const, content });
const assistant = (taskId: string, shape: 'taskIds' | 'images' | 'attachments' = 'taskIds') => ({
  role: 'assistant' as const,
  content: '好了',
  ...(shape === 'taskIds' ? { taskIds: [taskId] } : {}),
  ...(shape === 'images' ? { images: [{ url: '/api/x', taskId }] } : {}),
  ...(shape === 'attachments' ? { attachments: [{ handle: '@img1', kind: 'image' as const, taskId }] } : {}),
});

describe('promptOfObject', () => {
  const messages = [user('赛博朋克女刺客，全身立绘'), assistant('t1', 'images'), user('换成红黑配色，出 3 张'), assistant('t2', 'attachments')];

  it('is what the person wrote in the turn that made the object', () => {
    expect(promptOfObject(messages, { taskId: 't1' })).toBe('赛博朋克女刺客，全身立绘');
    expect(promptOfObject(messages, { taskId: 't2' })).toBe('换成红黑配色，出 3 张');
    expect(promptOfObject([user('做个模型'), assistant('t9')], { taskId: 't9' })).toBe('做个模型');
  });

  it('is empty when no turn made it', () => {
    expect(promptOfObject(messages, { taskId: 'other' })).toBe('');
    expect(promptOfObject(messages, null)).toBe('');
    expect(promptOfObject([assistant('t1')], { taskId: 't1' })).toBe('');
  });
});
