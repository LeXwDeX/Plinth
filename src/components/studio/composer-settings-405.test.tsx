// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ComposerSettings, type ComposerSettingsProps } from '@/components/studio/composer-settings';
import { COMPOSER_SETTINGS_TABS } from '@/lib/workspace/composer-settings';
import { openComposerSettings, useComposerSettingsStore } from '@/store/composer-settings-store';
import { useWorkspaceStore } from '@/store/workspace-store';

// #405: the gear beside the send button and the dialog behind it. The four tabs' contents are stand-ins carrying
// the test ids the real panels carry; what is under test is the entry, the tabs, the keyboard, the focus and the
// "changed" marker.
const schema = defaultCatalog.application as unknown as { content: { messages: Record<string, string> } };
const messages = schema.content.messages;
const english = (defaultCatalog.application as unknown as { localization: { bundles: { en: { messages: Record<string, string> } } } }).localization.bundles.en.messages;

const mock = vi.hoisted(() => ({
  chat: { offered: true, sections: [] as unknown[], value: 'glm-5.3 · high', changed: false, attention: [] as string[] },
  params: { pinned: false },
  hint: [] as Array<Record<string, unknown>>,
  cleared: [] as string[],
}));

vi.mock('@/components/product-schema-provider', async () => {
  const catalog = await import('../../../internal/catalog/default_catalog.json');
  return { useProductSchema: () => ({ schema: catalog.default.application }) };
});
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children, ...content }: { children: React.ReactNode } & Record<string, unknown>) => {
    mock.hint.push(content);
    return <span data-testid="gear-hint" data-name={String(content.name ?? '')} data-hint={String(content.hint ?? '')} data-reason={String(content.reason ?? '')}>{children}</span>;
  },
}));
vi.mock('@/components/studio/model-picker', () => ({
  useDomainModelPick: (domain: 'image' | 'model-3d') => ({
    section: { title: domain }, short: domain === 'image' ? 'GPT IMAGE' : 'MESHY', full: domain,
    clear: () => { mock.cleared.push(domain); useWorkspaceStore.getState().setModel(domain, null); },
  }),
  generationModelPair: (_schema: unknown, image: { short: string }, model: { short: string }) => `${image.short} · ${model.short}`,
  ModelPicker: () => <div data-testid="workspace-model-picker-panel"><button type="button">pick model</button></div>,
}));
vi.mock('@/components/studio/skill-picker', () => ({
  SkillPicker: () => <div data-testid="workspace-skill-picker-panel"><button type="button">pick skill</button></div>,
}));
vi.mock('@/components/studio/chat-model-picker', () => ({
  useChatModelSettings: () => mock.chat,
  ChatModelPicker: () => <div data-testid="workspace-chat-model-picker-panel"><button type="button">pick chat model</button></div>,
}));
vi.mock('@/components/studio/params-popover', () => ({
  ParamsPanel: () => <div data-testid="composer-params-panel"><button type="button">pick size</button></div>,
  paramsSummary: () => '9:16 · 1',
}));

const noop = () => undefined;
const generation = (states: Record<string, { source: 'default' | 'agent' | 'user' }> = {}) => ({ domain: 'image', parameterStates: states });
function props(overrides: Partial<ComposerSettingsProps> = {}): ComposerSettingsProps {
  return {
    locked: false,
    skill: { sessionId: 's1', catalog: { revision: 'r', skills: [{ id: 'three-view', displayName: '三视图', name: '三视图', prompt: 'p', recommendedSize: '' }] } as never, effective: null, selectedId: null, unavailable: false, loading: false, failed: false, disabled: false, onSelect: noop, onCreated: noop },
    params: {
      params: generation(), domains: [generation(), generation({ ...(mock.params.pinned ? { ratio: { source: 'user' as const } } : {}) })] as never,
      hasSession: true, skill: null, effective: {} as never, canvas: null,
    } as never,
    ...overrides,
  };
}

const gear = () => screen.getByTestId('composer-settings-open');
const tab = (id: string) => screen.getByTestId(`composer-settings-tab-${id}`);

