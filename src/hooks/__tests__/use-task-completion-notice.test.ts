import { readFileSync } from 'node:fs';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { requestPermissionFromGesture } from '@/lib/workspace/completion-notice';
import { useWorkspaceStore } from '@/store/workspace-store';

const application = defaultCatalog.application as unknown as { content: { messages: Record<string, string> }; capabilities: unknown[]; runtime: { frontend: { percentage_multiplier: number } } };
const englishMessages = (defaultCatalog.application as unknown as {
  localization: { bundles: { en: { messages: Record<string, string> } } };
}).localization.bundles.en.messages;
const englishApplication = {
  ...application,
  localization: { ...(defaultCatalog.application as unknown as { localization: object }).localization, active_locale: 'en' },
  content: {
    ...application.content,
    messages: {
      ...application.content.messages,
      'ui.workspace.notice.tab_title': englishMessages['ui.workspace.notice.tab_title'],
      'ui.workspace.notice.tab_title_one': englishMessages['ui.workspace.notice.tab_title_one'],
    },
  },
};

const schemaState = vi.hoisted(() => ({ current: null as unknown }));
vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({
    schema: schemaState.current,
    text: (key: string) => (schemaState.current as typeof application).content.messages[key] ?? '',
  }),
}));
// The hook is one effect; run it directly instead of mounting a component.
let cleanup: (() => void) | undefined;
vi.mock('react', async (original) => ({
  ...(await original<typeof import('react')>()),
  useEffect: (effect: () => (() => void) | void) => { cleanup = effect() ?? undefined; },
}));

import { useTaskCompletionNotice } from '@/hooks/use-task-completion-notice';

class FakeNotification {
  static permission: NotificationPermission = 'granted';
  static requested = 0;
  static created: Array<{ title: string; options: NotificationOptions }> = [];
  static requestPermission() { FakeNotification.requested += 1; return Promise.resolve(FakeNotification.permission); }
  onclick: (() => void) | null = null;
  constructor(title: string, options: NotificationOptions) { FakeNotification.created.push({ title, options }); }
  close() {}
}

const listeners = new Map<string, () => void>();
const storage = new Map<string, string>();
const doc = { title: '画板', visibilityState: 'hidden' as DocumentVisibilityState };

function install(desktop: boolean) {
  vi.stubGlobal('window', {
    matchMedia: () => ({ matches: desktop }),
    localStorage: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => { storage.set(key, value); } },
    focus: () => {},
  });
  vi.stubGlobal('document', {
    get title() { return doc.title; }, set title(value: string) { doc.title = value; },
    get visibilityState() { return doc.visibilityState; },
    addEventListener: (name: string, fn: () => void) => { listeners.set(name, fn); },
    removeEventListener: (name: string) => { listeners.delete(name); },
  });
  vi.stubGlobal('Notification', FakeNotification);
}

const register = (taskId: string) => useWorkspaceStore.getState().registerTask({ taskId, sessionId: 's1', capability: 'text-to-image', prompt: `prompt ${taskId}`, progressMaximum: 100 });
const finish = (taskId: string, outcome: 'done' | 'failed') => useWorkspaceStore.setState((state) => ({
  trackedTasks: state.trackedTasks.map((task) => task.taskId !== taskId ? task : {
    ...task, terminal: true,
    failure: outcome === 'failed' ? { failureClass: 'rejected', detail: 'x', retryable: false, ambiguous: false } : null,
    downloadables: outcome === 'done' ? [{ id: 'a', kind: 'image', role: 'image', variant: '', url: '/x.png', mediaType: 'image/png' } as never] : [],
  }),
}));

beforeEach(() => {
  schemaState.current = application;
  FakeNotification.permission = 'granted'; FakeNotification.requested = 0; FakeNotification.created = [];
  storage.clear(); listeners.clear(); doc.title = '画板'; doc.visibilityState = 'hidden';
  useWorkspaceStore.setState({ trackedTasks: [] });
});
afterEach(() => { cleanup?.(); cleanup = undefined; vi.unstubAllGlobals(); });

