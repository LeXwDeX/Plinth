import type { AgentParameterState } from '@/lib/agent-params';
import type { ChatModelChoice, ChatReasoning } from '@/lib/chat/browser-client';

/**
 * #405: the settings dialog behind the gear beside the composer's send button. One tab per kind of choice the
 * composer used to carry as a picker of its own, in the order the Issue names them.
 */
export const COMPOSER_SETTINGS_TABS = ['models', 'skills', 'params', 'chat'] as const;

export type ComposerSettingsTab = (typeof COMPOSER_SETTINGS_TABS)[number];

/** The Schema message that names each tab (written out in full so the message-key guard can see them). */
export const SETTINGS_TAB_TEXT: Record<ComposerSettingsTab, string> = {
  models: 'ui.studio.composer.settings.tab.models',
  skills: 'ui.studio.composer.settings.tab.skills',
  params: 'ui.studio.composer.settings.tab.params',
  chat: 'ui.studio.composer.settings.tab.chat',
};

/**
 * #546: what the dot beside a changed tab means (its hover hint and its screen-reader name), one Schema message per
 * tab, written out in full so the message-key guard can see them.
 */
export const SETTINGS_CHANGED_NOTE_TEXT: Record<ComposerSettingsTab, string> = {
  models: 'ui.studio.composer.settings.changed_note.models',
  skills: 'ui.studio.composer.settings.changed_note.skills',
  params: 'ui.studio.composer.settings.changed_note.params',
  chat: 'ui.studio.composer.settings.changed_note.chat',
};

export function isComposerSettingsTab(value: unknown): value is ComposerSettingsTab {
  return typeof value === 'string' && (COMPOSER_SETTINGS_TABS as readonly string[]).includes(value);
}

/**
 * The tab to show: the one wanted when it is offered, else the first one that is (the chat model tab is left out
 * when the administrator offers nothing to choose). Null when no tab is offered.
 */
export function visibleSettingsTab(wanted: ComposerSettingsTab, offered: readonly ComposerSettingsTab[]): ComposerSettingsTab | null {
  return offered.includes(wanted) ? wanted : offered[0] ?? null;
}

/** A generation model picked for either domain is the person's explicit lock; no pick leaves the choice to the Agent. */
export function modelsLocked(modelByDomain: Readonly<Record<'image' | 'model-3d', string | null>>): boolean {
  return modelByDomain.image !== null || modelByDomain['model-3d'] !== null;
}

/**
 * #546: the domains whose generation model the person pinned, in the order 2D, 3D. The pins are kept across sessions
 * (#405/#406 store them with the workspace); the models tab names them and offers to give the choice back to the Agent.
 */
export function lockedModelDomains(modelByDomain: Readonly<Record<'image' | 'model-3d', string | null>>): Array<'image' | 'model-3d'> {
  return (['image', 'model-3d'] as const).filter((domain) => modelByDomain[domain] !== null);
}

/** A parameter the person set (a pin), in any of the domains; defaults and the Agent's own values do not count. */
export function parametersPinned(domains: ReadonlyArray<Readonly<Record<string, AgentParameterState>> | undefined>): boolean {
  return domains.some((states) => Object.values(states ?? {}).some((state) => state.source === 'user'));
}

/** The chat model the next turn uses, or its thinking level, differs from the administrator's default. */
export function chatSettingsChanged(
  choice: Pick<ChatModelChoice, 'selected' | 'defaultId'> | null,
  reasoning: Pick<ChatReasoning, 'effort' | 'defaultLevel'> | null,
): boolean {
  const model = Boolean(choice && choice.selected !== '' && choice.defaultId !== '' && choice.selected !== choice.defaultId);
  const level = Boolean(reasoning && reasoning.effort !== '' && reasoning.defaultLevel !== '' && reasoning.effort !== reasoning.defaultLevel);
  return model || level;
}

export interface SettingsChange {
  tab: ComposerSettingsTab;
  /** What the tab holds now, when it can be said in a few words (a model pair, a Skill name). */
  value?: string;
}

/** The tabs whose choice differs from the default, in tab order, each with the value it holds. */
export function changedSettings(entries: Partial<Record<ComposerSettingsTab, { changed: boolean; value?: string }>>): SettingsChange[] {
  return COMPOSER_SETTINGS_TABS.flatMap((tab): SettingsChange[] => {
    const entry = entries[tab];
    if (!entry?.changed) return [];
    return [entry.value ? { tab, value: entry.value } : { tab }];
  });
}