/** Opens from the gear and waits for the dialog to take the focus (onto the selected tab) before anything else happens. */
async function openFromGear() {
  fireEvent.click(gear());
  const dialog = await screen.findByRole('dialog');
  await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
  return dialog;
}

beforeEach(() => {
  vi.stubGlobal('React', React);
  mock.chat = { offered: true, sections: [], value: 'glm-5.3 · high', changed: false, attention: [] };
  mock.params.pinned = false;
  mock.hint.length = 0;
  mock.cleared.length = 0;
  useWorkspaceStore.setState({ modelByDomain: { image: null, 'model-3d': null } });
  useComposerSettingsStore.setState({ open: false, tab: 'models' });
});
afterEach(() => { cleanup(); });

describe('the gear (#405)', () => {
  it('is one focusable button named from the Schema, with no dot while every setting is the default', () => {
    render(<ComposerSettings {...props()} />);
    const button = gear();
    expect(button.tagName).toBe('BUTTON');
    expect(button.getAttribute('aria-label')).toBe(messages['ui.studio.composer.settings.title']);
    expect(button.getAttribute('aria-haspopup')).toBe('dialog');
    expect(button.getAttribute('aria-expanded')).toBe('false');
    expect(button.getAttribute('data-changed')).toBe('false');
    expect(screen.queryByTestId('composer-settings-dot')).toBeNull();
    expect(screen.getByTestId('gear-hint').getAttribute('data-hint')).toBe(messages['ui.studio.composer.settings.hint']);
    button.focus();
    expect(document.activeElement).toBe(button);
  });

  it('has an English name and hint in the Schema too', () => {
    expect(english['ui.studio.composer.settings.title']).toBeTruthy();
    expect(english['ui.studio.composer.settings.hint']).toBeTruthy();
    expect(english['ui.studio.composer.settings.close']).toBeTruthy();
    expect(english['ui.studio.composer.settings.tabs']).toBeTruthy();
    expect(english['ui.studio.composer.settings.changed']).toContain('{items}');
    expect(english['ui.studio.composer.settings.label_changed']).toContain('{items}');
  });

  it('shows a dot and names what changed when a Skill is selected', () => {
    const view = props();
    render(<ComposerSettings {...view} skill={{ ...view.skill, selectedId: 'three-view' }} />);
    const button = gear();
    expect(button.getAttribute('data-changed')).toBe('true');
    expect(screen.getByTestId('composer-settings-dot').getAttribute('aria-hidden')).toBe('true');
    const label = button.getAttribute('aria-label') ?? '';
    expect(label).toContain(messages['ui.studio.composer.settings.title']);
    expect(label).toContain(messages['ui.studio.composer.settings.tab.skills']);
    expect(label).toContain('三视图');
    expect(screen.getByTestId('gear-hint').getAttribute('data-hint')).toContain('三视图');
  });

  // #670: before the Skill catalog is read, the selected Skill reads as loading, never as its id.
  it('names a selected Skill as loading until its catalog is read', () => {
    const view = props();
    render(<ComposerSettings {...view} skill={{ ...view.skill, catalog: undefined as never, selectedId: 'three-view' }} />);
    const label = gear().getAttribute('aria-label') ?? '';
    expect(label).toContain(messages['ui.workspace.skills.loading']);
    expect(label).not.toContain('three-view');
  });

  it('shows a dot when a generation model is locked, with the model pair as its value', () => {
    useWorkspaceStore.setState({ modelByDomain: { image: 'gpt-image', 'model-3d': null } });
    render(<ComposerSettings {...props()} />);
    expect(gear().getAttribute('data-changed')).toBe('true');
    expect(gear().getAttribute('aria-label')).toContain(messages['ui.studio.composer.settings.tab.models']);
    expect(gear().getAttribute('aria-label')).toContain('GPT IMAGE · MESHY');
  });

  it('shows a dot for a parameter set by hand and for a chat model or level off its default', () => {
    mock.params.pinned = true;
    mock.chat = { ...mock.chat, changed: true };
    render(<ComposerSettings {...props()} />);
    const label = gear().getAttribute('aria-label') ?? '';
    expect(label).toContain(messages['ui.studio.composer.settings.tab.params']);
    expect(label).toContain('9:16 · 1');
    expect(label).toContain(messages['ui.studio.composer.settings.tab.chat']);
    expect(label).toContain('glm-5.3 · high');
  });

  it('lists the changed settings in tab order', () => {
    useWorkspaceStore.setState({ modelByDomain: { image: 'gpt-image', 'model-3d': 'meshy' } });
    mock.chat = { ...mock.chat, changed: true };
    const view = props();
    render(<ComposerSettings {...view} skill={{ ...view.skill, selectedId: 'three-view' }} />);
    const label = gear().getAttribute('aria-label') ?? '';
    const at = (tabId: string) => label.indexOf(messages[`ui.studio.composer.settings.tab.${tabId}`]);
    expect(at('models')).toBeGreaterThan(-1);
    expect(at('models')).toBeLessThan(at('skills'));
    expect(at('skills')).toBeLessThan(at('chat'));
  });

  it('warns on the gear when the selected Skill cannot be used or the chat model could not be read', () => {
    const view = props();
    const { rerender } = render(<ComposerSettings {...view} skill={{ ...view.skill, selectedId: 'three-view', unavailable: true }} />);
    expect(gear().getAttribute('data-attention')).toBe('true');
    expect(screen.getByTestId('gear-hint').getAttribute('data-reason')).toContain(messages['ui.unavailable.reason.picker.skill_unavailable']);
    rerender(<ComposerSettings {...view} skill={{ ...view.skill, selectedId: 'three-view', failed: true }} />);
    expect(screen.getByTestId('gear-hint').getAttribute('data-reason')).toContain(messages['ui.unavailable.reason.picker.skill_load_failed']);
    rerender(<ComposerSettings {...view} />);
    expect(gear().getAttribute('data-attention')).toBe('false');
    mock.chat = { ...mock.chat, attention: [messages['ui.unavailable.reason.picker.chat_model_load_failed']] };
    rerender(<ComposerSettings {...view} />);
    expect(gear().getAttribute('data-attention')).toBe('true');
    expect(screen.getByTestId('gear-hint').getAttribute('data-reason')).toContain(messages['ui.unavailable.reason.picker.chat_model_load_failed']);
  });
});