describe('task completion notice', () => {
  it('updates the tab title and notifies when a task ends while the tab is hidden', () => {
    install(true);
    useTaskCompletionNotice();
    register('t1');
    finish('t1', 'done');
    expect(doc.title).toBe(application.content.messages['ui.workspace.notice.tab_title'].replace('{count}', '1').replace('{title}', '画板'));
    expect(FakeNotification.created).toHaveLength(1);
    expect(FakeNotification.created[0].title).toBe(application.content.messages['ui.workspace.notice.done']);
    expect(FakeNotification.created[0].options.body).toBe('prompt t1');
    // Coming back to the tab restores the title.
    doc.visibilityState = 'visible';
    listeners.get('visibilitychange')?.();
    expect(doc.title).toBe('画板');
  });

  it('uses the English singular tab title for one finished task', () => {
    schemaState.current = englishApplication;
    install(true);
    useTaskCompletionNotice();
    register('t1');
    finish('t1', 'done');
    expect(doc.title).toBe('(1) Task finished · 画板');
  });

  it('says nothing while the tab is in view', () => {    install(true);
    doc.visibilityState = 'visible';
    useTaskCompletionNotice();
    register('t1');
    finish('t1', 'failed');
    expect(doc.title).toBe('画板');
    expect(FakeNotification.created).toHaveLength(0);
  });

  it('never asks for permission when a task starts or finishes', () => {
    install(true);
    FakeNotification.permission = 'default';
    useTaskCompletionNotice();
    register('t1');
    expect(FakeNotification.requested).toBe(0);
    register('t2');
    finish('t2', 'done');
    expect(FakeNotification.requested).toBe(0);

    cleanup?.();
    FakeNotification.permission = 'denied';
    storage.clear();
    useWorkspaceStore.setState({ trackedTasks: [] });
    useTaskCompletionNotice();
    register('t3');
    finish('t3', 'failed');
    expect(FakeNotification.requested).toBe(0);
    // A refusal still leaves the quiet tab title, but never a popup.
    expect(FakeNotification.created).toHaveLength(0);
    expect(doc.title).toContain('画板');
  });

  it('lets the explicit gesture request run even when the retired asked marker is present', async () => {
    install(true);
    FakeNotification.permission = 'default';
    storage.set('task-notice-permission-asked', '1');
    const events: string[] = [];
    const result = requestPermissionFromGesture(
      () => ({ desktop: true, supported: true, permission: 'default' }),
      () => events.push('pending'),
      () => Notification.requestPermission(),
    );
    events.push('after-call');
    expect(FakeNotification.requested).toBe(1);
    expect(events).toEqual(['pending', 'after-call']);
    await expect(result).resolves.toBe('default');
  });

  it('does nothing on phones and tablets', () => {
    install(false);
    FakeNotification.permission = 'default';
    useTaskCompletionNotice();
    register('t1');
    finish('t1', 'done');
    expect(FakeNotification.requested).toBe(0);
    expect(FakeNotification.created).toHaveLength(0);
  });

  it('ignores cancelled tasks and tasks first seen already finished', () => {
    install(true);
    useWorkspaceStore.setState({ trackedTasks: [{ taskId: 'old', terminal: true, failure: null, downloadables: [], notFound: false, prompt: '', capability: 'text-to-image' } as never] });
    useTaskCompletionNotice();
    register('t1');
    finish('t1', 'done');
    useWorkspaceStore.setState((state) => ({ trackedTasks: state.trackedTasks.map((task) => task.taskId === 't1' ? task : { ...task, terminal: true }) }));
    expect(FakeNotification.created).toHaveLength(1);
  });

  it('does not request permission for terminal tasks restored from history', () => {
    install(true);
    FakeNotification.permission = 'default';
    useWorkspaceStore.setState({ trackedTasks: [{ taskId: 'old', terminal: true, failure: null, downloadables: [], notFound: false, prompt: '', capability: 'text-to-image' } as never] });
    useTaskCompletionNotice();
    expect(FakeNotification.requested).toBe(0);
  });
});

it('is mounted once by the workspace shell', () => {
  const page = readFileSync(new URL('../../components/studio/studio-shell.tsx', import.meta.url), 'utf8');
  expect(page.match(/useTaskCompletionNotice\(\);/g)).toHaveLength(1);
});
