import { describe, expect, it, vi } from 'vitest';
import { branchConversation, branchCopiedCurrentState, branchUnavailableReason } from './session-branch';
import { useChatStore } from '@/store/chat-store';

describe('session branch', () => {
  it('gives a reason for busy, pending and compacting sessions but never for 3D', () => {
    expect(branchUnavailableReason({ running: true })).toBe('ui.unavailable.reason.branch.running');
    expect(branchUnavailableReason({ approval: true })).toBe('ui.unavailable.reason.branch.approval');
    expect(branchUnavailableReason({ compacting: true })).toBe('ui.unavailable.reason.branch.compacting');
    expect(branchUnavailableReason({ busy: true })).toBe('ui.unavailable.reason.branch.busy');
    expect(branchUnavailableReason({})).toBe('');
  });

  it('says the current state was copied when the canvas, scene or captures were copied', () => {
    expect(branchCopiedCurrentState({ canvasCopied: true, sceneCopied: false, capturesCopied: false })).toBe(true);
    expect(branchCopiedCurrentState({ canvasCopied: false, sceneCopied: true, capturesCopied: false })).toBe(true);
    expect(branchCopiedCurrentState({ canvasCopied: false, sceneCopied: false, capturesCopied: true })).toBe(true);
    expect(branchCopiedCurrentState({ canvasCopied: false, sceneCopied: false, capturesCopied: false })).toBe(false);
  });

  it('switches and refreshes on success, and leaves the current session on failure', async () => {
    const source = { id: 'source', title: 'Source', createdAt: '', updatedAt: '' };
    useChatStore.setState({ sessions: [source], activeSessionId: 'source', sessionListRevision: 0 });
    const select = useChatStore.getState().addSession;
    const clear = vi.fn();
    const done = vi.fn();
    const failed = vi.fn();
    const deps = { flush: async () => true, isCurrent: () => useChatStore.getState().activeSessionId === 'source', select, clear, remember: vi.fn(), done, failed };
    const branch = vi.fn(async () => ({
      session: { id: 'new', title: 'New', createdAt: '', updatedAt: '' },
      canvasCopied: true, sceneCopied: true, capturesCopied: false,
    }));
    await branchConversation(branch, 'source', 'message', deps);
    expect(useChatStore.getState().activeSessionId).toBe('new');
    expect(useChatStore.getState().sessionListRevision).toBe(1);
    expect(clear).toHaveBeenCalledOnce();
    expect(done).toHaveBeenCalledWith({ canvasCopied: true, sceneCopied: true, capturesCopied: false });
    useChatStore.getState().setActiveSession('source');
    await branchConversation(async () => { throw new Error('server refusal'); }, 'source', 'message', deps);
    expect(useChatStore.getState().activeSessionId).toBe('source');
    expect(useChatStore.getState().sessionListRevision).toBe(1);
    expect(failed).toHaveBeenCalledWith(expect.objectContaining({ message: 'server refusal' }));
  });

  it('does not switch a session the person left while the request was running', async () => {
    const select = vi.fn();
    const clear = vi.fn();
    const remember = vi.fn();
    let current = true;
    await branchConversation(async () => {
      current = false;
      return {
        session: { id: 'new', title: '', createdAt: '', updatedAt: '' },
        canvasCopied: false, sceneCopied: false, capturesCopied: false,
      };
    }, 'source', 'message', { flush: async () => true, isCurrent: () => current, select, clear, remember, done: vi.fn(), failed: vi.fn() });
    expect(select).not.toHaveBeenCalled();
    expect(clear).not.toHaveBeenCalled();
    expect(remember).toHaveBeenCalledOnce();
  });
});