describe('the settings dialog (#405)', () => {
  it('opens from the gear with the four tabs of the Issue and the first one selected', async () => {
    render(<ComposerSettings {...props()} />);
    expect(screen.queryByRole('dialog')).toBeNull();
    const dialog = await openFromGear();
    expect(gear().getAttribute('aria-expanded')).toBe('true');
    expect(within(dialog).getByText(messages['ui.studio.composer.settings.title'])).toBeTruthy();
    const list = within(dialog).getByRole('tablist');
    expect(list.getAttribute('aria-label')).toBe(messages['ui.studio.composer.settings.tabs']);
    const tabs = within(list).getAllByRole('tab');
    expect(tabs.map((item) => item.textContent)).toEqual(['生成模型', 'Skills', '生成参数', '对话模型与思考档位']);
    expect(tabs.map((item) => item.getAttribute('aria-selected'))).toEqual(['true', 'false', 'false', 'false']);
    expect(tabs.map((item) => item.getAttribute('tabindex'))).toEqual(['0', '-1', '-1', '-1']);
    expect(within(dialog).getByRole('tabpanel')).toBeTruthy();
  });

  it('shows each tab its own content, and only that content', async () => {
    render(<ComposerSettings {...props()} />);
    await openFromGear();
    const panels = { models: 'workspace-model-picker-panel', skills: 'workspace-skill-picker-panel', params: 'composer-params-panel', chat: 'workspace-chat-model-picker-panel' };
    for (const id of COMPOSER_SETTINGS_TABS) {
      fireEvent.click(tab(id));
      await waitFor(() => expect(tab(id).getAttribute('aria-selected')).toBe('true'));
      expect(await screen.findByTestId(panels[id])).toBeTruthy();
      for (const other of COMPOSER_SETTINGS_TABS.filter((name) => name !== id)) expect(screen.queryByTestId(panels[other])).toBeNull();
      expect(screen.getByRole('tabpanel').contains(screen.getByTestId(panels[id]))).toBe(true);
    }
  });

  it('moves between the tabs with the left and right arrows, wrapping at the ends', async () => {
    render(<ComposerSettings {...props()} />);
    await openFromGear();
    tab('models').focus();
    fireEvent.keyDown(tab('models'), { key: 'ArrowRight' });
    await waitFor(() => expect(tab('skills').getAttribute('aria-selected')).toBe('true'));
    await waitFor(() => expect(document.activeElement).toBe(tab('skills')));
    expect(await screen.findByTestId('workspace-skill-picker-panel')).toBeTruthy();
    fireEvent.keyDown(tab('skills'), { key: 'ArrowLeft' });
    await waitFor(() => expect(tab('models').getAttribute('aria-selected')).toBe('true'));
    fireEvent.keyDown(tab('models'), { key: 'ArrowLeft' });
    await waitFor(() => expect(tab('chat').getAttribute('aria-selected')).toBe('true'));
    await waitFor(() => expect(document.activeElement).toBe(tab('chat')));
    expect(await screen.findByTestId('workspace-chat-model-picker-panel')).toBeTruthy();
    fireEvent.keyDown(tab('chat'), { key: 'ArrowRight' });
    await waitFor(() => expect(tab('models').getAttribute('aria-selected')).toBe('true'));
  });

  it('closes on Escape and gives the focus back to the gear', async () => {
    render(<ComposerSettings {...props()} />);
    gear().focus();
    const dialog = await openFromGear();
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await act(async () => {});
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(gear()));
    expect(gear().getAttribute('aria-expanded')).toBe('false');
  });

  it('closes with its own close button and keeps the focus on the gear', async () => {
    render(<ComposerSettings {...props()} />);
    const dialog = await openFromGear();
    fireEvent.click(within(dialog).getByRole('button', { name: messages['ui.studio.composer.settings.close'] }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(gear()));
  });

  it('opens on the tab it was left on', async () => {
    render(<ComposerSettings {...props()} />);
    await openFromGear();
    fireEvent.click(tab('params'));
    await waitFor(() => expect(tab('params').getAttribute('aria-selected')).toBe('true'));
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await openFromGear();
    expect(tab('params').getAttribute('aria-selected')).toBe('true');
    expect(screen.getByTestId('composer-params-panel')).toBeTruthy();
  });

  it('opens on the tab other code asks for, and the focus still returns to the gear', async () => {
    render(<ComposerSettings {...props()} />);
    act(() => openComposerSettings('skills'));
    await screen.findByRole('dialog');
    expect(tab('skills').getAttribute('aria-selected')).toBe('true');
    expect(screen.getByTestId('workspace-skill-picker-panel')).toBeTruthy();
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(gear()));
  });

  it('moves to the requested tab when it is asked for while the dialog is open', async () => {
    render(<ComposerSettings {...props()} />);
    await openFromGear();
    act(() => openComposerSettings('chat'));
    await waitFor(() => expect(tab('chat').getAttribute('aria-selected')).toBe('true'));
  });

  it('leaves the chat model tab out when there is nothing to choose, and falls back to the first tab', async () => {
    mock.chat = { ...mock.chat, offered: false, value: '' };
    render(<ComposerSettings {...props()} />);
    act(() => openComposerSettings('chat'));
    await screen.findByRole('dialog');
    const tabs = screen.getAllByRole('tab');
    expect(tabs.map((item) => item.textContent)).toEqual(['生成模型', 'Skills', '生成参数']);
    expect(tab('models').getAttribute('aria-selected')).toBe('true');
    expect(screen.queryByTestId('workspace-chat-model-picker-panel')).toBeNull();
  });

  it('is sized as a large dialog that scrolls inside and never outgrows the window', async () => {
    render(<ComposerSettings {...props()} />);
    const dialog = await openFromGear();
    const classes = dialog.className.split(/\s+/);
    // Wider than the 59rem popovers it replaces, bounded by the window on both axes.
    expect(classes).toContain('w-[min(76rem,calc(100vw-2rem))]');
    expect(classes).toContain('h-[min(48rem,calc(100dvh-3rem))]');
    expect(classes).toContain('overflow-hidden');
    // The head (title and tabs) stays; the body scrolls.
    const body = within(dialog).getByTestId('composer-settings-body');
    expect(body.className).toMatch(/\boverflow-y-auto\b/);
    expect(body.className).toMatch(/\bmin-h-0\b/);
    const list = within(dialog).getByRole('tablist');
    expect(list.className).toMatch(/\boverflow-x-auto\b/);
  });
});

describe('the dot of a changed tab (#546)', () => {
  it('explains itself on every changed tab, in the hover hint and as the dot\'s name, from the Schema in both languages', async () => {
    useWorkspaceStore.setState({ modelByDomain: { image: 'gpt-image', 'model-3d': null } });
    mock.params.pinned = true;
    mock.chat = { ...mock.chat, changed: true };
    const view = props();
    render(<ComposerSettings {...view} skill={{ ...view.skill, selectedId: 'three-view' }} />);
    await openFromGear();
    for (const id of COMPOSER_SETTINGS_TABS) {
      const note = messages[`ui.studio.composer.settings.changed_note.${id}`];
      expect(note).toBeTruthy();
      expect(english[`ui.studio.composer.settings.changed_note.${id}`]).toBeTruthy();
      expect(screen.getByTestId(`composer-settings-tab-dot-${id}`).getAttribute('aria-label')).toBe(note);
      const wrapper = tab(id).parentElement as HTMLElement;
      expect(wrapper.getAttribute('data-hint')).toBe(note);
    }
    expect(messages['ui.studio.composer.settings.changed_note.models']).toBe('已固定生成模型：Agent 不再自行选择');
  });

  it('shows no dot, no hint and no restore line while nothing is pinned', async () => {
    render(<ComposerSettings {...props()} />);
    await openFromGear();
    expect(screen.queryByTestId('composer-settings-tab-dot-models')).toBeNull();
    expect(tab('models').parentElement?.getAttribute('data-testid')).not.toBe('gear-hint');
    expect(screen.queryByTestId('composer-settings-models-locked')).toBeNull();
  });

  it('names the pinned models on the models tab and gives the choice back to the Agent', async () => {
    useWorkspaceStore.setState({ modelByDomain: { image: 'gpt-image', 'model-3d': 'meshy' } });
    render(<ComposerSettings {...props()} />);
    await openFromGear();
    const line = screen.getByTestId('composer-settings-models-locked');
    expect(line.textContent).toContain('GPT IMAGE');
    expect(line.textContent).toContain('MESHY');
    expect(english['ui.studio.composer.settings.models_locked']).toContain('{models}');
    fireEvent.click(within(line).getByRole('button', { name: messages['ui.studio.composer.settings.models_reset'] }));
    expect(mock.cleared).toEqual(['image', 'model-3d']);
    expect(useWorkspaceStore.getState().modelByDomain).toEqual({ image: null, 'model-3d': null });
    await waitFor(() => expect(screen.queryByTestId('composer-settings-tab-dot-models')).toBeNull());
    expect(screen.queryByTestId('composer-settings-models-locked')).toBeNull();
    expect(gear().getAttribute('data-changed')).toBe('false');
  });

  it('clears only the domain that is pinned', async () => {
    useWorkspaceStore.setState({ modelByDomain: { image: null, 'model-3d': 'meshy' } });
    render(<ComposerSettings {...props()} />);
    await openFromGear();
    expect(screen.getByTestId('composer-settings-models-locked').textContent).not.toContain('GPT IMAGE');
    fireEvent.click(screen.getByTestId('composer-settings-models-reset'));
    expect(mock.cleared).toEqual(['model-3d']);
  });
});
